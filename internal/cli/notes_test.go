package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/workspace"
)

// notesWS is a folder notes commands take for a workspace (project.toml
// is all they look for) and its notes dir.
func notesWS(t *testing.T) (ws, dir string) {
	ws = t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "project.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return ws, filepath.Join(ws, "notes")
}

// D144: write creates, replaces only at the hash read, rm likewise; ls is
// newest first, .md regular files only.
func TestNotes(t *testing.T) {
	ws, dir := notesWS(t)
	f := fakeHost()
	if out := mustRun(t, "-C", ws, "--json", "notes", "ls"); out != `{"notes":[]}`+"\n" {
		t.Errorf("no notes dir: %q", out)
	}
	mustRunStdin(t, f, "old\n", "-C", ws, "notes", "write", "a.md", "--if-hash", "")
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("notes dir: %v %v", fi, err)
	}
	if code, _, errs := runStdin(t, f, "x", "-C", ws, "notes", "write", "a.md", "--if-hash", ""); code != 1 || !strings.Contains(errs, "exists") {
		t.Errorf("create over a note: exit %d %s", code, errs)
	}
	if code, _, _ := runStdin(t, f, "x", "-C", ws, "notes", "write", "a.md", "--if-hash", "sha256:00"); code != 1 {
		t.Errorf("stale hash: exit %d", code)
	}
	var doc noteDoc
	if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "--json", "notes", "show", "a.md")), &doc); err != nil || doc.Text != "old\n" || doc.Hash != workspace.Hash([]byte("old\n")) {
		t.Fatalf("show: %+v %v", doc, err)
	}
	mustRunStdin(t, f, "new", "-C", ws, "notes", "write", "a.md", "--if-hash", doc.Hash)
	if b, _ := os.ReadFile(filepath.Join(dir, "a.md")); string(b) != "new" {
		t.Errorf("a.md %q", b)
	}

	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "a.md"), old, old); err != nil {
		t.Fatal(err)
	}
	mustRunStdin(t, f, "b", "-C", ws, "notes", "write", "b.md", "--if-hash", "")
	for _, n := range []string{"x.txt", ".hidden.md"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "d.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	var ls struct{ Notes []noteRow }
	if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "--json", "notes", "ls")), &ls); err != nil {
		t.Fatal(err)
	}
	if len(ls.Notes) != 2 || ls.Notes[0].Name != "b.md" || ls.Notes[1].Name != "a.md" || ls.Notes[1].Size != 3 {
		t.Errorf("ls %+v", ls.Notes)
	}

	for _, bad := range []string{"../a.md", "a", ".a.md", "a/b.md"} {
		if code, _, _ := runIn(t, "-C", ws, "notes", "show", bad); code != 2 {
			t.Errorf("show %q: exit %d; want 2", bad, code)
		}
	}
	if code, _, _ := runIn(t, "-C", ws, "notes", "rm", "a.md", "--if-hash", ""); code != 2 {
		t.Errorf("rm without a hash: exit %d; want 2", code)
	}
	mustRunStdin(t, f, "", "-C", ws, "notes", "write", "empty.md", "--if-hash", "")
	if fi, err := os.Stat(filepath.Join(dir, "empty.md")); err != nil || fi.Size() != 0 || fi.Mode().Perm() != 0o644 {
		t.Errorf("empty note: %v %v", fi, err)
	}
	mustRun(t, "-C", ws, "notes", "rm", "a.md", "--if-hash", workspace.Hash([]byte("new")))
	if _, err := os.Lstat(filepath.Join(dir, "a.md")); !os.IsNotExist(err) {
		t.Errorf("a.md not removed: %v", err)
	}
}

// D144: a symlink an agent plants in notes/ is never listed, read, written
// through or removed.
func TestNotesSymlink(t *testing.T) {
	ws, dir := notesWS(t)
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("s3cret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "evil.md")); err != nil {
		t.Fatal(err)
	}
	if out := mustRun(t, "-C", ws, "--json", "notes", "ls"); out != `{"notes":[]}`+"\n" {
		t.Errorf("ls lists the symlink: %q", out)
	}
	if code, out, _ := runIn(t, "-C", ws, "--json", "notes", "show", "evil.md"); code != 1 || strings.Contains(out, "s3cret") {
		t.Errorf("show: exit %d %q", code, out)
	}
	h := workspace.Hash([]byte("s3cret"))
	f := fakeHost()
	for _, hash := range []string{"", h} {
		if code, _, _ := runStdin(t, f, "pwned", "-C", ws, "notes", "write", "evil.md", "--if-hash", hash); code != 1 {
			t.Errorf("write --if-hash %q: exit %d; want 1", hash, code)
		}
	}
	if code, _, _ := runIn(t, "-C", ws, "notes", "rm", "evil.md", "--if-hash", h); code != 1 {
		t.Errorf("rm: exit %d; want 1", code)
	}
	if b, _ := os.ReadFile(secret); string(b) != "s3cret" {
		t.Errorf("target changed: %q", b)
	}
	if fi, err := os.Lstat(filepath.Join(dir, "evil.md")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink gone: %v", err)
	}
}
