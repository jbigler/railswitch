package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/tomlfile"
	"github.com/jbigler/railswitch/internal/workspace"
)

const whiteboardUsage = "usage: railswitch [--json] whiteboard show | whiteboard write --if-hash <hash> | whiteboard rm --if-hash <hash>"

// Whiteboard limits (D151): the scene (Excalidraw JSON, pasted images
// included), its picture, and the picture's longest side.
const (
	boardSceneMax = 12 << 20
	boardPNGMax   = 4 << 20
	boardSideMax  = 4096
)

// boardDoc is whiteboard show --json, and (without Slug and Hash) what
// whiteboard write reads on stdin.
type boardDoc struct {
	Slug  string          `json:"slug,omitempty"`
	Hash  string          `json:"hash"`  // the scene's sha256, "" without a board
	Scene json.RawMessage `json:"scene"` // the Excalidraw scene, null without a board
	PNG   string          `json:"png"`   // the board's picture, base64; "" for none
	// Light is the picture on white, for the dashboard's light theme
	// (D218), base64; "" for none (a board saved before it had one).
	Light string `json:"png_light"`
}

// railswitch whiteboard show|write|rm: worktree's office whiteboard (D151),
// <ws>/.railswitch/whiteboard/<slug>/: an Excalidraw scene, the picture
// the office shows on the board, and the same on white for the dashboard's
// light theme (D218). wt rm removes it with the worktree.
func cmdWhiteboard(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", whiteboardUsage)
	}
	var fset *flag.FlagSet
	var hash *string
	const hashHelp = "the hash of the board the edit started from (whiteboard show --json); \"\" when there was none (required)"
	switch args[0] {
	case "show":
		fset = e.flags("whiteboard show")
	case "write":
		fset = e.flags("whiteboard write")
		hash = fset.String("if-hash", "", hashHelp)
	case "rm":
		fset = e.flags("whiteboard rm")
		hash = fset.String("if-hash", "", hashHelp)
	default:
		return e.usageErr("whiteboard: unknown subcommand %q\n%s", args[0], whiteboardUsage)
	}
	if code, ok := parse(fset, args[1:]); !ok {
		return code
	}
	if fset.NArg() != 0 || (hash != nil && !isSet(fset, "if-hash")) {
		return e.usageErr("%s", whiteboardUsage)
	}
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return e.fail(err)
	}
	if w.Slug == "" {
		return e.usageErr("whiteboard: not in a worktree; run it in <workspace>/worktrees/<worktree>/ or pass -w <worktree>")
	}
	dir := filepath.Join(w.Root, ".railswitch", "whiteboard", w.Slug)
	if hash == nil {
		return boardShow(e, dir, w.Slug)
	}
	var doc boardDoc
	if args[0] == "write" {
		data, err := io.ReadAll(io.LimitReader(e.stdin, boardSceneMax+2*boardPNGMax*4/3+4096))
		if err != nil {
			return e.fail(err)
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			return e.fail(fmt.Errorf("whiteboard write: stdin is not {\"scene\", \"png\", \"png_light\"}: %w", err))
		}
		if doc.Scene == nil || string(doc.Scene) == "null" {
			return e.fail(errors.New("whiteboard write: no scene (whiteboard rm clears the board)"))
		}
	}
	return boardChange(e, dir, *hash, doc)
}

// boardFiles are a board's files in dir: its scene, then its pictures.
var boardFiles = []string{"board.excalidraw", "board.png", "board-light.png"}

