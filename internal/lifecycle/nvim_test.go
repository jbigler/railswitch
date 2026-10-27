package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

const (
	nvimUnit = "filial-feat-nvim.service"
	nvimRepo = "https://github.com/[ORG]/nvim-config.git"
)

var nvimGit = []string{"podman", "exec", "filial-feat-nvim", "git", "-C", "/home/appuser/.config/nvim"}

func gitArgv(args ...string) []string { return append(slices.Clone(nvimGit), args...) }

func findNvim(name string) (string, error) {
	if name == "nvim" {
		return "/usr/bin/nvim", nil
	}
	return "", errors.New("not found")
}

func noNvim(string) (string, error) { return "", errors.New("not found") }

// hooks writes executable nvim-attach and nvim-detach under a temp config
// dir and returns the dir.
func hooks(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"nvim-attach", "nvim-detach"} {
		if err := os.WriteFile(filepath.Join(dir, "hooks", h), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// C4: the attach hook, the host's nvim on the loopback port, the detach hook.
func TestNvimAttachArgv(t *testing.T) {
	e := setup(t)
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=active\n", nvimUnit)
	cfg := hooks(t)
	e.l.ConfigDir, e.l.LookPath = cfg, findNvim
	if err := e.l.Nvim(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	attach, detach := filepath.Join(cfg, "hooks", "nvim-attach"), filepath.Join(cfg, "hooks", "nvim-detach")
	var got []string
	for _, c := range e.f.Cmds {
		if c.Name == "systemctl" || c.Name == "git" {
			continue
		}
		got = append(got, joined(append([]string{c.Name}, c.Args...)))
		switch c.Name {
		case attach, detach:
			if !slices.Contains(c.Env, "RAILSWITCH_NVIM_SERVER=127.0.0.1:17002") || c.Timeout != 30*time.Second {
				t.Errorf("%s: env has no server or timeout %v", c.Name, c.Timeout)
			}
		case "nvim":
			if c.Timeout != sys.NoTimeout {
				t.Errorf("nvim timeout %v; want NoTimeout", c.Timeout)
			}
		}
	}
	want := []string{attach, "nvim --remote-ui --server 127.0.0.1:17002", detach}
	if !slices.Equal(got, want) {
		t.Errorf("calls\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// No host nvim, no hooks, an inactive unit: start it, then podman exec.
	e.reset()
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=inactive\n", nvimUnit)
	e.l.ConfigDir, e.l.LookPath, e.l.TTY = t.TempDir(), noNvim, true
	if err := e.l.Nvim(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	calls := e.calls()
	wantInOrder(t, calls, "systemctl --user start "+nvimUnit, "podman exec -i -t filial-feat-nvim nvim --remote-ui --server 127.0.0.1:7777")
	if last := joined(calls[len(calls)-1]); last != "podman exec -i -t filial-feat-nvim nvim --remote-ui --server 127.0.0.1:7777" {
		t.Errorf("last call %q", last)
	}
}

// A failing hook only warns.
func TestNvimHookFailureWarns(t *testing.T) {
	e := setup(t)
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=active\n", nvimUnit)
	cfg := hooks(t)
	e.l.ConfigDir, e.l.LookPath = cfg, findNvim
	e.f.On([]string{filepath.Join(cfg, "hooks", "nvim-attach")}, sys.Result{ExitCode: 1}, sys.ErrExit)
	if err := e.l.Nvim(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.l.Err.(interface{ String() string }).String(), "nvim-attach") {
		t.Errorf("no warning naming nvim-attach")
	}
}

func (e *tenv) nvimActive() {
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=active\n", nvimUnit)
}

func TestNvimUpdateRefusesDirty(t *testing.T) {
	e := setup(t)
	e.nvimActive()
	e.f.On(gitArgv("status", "--porcelain"), sys.Result{Stdout: []byte(" M init.lua\n")}, nil)
	err := e.l.NvimUpdate(ctx, "feat")
	if err == nil || !strings.Contains(err.Error(), "init.lua") {
		t.Fatalf("NvimUpdate = %v; want an error naming init.lua", err)
	}
	if n := count(e.calls(), joined(gitArgv("pull"))); n != 0 {
		t.Errorf("pulled with local changes")
	}
}

func TestNvimUpdateRefusesOrigin(t *testing.T) {
	e := setup(t)
	e.nvimActive()
	e.f.On(gitArgv("remote", "get-url", "origin"), sys.Result{Stdout: []byte("https://x/old\n")}, nil)
	err := e.l.NvimUpdate(ctx, "feat")
	for _, want := range []string{"https://x/old", nvimRepo, "nvim reset-config"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("NvimUpdate = %v; want an error containing %q", err, want)
		}
	}
	if n := count(e.calls(), joined(gitArgv("pull"))); n != 0 {
		t.Errorf("pulled from another origin")
	}
}

func TestNvimUpdatePull(t *testing.T) {
	e := setup(t)
	e.nvimActive()
	e.f.On(gitArgv("remote", "get-url", "origin"), sys.Result{Stdout: []byte(nvimRepo + "\n")}, nil)
	if err := e.l.NvimUpdate(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, e.calls(), joined(gitArgv("status", "--porcelain")), joined(gitArgv("remote", "get-url", "origin")), joined(gitArgv("pull", "--ff-only")))
	for _, c := range e.f.Cmds {
		if c.Name == "podman" && slices.Contains(c.Args, "git") && c.Timeout != 5*time.Minute {
			t.Errorf("%v: timeout %v; want 5m", c.Args, c.Timeout)
		}
	}
}

func TestNvimUpdateRef(t *testing.T) {
	e := setup(t)
	e.editProject(t, func(s string) string {
		return strings.Replace(s, "config_repo = ", "config_ref = \"v2\"\nconfig_repo = ", 1)
	})
	e.nvimActive()
	e.f.On(gitArgv("remote", "get-url", "origin"), sys.Result{Stdout: []byte(nvimRepo + "\n")}, nil)
	if err := e.l.NvimUpdate(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	calls := e.calls()
	wantInOrder(t, calls, joined(gitArgv("fetch", "origin", "v2")), joined(gitArgv("checkout", "--detach", "FETCH_HEAD")))
	if n := count(calls, joined(gitArgv("pull"))); n != 0 {
		t.Errorf("pulled with config_ref")
	}
}

func TestNvimUpdateNoRepo(t *testing.T) {
	e := setup(t)
	e.editProject(t, func(s string) string { return strings.Replace(s, "config_repo = \""+nvimRepo+"\"\n", "", 1) })
	if err := e.l.NvimUpdate(ctx, "feat"); err == nil || !strings.Contains(err.Error(), "config_repo") {
		t.Fatalf("NvimUpdate = %v; want an error naming config_repo", err)
	}
}

func TestNvimResetConfig(t *testing.T) {
	e := setup(t)
	err := e.l.NvimResetConfig(ctx, false)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("no TTY, no --yes: %v; want a refusal naming --yes", err)
	}
	if n := count(e.calls(), "podman volume rm"); n != 0 {
		t.Fatalf("removed the volume without confirmation")
	}
	if err := e.l.NvimResetConfig(ctx, true); err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, e.calls(), "systemctl --user stop filial-master-nvim.service", "systemctl --user stop filial-feat-nvim.service", "podman volume rm filial_nvim_config")
	if !strings.Contains(e.out.String(), "the next nvim start clones "+nvimRepo) {
		t.Errorf("output %q", e.out.String())
	}

	// A TTY answer other than yes aborts.
	e.reset()
	e.l.TTY, e.l.In = true, strings.NewReader("n\n")
	if err := e.l.NvimResetConfig(ctx, false); err != nil {
		t.Fatal(err)
	}
	if n := count(e.calls(), "podman volume rm"); n != 0 {
		t.Errorf("removed the volume after no")
	}
}
