package workspace

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/lockstore"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
)

// acceptVariant makes root's stack.lock pin, for the package of kind at
// key, a stored copy of its current content changed by edit: as if the
// lock accepted that copy and the package changed since (a railswitch
// upgrade, or an edit in a text editor). It returns the copy's sha.
func acceptVariant(t *testing.T, root, kind, key string, edit func(fstest.MapFS)) string {
	t.Helper()
	path := filepath.Join(root, "stack.lock")
	l, err := config.ReadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	pl, err := plan(root, Accept{All: true})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(stackPackages(pl.st), func(sp stackPkg) bool { return sp.kind == kind && sp.key == key })
	if i < 0 {
		t.Fatalf("no %s %s in the stack", kind, key)
	}
	mem := fstest.MapFS{}
	files := stackPackages(pl.st)[i].files
	if err := fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(files, p)
		mem[p] = &fstest.MapFile{Data: b}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	edit(mem)
	sha, err := testStore(t).Put(mem)
	if err != nil {
		t.Fatal(err)
	}
	e := table(l, kind)[key]
	e.SHA256 = sha
	table(l, kind)[key] = e
	if err := l.Write(path); err != nil {
		t.Fatal(err)
	}
	return sha
}

func testStore(t *testing.T) lockstore.Store {
	t.Helper()
	dirs, err := machine.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	return store(dirs)
}