// readBoard reads the board in dir: its scene (nil: no board) and its
// pictures, dark then light (nil: none).
func readBoard(dir string) (scene, pic, light []byte, err error) {
	scene, err = safefile.Read(filepath.Join(dir, boardFiles[0]), boardSceneMax)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	pics := [2][]byte{}
	for i, name := range boardFiles[1:] {
		pics[i], err = safefile.Read(filepath.Join(dir, name), boardPNGMax)
		if errors.Is(err, fs.ErrNotExist) {
			err = nil
		}
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return scene, pics[0], pics[1], nil
}

func boardShow(e *env, dir, slug string) int {
	scene, pic, light, err := readBoard(dir)
	if err != nil {
		return e.fail(err)
	}
	doc := boardDoc{Slug: slug, Scene: json.RawMessage("null")}
	if scene != nil {
		doc.Hash, doc.Scene = workspace.Hash(scene), scene
		doc.PNG = base64.StdEncoding.EncodeToString(pic)
		doc.Light = base64.StdEncoding.EncodeToString(light)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	if scene == nil {
		fmt.Fprintf(e.stdout, "worktree %s has an empty whiteboard\n", slug)
		return 0
	}
	fmt.Fprintf(e.stdout, "worktree %s's whiteboard: %d bytes of scene, %d of picture, %s\n", slug, len(scene), len(pic), doc.Hash)
	return 0
}

// checkBoard refuses a scene that isn't an Excalidraw document or a picture
// that isn't a PNG within the limits; it returns the pictures' bytes, dark
// then light.
func checkBoard(doc boardDoc) (pics [2][]byte, err error) {
	if len(doc.Scene) > boardSceneMax {
		return pics, fmt.Errorf("whiteboard write: the scene is over %d MiB", boardSceneMax>>20)
	}
	var head struct {
		Type     string            `json:"type"`
		Elements []json.RawMessage `json:"elements"`
	}
	if err := json.Unmarshal(doc.Scene, &head); err != nil || head.Type != "excalidraw" || head.Elements == nil {
		return pics, errors.New("whiteboard write: the scene is not an Excalidraw document")
	}
	for i, b64 := range []string{doc.PNG, doc.Light} {
		if pics[i], err = checkPNG(b64); err != nil {
			return pics, fmt.Errorf("whiteboard write: %s: %w", []string{"the picture", "the light picture"}[i], err)
		}
	}
	return pics, nil
}

// checkPNG is b64's PNG, refused unless it is one within the limits; ""
// is no picture.
func checkPNG(b64 string) ([]byte, error) {
	pic, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("not base64")
	}
	if len(pic) > boardPNGMax {
		return nil, fmt.Errorf("over %d MiB", boardPNGMax>>20)
	}
	if len(pic) > 0 {
		c, err := png.DecodeConfig(bytes.NewReader(pic))
		if err != nil {
			return nil, fmt.Errorf("not a PNG: %w", err)
		}
		if c.Width < 1 || c.Height < 1 || c.Width > boardSideMax || c.Height > boardSideMax {
			return nil, fmt.Errorf("%d×%d; at most %d a side", c.Width, c.Height, boardSideMax)
		}
	}
	return pic, nil
}

// boardChange writes doc as the board in dir (an empty doc: removes it)
// when the board still is what the caller read (hash; "": no board).
func boardChange(e *env, dir, hash string, doc boardDoc) int {
	var pics [2][]byte
	if doc.Scene != nil {
		var err error
		if pics, err = checkBoard(doc); err != nil {
			return e.fail(err)
		}
	}
	unlock, err := lockFile(filepath.Join(filepath.Dir(dir), "lock"))
	if err != nil {
		return e.fail(err)
	}
	defer unlock()
	cur, _, _, err := readBoard(dir)
	switch {
	case err != nil:
		return e.fail(err)
	case hash == "" && cur != nil:
		return e.fail(errors.New("the whiteboard has a drawing: pass its hash (whiteboard show --json) to replace it"))
	case hash != "" && workspace.Hash(cur) != hash:
		return e.fail(errors.New("the whiteboard changed since it was read: open it again"))
	}
	h := ""
	if doc.Scene == nil {
		for _, name := range boardFiles {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return e.fail(err)
			}
		}
	} else {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return e.fail(err)
		}
		// the pictures first: the scene's hash is the board's version; a
		// picture the save has none of goes, so none is left from before
		for i, name := range boardFiles[1:] {
			path := filepath.Join(dir, name)
			if len(pics[i]) == 0 {
				err = os.Remove(path)
				if errors.Is(err, fs.ErrNotExist) {
					err = nil
				}
			} else {
				err = tomlfile.WriteAtomic(path, pics[i], 0o600)
			}
			if err != nil {
				return e.fail(err)
			}
		}
		if err := tomlfile.WriteAtomic(filepath.Join(dir, boardFiles[0]), doc.Scene, 0o600); err != nil {
			return e.fail(err)
		}
		h = workspace.Hash(doc.Scene)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(struct {
			Hash string `json:"hash"`
		}{h}); err != nil {
			return e.fail(err)
		}
	}
	return 0
}
