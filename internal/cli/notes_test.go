package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	// --json prints the new hash: the editor's next save starts from it, with
	// no second read in which another writer could slip in
	var wrote struct{ Hash string }
	if err := json.Unmarshal([]byte(mustRunStdin(t, f, "new", "-C", ws, "--json", "notes", "write", "a.md", "--if-hash", doc.Hash)), &wrote); err != nil || wrote.Hash != workspace.Hash([]byte("new")) {
		t.Fatalf("write --json: %+v %v", wrote, err)
	}
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

// Saves at once from the same version: one wins, the others are told the
// note changed (the hash check and the write hold one lock).
func TestNotesRace(t *testing.T) {
	ws, _ := notesWS(t)
	const n = 8
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _, _ := runStdin(t, fakeHost(), fmt.Sprintf("v%d\n", i), "-C", ws, "notes", "write", "r.md", "--if-hash", "")
			codes <- code
		}()
	}
	wg.Wait()
	close(codes)
	won := 0
	for c := range codes {
		if c == 0 {
			won++
		}
	}
	if won != 1 {
		t.Errorf("%d of %d saves from one version won; want 1", won, n)
	}
}

// D199: an agent's suggestion is listed, shown with the note it replaces,
// accepted only at the hash reviewed, and rejected by exact name; a
// symlink in an inbox is never read or followed.
func TestNotesInbox(t *testing.T) {
	ws, dir := notesWS(t)
	f := fakeHost()
	inbox := filepath.Join(ws, ".railswitch", "notes-inbox", "feat")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	if out := mustRun(t, "-C", ws, "--json", "notes", "inbox", "ls"); out != `{"suggestions":[]}`+"\n" {
		t.Errorf("empty inbox: %q", out)
	}
	mustRunStdin(t, f, "old\n", "-C", ws, "notes", "write", "a.md", "--if-hash", "")
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(inbox, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.md", "new\n")
	write("b.md", "b\n")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(inbox, "link.md")); err != nil {
		t.Fatal(err)
	}

	var ls struct{ Suggestions []inboxRow }
	if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "--json", "notes", "inbox", "ls")), &ls); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range ls.Suggestions {
		if r.Worktree != "feat" {
			t.Errorf("row %+v", r)
		}
		got[r.Name] = r.Replaces
	}
	if len(got) != 2 || !got["a.md"] || got["b.md"] {
		t.Errorf("ls %+v", ls.Suggestions)
	}

	var doc inboxDoc
	if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "--json", "notes", "inbox", "show", "feat", "a.md")), &doc); err != nil || doc.Text != "new\n" || doc.Note == nil || doc.Note.Text != "old\n" {
		t.Fatalf("show: %+v %v", doc, err)
	}
	if code, _, _ := runIn(t, "-C", ws, "notes", "inbox", "show", "feat", "link.md"); code != 1 {
		t.Errorf("show through a symlink: exit %d", code)
	}
	// The agent edits it after the review: the reviewed hash no longer matches.
	write("a.md", "do something else\n")
	if code, _, errs := runIn(t, "-C", ws, "notes", "inbox", "accept", "feat", "a.md", "--hash", doc.Hash); code != 1 || !strings.Contains(errs, "changed") {
		t.Errorf("accept a changed suggestion: exit %d %s", code, errs)
	}
	write("a.md", "new\n")
	mustRun(t, "-C", ws, "notes", "inbox", "accept", "feat", "a.md", "--hash", doc.Hash)
	if b, _ := os.ReadFile(filepath.Join(dir, "a.md")); string(b) != "new\n" {
		t.Errorf("accepted note %q", b)
	}
	if _, err := os.Lstat(filepath.Join(inbox, "a.md")); !os.IsNotExist(err) {
		t.Errorf("accepted suggestion left in the inbox: %v", err)
	}
	// --as renames it on the way in.
	var b inboxDoc
	if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "--json", "notes", "inbox", "show", "feat", "b.md")), &b); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "-C", ws, "notes", "inbox", "accept", "feat", "b.md", "--hash", b.Hash, "--as", "better_name.md")
	if _, err := os.Stat(filepath.Join(dir, "better_name.md")); err != nil {
		t.Error(err)
	}
	// Reject removes the link itself, never its target.
	mustRun(t, "-C", ws, "notes", "inbox", "reject", "feat", "link.md")
	if _, err := os.Stat(secret); err != nil {
		t.Errorf("reject followed the symlink: %v", err)
	}
	if code, _, _ := runIn(t, "-C", ws, "notes", "inbox", "reject", "feat", "link.md"); code != 1 {
		t.Errorf("reject a missing suggestion: exit %d", code)
	}
	for _, bad := range [][]string{{"../x", "a.md"}, {"Feat", "a.md"}, {"feat", "../a.md"}} {
		if code, _, _ := runIn(t, append([]string{"-C", ws, "notes", "inbox", "reject"}, bad...)...); code != 2 {
			t.Errorf("reject %v: exit %d", bad, code)
		}
	}
}

// D219: search keeps the notes whose name or text holds every word, any
// case, newest first, with the first text line that has a word.
func TestNotesSearch(t *testing.T) {
	ws, dir := notesWS(t)
	f := fakeHost()
	long := strings.Repeat("x", 100) + " Postgres " + strings.Repeat("y", 200)
	mustRunStdin(t, f, "intro\nRun DB prepare\x1b[31m\tnow\n", "-C", ws, "notes", "write", "agent_queue.md", "--if-hash", "")
	mustRunStdin(t, f, long+"\nqueue\n", "-C", ws, "notes", "write", "db.md", "--if-hash", "")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "agent_queue.md"), old, old); err != nil {
		t.Fatal(err)
	}
	search := func(q ...string) []noteHit {
		var out struct{ Notes []noteHit }
		if err := json.Unmarshal([]byte(mustRun(t, append([]string{"-C", ws, "--json", "notes", "search", "--"}, q...)...)), &out); err != nil {
			t.Fatal(err)
		}
		return out.Notes
	}
	if h := search("QUEUE"); len(h) != 2 || h[0].Name != "db.md" || h[0].Line != "queue" || h[1].Name != "agent_queue.md" || h[1].Line != "" {
		t.Errorf("queue: %+v", h)
	}
	if h := search("queue prepare"); len(h) != 1 || h[0].Line != "Run DB prepare [31m now" {
		t.Errorf("queue prepare: %+v", h)
	}
	if h := search("postgres"); len(h) != 1 || !strings.HasPrefix(h[0].Line, "…") || !strings.HasSuffix(h[0].Line, "…") || !strings.Contains(h[0].Line, "Postgres") || len([]rune(h[0].Line)) != 162 {
		t.Errorf("postgres: %+v", h)
	}
	if h := search("-x"); len(h) != 0 {
		t.Errorf("-x: %+v", h)
	}
	if code, _, _ := runIn(t, "-C", ws, "notes", "search", " "); code != 2 {
		t.Errorf("no words: exit %d; want 2", code)
	}
}
