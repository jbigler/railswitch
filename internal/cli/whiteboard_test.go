package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/workspace"
)

func pngB64(t *testing.T, w, h int) string {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

// D151: a worktree's whiteboard is written only over the version it was
// read at, holds an Excalidraw scene and a PNG, and rm clears it.
func TestWhiteboard(t *testing.T) {
	ws, _ := lockedWS(t)
	f := fakeHost()
	show := func() boardDoc {
		t.Helper()
		var d boardDoc
		if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "-w", "feat", "--json", "whiteboard", "show")), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := show(); d.Slug != "feat" || d.Hash != "" || string(d.Scene) != "null" || d.PNG != "" {
		t.Fatalf("empty board: %+v", d)
	}
	scene := `{"type":"excalidraw","version":2,"elements":[{"id":"a"}],"appState":{},"files":{}}`
	in := `{"scene":` + scene + `,"png":"` + pngB64(t, 40, 20) + `"}`
	code, out, errs := runStdin(t, f, in, "-C", ws, "-w", "feat", "--json", "whiteboard", "write", "--if-hash", "")
	if code != 0 || !strings.Contains(out, workspace.Hash([]byte(scene))) {
		t.Fatalf("write: exit %d %s%s", code, out, errs)
	}
	d := show()
	if d.Hash != workspace.Hash([]byte(scene)) || string(d.Scene) != scene || d.PNG == "" {
		t.Errorf("after write: %+v", d)
	}
	if fi, err := os.Stat(filepath.Join(ws, ".railswitch", "whiteboard", "feat", "board.png")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("picture: %v %v", fi, err)
	}

	for name, c := range map[string]struct{ in, hash, want string }{
		"stale":     {in, "", "has a drawing"},
		"old hash":  {in, "sha256:00", "changed since"},
		"not excal": {`{"scene":{"type":"x","elements":[]},"png":""}`, d.Hash, "not an Excalidraw"},
		"not png":   {`{"scene":` + scene + `,"png":"aGVsbG8="}`, d.Hash, "not a PNG"},
		"too wide":  {`{"scene":` + scene + `,"png":"` + pngB64(t, 4097, 1) + `"}`, d.Hash, "at most 4096"},
		"no scene":  {`{"png":""}`, d.Hash, "no scene"},
	} {
		if code, _, errs := runStdin(t, f, c.in, "-C", ws, "-w", "feat", "whiteboard", "write", "--if-hash", c.hash); code != 1 || !strings.Contains(errs, c.want) {
			t.Errorf("%s: exit %d %s", name, code, errs)
		}
	}
	if code, _, _ := runStdin(t, f, "", "-C", ws, "-w", "feat", "whiteboard", "write"); code != 2 {
		t.Errorf("write without --if-hash: exit %d; want 2", code)
	}
	if code, _, errs := runStdin(t, f, "", "-C", ws, "-w", "feat", "whiteboard", "rm", "--if-hash", d.Hash); code != 0 {
		t.Fatalf("rm: exit %d %s", code, errs)
	}
	if d := show(); d.Hash != "" {
		t.Errorf("after rm: %+v", d)
	}
}
