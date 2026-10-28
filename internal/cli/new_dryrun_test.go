package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

// dryRun runs project new --dry-run --json and decodes its document.
func dryRun(t *testing.T, f *systest.Fake, args ...string) newDryRunDoc {
	t.Helper()
	code, out, errs := runWith(t, f, append([]string{"--json", "project", "new", "--dry-run"}, args...)...)
	if code != 0 {
		t.Fatalf("new --dry-run %v: exit %d\nstdout: %s\nstderr: %s", args, code, out, errs)
	}
	if strings.Contains(out, "null") {
		t.Errorf("a JSON slice is null:\n%s", out)
	}
	var doc newDryRunDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	return doc
}

// dirFiles maps every file under dir to its bytes; a missing dir has none.
func dirFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		out[p] = string(data)
		return err
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return out
}

func TestNewDryRunWritesNothing(t *testing.T) {
	dirs := newEnv(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	repo := bareRepo(t, "main")
	ws := filepath.Join(t.TempDir(), "shop")
	f := gitHost()
	doc := dryRun(t, f, ws, "--repo", repo, "--prefix", "shop") // no --branch: ls-remote reads it

	if !strings.Contains(doc.ProjectTOML, "base_branch = \"main\"\n") {
		t.Errorf("project_toml lacks the remote's branch:\n%s", doc.ProjectTOML)
	}
	if _, err := os.Lstat(ws); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s exists after a dry run: %v", ws, err)
	}
	if got := dirFiles(t, dirs.Data); len(got) != 0 {
		t.Errorf("the dry run wrote machine data: %v", slices.Sorted(maps.Keys(got)))
	}
	if got := dirFiles(t, dirs.Quadlet); len(got) != 0 {
		t.Errorf("the dry run wrote units: %d files", len(got))
	}
	if ents, _ := os.ReadDir(tmp); len(ents) != 0 {
		t.Errorf("the dry run left %d entries in TMPDIR", len(ents))
	}
	for _, c := range f.Calls {
		if c[0] != "git" || slices.Contains(c, "clone") || slices.Contains(c, "init") {
			t.Errorf("dry run called %q", c)
		}
	}

	// With a project registered: registry and units stay byte for byte.
	mustNew(t, filepath.Join(t.TempDir(), "one"), "--repo", repo, "--prefix", "one", "--agent", "none")
	data, units := dirFiles(t, dirs.Data), dirFiles(t, dirs.Quadlet)
	dryRun(t, gitHost(), ws, "--repo", repo, "--prefix", "shop", "--branch", "main")
	if got := dirFiles(t, dirs.Data); !maps.Equal(got, data) {
		t.Error("the dry run changed machine data")
	}
	if got := dirFiles(t, dirs.Quadlet); !maps.Equal(got, units) {
		t.Error("the dry run changed units")
	}
}

func TestNewDryRunTOMLMatchesReal(t *testing.T) {
	for _, flags := range [][]string{
		{"--branch", "main", "--agent", "none"},
		// secrets (agent, github_token for agent and nvim), addons, pins
		{"--option", "cache=redis@1", "--option", "browser=playwright@1", "--tool", "nvim"},
		// a workspace-local pack: the catalog comes from <dir>/packs/
		{"--branch", "main", "--pack", "local:rails@1"},
	} {
		newEnv(t)
		gh := asGithub(t, bareRepo(t, "main"))
		ws := filepath.Join(t.TempDir(), "shop")
		if slices.Contains(flags, "local:rails@1") {
			cat, err := pack.LoadBuiltins()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.CopyFS(filepath.Join(ws, "packs", "rails"), cat.Packs[pack.Ref{Name: "rails", Version: 1}].Files); err != nil {
				t.Fatal(err)
			}
		}
		args := append([]string{ws, "--repo", gh, "--prefix", "shop"}, flags...)
		doc := dryRun(t, gitHost(), args...)
		mustNew(t, args...)
		if real := readFile(t, filepath.Join(ws, "project.toml")); doc.ProjectTOML != real {
			t.Errorf("%v: project_toml differs from the real run's project.toml\ndry:\n%s\nreal:\n%s", flags, doc.ProjectTOML, real)
		}
		if want := fmt.Sprintf("Eject %d Containerfiles", len(containerfiles(t, ws))); doc.Steps[2] != want {
			t.Errorf("%v: steps[2] = %q; the real run ejected: %q", flags, doc.Steps[2], want)
		}
	}
}

func TestNewDryRunRefusals(t *testing.T) {
	newEnv(t)
	repo := bareRepo(t, "main")
	first := filepath.Join(t.TempDir(), "one")
	mustNew(t, first, "--repo", repo, "--prefix", "x1", "--agent", "none")
	existing := t.TempDir()
	writeFile(t, filepath.Join(existing, "project.toml"), "schema = 1\n")
	for _, tc := range []struct{ name, dir, prefix, want string }{
		{"prefix in use", filepath.Join(t.TempDir(), "two"), "x1", `prefix "x1" is already used by ` + first},
		{"existing project.toml", existing, "x2", "already exists"},
	} {
		args := []string{"project", "new", tc.dir, "--repo", repo, "--prefix", tc.prefix}
		code, out, dry := runWith(t, gitHost(), append([]string{"--json"}, append(args, "--dry-run")...)...)
		if code != 1 || out != "" || !strings.Contains(dry, tc.want) {
			t.Errorf("%s: dry run exit %d, stdout %q, stderr %q; want exit 1 with %q", tc.name, code, out, dry, tc.want)
		}
		code, _, real := runWith(t, gitHost(), args...)
		if code != 1 || real != dry {
			t.Errorf("%s: real run exit %d, stderr %q; want exit 1 with the dry run's %q", tc.name, code, real, dry)
		}
	}
}

