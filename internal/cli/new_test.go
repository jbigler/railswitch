package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

// newEnv points XDG_CONFIG_HOME and XDG_DATA_HOME into a temp dir and
// isolates git from the host's config.
func newEnv(t *testing.T) machine.Dirs {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR", "GIT_COMMITTER"} {
		t.Setenv(k+"_NAME", "Test")
		t.Setenv(k+"_EMAIL", "test@example.com")
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	return dirs
}

// bareRepo is a file:// URL of a bare repo holding one commit, whose HEAD
// is refs/heads/<head>.
func bareRepo(t *testing.T, head string) string {
	t.Helper()
	src := t.TempDir()
	gitIn(t, src, "init", "-q", "-b", head)
	writeFile(t, filepath.Join(src, "README"), "app\n")
	gitIn(t, src, "add", ".")
	gitIn(t, src, "commit", "-q", "-m", "init")
	bare := filepath.Join(t.TempDir(), "app.git")
	gitIn(t, src, "clone", "-q", "--bare", src, bare)
	return "file://" + bare
}

// asGithub makes git fetch url when asked for the github.com URL it returns.
func asGithub(t *testing.T, url string) string {
	t.Helper()
	gh := "https://github.com/acme/app.git"
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+url+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", gh)
	return gh
}

// gitHost is fakeHost with git run for real.
func gitHost() *systest.Fake {
	f := fakeHost()
	f.Passthrough = []string{"git"}
	return f
}

// mustNew runs railswitch project new and fails the test on a non-zero exit.
func mustNew(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errs := runWith(t, gitHost(), append([]string{"project", "new"}, args...)...)
	if code != 0 {
		t.Fatalf("new %v: exit %d\nstdout: %s\nstderr: %s", args, code, out, errs)
	}
	return out
}

func mode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func headerOK(t *testing.T, path string) {
	t.Helper()
	if data := readFile(t, path); !strings.HasPrefix(data, "# Ejected from ") {
		t.Errorf("%s does not start with the # Ejected from header:\n%.200s", path, data)
	}
}

