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
}

// railswitch whiteboard show|write|rm: worktree's office whiteboard (D151),
// <ws>/.railswitch/whiteboard/<slug>/: an Excalidraw scene and the picture
// the office shows on the board. wt rm removes it with the worktree.
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
		data, err := io.ReadAll(io.LimitReader(e.stdin, boardSceneMax+boardPNGMax*4/3+4096))
		if err != nil {
			return e.fail(err)
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			return e.fail(fmt.Errorf("whiteboard write: stdin is not {\"scene\", \"png\"}: %w", err))
		}
		if doc.Scene == nil || string(doc.Scene) == "null" {
			return e.fail(errors.New("whiteboard write: no scene (whiteboard rm clears the board)"))
		}
	}
	return boardChange(e, dir, *hash, doc)
}

func readBoard(dir string) (scene, pic []byte, err error) {
	scene, err = safefile.Read(filepath.Join(dir, "board.excalidraw"), boardSceneMax)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	pic, err = safefile.Read(filepath.Join(dir, "board.png"), boardPNGMax)
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	return scene, pic, err
}

func boardShow(e *env, dir, slug string) int {
	scene, pic, err := readBoard(dir)
	if err != nil {
		return e.fail(err)
	}
	doc := boardDoc{Slug: slug, Scene: json.RawMessage("null")}
	if scene != nil {
		doc.Hash, doc.Scene = workspace.Hash(scene), scene
		doc.PNG = base64.StdEncoding.EncodeToString(pic)
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
// that isn't a PNG within the limits; it returns the picture's bytes.
func checkBoard(doc boardDoc) ([]byte, error) {
	if len(doc.Scene) > boardSceneMax {
		return nil, fmt.Errorf("whiteboard write: the scene is over %d MiB", boardSceneMax>>20)
	}
	var head struct {
		Type     string            `json:"type"`
		Elements []json.RawMessage `json:"elements"`
	}
	if err := json.Unmarshal(doc.Scene, &head); err != nil || head.Type != "excalidraw" || head.Elements == nil {
		return nil, errors.New("whiteboard write: the scene is not an Excalidraw document")
	}
	pic, err := base64.StdEncoding.DecodeString(doc.PNG)
	if err != nil {
		return nil, errors.New("whiteboard write: the picture is not base64")
	}
	if len(pic) > boardPNGMax {
		return nil, fmt.Errorf("whiteboard write: the picture is over %d MiB", boardPNGMax>>20)
	}
	if len(pic) > 0 {
		c, err := png.DecodeConfig(bytes.NewReader(pic))
		if err != nil {
			return nil, fmt.Errorf("whiteboard write: the picture is not a PNG: %w", err)
		}
		if c.Width < 1 || c.Height < 1 || c.Width > boardSideMax || c.Height > boardSideMax {
			return nil, fmt.Errorf("whiteboard write: the picture is %d×%d; at most %d a side", c.Width, c.Height, boardSideMax)
		}
	}
	return pic, nil
}

// boardChange writes doc as the board in dir (an empty doc: removes it)
// when the board still is what the caller read (hash; "": no board).
func boardChange(e *env, dir, hash string, doc boardDoc) int {
	var pic []byte
	if doc.Scene != nil {
		var err error
		if pic, err = checkBoard(doc); err != nil {
			return e.fail(err)
		}
	}
	unlock, err := lockFile(filepath.Join(filepath.Dir(dir), "lock"))
	if err != nil {
		return e.fail(err)
	}
	defer unlock()
	cur, _, err := readBoard(dir)
	switch {
	case err != nil:
		return e.fail(err)
	case hash == "" && cur != nil:
		return e.fail(errors.New("the whiteboard has a drawing: pass its hash (whiteboard show --json) to replace it"))
	case hash != "" && workspace.Hash(cur) != hash:
		return e.fail(errors.New("the whiteboard changed since it was read: open it again"))
	}
	scene, picPath := filepath.Join(dir, "board.excalidraw"), filepath.Join(dir, "board.png")
	h := ""
	switch {
	case doc.Scene == nil:
		for _, p := range []string{scene, picPath} {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return e.fail(err)
			}
		}
	default:
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return e.fail(err)
		}
		// the picture first: the scene's hash is the board's version
		if len(pic) == 0 {
			err = os.Remove(picPath)
			if errors.Is(err, fs.ErrNotExist) {
				err = nil
			}
		} else {
			err = tomlfile.WriteAtomic(picPath, pic, 0o600)
		}
		if err == nil {
			err = tomlfile.WriteAtomic(scene, doc.Scene, 0o600)
		}
		if err != nil {
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
