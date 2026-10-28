package workspace

import (
	"context"
	"io/fs"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/pack"
)

var nvimRef = pack.Ref{Name: "nvim"}

// D107: the nvim tool and the agent base image are locked like packages.
func TestLockTools(t *testing.T) {
	root := projectRoot(t)
	if err := Lock(context.Background(), root, Accept{All: true}); err != nil {
		t.Fatal(err)
	}
	cat, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	l := lockOf(t, root)
	for _, c := range []struct {
		name  string
		got   map[string]string
		files fs.FS
	}{
		{"tool.nvim", map[string]string{"sha": l.Tools["nvim"].SHA256, "version": l.Tools["nvim"].Version}, cat.Tools["nvim"].Files},
		{"base.agent", map[string]string{"sha": l.Base["agent"].SHA256, "version": l.Base["agent"].Version}, cat.AgentBase},
	} {
		sum, err := pack.ContentHash(c.files)
		if err != nil {
			t.Fatal(err)
		}
		if c.got["sha"] != sum || c.got["version"] != "" {
			t.Errorf("%s = %v, want sha %s and no version", c.name, c.got, sum)
		}
		if _, err := testStore(t).Get(sum); err != nil {
			t.Errorf("%s not stored: %v", c.name, err)
		}
	}
	if err := checkLock(t, root); err != nil {
		t.Fatalf("CheckLock = %v", err)
	}
	// A lock from before 7c (no tool or base entries) is out of date.
	delete(l.Tools, "nvim")
	if err := l.Write(root + "/stack.lock"); err != nil {
		t.Fatal(err)
	}
	if err := checkLock(t, root); err == nil {
		t.Fatal("CheckLock accepts a lock without tool.nvim")
	}
}

// A built-in nvim tool changed under the lock (a railswitch upgrade):
// pending review, and the project runs the accepted copy.
func TestToolPending(t *testing.T) {
	ctx := context.Background()
	root := projectRoot(t)
	if err := Lock(ctx, root, Accept{All: true}); err != nil {
		t.Fatal(err)
	}
	accepted := []byte("FROM accepted\n")
	acceptVariant(t, root, pack.KindTool, "nvim", func(m fstest.MapFS) { m["Containerfile.nvim"] = &fstest.MapFile{Data: accepted} })
	w, err := Load(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(w.Pending, nvimRef) || w.CheckLock() != nil {
		t.Fatalf("Pending %v, CheckLock %v", w.Pending, w.CheckLock())
	}
	if b, err := fs.ReadFile(w.Stack.Tools[0].Files, "Containerfile.nvim"); err != nil || string(b) != string(accepted) {
		t.Errorf("nvim Containerfile = %q, %v; want the accepted copy", b, err)
	}
	pl, err := Review(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(pl.Packages, func(c Change) bool {
		return c.Kind == pack.KindTool && c.Ref == nvimRef && c.Status == StatusChanged
	}) {
		t.Errorf("review = %+v", pl.Packages)
	}
	// A write that accepts nothing keeps the accepted entry.
	sha := lockOf(t, root).Tools["nvim"].SHA256
	if err := Lock(ctx, root, Accept{}); err != nil {
		t.Fatal(err)
	}
	if got := lockOf(t, root).Tools["nvim"].SHA256; got != sha {
		t.Errorf("tool.nvim = %s, want the accepted %s", got, sha)
	}
}

func TestBasePending(t *testing.T) {
	ctx := context.Background()
	root := projectRoot(t)
	if err := Lock(ctx, root, Accept{All: true}); err != nil {
		t.Fatal(err)
	}
	accepted := []byte("FROM accepted-base\n")
	acceptVariant(t, root, pack.KindBase, "agent", func(m fstest.MapFS) { m[pack.AgentBaseFile] = &fstest.MapFile{Data: accepted} })
	w, err := Load(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(w.Pending, pack.AgentBaseRef) || w.CheckLock() != nil {
		t.Fatalf("Pending %v, CheckLock %v", w.Pending, w.CheckLock())
	}
	if b, err := fs.ReadFile(w.Stack.AgentBase, pack.AgentBaseFile); err != nil || string(b) != string(accepted) {
		t.Errorf("agent base = %q, %v; want the accepted copy", b, err)
	}
}
