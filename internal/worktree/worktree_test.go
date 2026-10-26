package worktree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

const prefix = "demo"

// tenv is a workspace whose base worktree (master, ID 1) is a clone of a
// temp bare remote, with a Manager on a Fake that runs git for real.
type tenv struct {
	m       *Manager
	f       *systest.Fake
	out     *bytes.Buffer
	ws      string
	base    string
	renders int
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func setup(t *testing.T) *tenv {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR", "GIT_COMMITTER"} {
		t.Setenv(k+"_NAME", "Test")
		t.Setenv(k+"_EMAIL", "test@example.com")
	}
	seed := filepath.Join(tmp, "seed")
	git(t, tmp, "init", "-q", "-b", "master", seed)
	writeFile(t, filepath.Join(seed, "README"), "hello\n")
	git(t, seed, "add", "README")
	git(t, seed, "commit", "-q", "-m", "init")
	git(t, tmp, "clone", "-q", "--bare", seed, filepath.Join(tmp, "remote.git"))
	ws := filepath.Join(tmp, "ws")
	if err := os.Mkdir(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(ws, "master")
	git(t, ws, "clone", "-q", filepath.Join(tmp, "remote.git"), base)

	data := filepath.Join(tmp, "data")
	reg, err := machine.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.AddProject(prefix, ws, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.AddWorktree(prefix, "master", nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	reg.Close()

	proc := t.TempDir() // no listening ports
	writeFile(t, filepath.Join(proc, "net", "tcp"), "  sl  local_address rem_address   st\n")
	e := &tenv{f: &systest.Fake{Passthrough: []string{"git"}}, out: &bytes.Buffer{}, ws: ws, base: base}
	e.m = &Manager{
		R:    e.f,
		WS:   ws,
		Pfx:  prefix,
		Data: data,
		Base: "master",
		Proc: proc,
		In:   strings.NewReader(""),
		Out:  e.out,
		// Like workspace.Render: an env dir for every registered worktree.
		Render: func(context.Context) error {
			e.renders++
			for _, w := range e.registered(t) {
				if err := os.MkdirAll(filepath.Join(ws, ".railswitch", "env", w.Slug), 0o700); err != nil {
					return err
				}
			}
			return nil
		},
		Ports: func(*machine.Registry) (map[string]int, error) { return map[string]int{"app": 13000}, nil },
		Names: fakeNames,
		// Recorded as an "unshare <slug>" call, to order it among the runner's.
		Unshare: func(_ context.Context, slug string) error {
			e.f.Calls = append(e.f.Calls, []string{"unshare", slug})
			return nil
		},
	}
	return e
}

// fakeNames are the names render.Model.NamesFor gives a worktree with a
// rails and a db service.
func fakeNames(slug string) (render.Names, error) {
	var n render.Names
	for _, svc := range []string{"rails", "db"} {
		c := naming.Container(prefix, slug, svc)
		n.Containers = append(n.Containers, c)
		n.Units = append(n.Units, c+".service")
	}
	n.Units = append(n.Units, naming.DevNetwork(prefix, slug)+"-network.service")
	n.Volumes = []string{naming.WorktreeVolume(prefix, slug, "db-data")}
	n.Networks = []string{naming.DevNetwork(prefix, slug)}
	return n, nil
}

func (e *tenv) registered(t *testing.T) []machine.WorktreeEntry {
	t.Helper()
	reg, err := machine.Open(e.m.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	return reg.WorktreesOf(prefix)
}

func (e *tenv) project(t *testing.T) machine.ProjectEntry {
	t.Helper()
	reg, err := machine.Open(e.m.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	pe, _ := reg.Project(prefix)
	return pe
}

func (e *tenv) slugs(t *testing.T) []string {
	t.Helper()
	var s []string
	for _, w := range e.registered(t) {
		s = append(s, w.Slug)
	}
	return s
}

func (e *tenv) add(t *testing.T, target, name string) render.Worktree {
	t.Helper()
	w, err := e.m.Add(context.Background(), target, name)
	if err != nil {
		t.Fatalf("Add(%q, %q): %v", target, name, err)
	}
	return w
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestAddAllocatesAfterGit(t *testing.T) {
	e := setup(t)
	w := e.add(t, "feat", "")
	if w.Slug != "feat" || w.ID != 2 || w.Branch != "feat" || w.Dir != filepath.Join(e.ws, "feat") {
		t.Errorf("Add = %+v; want slug feat, ID 2, branch feat in %s", w, filepath.Join(e.ws, "feat"))
	}
	if got := e.registered(t); !slices.Contains(got, machine.WorktreeEntry{Prefix: prefix, Slug: "feat", ID: 2}) {
		t.Errorf("registry = %+v; want demo/feat ID 2", got)
	}
	if b := strings.TrimSpace(git(t, w.Dir, "branch", "--show-current")); b != "feat" {
		t.Errorf("checked out %q; want feat", b)
	}
	if e.renders != 1 {
		t.Errorf("renders = %d; want 1", e.renders)
	}
	for _, c := range e.f.Calls {
		if c[0] == "git" && !slices.Contains(c, "core.fsmonitor=false") || c[0] == "git" && !slices.Contains(c, "core.hooksPath=/dev/null") {
			t.Errorf("git call without the safety config: %q", c)
		}
	}
}

// TestAddSkipsListeningPort: an ID whose port another program listens on
// (here 127.0.0.1:13002) is never handed out, even when no render recorded
// the project's bases yet (a registry from before they were recorded): Add
// records them first.
func TestAddSkipsListeningPort(t *testing.T) {
	e := setup(t)
	if pe := e.project(t); pe.Ports != nil {
		t.Fatalf("setup recorded ports %v", pe.Ports)
	}
	writeFile(t, filepath.Join(e.m.Proc, "net", "tcp"),
		"  sl  local_address rem_address   st\n   0: 0100007F:32CA 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 4242 1\n")
	if w := e.add(t, "feat", ""); w.ID != 3 {
		t.Errorf("ID = %d; want 3 (13002 is in use)", w.ID)
	}
	if pe := e.project(t); !maps.Equal(pe.Ports, map[string]int{"app": 13000}) {
		t.Errorf("registered ports = %v; want app 13000", pe.Ports)
	}
}

// TestAddPortsConflict: bases too close to another project's refuse the
// worktree.
func TestAddPortsConflict(t *testing.T) {
	e := setup(t)
	reg, err := machine.Open(e.m.Data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.AddProject("other", "/ws/other", nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetPorts("other", map[string]int{"web": 13050}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	reg.Close()
	if _, err := e.m.Add(context.Background(), "feat", ""); err == nil || !strings.Contains(err.Error(), "project other's port web base 13050") {
		t.Errorf("Add = %v; want a port base conflict with other", err)
	}
	if slices.Contains(e.slugs(t), "feat") {
		t.Error("feat registered despite the conflict")
	}
}

func TestAddSeedFiles(t *testing.T) {
	e := setup(t)
	e.m.SeedFiles = []string{".env", "config/master.key"}
	writeFile(t, filepath.Join(e.base, ".env"), "SECRET=1\n")
	writeFile(t, filepath.Join(e.base, "real.key"), "key\n")
	if err := os.MkdirAll(filepath.Join(e.base, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(e.base, "real.key"), filepath.Join(e.base, "config", "master.key")); err != nil {
		t.Fatal(err)
	}
	w := e.add(t, "feat", "")
	fi, err := os.Stat(filepath.Join(w.Dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf(".env mode = %v; want 0600", fi.Mode().Perm())
	}
	if data, _ := os.ReadFile(filepath.Join(w.Dir, ".env")); string(data) != "SECRET=1\n" {
		t.Errorf(".env = %q", data)
	}
	if exists(filepath.Join(w.Dir, "config", "master.key")) {
		t.Error("the symlinked config/master.key was copied")
	}
	if !strings.Contains(e.out.String(), "config/master.key") {
		t.Errorf("output %q does not warn about config/master.key", e.out.String())
	}
}

func TestAddPrecreatesWorktreeVolumes(t *testing.T) {
	e := setup(t)
	e.m.WorktreeVolumes = []string{"node_modules"}
	e.add(t, "feat", "")
	if fi, err := os.Stat(filepath.Join(e.ws, "feat", "node_modules")); err != nil || !fi.IsDir() {
		t.Errorf("feat/node_modules: %v", err)
	}
}

func TestAddGitFailsNoRegistry(t *testing.T) {
	e := setup(t)
	if _, err := e.m.Add(context.Background(), "master", ""); err == nil {
		t.Fatal("Add of the branch checked out in base succeeded")
	}
	if got := e.slugs(t); !slices.Equal(got, []string{"master"}) {
		t.Errorf("registry = %v; want only master", got)
	}
	if exists(filepath.Join(e.ws, ".railswitch", "env")) {
		t.Error("an env dir exists")
	}
	if e.renders != 0 {
		t.Errorf("renders = %d; want 0", e.renders)
	}
	entries, _ := os.ReadDir(e.ws)
	if len(entries) != 1 {
		t.Errorf("workspace holds %v; want only master", entries)
	}
	// A second run succeeds.
	if w := e.add(t, "feat", ""); w.ID != 2 {
		t.Errorf("second Add ID = %d; want 2", w.ID)
	}
}

// rolledBack fails unless the failed Add of slug left nothing behind: no
// registry entry, checkout, env dir or home dir.
func (e *tenv) rolledBack(t *testing.T, slug string) {
	t.Helper()
	if got := e.slugs(t); !slices.Equal(got, []string{"master"}) {
		t.Errorf("registry = %v; want only master", got)
	}
	for _, p := range []string{slug, ".railswitch/env/" + slug, ".railswitch/home/" + slug} {
		if exists(filepath.Join(e.ws, p)) {
			t.Errorf("%s remains after the failed Add", p)
		}
	}
	if strings.Contains(git(t, e.base, "worktree", "list"), filepath.Join(e.ws, slug)) {
		t.Errorf("git still lists %s", slug)
	}
}

func hasBranch(dir, b string) bool {
	return exec.Command("git", "-C", dir, "show-ref", "--verify", "--quiet", "refs/heads/"+b).Run() == nil
}

func TestAddRenderFailsRollsBack(t *testing.T) {
	e := setup(t)
	render := e.m.Render
	fail := true
	e.m.Render = func(ctx context.Context) error {
		if err := render(ctx); err != nil {
			return err
		}
		if fail {
			// As Apply would have: the home dir, besides the env dir.
			if err := os.MkdirAll(filepath.Join(e.ws, ".railswitch", "home", "feat"), 0o700); err != nil {
				return err
			}
			fail = false
			return errors.New("render boom")
		}
		return nil
	}
	_, err := e.m.Add(context.Background(), "feat", "")
	if err == nil || !strings.Contains(err.Error(), "render boom") {
		t.Fatalf("Add = %v; want the render error", err)
	}
	e.rolledBack(t, "feat")
	if hasBranch(e.base, "feat") {
		t.Error("the branch feat that the failed Add created remains")
	}
	if e.renders != 2 {
		t.Errorf("renders = %d; want 2 (the failed one and the rollback's)", e.renders)
	}
	if w := e.add(t, "feat", ""); w.Slug != "feat" || w.ID != 2 {
		t.Errorf("second Add = %+v; want feat, ID 2", w)
	}
}

func TestAddSeedFailsRollsBack(t *testing.T) {
	e := setup(t)
	// Branch feat has a file sub where the seed file sub/x needs a dir.
	git(t, e.base, "checkout", "-q", "-b", "feat")
	writeFile(t, filepath.Join(e.base, "sub"), "file\n")
	git(t, e.base, "add", "sub")
	git(t, e.base, "commit", "-q", "-m", "sub")
	git(t, e.base, "checkout", "-q", "master")
	writeFile(t, filepath.Join(e.base, "sub", "x"), "seed\n")
	e.m.SeedFiles = []string{"sub/x"}
	_, err := e.m.Add(context.Background(), "feat", "")
	if err == nil || !strings.Contains(err.Error(), "sub/x") {
		t.Fatalf("Add = %v; want a seed error naming sub/x", err)
	}
	e.rolledBack(t, "feat")
	if !hasBranch(e.base, "feat") {
		t.Error("the existing branch feat was deleted")
	}
	if e.renders != 0 {
		t.Errorf("renders = %d; want 0: nothing was rendered, so the rollback has nothing to re-render", e.renders)
	}
	e.m.SeedFiles = nil
	if w := e.add(t, "feat", ""); w.Slug != "feat" || w.ID != 2 {
		t.Errorf("second Add = %+v; want feat, ID 2", w)
	}
}

func TestAddNameCollision(t *testing.T) {
	e := setup(t)
	e.add(t, "other", "feat")
	w := e.add(t, "feat", "")
	sum := sha256.Sum256([]byte("feat"))
	if want := "feat-" + hex.EncodeToString(sum[:])[:4]; w.Slug != want {
		t.Errorf("slug = %q; want %q", w.Slug, want)
	}
	if _, err := e.m.Add(context.Background(), "third", "feat"); err == nil {
		t.Error("--name of a taken slug succeeded")
	}
}

func TestAddPrompt(t *testing.T) {
	e := setup(t)
	e.m.TTY = true
	e.m.In = strings.NewReader("Custom Name\n")
	w := e.add(t, "feat", "")
	if w.Slug != "custom-name" {
		t.Errorf("slug = %q; want custom-name", w.Slug)
	}
	if !strings.Contains(e.out.String(), "Worktree name [feat]:") {
		t.Errorf("output %q lacks the prompt", e.out.String())
	}
}

func TestAddPR(t *testing.T) {
	e := setup(t)
	git(t, e.base, "branch", "pr-head")
	e.f.On([]string{"gh", "pr", "view", "42", "--json", "headRefName,isCrossRepository"}, sys.Result{Stdout: []byte(`{"headRefName":"pr-head","isCrossRepository":false}`)}, nil)
	w := e.add(t, "42", "")
	if w.Slug != "pr-head" || w.Branch != "pr-head" {
		t.Errorf("Add(42) = %+v; want pr-head", w)
	}
}

// TestAddPRSameRepo: a PR head on origin only becomes a tracking branch,
// not a fetched PR ref without upstream.
func TestAddPRSameRepo(t *testing.T) {
	e := setup(t)
	git(t, e.base, "push", "-q", "origin", "HEAD:refs/heads/pr-head")
	git(t, e.base, "fetch", "-q", "origin")
	e.f.On([]string{"gh", "pr", "view", "7", "--json", "headRefName,isCrossRepository"}, sys.Result{Stdout: []byte(`{"headRefName":"pr-head","isCrossRepository":false}`)}, nil)
	w := e.add(t, "7", "")
	if up := strings.TrimSpace(git(t, w.Dir, "rev-parse", "--abbrev-ref", "pr-head@{upstream}")); up != "origin/pr-head" {
		t.Errorf("upstream = %q; want origin/pr-head", up)
	}
	for _, c := range e.f.Calls {
		if slices.Contains(c, "fetch") {
			t.Errorf("fetched: %q", c)
		}
	}
}

// forkPR publishes a commit as origin's refs/pull/<n>/head, on no branch,
// like GitHub does for a PR from a fork; it returns the commit.
func (e *tenv) forkPR(t *testing.T, n string) string {
	t.Helper()
	git(t, e.base, "checkout", "-q", "-b", "fork-tmp")
	writeFile(t, filepath.Join(e.base, "FORK"), n+"\n")
	git(t, e.base, "add", "FORK")
	git(t, e.base, "commit", "-q", "-m", "fork "+n)
	sha := strings.TrimSpace(git(t, e.base, "rev-parse", "HEAD"))
	git(t, e.base, "push", "-q", "origin", "HEAD:refs/pull/"+n+"/head")
	git(t, e.base, "checkout", "-q", "master")
	git(t, e.base, "branch", "-q", "-D", "fork-tmp")
	e.f.On([]string{"gh", "pr", "view", n, "--json", "headRefName,isCrossRepository"}, sys.Result{Stdout: []byte(`{"headRefName":"master","isCrossRepository":true}`)}, nil)
	return sha
}

// TestAddPRFork: a fork's PR (head branch "master", which is also ours) is
// fetched from refs/pull/<n>/head into pr-<n>, never matched by name, with
// git unable to prompt.
func TestAddPRFork(t *testing.T) {
	e := setup(t)
	sha := e.forkPR(t, "5")
	w := e.add(t, "5", "")
	if w.Slug != "pr-5" || w.Branch != "pr-5" {
		t.Errorf("Add(5) = %+v; want pr-5", w)
	}
	if got := strings.TrimSpace(git(t, w.Dir, "rev-parse", "HEAD")); got != sha {
		t.Errorf("HEAD = %s; want the PR head %s", got, sha)
	}
	fetched := false
	for i, c := range e.f.Calls {
		if slices.Contains(c, "fetch") {
			fetched = true
			if !slices.Contains(c, "refs/pull/5/head:refs/heads/pr-5") {
				t.Errorf("fetch = %q", c)
			}
			if env := e.f.Cmds[i].Env; !slices.Contains(env, "GIT_TERMINAL_PROMPT=0") {
				t.Errorf("fetch env lacks GIT_TERMINAL_PROMPT=0: %q", env)
			}
		}
	}
	if !fetched {
		t.Error("no fetch")
	}
	// Re-adding after wt rm keeps the same pr-5 branch: it points at the head.
	if err := e.m.Remove(context.Background(), "pr-5", false); err != nil {
		t.Fatal(err)
	}
	if w := e.add(t, "5", ""); w.Branch != "pr-5" {
		t.Errorf("second Add(5) = %+v", w)
	}
}

// TestAddPRForkBranchElsewhere: a local pr-<n> that is not the PR's head is
// refused, and nothing is registered.
func TestAddPRForkBranchElsewhere(t *testing.T) {
	e := setup(t)
	e.forkPR(t, "6")
	git(t, e.base, "branch", "pr-6") // master's commit, not the PR head
	_, err := e.m.Add(context.Background(), "6", "")
	if err == nil || !strings.Contains(err.Error(), "pr-6") {
		t.Fatalf("Add(6) = %v; want a refusal naming pr-6", err)
	}
	if got := e.slugs(t); !slices.Equal(got, []string{"master"}) {
		t.Errorf("registry = %v", got)
	}
	if got := strings.TrimSpace(git(t, e.base, "rev-parse", "pr-6")); got != strings.TrimSpace(git(t, e.base, "rev-parse", "master")) {
		t.Errorf("pr-6 moved to %s", got)
	}
}

func TestRemovePrecedence(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.add(t, "pathy", "")
	e.add(t, "topic", "tw")
	e.add(t, "zz", "nm")
	for _, target := range []string{filepath.Join(e.ws, "pathy"), "topic", "nm"} {
		if err := e.m.Remove(ctx, target, false); err != nil {
			t.Fatalf("Remove(%q): %v", target, err)
		}
	}
	if got := e.slugs(t); !slices.Equal(got, []string{"master"}) {
		t.Errorf("registry = %v; want only master", got)
	}
	for _, slug := range []string{"pathy", "tw", "nm"} {
		if exists(filepath.Join(e.ws, slug)) {
			t.Errorf("%s still exists", slug)
		}
	}
}

func TestRemoveAmbiguous(t *testing.T) {
	e := setup(t)
	e.add(t, "x", "y")
	e.add(t, "other", "x")
	n := len(e.f.Calls)
	err := e.m.Remove(context.Background(), "x", false)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("Remove(x) = %v; want ambiguous", err)
	}
	if got := e.slugs(t); !slices.Equal(got, []string{"master", "y", "x"}) {
		t.Errorf("registry = %v", got)
	}
	for _, c := range e.f.Calls[n:] {
		if c[0] != "git" {
			t.Errorf("call after refusal: %q", c)
		}
	}
}

func TestRemoveBaseRefused(t *testing.T) {
	e := setup(t)
	for _, target := range []string{"master", e.base} {
		if err := e.m.Remove(context.Background(), target, true); err == nil || !strings.Contains(err.Error(), "base worktree") {
			t.Errorf("Remove(%q) = %v; want base worktree refused", target, err)
		}
	}
	if !exists(e.base) {
		t.Error("base removed")
	}
}

func TestRemoveDirtyRefused(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	w := e.add(t, "feat", "")
	writeFile(t, filepath.Join(w.Dir, "new.txt"), "wip\n")
	if err := e.m.Remove(ctx, "feat", false); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("Remove(dirty) = %v; want refused", err)
	}
	if !exists(w.Dir) || !slices.Contains(e.slugs(t), "feat") {
		t.Fatal("dirty worktree was touched")
	}
	if err := e.m.Remove(ctx, "feat", true); err != nil {
		t.Fatalf("Remove(force): %v", err)
	}
	if exists(w.Dir) || slices.Contains(e.slugs(t), "feat") {
		t.Error("forced Remove left the worktree")
	}
}

func TestRemovePartialViewAborts(t *testing.T) {
	e := setup(t)
	e.add(t, "feat", "")
	e.add(t, "other", "")
	if err := os.Rename(filepath.Join(e.ws, "other"), filepath.Join(e.ws, "moved")); err != nil {
		t.Fatal(err)
	}
	n := len(e.f.Calls)
	if err := e.m.Remove(context.Background(), "feat", false); err == nil || !strings.Contains(err.Error(), "other") {
		t.Fatalf("Remove = %v; want an error naming the missing other", err)
	}
	if calls := e.f.Calls[n:]; len(calls) != 0 {
		t.Errorf("calls after a partial view: %q", calls)
	}
	if !slices.Contains(e.slugs(t), "feat") || !exists(filepath.Join(e.ws, "feat")) {
		t.Error("feat was touched")
	}
}

// TestRemoveUnitNotLoaded: a unit systemd doesn't know (never rendered, or a
// service the stack gained since) is already stopped.
func TestRemoveUnitNotLoaded(t *testing.T) {
	e := setup(t)
	e.add(t, "feat", "")
	e.f.On([]string{"systemctl", "--user", "stop", "demo-feat-db.service"},
		sys.Result{ExitCode: 5, Stderr: []byte("Failed to stop demo-feat-db.service: Unit demo-feat-db.service not loaded.\n")},
		fmt.Errorf("systemctl: %w (exit 5)", sys.ErrExit))
	if err := e.m.Remove(context.Background(), "feat", false); err != nil {
		t.Fatalf("Remove with a not-loaded unit: %v", err)
	}
	if slices.Contains(e.slugs(t), "feat") {
		t.Error("feat is still registered")
	}
	// Any other stop failure aborts.
	e.add(t, "other", "")
	e.f.On([]string{"systemctl", "--user", "stop", "demo-other-db.service"}, sys.Result{ExitCode: 1}, fmt.Errorf("systemctl: %w (exit 1)", sys.ErrExit))
	if err := e.m.Remove(context.Background(), "other", false); err == nil {
		t.Error("Remove succeeded though a stop failed")
	}
}

func TestRemoveExactNames(t *testing.T) {
	e := setup(t)
	e.add(t, "api", "")
	e.add(t, "api-v2", "")
	n := len(e.f.Calls)
	if err := e.m.Remove(context.Background(), "api", false); err != nil {
		t.Fatal(err)
	}
	calls := e.f.Calls[n:]
	for _, c := range calls {
		for _, a := range c {
			if strings.Contains(a, "api-v2") {
				t.Errorf("call names api-v2: %q", c)
			}
			if c[0] != "git" && strings.HasPrefix(a, prefix+"-") && !strings.HasPrefix(a, prefix+"-api-") {
				t.Errorf("call names a non-api object: %q", c)
			}
		}
	}
	for _, want := range [][]string{
		{"systemctl", "--user", "stop", "demo-api-rails.service"},
		{"systemctl", "--user", "stop", "demo-api-db.service"},
		{"systemctl", "--user", "stop", "demo-api-dev-network.service"},
		{"podman", "rm", "-f", "--ignore", "demo-api-rails"},
		{"podman", "rm", "-f", "--ignore", "demo-api-db"},
		{"podman", "volume", "rm", "-f", "demo-api-db-data"},
		{"podman", "network", "rm", "-f", "demo-api-dev"},
	} {
		if !slices.ContainsFunc(calls, func(c []string) bool { return slices.Equal(c, want) }) {
			t.Errorf("no call %q in %q", want, calls)
		}
	}
	unshare := slices.IndexFunc(calls, func(c []string) bool { return slices.Equal(c, []string{"unshare", "api"}) })
	stop := slices.IndexFunc(calls, func(c []string) bool { return c[0] == "systemctl" && slices.Contains(c, "stop") })
	if unshare < 0 || unshare > stop {
		t.Errorf("the Unshare hook did not run before the units stopped: %q", calls)
	}
	if last := calls[len(calls)-1]; !slices.Equal(last, []string{"systemctl", "--user", "daemon-reload"}) {
		t.Errorf("last call = %q; want daemon-reload", last)
	}
	if exists(filepath.Join(e.ws, ".railswitch", "env", "api")) {
		t.Error("env/api remains")
	}
	for _, p := range []string{".railswitch/env/api-v2", "api-v2"} {
		if !exists(filepath.Join(e.ws, p)) {
			t.Errorf("%s was removed", p)
		}
	}
	if got := e.slugs(t); !slices.Equal(got, []string{"master", "api-v2"}) {
		t.Errorf("registry = %v; want master, api-v2", got)
	}
}

func TestNeverPrune(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.add(t, "feat", "")
	e.add(t, "gone", "")
	if err := e.m.Remove(ctx, "feat", false); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(e.ws, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Remove(ctx, "gone", true); err == nil || !strings.Contains(err.Error(), "out of view") {
		t.Errorf("Remove(gone) with its dir missing = %v; want the partial-view refusal", err)
	}
	if _, err := e.m.List(ctx); err != nil {
		t.Errorf("List with a missing worktree dir: %v", err)
	}
	for _, c := range e.f.Calls {
		if slices.ContainsFunc(c, func(a string) bool { return strings.Contains(a, "prune") }) {
			t.Errorf("prune call: %q", c)
		}
	}
}

func TestList(t *testing.T) {
	e := setup(t)
	w := e.add(t, "feat", "")
	writeFile(t, filepath.Join(w.Dir, "new.txt"), "wip\n")
	rows, err := e.m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{
		{Slug: "master", ID: 1, Branch: "master", Dir: e.base},
		{Slug: "feat", ID: 2, Branch: "feat", Dir: w.Dir, Dirty: true},
	}
	if !slices.Equal(rows, want) {
		t.Errorf("List = %+v; want %+v", rows, want)
	}
}

func TestVolumeDirs(t *testing.T) {
	pk := &pack.Pack{WorktreeVolumes: []pack.Volume{
		{Name: "node-modules", Mount: "/app/node_modules"},
		{Name: "tmp", Mount: "/app/tmp/cache"},
		{Name: "outside", Mount: "/home/appuser/x"},
	}}
	if got, want := VolumeDirs(pk), []string{"node_modules", "tmp/cache"}; !slices.Equal(got, want) {
		t.Errorf("VolumeDirs = %v; want %v", got, want)
	}
}

func TestPrecreateVolumesRefusesEscape(t *testing.T) {
	dir := t.TempDir()
	if err := PrecreateVolumes(dir, []string{"../x"}); err == nil {
		t.Error("PrecreateVolumes(../x) succeeded")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := PrecreateVolumes(dir, []string{"link/x"}); err == nil {
		t.Error("PrecreateVolumes through a symlink out of the worktree succeeded")
	}
}
