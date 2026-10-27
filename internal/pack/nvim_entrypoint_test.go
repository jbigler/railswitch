package pack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const nvimEntrypoint = "builtin/tools/nvim/entrypoints/entrypoint-nvim.sh"

// bareConfigRepo is a bare git repo with one commit holding init.lua.
func bareConfigRepo(t *testing.T, name string) string {
	t.Helper()
	src, bare := t.TempDir(), filepath.Join(t.TempDir(), name+".git")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@x", "-c", "init.defaultBranch=main"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(src, "init", "-q")
	if err := os.WriteFile(filepath.Join(src, "init.lua"), []byte("-- "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(src, "add", "init.lua")
	git(src, "commit", "-q", "-m", "init")
	git(src, "clone", "-q", "--bare", src, bare)
	return bare
}

// runNvimEntrypoint runs the entrypoint in home with repo as
// NVIM_CONFIG_REPO and `echo editor` as the server command.
func runNvimEntrypoint(t *testing.T, home, repo string) string {
	t.Helper()
	cmd := exec.Command("sh", nvimEntrypoint, "echo", "editor")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "NVIM_CONFIG_REPO=" + repo, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("entrypoint: %v\n%s", err, out)
	}
	if !strings.HasSuffix(string(out), "editor\n") {
		t.Fatalf("entrypoint did not run the editor:\n%s", out)
	}
	return string(out)
}

// D54: an empty config volume is cloned from NVIM_CONFIG_REPO; a clone from
// another repo is left alone, with the reset command.
func TestEntrypointNvimClone(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(cfg, 0o755); err != nil { // the empty volume's mount point
		t.Fatal(err)
	}
	repo := bareConfigRepo(t, "first")
	runNvimEntrypoint(t, home, repo)
	data, err := os.ReadFile(filepath.Join(cfg, "init.lua"))
	if err != nil || string(data) != "-- first\n" {
		t.Fatalf("init.lua = %q, %v; want the clone", data, err)
	}

	other := bareConfigRepo(t, "second")
	out := runNvimEntrypoint(t, home, other)
	if !strings.Contains(out, "railswitch nvim reset-config") || !strings.Contains(out, repo) || !strings.Contains(out, other) {
		t.Errorf("output without the reset command and both repos:\n%s", out)
	}
	if data, err := os.ReadFile(filepath.Join(cfg, "init.lua")); err != nil || string(data) != "-- first\n" {
		t.Errorf("init.lua = %q, %v after a different repo; want it unchanged", data, err)
	}
}

// A failed clone is reported, and the editor still starts.
func TestEntrypointNvimCloneFailure(t *testing.T) {
	home := t.TempDir()
	out := runNvimEntrypoint(t, home, filepath.Join(t.TempDir(), "missing.git"))
	if !strings.Contains(out, "missing.git") {
		t.Errorf("no clone error:\n%s", out)
	}
}
