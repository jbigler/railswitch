package cli

import (
	"errors"
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

// mustNew runs railswitch new and fails the test on a non-zero exit.
func mustNew(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errs := runWith(t, gitHost(), append([]string{"new"}, args...)...)
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
	out := mustNew(t, ws, "--repo", repo, "--prefix", "shop", "--branch", "main")

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
	if p.Tools.Nvim || p.Secrets != nil || strings.Contains(raw, "[tools]") || strings.Contains(raw, "[secrets") {
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

	if m := mode(t, filepath.Join(ws, "db-dumps")); m != 0o700 {
		t.Errorf("db-dumps mode = %v; want 0700", m)
	}
	if _, err := os.Stat(filepath.Join(ws, "main", "README")); err != nil {
		t.Errorf("base worktree not cloned: %v", err)
	}

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
	out := mustNew(t, ws, "--repo", gh, "--prefix", "shop", "--tool", "nvim")

	p := loadProject(t, ws)
	if !p.Tools.Nvim {
		t.Error("[tools] nvim is not true")
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
	mustNew(t, ws, "--repo", bareRepo(t, "main"), "--prefix", "shop", "--tool", "nvim")
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
	code, _, errs := runWith(t, gitHost(), "new", ws, "--repo", repo, "--prefix", "shop")
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
	code, _, errs := runWith(t, gitHost(), "new", second, "--repo", repo, "--prefix", "x1")
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
	code, _, errs := runWith(t, gitHost(), "new", ws, "--repo", bareRepo(t, "main"), "--prefix", "shop")
	if code == 0 || !strings.Contains(errs, "project.toml") {
		t.Errorf("exit %d, stderr %q; want a refusal naming project.toml", code, errs)
	}
}

func TestNewRenderFailureUnregisters(t *testing.T) {
	dirs := newEnv(t)
	ws := filepath.Join(t.TempDir(), "shop")
	f := &systest.Fake{Passthrough: []string{"git"}} // no podman: render fails
	code, _, errs := runWith(t, f, "new", ws, "--repo", bareRepo(t, "main"), "--prefix", "shop")
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
	code, out, errs := runWith(t, f, "-C", join, "init")
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
	if m := mode(t, filepath.Join(join, "db-dumps")); m != 0o700 {
		t.Errorf("db-dumps mode = %v; want 0700", m)
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

	code, _, errs = runWith(t, gitHost(), "-C", join, "init")
	if want := `prefix "shop" is already used by ` + join; code == 0 || !strings.Contains(errs, want) {
		t.Errorf("second init: exit %d, stderr %q; want %q", code, errs, want)
	}
}
