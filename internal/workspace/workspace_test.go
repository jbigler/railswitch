package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

const fixtureDir = "../../testdata/fixtures/filial"

// projectRoot is a temp workspace holding only the fixture's project.toml,
// its local agent packages (agents/) and its worktree dirs, with XDG dirs
// pointing into the temp dir.
func projectRoot(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	root := filepath.Join(tmp, "filial")
	data, err := os.ReadFile(filepath.Join(fixtureDir, "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"master/config", "feat"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "project.toml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(root, "agents"), os.DirFS(filepath.Join(fixtureDir, "agents"))); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLockWithoutExistingLock(t *testing.T) {
	root := projectRoot(t)
	if err := Lock(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	l, err := config.ReadLock(filepath.Join(root, "stack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	cat, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	sum, err := pack.ContentHash(cat.Packs[pack.Ref{Name: "rails", Version: 1}].Files)
	if err != nil {
		t.Fatal(err)
	}
	want := config.LockEntry{Version: "1", Source: "built in (railswitch dev)", SHA256: sum}
	if got := l.Packs["rails"]; got != want {
		t.Fatalf("pack rails = %+v, want %+v", got, want)
	}
	for _, o := range []string{"postgres", "redis", "rustfs", "playwright"} {
		if e, ok := l.Options[o]; !ok || e.Version != "1" || len(e.SHA256) != 64 {
			t.Errorf("option %s = %+v (present %v)", o, e, ok)
		}
	}
	if len(l.Options) != 4 {
		t.Errorf("options = %v", l.Options)
	}
}

func TestLockKeepsImageDigests(t *testing.T) {
	root := projectRoot(t)
	path := filepath.Join(root, "stack.lock")
	old := &config.Lock{
		Packs:  map[string]config.LockEntry{"old": {Version: "9"}},
		Images: []config.LockImage{{Ref: "docker.io/library/postgres:16", Digest: "sha256:aa"}},
	}
	old.SetBaseDigest("docker.io/library/ruby:3", "sha256:bb")
	if err := old.Write(path); err != nil {
		t.Fatal(err)
	}
	if err := Lock(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	l, err := config.ReadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if l.Digest("docker.io/library/postgres:16") != "sha256:aa" || l.BaseDigest("docker.io/library/ruby:3") != "sha256:bb" {
		t.Fatalf("digests lost: %+v", l)
	}
	if _, ok := l.Packs["old"]; ok {
		t.Fatal("stale pack entry kept")
	}
}

func TestLoadFindsRootAndSlug(t *testing.T) {
	root := projectRoot(t)
	ctx := context.Background()
	if err := Lock(ctx, root); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ start, slug, want string }{
		{root, "", ""},
		{filepath.Join(root, "master", "config"), "", "master"},
		{filepath.Join(root, "master"), "feat", "feat"},
	} {
		w, err := Load(ctx, c.start, c.slug)
		if err != nil {
			t.Fatalf("Load(%s, %q): %v", c.start, c.slug, err)
		}
		if w.Root != root || w.Slug != c.want {
			t.Errorf("Load(%s, %q): root=%s slug=%q, want %s %q", c.start, c.slug, w.Root, w.Slug, root, c.want)
		}
		if w.Project == nil || w.Stack == nil || w.Lock == nil || w.Catalog == nil || w.Dirs.Quadlet == "" {
			t.Errorf("Load(%s): incomplete workspace %+v", c.start, w)
		}
	}
	if _, err := Load(ctx, root, "Not A Slug"); err == nil {
		t.Error("Load accepted an invalid -w slug")
	}
	if _, err := Load(ctx, t.TempDir(), ""); err == nil || !strings.Contains(err.Error(), "project.toml") {
		t.Errorf("Load outside a workspace: %v", err)
	}
}

func TestLoadNoLock(t *testing.T) {
	root := projectRoot(t)
	_, err := Load(context.Background(), root, "")
	if !errors.Is(err, config.ErrNoLock) || !strings.Contains(err.Error(), "railswitch lock") {
		t.Fatalf("err = %v", err)
	}
}

// TestStaleLock: Load accepts a stale lock (teardown and doctor still run);
// CheckLock and Render refuse it.
func TestStaleLock(t *testing.T) {
	root := projectRoot(t)
	ctx := context.Background()
	if err := Lock(ctx, root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "stack.lock")
	l, err := config.ReadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	e := l.Options["redis"]
	e.SHA256 = strings.Repeat("0", 64)
	l.Options["redis"] = e
	if err := l.Write(path); err != nil {
		t.Fatal(err)
	}
	w, err := Load(ctx, root, "")
	if err != nil {
		t.Fatalf("Load on a stale lock: %v", err)
	}
	if err := w.CheckLock(); err == nil || !strings.Contains(err.Error(), "stack.lock is out of date for redis@1: run railswitch lock") {
		t.Errorf("CheckLock: %v", err)
	}
	if _, err := w.Render(ctx, &systest.Fake{}, true); err == nil || !strings.Contains(err.Error(), "run railswitch lock") {
		t.Errorf("Render: %v", err)
	}
	if err := Lock(ctx, root); err != nil {
		t.Fatal(err)
	}
	if w, err = Load(ctx, root, ""); err != nil || w.CheckLock() != nil {
		t.Errorf("after re-lock: %v", err)
	}
}

func TestBranchOf(t *testing.T) {
	tmp := t.TempDir()
	base := filepath.Join(tmp, "master")
	write := func(path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(base, ".git", "HEAD"), "ref: refs/heads/master\n")
	write(filepath.Join(base, ".git", "worktrees", "feat", "HEAD"), "ref: refs/heads/feature/x\n")
	write(filepath.Join(tmp, "feat", ".git"), "gitdir: "+filepath.Join(base, ".git", "worktrees", "feat")+"\n")
	write(filepath.Join(base, ".git", "worktrees", "det", "HEAD"), "0123456789abcdef\n")
	write(filepath.Join(tmp, "det", ".git"), "gitdir: ../master/.git/worktrees/det\n")
	for dir, want := range map[string]string{"master": "master", "feat": "feature/x", "det": "", "none": ""} {
		if got := branchOf(filepath.Join(tmp, dir)); got != want {
			t.Errorf("branchOf(%s) = %q, want %q", dir, got, want)
		}
	}
}

func TestLockAgents(t *testing.T) {
	builtin := &pack.Agent{Name: "a", Version: 1, Files: fstest.MapFS{"agent.toml": {Data: []byte("a")}}}
	local := &pack.Agent{Name: "probe", Version: 1, Files: fstest.MapFS{"agent.toml": {Data: []byte("p")}}}
	cat, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	st := &pack.Stack{Pack: cat.Packs[pack.Ref{Name: "rails", Version: 1}], Agents: []*pack.ChosenAgent{
		{Name: "a", Agent: builtin, Ref: pack.Ref{Name: "a", Version: 1}},
		{Name: "b", Agent: builtin, Ref: pack.Ref{Name: "a", Version: 1}},
		{Name: "probe", Agent: local, Ref: pack.Ref{Name: "probe", Version: 1, Local: true}},
	}}
	l, err := lockEntries(st)
	if err != nil {
		t.Fatal(err)
	}
	if e := l.Agents["a@1"]; e.Version != "1" || e.Source != "built in (railswitch dev)" || len(e.SHA256) != 64 {
		t.Errorf(`agent."a@1" = %+v`, e)
	}
	if e := l.Agents["local:probe@1"]; e.Version != "1" || e.Source != "local" || len(e.SHA256) != 64 {
		t.Errorf(`agent."local:probe@1" = %+v`, e)
	}
	if len(l.Agents) != 2 {
		t.Errorf("Agents = %v", l.Agents)
	}

	root := projectRoot(t)
	ctx := context.Background()
	if err := Lock(ctx, root); err != nil {
		t.Fatal(err)
	}
	w, err := Load(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.CheckLock(); err != nil {
		t.Fatalf("CheckLock after lock: %v", err)
	}
	toml := filepath.Join(root, "agents", "probe", "agent.toml")
	if err := os.WriteFile(toml, []byte("name = \"probe\"\nversion = 1\nallow = [\"x.example\"]\n[image]\nfile = \"Containerfile.probe\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w, err = Load(ctx, root, ""); err != nil {
		t.Fatal(err)
	}
	err = w.CheckLock()
	if !errors.Is(err, ErrStaleLock) || !strings.Contains(err.Error(), "for local:probe@1:") {
		t.Errorf("CheckLock after edit: %v", err)
	}
}