// containerfiles lists ws/containerfiles.
func containerfiles(t *testing.T, ws string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(ws, "containerfiles"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

func registry(t *testing.T, dirs machine.Dirs) *machine.Registry {
	t.Helper()
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	return reg.Registry
}

var dbPasswordRe = regexp.MustCompile(`(?m)^DB_PASSWORD = "([0-9a-f]{32})"$`)

// dbPassword is the generated DB_PASSWORD in ws's mise.local.toml, which
// must be mode 0600.
func dbPassword(t *testing.T, ws string) string {
	t.Helper()
	path := filepath.Join(ws, "mise.local.toml")
	if m := mode(t, path); m != 0o600 {
		t.Errorf("mise.local.toml mode = %v; want 0600", m)
	}
	m := dbPasswordRe.FindStringSubmatch(readFile(t, path))
	if m == nil {
		t.Fatalf("mise.local.toml has no DB_PASSWORD of 32 hex:\n%s", readFile(t, path))
	}
	return m[1]
}

func TestNew(t *testing.T) {
	dirs := newEnv(t)
	repo := bareRepo(t, "main")
	ws := filepath.Join(t.TempDir(), "shop")
	out := mustNew(t, ws, "--repo", repo, "--prefix", "shop", "--branch", "main", "--agent", "none") // agents: TestNewDefaultAgent

	p, err := config.Load(filepath.Join(ws, "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, filepath.Join(ws, "project.toml"))
	if p.Schema != 1 {
		t.Errorf("schema = %d; want 1", p.Schema)
	}
	if p.Project.Name != "shop" || p.Project.Prefix != "shop" || p.Project.Repo != repo || p.Project.BaseBranch != "main" {
		t.Errorf("[project] = %+v", p.Project)
	}
	if p.Stack.Pack != "rails@1" || p.Stack.App != "rails" {
		t.Errorf("[stack] = %+v", p.Stack)
	}
	if p.Options["database"] != "postgres@1" || p.Options["browser"] != "none" {
		t.Errorf("[options] = %v", p.Options)
	}
	if p.Versions["postgres"] != "16" {
		t.Errorf("[versions] = %v; want postgres = 16 from the option's pin", p.Versions)
	}
	if p.Image.Base != "containerfiles/Containerfile.base" || !slices.Equal(p.Image.Addons, []string{"containerfiles/Containerfile.pg-client"}) {
		t.Errorf("[image] = %+v", p.Image)
	}
	if p.Tools.Nvim != nil || p.Secrets != nil || strings.Contains(raw, "[tools") || strings.Contains(raw, "[secrets") {
		t.Errorf("project.toml has tools or secrets:\n%s", raw)
	}

	if got := containerfiles(t, ws); !slices.Equal(got, []string{"Containerfile.base", "Containerfile.pg-client"}) {
		t.Errorf("containerfiles/ = %v", got)
	}
	for _, f := range []string{"Containerfile.base", "Containerfile.pg-client"} {
		headerOK(t, filepath.Join(ws, "containerfiles", f))
	}

	pw := dbPassword(t, ws)
	for _, f := range []string{"project.toml", "stack.lock"} {
		if strings.Contains(readFile(t, filepath.Join(ws, f)), pw) {
			t.Errorf("%s holds the generated password", f)
		}
	}
	if strings.Contains(out, pw) {
		t.Errorf("new printed the generated password:\n%s", out)
	}

	if m := mode(t, filepath.Join(ws, "db-dumps")); m != 0o755 {
		t.Errorf("db-dumps mode = %v; want 0755", m)
	}
	if _, err := os.Stat(filepath.Join(ws, "main", "README")); err != nil {
		t.Errorf("base worktree not cloned: %v", err)
	}

	// Git never lists an empty dir: give each never-tracked one a file.
	writeFile(t, filepath.Join(ws, "db-dumps", "dump.sql"), "-- dump\n")
	if err := os.MkdirAll(filepath.Join(ws, ".railswitch"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(ws, ".railswitch", "x"), "x\n")
	cmd := exec.Command("git", "-C", ws, "status", "--porcelain", "--untracked-files=all")
	st, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, l := range strings.Split(strings.TrimSpace(string(st)), "\n") {
		files = append(files, strings.TrimPrefix(l, "?? "))
	}
	want := []string{".gitignore", "containerfiles/Containerfile.base", "containerfiles/Containerfile.pg-client", "project.toml", "stack.lock"}
	if !slices.Equal(files, want) {
		t.Errorf("git status lists %v; want %v", files, want)
	}

	reg := registry(t, dirs)
	if pe, ok := reg.Project("shop"); !ok || pe.Workspace != ws {
		t.Errorf("registry project shop = %+v, %v; want workspace %s", pe, ok, ws)
	}
	if wts := reg.WorktreesOf("shop"); len(wts) != 1 || wts[0].Slug != "main" || wts[0].ID != 1 {
		t.Errorf("registry worktrees = %+v; want main ID 1", wts)
	}
	if _, err := os.Stat(filepath.Join(dirs.Quadlet, "shop-main-rails.container")); err != nil {
		t.Errorf("not rendered: %v", err)
	}

	code, lout, errs := runWith(t, gitHost(), "-C", filepath.Join(ws, "main"), "containerfile", "list")
	if code != 0 || strings.Contains(lout, "warning") || errs != "" {
		t.Errorf("containerfile list: exit %d, stderr %q, stdout:\n%s", code, errs, lout)
	}
}

func TestNewNvimGithub(t *testing.T) {
	newEnv(t)
	gh := asGithub(t, bareRepo(t, "main"))
	ws := filepath.Join(t.TempDir(), "shop")
	out := mustNew(t, ws, "--repo", gh, "--prefix", "shop", "--tool", "nvim", "--agent", "none")

	p := loadProject(t, ws)
	if p.Tools.Nvim == nil || !strings.Contains(readFile(t, filepath.Join(ws, "project.toml")), "\n[tools.nvim]") {
		t.Error("project.toml has no [tools.nvim] table")
	}
	sec, ok := p.Secrets["github_token"]
	if !ok || !slices.Equal(sec.Services, []string{"nvim"}) || sec.GitHost != "github.com" {
		t.Errorf("[secrets.github_token] = %+v, %v", sec, ok)
	}
	headerOK(t, filepath.Join(ws, "containerfiles", "Containerfile.nvim"))
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if last := lines[len(lines)-1]; last != "next: railswitch secret set github_token" {
		t.Errorf("last line = %q; want next: railswitch secret set github_token\n%s", last, out)
	}
}

func TestNewNvimOtherHost(t *testing.T) {
	newEnv(t)
	ws := filepath.Join(t.TempDir(), "shop")
	mustNew(t, ws, "--repo", bareRepo(t, "main"), "--prefix", "shop", "--tool", "nvim", "--agent", "none")
	if p := loadProject(t, ws); p.Secrets != nil {
		t.Errorf("a non-github repo declares secrets %v", p.Secrets)
	}
}

func TestNewPlaywright(t *testing.T) {
	newEnv(t)
	ws := filepath.Join(t.TempDir(), "shop")
	mustNew(t, ws, "--repo", bareRepo(t, "main"), "--prefix", "shop", "--option", "browser=playwright@1")
	headerOK(t, filepath.Join(ws, "containerfiles", "Containerfile.playwright"))
	p := loadProject(t, ws)
	if p.Options["browser"] != "playwright@1" {
		t.Errorf("browser = %q", p.Options["browser"])
	}
	if p.Image.Base != "containerfiles/Containerfile.base" || !slices.Equal(p.Image.Addons, []string{"containerfiles/Containerfile.pg-client"}) {
		t.Errorf("[image] = %+v; the standalone playwright build must not join the chain", p.Image)
	}
}

// C3: a pin the pack also resolves as a runtime (rails reads playwright from
// Gemfile.lock) is not written to [versions], which would beat the app's
// lockfile; a pin with no runtime is.
func TestNewKeepsRuntimePinsOut(t *testing.T) {
	newEnv(t)
	ws := filepath.Join(t.TempDir(), "shop")
	mustNew(t, ws, "--repo", bareRepo(t, "main"), "--prefix", "shop", "--option", "browser=playwright@1", "--agent", "none")
	p := loadProject(t, ws)
	if v, ok := p.Versions["playwright"]; ok {
		t.Errorf("[versions] playwright = %q; want none", v)
	}
	if p.Versions["postgres"] != "16" {
		t.Errorf("[versions] postgres = %q; want 16", p.Versions["postgres"])
	}
}

// A repo with no commits and one with a detached HEAD name no branch; the
// error says which, so the fix is clear.
func TestRemoteHeadNoBranch(t *testing.T) {
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	unborn := t.TempDir()
	git(unborn, "init", "-q", "-b", "main")
	detached := t.TempDir()
	git(detached, "init", "-q", "-b", "main")
	git(detached, "commit", "-q", "--allow-empty", "-m", "x")
	git(detached, "checkout", "-q", "--detach")
	for _, tc := range []struct{ repo, want string }{
		{unborn, "has no commits yet"},
		{detached, "has a detached HEAD"},
	} {
		_, err := remoteHead(context.Background(), sys.Exec{}, tc.repo)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "--branch") {
			t.Errorf("%s: err = %v, want %q and --branch", tc.repo, err, tc.want)
		}
	}
}

func TestNewDefaultBranch(t *testing.T) {
	newEnv(t)
	ws := filepath.Join(t.TempDir(), "shop")
	mustNew(t, ws, "--repo", bareRepo(t, "trunk"), "--prefix", "shop")
	if fi, err := os.Stat(filepath.Join(ws, "trunk")); err != nil || !fi.IsDir() {
		t.Errorf("base dir %s/trunk: %v", ws, err)
	}
	if b := loadProject(t, ws).Project.BaseBranch; b != "trunk" {
		t.Errorf("base_branch = %q; want trunk", b)
	}
}

func TestNewNoRemoteHead(t *testing.T) {
	newEnv(t)
	repo := bareRepo(t, "main")
	gitIn(t, strings.TrimPrefix(repo, "file://"), "symbolic-ref", "HEAD", "refs/heads/gone")
	ws := filepath.Join(t.TempDir(), "shop")
	code, _, errs := runWith(t, gitHost(), "project", "new", ws, "--repo", repo, "--prefix", "shop")
	if code == 0 || !strings.Contains(errs, "--branch") {
		t.Errorf("exit %d, stderr %q; want an error asking for --branch", code, errs)
	}
	if _, err := os.Stat(ws); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s was created: %v", ws, err)
	}
}

func TestNewPrefixTaken(t *testing.T) {
	newEnv(t)
	repo := bareRepo(t, "main")
	first := filepath.Join(t.TempDir(), "one")
	mustNew(t, first, "--repo", repo, "--prefix", "x1")
	second := filepath.Join(t.TempDir(), "two")
	code, _, errs := runWith(t, gitHost(), "project", "new", second, "--repo", repo, "--prefix", "x1")
	if want := `prefix "x1" is already used by ` + first; code == 0 || !strings.Contains(errs, want) {
		t.Errorf("exit %d, stderr %q; want %q", code, errs, want)
	}
	if _, err := os.Stat(second); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s was created: %v", second, err)
	}
}

func TestNewRefusesExistingProject(t *testing.T) {
	newEnv(t)
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "project.toml"), "schema = 1\n")
	code, _, errs := runWith(t, gitHost(), "project", "new", ws, "--repo", bareRepo(t, "main"), "--prefix", "shop")
	if code == 0 || !strings.Contains(errs, "project.toml") {
		t.Errorf("exit %d, stderr %q; want a refusal naming project.toml", code, errs)
	}
}

