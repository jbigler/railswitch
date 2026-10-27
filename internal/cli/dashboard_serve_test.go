package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

// TestSessionEnv: the session's display, agent and terminal variables
// from systemctl --user show-environment over the dashboard's own.
func TestSessionEnv(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"systemctl", "--user", "show-environment"}, sys.Result{Stdout: []byte(
		"DISPLAY=:0\n" +
			"SSH_AUTH_SOCK=$'/run/user/1000/a b'\n" +
			"TERMINAL=$'my\\tterm\\x21\\'\\\\'\n" +
			"WAYLAND_DISPLAY=wayland-1\n" +
			"PATH=/session/bin\n" +
			"FOO=bar\n" +
			"garbage\n")}, nil)
	got := sessionEnv(context.Background(), f, []string{"HOME=/home/u", "DISPLAY=:1", "PATH=/usr/bin", "XDG_RUNTIME_DIR=/run/user/1000"})
	want := []string{"HOME=/home/u", "PATH=/usr/bin", "XDG_RUNTIME_DIR=/run/user/1000", "DISPLAY=:0", "WAYLAND_DISPLAY=wayland-1", "SSH_AUTH_SOCK=/run/user/1000/a b", "TERMINAL=my\tterm!'\\"}
	if !slices.Equal(got, want) {
		t.Errorf("env\n got %q\nwant %q", got, want)
	}
	if len(f.Cmds) != 1 || f.Cmds[0].Timeout != 10*time.Second {
		t.Errorf("calls %+v", f.Cmds)
	}

	f = &systest.Fake{}
	f.On([]string{"systemctl"}, sys.Result{}, errors.New("no session bus"))
	base := []string{"HOME=/home/u", "DISPLAY=:1"}
	if got := sessionEnv(context.Background(), f, base); !slices.Equal(got, base) {
		t.Errorf("on failure: %q", got)
	}
}

func TestUnquoteEnv(t *testing.T) {
	for in, want := range map[string]string{
		`plain`:            "plain",
		`$'a b'`:           "a b",
		`$'\n\t\\\'\"'`:    "\n\t\\'\"",
		`$'\x41\101\e'`:    "AA\x1b",
		`"x \"y\" \$z \\"`: `x "y" $z \`,
		`$'unterminated`:   `$'unterminated`,
	} {
		if got := unquoteEnv(in); got != want {
			t.Errorf("unquoteEnv(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSelfCLI: the dashboard's reads run its own executable; a failure
// is the command's last stderr line.
func TestSelfCLI(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"/bin/rs", "--json", "project", "ls"}, sys.Result{Stdout: []byte("[]\n")}, nil)
	f.On([]string{"/bin/rs", "--json", "-C", "/ws", "pr", "ls"}, sys.Result{Stderr: []byte("warning: x\nrailswitch: pr ls: gh: not logged in\n"), ExitCode: 1}, fmt.Errorf("/bin/rs: %w (exit 1)", sys.ErrExit))
	c := selfCLI{r: f, exe: "/bin/rs"}
	out, err := c.Run(context.Background(), "--json", "project", "ls")
	if err != nil || string(out) != "[]\n" {
		t.Errorf("project ls: %q %v", out, err)
	}
	if _, err := c.Run(context.Background(), "--json", "-C", "/ws", "pr", "ls"); err == nil || err.Error() != "pr ls: gh: not logged in" {
		t.Errorf("pr ls error %v", err)
	}
	if f.Cmds[0].Timeout <= 0 || f.Cmds[0].Stdin != nil {
		t.Errorf("cmd %+v", f.Cmds[0])
	}
}

// TestWatchExe: a changed executable ends the watch, but not while a job
// runs.
func TestWatchExe(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "railswitch")
	if err := os.WriteFile(exe, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	busy := make(chan bool, 1)
	busy <- true
	running := func() bool {
		select {
		case b := <-busy:
			busy <- b
			return b
		default:
			return false
		}
	}
	done := make(chan bool, 1)
	go func() { done <- watchExe(context.Background(), exe, 5*time.Millisecond, running) }()
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(exe, []byte("version 2"), 0o755); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		t.Fatal("watch ended while a job runs")
	case <-time.After(30 * time.Millisecond):
	}
	<-busy
	busy <- false
	select {
	case changed := <-done:
		if !changed {
			t.Error("watch ended without a change")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not end after the change")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- watchExe(ctx, exe, 5*time.Millisecond, running) }()
	cancel()
	if changed := <-done; changed {
		t.Error("cancelled watch reports a change")
	}
}

// TestDashboardServeUsage: serve takes no arguments.
func TestDashboardServeUsage(t *testing.T) {
	dashEnv(t)
	if code, _, errs := runWith(t, &systest.Fake{}, "dashboard", "serve", "x"); code != 2 || errs == "" {
		t.Errorf("serve x: %d %q", code, errs)
	}
}
