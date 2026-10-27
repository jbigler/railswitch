package term

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func lookOnly(names ...string) func(string) (string, error) {
	return func(n string) (string, error) {
		if slices.Contains(names, n) {
			return "/usr/bin/" + n, nil
		}
		return "", errors.New("not found")
	}
}

// emptyProc is a procfs with no processes: the parent walk finds nothing.
func emptyProc(t *testing.T) string { return t.TempDir() }

func TestDetectEnv(t *testing.T) {
	for _, c := range []struct{ key, want string }{
		{"TMUX", "tmux"},
		{"KITTY_PID", "kitty"},
		{"WEZTERM_PANE", "wezterm"},
		{"KONSOLE_VERSION", "konsole"},
		{"TERMINATOR_UUID", "terminator"},
		{"ALACRITTY_WINDOW_ID", "alacritty"},
		{"GHOSTTY_RESOURCES_DIR", "ghostty"},
	} {
		l := Launcher{Env: envOf(map[string]string{c.key: "1"}), ProcRoot: emptyProc(t)}
		if got := l.Detect(); got != c.want {
			t.Errorf("%s: %q; want %q", c.key, got, c.want)
		}
	}
}

// writeProc writes <root>/<pid>/{comm,stat} for a chain of comms, the
// first being our parent (pid 100), each parented by the next.
func writeProc(t *testing.T, comms ...string) string {
	t.Helper()
	root := t.TempDir()
	for i, comm := range comms {
		pid, ppid := 100+i, 100+i+1
		if i == len(comms)-1 {
			ppid = 1
		}
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		stat := strconv.Itoa(pid) + " (" + comm + ") S " + strconv.Itoa(ppid) + " 0 0\n"
		if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDetectParents(t *testing.T) {
	l := Launcher{Env: envOf(nil), ProcRoot: writeProc(t, "bash", "foot", "systemd"), ppid: 100}
	if got := l.Detect(); got != "foot" {
		t.Errorf("bash → foot → systemd: %q; want foot", got)
	}
	l = Launcher{Env: envOf(nil), ProcRoot: writeProc(t, "bash", "sshd", "systemd"), ppid: 100}
	if got := l.Detect(); got != "unknown" {
		t.Errorf("no terminal: %q; want unknown", got)
	}
}

var argv = []string{"railswitch", "-C", "/ws", "-w", "feat", "agent", "claude"}

func TestLaunchKittyTab(t *testing.T) {
	f := &systest.Fake{}
	l := Launcher{R: f, Env: envOf(map[string]string{"KITTY_PID": "1"}), LookPath: lookOnly("kitty"), ProcRoot: emptyProc(t), Err: &bytes.Buffer{}}
	if err := l.Launch(context.Background(), "T", "/w", argv); err != nil {
		t.Fatal(err)
	}
	want := append([]string{"kitty", "@", "launch", "--type=tab", "--cwd=/w", "--tab-title=T", "--copy-env=yes", "--"}, argv...)
	if len(f.Calls) != 1 || !slices.Equal(f.Calls[0], want) {
		t.Fatalf("calls %q; want %q", f.Calls, want)
	}
	if c := f.Cmds[0]; c.Detach || c.Timeout != 10*time.Second {
		t.Errorf("Detach %v Timeout %v; want false, 10s", c.Detach, c.Timeout)
	}
}

func TestLaunchFallbackWindow(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"tmux", "new-window"}, sys.Result{ExitCode: 1}, sys.ErrExit)
	var errb bytes.Buffer
	l := Launcher{R: f, Env: envOf(map[string]string{"TMUX": "x", "DISPLAY": ":0"}), LookPath: lookOnly("tmux", "foot"), ProcRoot: emptyProc(t), Err: &errb}
	if err := l.Launch(context.Background(), "T", "/w", argv); err != nil {
		t.Fatal(err)
	}
	want := append([]string{"foot", "-D", "/w"}, argv...)
	last := len(f.Calls) - 1
	if last < 0 || !slices.Equal(f.Calls[last], want) || !f.Cmds[last].Detach {
		t.Fatalf("calls %q; want last %q detached", f.Calls, want)
	}
	if len(f.Calls) != 2 {
		t.Errorf("calls %q; want tmux tab then foot", f.Calls)
	}
	if !strings.Contains(errb.String(), "railswitch: tmux gave no tab or window; opened foot instead") {
		t.Errorf("stderr %q", errb.String())
	}
}

func TestLaunchNoDisplayNoTTY(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"tmux"}, sys.Result{ExitCode: 1}, sys.ErrExit)
	l := Launcher{R: f, Env: envOf(map[string]string{"TMUX": "x"}), LookPath: lookOnly("tmux", "foot", "xterm"), ProcRoot: emptyProc(t), Err: &bytes.Buffer{}}
	err := l.Launch(context.Background(), "T", "/w", argv)
	if err == nil || !strings.Contains(err.Error(), "no terminal emulator could be launched (detected: tmux)") {
		t.Fatalf("err %v", err)
	}
}

func TestLaunchHere(t *testing.T) {
	f := &systest.Fake{}
	var errb bytes.Buffer
	l := Launcher{R: f, Env: envOf(nil), LookPath: lookOnly(), ProcRoot: emptyProc(t), TTY: true, Err: &errb}
	if err := l.Launch(context.Background(), "T", "/w", argv); err != nil {
		t.Fatal(err)
	}
	if len(f.Calls) != 1 || !slices.Equal(f.Calls[0], argv) || f.Cmds[0].Timeout != sys.NoTimeout || f.Cmds[0].Dir != "/w" {
		t.Fatalf("calls %q cmds %+v", f.Calls, f.Cmds)
	}
	if !strings.Contains(errb.String(), "railswitch: no terminal emulator could be launched; running here") {
		t.Errorf("stderr %q", errb.String())
	}
}

func TestLaunchTerminalEnv(t *testing.T) {
	f := &systest.Fake{}
	l := Launcher{R: f, Env: envOf(map[string]string{"TERMINAL": "/usr/bin/xterm", "WAYLAND_DISPLAY": "w"}), LookPath: lookOnly("xterm", "kitty"), ProcRoot: emptyProc(t), Err: &bytes.Buffer{}}
	if err := l.Launch(context.Background(), "T", "/w d", []string{"a", "it's"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"xterm", "-title", "T", "-e", "sh", "-c", `cd '/w d' && exec 'a' 'it'\''s'`}
	if len(f.Calls) != 1 || !slices.Equal(f.Calls[0], want) {
		t.Fatalf("calls %q; want %q", f.Calls, want)
	}
}

func TestQuote(t *testing.T) {
	if got := quote("it's"); got != `'it'\''s'` {
		t.Errorf("%s", got)
	}
}