func TestNewDryRunSummary(t *testing.T) {
	newEnv(t)
	gh := asGithub(t, bareRepo(t, "main"))
	ws := filepath.Join(t.TempDir(), "shop")
	doc := dryRun(t, gitHost(), ws, "--repo", gh, "--prefix", "shop", "--branch", "main",
		"--option", "cache=redis@1", "--option", "storage=rustfs@1", "--option", "browser=playwright@1", "--tool", "nvim")

	w := doc.Worktree
	if w.Slug != "main" {
		t.Errorf("slug = %q", w.Slug)
	}
	if !slices.Contains(w.Routes, dryRoute{Host: "main.shop.localhost", Service: "rails"}) {
		t.Errorf("routes = %+v; want main.shop.localhost to rails", w.Routes)
	}
	for _, c := range []dryContainer{
		{"shop-main-rails", "app"}, {"shop-main-deps", "deps"}, {"shop-main-db", "data"}, {"shop-main-cache", "data"}, {"shop-main-storage", "data"},
		{"shop-main-browser", "tool"}, {"shop-main-nvim", "tool"}, {"shop-main-agent-claude", "agent"},
	} {
		if !slices.Contains(w.Containers, c) {
			t.Errorf("containers = %+v; want %+v", w.Containers, c)
		}
	}
	for _, n := range []string{"DATABASE_URL", "REDIS_URL"} {
		if !slices.Contains(w.Env, n) {
			t.Errorf("env = %v; want %s", w.Env, n)
		}
	}
	if !slices.IsSorted(w.Env) {
		t.Errorf("env is not sorted: %v", w.Env)
	}
	for _, n := range w.Env {
		if strings.ContainsAny(n, "=:/") {
			t.Errorf("env entry %q is not a bare name", n)
		}
	}
	if len(doc.Steps) != 4 || !strings.HasPrefix(doc.Steps[0], "Clone "+gh+" (main) into "+filepath.Join(ws, "main")) {
		t.Errorf("steps = %q", doc.Steps)
	}

	up := dryRun(t, gitHost(), ws, "--repo", gh, "--prefix", "shop", "--branch", "main", "--up")
	if len(up.Steps) != 5 || up.Steps[4] != "Start main (up)" {
		t.Errorf("--up steps = %q", up.Steps)
	}
}

func TestNewDryRunText(t *testing.T) {
	newEnv(t)
	ws := filepath.Join(t.TempDir(), "shop")
	code, out, errs := runWith(t, gitHost(), "project", "new", "--dry-run", ws, "--repo", bareRepo(t, "main"), "--prefix", "shop", "--agent", "none")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"[project]\n", "route main.shop.localhost -> rails", "container shop-main-rails (app)", "1. Clone "} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Lstat(ws); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s exists after a dry run", ws)
	}
}

func TestNewUp(t *testing.T) {
	newEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)
	old := traefikAPI
	t.Cleanup(func() { traefikAPI = old })
	traefikAPI = srv.URL
	start := []string{"systemctl", "--user", "start", "shop-main-rails.service"}
	started := func(f *systest.Fake) bool {
		return slices.ContainsFunc(f.Calls, func(c []string) bool { return slices.Equal(c, start) })
	}

	f := gitHost()
	f.On([]string{"podman", "image", "inspect", "--format", "{{.Digest}}"}, sys.Result{Stdout: []byte("sha256:aa\n")}, nil)
	ws := filepath.Join(t.TempDir(), "shop")
	code, out, errs := runWith(t, f, "project", "new", ws, "--repo", bareRepo(t, "main"), "--prefix", "shop", "--agent", "none", "--up")
	if code != 0 {
		t.Fatalf("new --up: exit %d\n%s%s", code, out, errs)
	}
	if !started(f) || !strings.Contains(out, "created workspace") || !strings.Contains(out, "http://main.shop.localhost") {
		t.Errorf("started %v, stdout:\n%s", started(f), out)
	}

	// A created project whose up fails exits 3 and says it was created.
	f = gitHost()
	f.On([]string{"podman", "image", "inspect", "--format", "{{.Digest}}"}, sys.Result{Stdout: []byte("sha256:aa\n")}, nil)
	f.On([]string{"systemctl", "--user", "start"}, sys.Result{ExitCode: 1}, fmt.Errorf("systemctl: %w (exit 1)", sys.ErrExit))
	ws = filepath.Join(t.TempDir(), "cart")
	code, out, errs = runWith(t, f, "project", "new", ws, "--repo", bareRepo(t, "main"), "--prefix", "cart", "--agent", "none", "--up")
	if code != 3 || !strings.Contains(out, "created workspace") || !strings.Contains(errs, "project cart was created in "+ws+", but up failed") {
		t.Errorf("new --up with a failing up: exit %d\n%s%s; want exit 3 and the project named as created", code, out, errs)
	}

	// A create whose render fails (no podman) starts nothing.
	failing := &systest.Fake{Passthrough: []string{"git"}}
	code, _, _ = runWith(t, failing, "project", "new", filepath.Join(t.TempDir(), "two"), "--repo", bareRepo(t, "main"), "--prefix", "two", "--agent", "none", "--up")
	if code == 0 {
		t.Fatal("new succeeded without podman")
	}
	if slices.ContainsFunc(failing.Calls, func(c []string) bool { return c[0] == "systemctl" && slices.Contains(c, "start") }) {
		t.Error("up ran after a failed create")
	}
}
