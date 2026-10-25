package worktree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
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
	if _, err := reg.AddProject(prefix, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.AddWorktree(prefix, "master"); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	reg.Close()

	e := &tenv{f: &systest.Fake{Passthrough: []string{"git"}}, out: &bytes.Buffer{}, ws: ws, base: base}
	e.m = &Manager{
		R:    e.f,
		WS:   ws,
		Pfx:  prefix,
		Data: data,
		Base: "master",
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
		Names: fakeNames,
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
	e.f.On([]string{"gh", "pr", "view", "42", "--json", "headRefName", "-q", ".headRefName"}, sys.Result{Stdout: []byte("pr-head\n")}, nil)
	w := e.add(t, "42", "")
	if w.Slug != "pr-head" || w.Branch != "pr-head" {
		t.Errorf("Add(42) = %+v; want pr-head", w)
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
		{"systemctl", "--user", "stop", "demo-api-rails.service", "demo-api-db.service", "demo-api-dev-network.service"},
		{"podman", "rm", "-f", "--ignore", "demo-api-rails"},
		{"podman", "rm", "-f", "--ignore", "demo-api-db"},
		{"podman", "volume", "rm", "-f", "demo-api-db-data"},
		{"podman", "network", "rm", "-f", "demo-api-dev"},
	} {
		if !slices.ContainsFunc(calls, func(c []string) bool { return slices.Equal(c, want) }) {
			t.Errorf("no call %q in %q", want, calls)
		}
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
	_ = e.m.Remove(ctx, "gone", true)
	if _, err := e.m.List(ctx); err != nil {
		t.Logf("List: %v", err)
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
