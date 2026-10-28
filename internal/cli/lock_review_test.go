package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/review"
	"github.com/jbigler/railswitch/internal/workspace"
)

// acceptLock accepts whatever ws's stack.lock would change: lock --review,
// then lock --accept with its hash.
func acceptLock(t *testing.T, ws string) {
	t.Helper()
	doc := reviewOf(t, ws)
	mustRun(t, "-C", ws, "lock", "--accept", doc.Hash)
}

func reviewOf(t *testing.T, ws string) reviewDoc {
	t.Helper()
	out := mustRun(t, "-C", ws, "--json", "lock", "--review")
	var doc reviewDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return doc
}

// oldPull makes the accepted postgres pull another image, so the current
// built-in changes the pull back (an image flag), and adds a file the
// built-in doesn't have (removed).
func oldPull(m fstest.MapFS) {
	m["option.toml"].Data = []byte(strings.Replace(string(m["option.toml"].Data), "docker.io/library/postgres:", "docker.io/library/oldpg:", 1))
	addFile(m)
}

func TestLockReviewJSON(t *testing.T) {
	ws, dirs := lockedWS(t)
	// A second project whose stack.lock already pins the new content.
	other := filepath.Join(t.TempDir(), "maybe")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "stack.lock"), []byte(readFile(t, filepath.Join(ws, "stack.lock"))), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.AddProject("maybe", other, nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	reg.Close()
	from := pendingOption(t, ws, "postgres", oldPull)
	to := readLock(t, other).Options["postgres"].SHA256

	code, out, errs := runWith(t, unitsFake(), "-C", ws, "--json", "lock", "--review")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if strings.Contains(out, ":null") {
		t.Errorf("a null in %s", out)
	}
	var doc reviewDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	pl, err := workspace.Review(t.Context(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Hash != workspace.Hash(pl.Lock) || !strings.HasPrefix(doc.Hash, "sha256:") {
		t.Errorf("hash %s, want %s", doc.Hash, workspace.Hash(pl.Lock))
	}
	if len(doc.Packages) != 1 {
		t.Fatalf("packages %+v", doc.Packages)
	}
	p := doc.Packages[0]
	if p.Ref != "postgres@1" || p.Kind != "option" || p.Status != "changed" || p.From != from || p.To != to || !slices.Equal(p.AlsoLockedIn, []string{"maybe"}) {
		t.Errorf("package %+v", p)
	}
	var rows []string
	for _, f := range p.Files {
		rows = append(rows, f.Path+" "+f.Status)
	}
	if !slices.Equal(rows, []string{"NOTES.md removed", "option.toml changed"}) {
		t.Errorf("files %v", rows)
	}
	manifest := p.Files[1]
	if len(manifest.Flags) != 1 || manifest.Flags[0] != (review.Flag{Check: "image", Text: "image.pull: docker.io/library/oldpg:{postgres} -> docker.io/library/postgres:{postgres}"}) || !strings.Contains(manifest.Diff, "+pull = \"docker.io/library/postgres:{postgres}\"") {
		t.Errorf("option.toml %+v", manifest)
	}
	if doc.Flags != 1 {
		t.Errorf("flags %d, want 1", doc.Flags)
	}
	if !slices.Equal(doc.Restart, []string{"master"}) {
		t.Errorf("restart %v, want [master]", doc.Restart)
	}
	if out := mustRun(t, "-C", ws, "lock", "--review"); !strings.Contains(out, "! image: image.pull:") || !strings.Contains(out, "also locked in maybe") {
		t.Errorf("text review:\n%s", out)
	}
}

func TestLockReviewNothing(t *testing.T) {
	ws, _ := lockedWS(t)
	out := mustRun(t, "-C", ws, "--json", "lock", "--review")
	if !strings.Contains(out, `"flags":0,"packages":[],"restart":[]`) {
		t.Errorf("empty review %s", out)
	}
}

func TestLockReviewFirst(t *testing.T) {
	ws, _ := lockedWS(t)
	pendingOption(t, ws, "postgres", oldPull)
	dropStore(t)
	doc := reviewOf(t, ws)
	if len(doc.Packages) != 1 || doc.Packages[0].Status != "first" {
		t.Fatalf("packages %+v", doc.Packages)
	}
	for _, f := range doc.Packages[0].Files {
		if f.Status != "added" {
			t.Errorf("%s %s: without a stored copy every file is added", f.Path, f.Status)
		}
	}
}

func TestLockPlainRefuses(t *testing.T) {
	ws, _ := lockedWS(t)
	pendingOption(t, ws, "postgres", oldPull)
	path := filepath.Join(ws, "stack.lock")
	before := readFile(t, path)
	code, out, errs := runIn(t, "-C", ws, "lock")
	if code != 1 || !strings.Contains(errs, "accept with: railswitch lock --accept sha256:") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out, "changed option postgres@1") || !strings.Contains(out, "! image") {
		t.Errorf("review not printed:\n%s", out)
	}
	if readFile(t, path) != before {
		t.Error("stack.lock written")
	}
}

func TestLockPlainNoChange(t *testing.T) {
	ws, _ := lockedWS(t)
	path := filepath.Join(ws, "stack.lock")
	before := readFile(t, path)
	if out := mustRun(t, "-C", ws, "lock"); !strings.Contains(out, "wrote "+path) {
		t.Errorf("out %q", out)
	}
	if readFile(t, path) != before {
		t.Error("an empty review changed stack.lock")
	}
}

// Review Focus: accepting an earlier review after the package changed again
// is refused, and nothing is written.
func TestLockAcceptStale(t *testing.T) {
	ws, dir := localRails(t)
	allow := filepath.Join(dir, "deps.allow")
	appendFile(t, allow, "first.example\n")
	first := reviewOf(t, ws)
	appendFile(t, allow, "second.example\n")
	path := filepath.Join(ws, "stack.lock")
	before := readFile(t, path)
	code, _, errs := runIn(t, "-C", ws, "lock", "--accept", first.Hash)
	if code != 1 || !strings.Contains(errs, "the update changed since it was reviewed") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if readFile(t, path) != before {
		t.Error("stack.lock written")
	}
	if len(first.Packages) != 1 || len(first.Packages[0].Files) != 1 || len(first.Packages[0].Files[0].Flags) != 1 || first.Packages[0].Files[0].Flags[0].Line == 0 {
		t.Errorf("the edited deps.allow review %+v", first.Packages)
	}
}

func TestLockAccept(t *testing.T) {
	ws, dirs := lockedWS(t)
	pendingOption(t, ws, "postgres", oldPull)
	doc := reviewOf(t, ws)
	code, out, errs := runWith(t, unitsFake(), "-C", ws, "lock", "--accept", doc.Hash)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out, "wrote "+filepath.Join(ws, "stack.lock")) {
		t.Errorf("out:\n%s", out)
	}
	if got := readLock(t, ws).Options["postgres"].SHA256; got != doc.Packages[0].To {
		t.Errorf("postgres locked at %s, want %s", got, doc.Packages[0].To)
	}
	storeHolds(t, ws)
	unit := readFile(t, filepath.Join(dirs.Quadlet, "filial-master-db.container"))
	if !strings.Contains(unit, "docker.io/library/postgres:") || strings.Contains(unit, "oldpg") {
		t.Errorf("units not rendered with the accepted pull:\n%s", unit)
	}
	w, err := workspace.Load(t.Context(), ws, "")
	if err != nil || len(w.Pending) != 0 || w.CheckLock() != nil {
		t.Errorf("after accept: %v, Pending %v", err, w.Pending)
	}
	if len(reviewOf(t, ws).Packages) != 0 {
		t.Error("something left to review")
	}
}

// --restart restarts the running worktrees the update changes; its
// failure exits 3: written, not restarted.
func TestLockAcceptRestart(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	for _, slug := range []string{"master", "feat"} {
		if err := os.MkdirAll(filepath.Join(ws, slug, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pendingOption(t, ws, "postgres", oldPull)
	doc := reviewOf(t, ws)
	f := unitsFake()
	code, _, errs := runWith(t, f, "-C", ws, "lock", "--accept", doc.Hash, "--restart")
	if code != exitWrittenNotRestarted || !strings.Contains(errs, "restart master:") || strings.Contains(errs, "restart feat") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if readLock(t, ws).Options["postgres"].SHA256 != doc.Packages[0].To {
		t.Error("not written")
	}
	var stopped []string
	for _, c := range f.Calls {
		if len(c) > 2 && c[0] == "systemctl" && c[2] == "stop" {
			stopped = append(stopped, c[3:]...)
		}
	}
	if !slices.Contains(stopped, "filial-master-rails.service") {
		t.Errorf("stopped %v", stopped)
	}
	if code, _, _ := runIn(t, "-C", ws, "lock", "--restart"); code != 2 {
		t.Errorf("--restart without --accept: exit %d, want 2", code)
	}
}