func TestNewRenderFailureUnregisters(t *testing.T) {
	dirs := newEnv(t)
	ws := filepath.Join(t.TempDir(), "shop")
	f := &systest.Fake{Passthrough: []string{"git"}} // no podman: render fails
	code, _, errs := runWith(t, f, "project", "new", ws, "--repo", bareRepo(t, "main"), "--prefix", "shop")
	if code == 0 {
		t.Fatal("new succeeded without podman")
	}
	if _, ok := registry(t, dirs).Project("shop"); ok {
		t.Error("the project stays registered after render failed")
	}
	if !strings.Contains(errs, "rm -rf "+ws) {
		t.Errorf("stderr does not say how to remove %s:\n%s", ws, errs)
	}
	if _, err := os.Stat(filepath.Join(ws, "main", "README")); err != nil {
		t.Errorf("the clone was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirs.Quadlet, machine.ProxyUnitName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the Traefik unit render never wrote exists: %v", err)
	}
}

// A render that wrote the shared Traefik unit before failing: unregistering
// rewrites it without the project's network, for the projects still there.
func TestNewRenderFailureRefreshesProxy(t *testing.T) {
	dirs := newEnv(t)
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.AddProject("other", "/ws/other", nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	only := *reg.Registry
	both := only
	both.Projects = append(slices.Clone(only.Projects), machine.ProjectEntry{Prefix: "shop", Workspace: "/ws/shop", Subnet: 1})
	reg.Close()
	unit := filepath.Join(dirs.Quadlet, machine.ProxyUnitName)
	if err := os.MkdirAll(dirs.Quadlet, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, unit, string(machine.ProxyUnits(&both)[machine.ProxyUnitName]))

	ws := filepath.Join(t.TempDir(), "shop")
	f := &systest.Fake{Passthrough: []string{"git"}} // no podman: render fails
	if code, _, _ := runWith(t, f, "project", "new", ws, "--repo", bareRepo(t, "main"), "--prefix", "shop"); code == 0 {
		t.Fatal("new succeeded without podman")
	}
	got := readFile(t, unit)
	if strings.Contains(got, "shop") {
		t.Errorf("the Traefik unit still names shop:\n%s", got)
	}
	if want := string(machine.ProxyUnits(&only)[machine.ProxyUnitName]); got != want {
		t.Errorf("Traefik unit =\n%s\nwant\n%s", got, want)
	}
}

func TestGitArg(t *testing.T) {
	for _, v := range []string{"", "--upload-pack=touch /tmp/x", "-oProxyCommand=x", "-b", "main\nx", "a\rb", "a\x00b"} {
		if gitArg("--branch", v) == nil {
			t.Errorf("gitArg(%q) accepted", v)
		}
	}
	for _, v := range []string{"main", "feat/x-1", "file:///tmp/a.git", "git@github.com:a/b.git"} {
		if err := gitArg("--branch", v); err != nil {
			t.Errorf("gitArg(%q) = %v", v, err)
		}
	}
}

func TestNewRefusesOptionLikeGitValues(t *testing.T) {
	newEnv(t)
	repo := bareRepo(t, "main")
	for _, args := range [][]string{
		{"--repo", "--upload-pack=touch /tmp/pwned", "--prefix", "shop", "--branch", "main"},
		{"--repo", repo, "--prefix", "shop", "--branch", "-main"},
		{"--repo", repo, "--prefix", "shop", "--branch", "ma\nin"},
	} {
		ws := filepath.Join(t.TempDir(), "shop")
		f := gitHost()
		code, _, errs := runWith(t, f, append([]string{"project", "new", ws}, args...)...)
		if code == 0 || !strings.Contains(errs, "not a usable git value") {
			t.Errorf("new %q: exit %d, stderr %q; want a refusal", args, code, errs)
		}
		for _, c := range f.Calls {
			if slices.Contains(c, "clone") || slices.Contains(c, "ls-remote") {
				t.Errorf("new %q ran %v", args, c)
			}
		}
	}
}

func TestInitRefusesOptionLikeGitValues(t *testing.T) {
	newEnv(t)
	ws := filepath.Join(t.TempDir(), "shop")
	mustNew(t, ws, "--repo", bareRepo(t, "main"), "--prefix", "shop")
	editProject(t, ws, `repo = "file://`, `repo = "--upload-pack=x file://`)
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	f := gitHost()
	code, _, errs := runWith(t, f, "-C", ws, "project", "init")
	if code == 0 || !strings.Contains(errs, "not a usable git value") {
		t.Errorf("init: exit %d, stderr %q; want a refusal", code, errs)
	}
	for _, c := range f.Calls {
		if slices.Contains(c, "clone") {
			t.Errorf("init ran %v", c)
		}
	}
}

func TestInitJoin(t *testing.T) {
	newEnv(t)
	gh := asGithub(t, bareRepo(t, "main"))
	ws := filepath.Join(t.TempDir(), "shop")
	mustNew(t, ws, "--repo", gh, "--prefix", "shop", "--tool", "nvim")
	oldPW := dbPassword(t, ws)

	// A teammate's clone of the workspace repo: its tracked files only.
	join := filepath.Join(t.TempDir(), "joined")
	ls, err := exec.Command("git", "-C", ws, "ls-files", "--others", "--exclude-standard").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range strings.Fields(string(ls)) {
		dst := filepath.Join(join, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, dst, readFile(t, filepath.Join(ws, rel)))
	}
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dirs, err := machine.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}

	f := gitHost()
	unset(f, "shop_github_token")
	code, out, errs := runWith(t, f, "-C", join, "project", "init")
	if code != 0 {
		t.Fatalf("init: exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(join, "main", "README")); err != nil {
		t.Errorf("init did not clone the repo into main/: %v", err)
	}
	if pw := dbPassword(t, join); pw == oldPW {
		t.Error("init reused the other workspace's DB_PASSWORD")
	} else if strings.Contains(out, pw) {
		t.Error("init printed the generated password")
	}
	if m := mode(t, filepath.Join(join, "db-dumps")); m != 0o755 {
		t.Errorf("db-dumps mode = %v; want 0755", m)
	}
	reg := registry(t, dirs)
	if pe, ok := reg.Project("shop"); !ok || pe.Workspace != join {
		t.Errorf("registry project shop = %+v, %v; want workspace %s", pe, ok, join)
	}
	if wts := reg.WorktreesOf("shop"); len(wts) != 1 || wts[0].Slug != "main" {
		t.Errorf("registry worktrees = %+v; want main", wts)
	}
	if _, err := os.Stat(filepath.Join(dirs.Quadlet, "shop-main-rails.container")); err != nil {
		t.Errorf("not rendered: %v", err)
	}
	if !strings.Contains(out, "railswitch secret set github_token\n") {
		t.Errorf("init does not list the unset secret:\n%s", out)
	}

	code, _, errs = runWith(t, gitHost(), "-C", join, "project", "init")
	if want := `prefix "shop" is already used by ` + join; code == 0 || !strings.Contains(errs, want) {
		t.Errorf("second init: exit %d, stderr %q; want %q", code, errs, want)
	}
}

// TestNewGitNonInteractive: git calls that may touch the network (ls-remote,
// clone) cannot prompt: GIT_TERMINAL_PROMPT=0, and ssh in batch mode unless
// the user's git config names an ssh, which is then kept.
func TestNewGitNonInteractive(t *testing.T) {
	for _, tc := range []struct{ config, want string }{
		{"", "GIT_SSH_COMMAND=ssh -o BatchMode=yes"},
		{"[core]\n\tsshCommand = ssh -i /keys/work\n", "GIT_SSH_COMMAND=ssh -i /keys/work"},
	} {
		newEnv(t)
		t.Setenv("GIT_SSH_COMMAND", "")
		os.Unsetenv("GIT_SSH_COMMAND")
		global := filepath.Join(t.TempDir(), "gitconfig")
		if err := os.WriteFile(global, []byte(tc.config), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GIT_CONFIG_GLOBAL", global)
		t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
		repo := bareRepo(t, "main")
		f := gitHost()
		ws := filepath.Join(t.TempDir(), "shop")
		if code, _, errs := runWith(t, f, "project", "new", ws, "--repo", repo, "--prefix", "shop"); code != 0 {
			t.Fatalf("new: exit %d: %s", code, errs)
		}
		seen := 0
		for i, c := range f.Calls {
			if !slices.Contains(c, "clone") && !slices.Contains(c, "ls-remote") {
				continue
			}
			seen++
			env := f.Cmds[i].Env
			if !slices.Contains(env, "GIT_TERMINAL_PROMPT=0") || !slices.Contains(env, tc.want) {
				t.Errorf("config %q: %v env lacks GIT_TERMINAL_PROMPT=0 or %s: %q", tc.config, c, tc.want, env)
			}
		}
		if seen == 0 {
			t.Fatal("no clone or ls-remote call")
		}
	}
}

// TestNewSkipsPodmanSubnets: a /24 another podman network uses (the old
// stack's filial_proxy) is never handed out.
func TestNewSkipsPodmanSubnets(t *testing.T) {
	dirs := newEnv(t)
	repo := bareRepo(t, "main")
	ws := filepath.Join(t.TempDir(), "shop")
	f := gitHost()
	f.On([]string{"podman", "network", "ls", "--format", "json"},
		sys.Result{Stdout: []byte(`[{"name":"filial_proxy","subnets":[{"subnet":"10.214.0.0/24","gateway":"10.214.0.1"}]}]`)}, nil)
	if code, out, errs := runWith(t, f, "project", "new", ws, "--repo", repo, "--prefix", "shop", "--branch", "main"); code != 0 {
		t.Fatalf("new: exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	if pe, _ := registry(t, dirs).Project("shop"); pe.Subnet != 1 {
		t.Errorf("shop subnet = %d; want 1", pe.Subnet)
	}
	if net := readFile(t, filepath.Join(dirs.Quadlet, "shop-proxy.network")); !strings.Contains(net, "Subnet=10.214.1.0/24") {
		t.Errorf("shop-proxy.network:\n%s", net)
	}
}

func TestNewNetworkLsFails(t *testing.T) {
	dirs := newEnv(t)
	repo := bareRepo(t, "main")
	ws := filepath.Join(t.TempDir(), "shop")
	f := gitHost()
	f.On([]string{"podman", "network", "ls"}, sys.Result{ExitCode: 125, Stderr: []byte("Error: boom\n")},
		fmt.Errorf("podman: %w (exit 125): Error: boom", sys.ErrExit))
	code, _, errs := runWith(t, f, "project", "new", ws, "--repo", repo, "--prefix", "shop", "--branch", "main")
	if code == 0 || !strings.Contains(errs, "boom") {
		t.Fatalf("new: exit %d, stderr %q; want failure naming the podman error", code, errs)
	}
	if _, ok := registry(t, dirs).Project("shop"); ok {
		t.Error("shop registered despite the failed podman network ls")
	}
}

// TestNewSetsPortsBeforeWorktree: the project's port bases are recorded
// before its base worktree gets an ID, so another program's dev server on
// 127.0.0.1:13001 makes the base worktree ID 2.
func TestNewSetsPortsBeforeWorktree(t *testing.T) {
	dirs := newEnv(t)
	listening(t, 13001)
	ws := filepath.Join(t.TempDir(), "shop")
	mustNew(t, ws, "--repo", bareRepo(t, "main"), "--prefix", "shop", "--branch", "main")
	reg := registry(t, dirs)
	if wts := reg.WorktreesOf("shop"); len(wts) != 1 || wts[0].ID != 2 {
		t.Errorf("registry worktrees = %+v; want main ID 2", wts)
	}
	if pe, _ := reg.Project("shop"); pe.Ports["app"] != 13000 {
		t.Errorf("shop ports = %v; want app 13000", pe.Ports)
	}
}

// ejectNames puts agent-base and each distinct agent package's image after
// the tools, and nothing for agents when the stack has none.
func TestEjectNamesAgents(t *testing.T) {
	p := loadProject(t, filialFixture)
	cat, err := pack.LoadCatalog(filialFixture)
	if err != nil {
		t.Fatal(err)
	}
	p.Agents["other"] = config.Agent{Use: "claude@1"} // a second agent of one package
	st, err := cat.Resolve(p)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ejectNames(st), []string{"base", "pg-client", "nvim", "agent-base", "claude", "probe", "playwright"}; !slices.Equal(got, want) {
		t.Errorf("ejectNames = %q; want %q", got, want)
	}
	st.Agents = nil
	if got, want := ejectNames(st), []string{"base", "pg-client", "nvim", "playwright"}; !slices.Equal(got, want) {
		t.Errorf("ejectNames without agents = %q; want %q", got, want)
	}
}

func TestNewDefaultAgent(t *testing.T) {
	newEnv(t)
	gh := asGithub(t, bareRepo(t, "main"))
	ws := filepath.Join(t.TempDir(), "shop")
	out := mustNew(t, ws, "--repo", gh, "--prefix", "shop")

	raw := readFile(t, filepath.Join(ws, "project.toml"))
	if !strings.Contains(raw, "[agents.claude]\nuse = \"claude@1\"\n") {
		t.Errorf("project.toml lacks [agents.claude] use = \"claude@1\":\n%s", raw)
	}
	p := loadProject(t, ws)
	if a := p.Agents["claude"]; len(p.Agents) != 1 || a.Use != "claude@1" {
		t.Errorf("[agents] = %+v", p.Agents)
	}
	if s, ok := p.Secrets["claude_token"]; !ok || !slices.Equal(s.Services, []string{"agent-claude"}) || s.Env != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Errorf("[secrets.claude_token] = %+v, %v", s, ok)
	}
	if s, ok := p.Secrets["github_token"]; !ok || !slices.Equal(s.Services, []string{"agent-claude"}) || s.GitHost != "github.com" {
		t.Errorf("[secrets.github_token] = %+v, %v", s, ok)
	}
	for _, f := range []string{"Containerfile.agent-base", "Containerfile.claude"} {
		headerOK(t, filepath.Join(ws, "containerfiles", f))
	}
	for _, n := range []string{"claude_token", "github_token"} {
		if !strings.Contains(out, "next: railswitch secret set "+n+"\n") {
			t.Errorf("next steps lack secret set %s:\n%s", n, out)
		}
	}
}

func TestNewAgentNone(t *testing.T) {
	newEnv(t)
	gh := asGithub(t, bareRepo(t, "main"))
	ws := filepath.Join(t.TempDir(), "shop")
	mustNew(t, ws, "--repo", gh, "--prefix", "shop", "--agent", "none")
	p := loadProject(t, ws)
	if len(p.Agents) != 0 || p.Secrets != nil || strings.Contains(readFile(t, filepath.Join(ws, "project.toml")), "[agents") {
		t.Errorf("--agent none: agents %v secrets %v", p.Agents, p.Secrets)
	}
	if got := containerfiles(t, ws); slices.Contains(got, "Containerfile.agent-base") {
		t.Errorf("--agent none ejected agent-base: %v", got)
	}

	ws2 := filepath.Join(t.TempDir(), "shop2")
	mustNew(t, ws2, "--repo", gh, "--prefix", "shop2", "--agent", "none", "--tool", "nvim")
	if s, ok := loadProject(t, ws2).Secrets["github_token"]; !ok || !slices.Equal(s.Services, []string{"nvim"}) {
		t.Errorf("--agent none --tool nvim: github_token %+v, %v", s, ok)
	}
	if code, _, errs := runWith(t, gitHost(), "project", "new", filepath.Join(t.TempDir(), "x"), "--repo", gh, "--prefix", "x", "--agent", "none", "--agent", "a=claude@1"); code != 2 || !strings.Contains(errs, "none") {
		t.Errorf("--agent none with another: exit %d, stderr %q; want a usage error", code, errs)
	}
}

func TestNewAgentFlag(t *testing.T) {
	newEnv(t)
	gh := asGithub(t, bareRepo(t, "main"))
	ws := filepath.Join(t.TempDir(), "shop")
	mustNew(t, ws, "--repo", gh, "--prefix", "shop", "--agent", "a=claude@1", "--agent", "b-c=claude@1")
	p := loadProject(t, ws)
	if len(p.Agents) != 2 || p.Agents["a"].Use != "claude@1" || p.Agents["b-c"].Use != "claude@1" {
		t.Errorf("[agents] = %+v", p.Agents)
	}
	cfs := containerfiles(t, ws)
	if n := len(slices.DeleteFunc(slices.Clone(cfs), func(f string) bool { return f != "Containerfile.claude" })); n != 1 {
		t.Errorf("containerfiles %v; want one Containerfile.claude", cfs)
	}
	for name, svc := range map[string]string{"a_token": "agent-a", "b_c_token": "agent-b-c"} {
		if s, ok := p.Secrets[name]; !ok || !slices.Equal(s.Services, []string{svc}) {
			t.Errorf("[secrets.%s] = %+v, %v", name, s, ok)
		}
	}
	if s := p.Secrets["github_token"]; !slices.Equal(s.Services, []string{"agent-a", "agent-b-c"}) || s.GitHost != "github.com" {
		t.Errorf("github_token services %v", s.Services)
	}
	for _, bad := range []string{"claude", "a=", "Bad=claude@1", "ls=claude@1", "a=nope@1"} {
		code, _, _ := runWith(t, gitHost(), "project", "new", filepath.Join(t.TempDir(), "x"), "--repo", gh, "--prefix", "x", "--agent", bad)
		if code == 0 {
			t.Errorf("--agent %s: exit 0", bad)
		}
	}
}

// D43: project new takes a local pack that the workspace already holds.
func TestNewLocalPack(t *testing.T) {
	newEnv(t)
	ws := filepath.Join(t.TempDir(), "shop")
	cat, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(ws, "packs", "rails"), cat.Packs[pack.Ref{Name: "rails", Version: 1}].Files); err != nil {
		t.Fatal(err)
	}
	mustNew(t, ws, "--repo", bareRepo(t, "main"), "--prefix", "shop", "--branch", "main", "--agent", "none", "--pack", "local:rails@1")
	if p := loadProject(t, ws); p.Stack.Pack != "local:rails@1" {
		t.Errorf("[stack] pack = %q", p.Stack.Pack)
	}
	l, err := config.ReadLock(filepath.Join(ws, "stack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if e := l.Packs["rails"]; e.Source != "local" {
		t.Errorf("stack.lock pack rails = %+v; want source local", e)
	}
	if got := readFile(t, filepath.Join(ws, "containerfiles", "Containerfile.base")); !strings.HasPrefix(got, "# Ejected from local:rails@1 Containerfile.base ") {
		t.Errorf("Containerfile.base header: %.120s", got)
	}
}

func TestNewLocalPackMissing(t *testing.T) {
	newEnv(t)
	ws := filepath.Join(t.TempDir(), "shop")
	code, _, errs := runWith(t, gitHost(), "project", "new", ws, "--repo", bareRepo(t, "main"), "--prefix", "shop", "--branch", "main", "--pack", "local:web@1")
	if code != 1 || !strings.Contains(errs, filepath.Join(ws, "packs", "web")+" does not exist") {
		t.Fatalf("exit %d, stderr %q; want 1 naming %s", code, errs, filepath.Join(ws, "packs", "web"))
	}
	if _, err := os.Stat(ws); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("workspace created: %v", err)
	}
}