func lockOf(t *testing.T, root string) *config.Lock {
	t.Helper()
	l, err := config.ReadLock(filepath.Join(root, "stack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

var redis = pack.Ref{Name: "redis", Version: 1}

// A local package edited in a text editor after the lock renders exactly
// what it rendered before; Pending names it.
func TestLoadRunsAccepted(t *testing.T) {
	ctx := context.Background()
	w, f, _ := dashboardWS(t)
	localRails(t, w.Root)
	if err := Lock(ctx, w.Root, Accept{All: true}); err != nil {
		t.Fatal(err)
	}
	if w, _ = Load(ctx, w.Root, ""); w == nil {
		t.Fatal("no workspace")
	}
	if _, err := w.Render(ctx, f, false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(w.Root, "packs", "rails")
	appendTo(t, filepath.Join(dir, "deps.allow"), "edited.example\n")
	appendTo(t, filepath.Join(dir, "pack.toml"), "\n[unit]\nmemory = \"9g\"\n")
	w, err := Load(ctx, w.Root, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []pack.Ref{{Name: "rails", Version: 1, Local: true}}; !slices.Equal(w.Pending, want) || !w.ReviewPending() {
		t.Fatalf("Pending = %v, want %v", w.Pending, want)
	}
	d, err := w.Render(ctx, f, true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Text != "" {
		t.Errorf("the accepted pack renders differently:\n%s", d.Text)
	}
}

// A built-in changed under the lock: the stack holds the accepted copy.
func TestLoadRunsAcceptedBuiltin(t *testing.T) {
	ctx := context.Background()
	root := projectRoot(t)
	if err := Lock(ctx, root, Accept{All: true}); err != nil {
		t.Fatal(err)
	}
	acceptVariant(t, root, pack.KindOption, "redis", func(m fstest.MapFS) { m["README.md"] = &fstest.MapFile{Data: []byte("accepted\n")} })
	w, err := Load(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(w.Pending, redis) || w.CheckLock() != nil {
		t.Fatalf("Pending %v, CheckLock %v", w.Pending, w.CheckLock())
	}
	if b, err := fs.ReadFile(w.Stack.Option("cache").Option.Files, "README.md"); err != nil || string(b) != "accepted\n" {
		t.Errorf("redis README = %q, %v", b, err)
	}
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingCopy(t *testing.T) {
	ctx := context.Background()
	root := projectRoot(t)
	if err := Lock(ctx, root, Accept{All: true}); err != nil {
		t.Fatal(err)
	}
	sha := acceptVariant(t, root, pack.KindOption, "redis", addFile)
	// A copy whose bytes changed on disk is missing: never rendered.
	if err := os.WriteFile(filepath.Join(testStore(t).Dir, sha, "option.toml"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := Load(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Pending) != 0 || !w.ReviewPending() {
		t.Errorf("Pending %v, ReviewPending %v", w.Pending, w.ReviewPending())
	}
	if err := w.CheckLock(); !errors.Is(err, ErrStaleLock) || !strings.Contains(err.Error(), "redis@1: an update is waiting for review: railswitch lock --review") {
		t.Errorf("CheckLock: %v", err)
	}
}

// addFile changes a package by adding a file.
func addFile(m fstest.MapFS) { m["extra.txt"] = &fstest.MapFile{Data: []byte("x\n")} }

func TestLoadRefChange(t *testing.T) {
	ctx := context.Background()
	root := projectRoot(t)
	localRails(t, root)
	if err := Lock(ctx, root, Accept{All: true}); err != nil {
		t.Fatal(err)
	}
	toml := filepath.Join(root, "packs", "rails", "pack.toml")
	data, err := os.ReadFile(toml)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(toml, []byte(strings.Replace(string(data), "version = 1", "version = 2", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	setPack(t, root, "local:rails@2")
	w, err := Load(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Pending) != 0 {
		t.Errorf("Pending %v: another version never runs an accepted copy", w.Pending)
	}
	if err := w.CheckLock(); !errors.Is(err, ErrStaleLock) || !strings.Contains(err.Error(), "local:rails@2") || !strings.Contains(err.Error(), "railswitch lock --review") {
		t.Errorf("CheckLock: %v", err)
	}
}

// Review Focus: a write that accepts nothing leaves a waiting update's
// entry as it is; without a stored copy it refuses rather than accept it.
func TestLockKeepsPending(t *testing.T) {
	ctx := context.Background()
	root := projectRoot(t)
	if err := Lock(ctx, root, Accept{All: true}); err != nil {
		t.Fatal(err)
	}
	sha := acceptVariant(t, root, pack.KindOption, "redis", addFile)
	before := lockOf(t, root).Options["redis"]
	if err := Lock(ctx, root, Accept{}); err != nil {
		t.Fatal(err)
	}
	if got := lockOf(t, root).Options["redis"]; got != before || got.SHA256 != sha {
		t.Fatalf("redis entry %+v, want it kept: %+v", got, before)
	}
	if err := os.RemoveAll(filepath.Join(testStore(t).Dir, sha)); err != nil {
		t.Fatal(err)
	}
	if err := Lock(ctx, root, Accept{}); err == nil || !strings.Contains(err.Error(), "redis@1: an update is waiting for review") {
		t.Fatalf("Lock without the accepted copy: %v", err)
	}
	if got := lockOf(t, root).Options["redis"]; got != before {
		t.Fatalf("redis entry %+v after a refused write", got)
	}
	if err := Lock(ctx, root, Accept{All: true}); err != nil {
		t.Fatal(err)
	}
	if got := lockOf(t, root).Options["redis"]; got.SHA256 == sha {
		t.Error("Accept All kept the old entry")
	}
}

// Review Focus: accepting one package leaves another's waiting update.
func TestLockAcceptOnly(t *testing.T) {
	ctx := context.Background()
	root := projectRoot(t)
	localRails(t, root)
	if err := Lock(ctx, root, Accept{All: true}); err != nil {
		t.Fatal(err)
	}
	sha := acceptVariant(t, root, pack.KindOption, "redis", addFile)
	appendTo(t, filepath.Join(root, "packs", "rails", "deps.allow"), "edited.example\n")
	rails := pack.Ref{Name: "rails", Version: 1, Local: true}
	if err := Lock(ctx, root, Accept{Refs: []pack.Ref{rails}}); err != nil {
		t.Fatal(err)
	}
	l := lockOf(t, root)
	if want, _ := pack.ContentHash(os.DirFS(filepath.Join(root, "packs", "rails"))); l.Packs["rails"].SHA256 != want {
		t.Errorf("rails entry %+v, want the edited content %s", l.Packs["rails"], want)
	}
	if l.Options["redis"].SHA256 != sha {
		t.Errorf("redis entry %+v advanced", l.Options["redis"])
	}
	if _, err := testStore(t).Get(l.Packs["rails"].SHA256); err != nil {
		t.Errorf("the accepted rails is not stored: %v", err)
	}
}

// Every lock write stores every package it pins; IfHash refuses other bytes.
func TestLockStores(t *testing.T) {
	ctx := context.Background()
	root := projectRoot(t)
	if err := Lock(ctx, root, Accept{}); err != nil {
		t.Fatal(err)
	}
	l := lockOf(t, root)
	for _, tbl := range []map[string]config.LockEntry{l.Packs, l.Options, l.Agents} {
		for k, e := range tbl {
			if _, err := testStore(t).Get(e.SHA256); err != nil {
				t.Errorf("%s not stored: %v", k, err)
			}
		}
	}
	if err := Lock(ctx, root, Accept{All: true, IfHash: "sha256:0"}); !errors.Is(err, ErrReviewChanged) {
		t.Errorf("IfHash mismatch: %v", err)
	}
}

func TestReview(t *testing.T) {
	ctx := context.Background()
	root := projectRoot(t)
	if err := Lock(ctx, root, Accept{All: true}); err != nil {
		t.Fatal(err)
	}
	p, err := Review(ctx, root)
	if err != nil || len(p.Packages) != 0 {
		t.Fatalf("Review of an accepted lock: %+v, %v", p, err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "stack.lock")); string(p.Lock) != string(data) {
		t.Errorf("an empty review's lock bytes differ from stack.lock")
	}
	sha := acceptVariant(t, root, pack.KindOption, "redis", addFile)
	if p, err = Review(ctx, root); err != nil {
		t.Fatal(err)
	}
	if len(p.Packages) != 1 {
		t.Fatalf("changes %+v", p.Packages)
	}
	c := p.Packages[0]
	if c.Ref != redis || c.Kind != pack.KindOption || c.Status != StatusChanged || c.From != sha || c.To == sha || c.Old == nil || c.New == nil {
		t.Errorf("change %+v", c)
	}
	if err := os.RemoveAll(filepath.Join(testStore(t).Dir, sha)); err != nil {
		t.Fatal(err)
	}
	if p, err = Review(ctx, root); err != nil || p.Packages[0].Status != StatusFirst || p.Packages[0].Old != nil {
		t.Errorf("no stored copy: %+v, %v", p.Packages, err)
	}
	if err := Lock(ctx, root, Accept{All: true, IfHash: Hash(p.Lock)}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "stack.lock")); string(p.Lock) != string(data) {
		t.Errorf("the accept wrote other bytes than the review's")
	}
}
