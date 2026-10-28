package cli

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

// noHostNvim makes lookPath find nothing for the test.
func noHostNvim(t *testing.T) {
	t.Helper()
	old := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookPath = old })
}

func fakeCalls(calls [][]string) string {
	var b strings.Builder
	for _, c := range calls {
		b.WriteString(strings.Join(c, " ") + "\n")
	}
	return b.String()
}

func TestNvimCommand(t *testing.T) {
	noHostNvim(t)
	ws, dirs := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	hook := filepath.Join(dirs.Config, "hooks", "nvim-attach")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := fakeHost()
	f.On([]string{"systemctl", "--user", "show"}, sys.Result{Stdout: []byte("LoadState=loaded\nNeedDaemonReload=no\nActiveState=active\n")}, nil)
	code, _, errs := runWith(t, f, "-C", ws, "-w", "feat", "nvim")
	if code != 0 {
		t.Fatalf("nvim: exit %d: %s", code, errs)
	}
	all := fakeCalls(f.Calls)
	i, j := strings.Index(all, hook+"\n"), strings.Index(all, " sh nvim --remote-ui --server 127.0.0.1:7777\n")
	if i < 0 || j < i {
		t.Errorf("want the hook, then podman exec nvim; calls:\n%s", all)
	}

	for _, args := range [][]string{{"nvim", "bogus"}, {"nvim", "update", "x"}, {"nvim", "reset-config", "x"}, {"nvim", "--yes"}} {
		if code, _, _ := runIn(t, append([]string{"-C", ws, "-w", "feat"}, args...)...); code != 2 {
			t.Errorf("%q: exit %d; want 2", args, code)
		}
	}
}

func TestNvimResetConfigCommand(t *testing.T) {
	ws, _ := lockedWS(t)
	f := fakeHost()
	code, _, errs := runWith(t, f, "-C", ws, "-w", "feat", "nvim", "reset-config")
	if code != 1 || !strings.Contains(errs, "--yes") {
		t.Fatalf("reset-config without a terminal: exit %d, %q; want 1 naming --yes", code, errs)
	}
	code, out, errs := runWith(t, f, "-C", ws, "-w", "feat", "nvim", "reset-config", "--yes")
	if code != 0 {
		t.Fatalf("reset-config --yes: exit %d: %s", code, errs)
	}
	if !slices.ContainsFunc(f.Calls, func(c []string) bool { return strings.Join(c, " ") == "podman volume rm filial_nvim_config" }) {
		t.Errorf("no volume rm; calls:\n%s", fakeCalls(f.Calls))
	}
	if !strings.Contains(out, "the next nvim start clones") {
		t.Errorf("out %q", out)
	}
}

func TestCompleteNvim(t *testing.T) {
	t.Chdir(t.TempDir())
	if got, want := complete(t, "nvim", ""), []string{"reset-config", "update"}; !slices.Equal(got, want) {
		t.Errorf("nvim: %q; want %q", got, want)
	}
	if got, want := complete(t, "nvim", "reset-config", "--"), []string{"--yes"}; !slices.Equal(got, want) {
		t.Errorf("nvim reset-config --: %q; want %q", got, want)
	}
}
