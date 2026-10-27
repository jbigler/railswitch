// Package term opens a command in a new terminal tab or window (agent
// --new-term), a port of the old tool's newterm.sh for Linux terminals:
// a tab in the terminal railswitch runs in, else a window of it, else a
// window of any terminal found, else the current window. It is the only
// package that knows terminal emulator names.
package term

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

// tabTimeout bounds a tab request: the terminal's CLI returns once the tab
// exists.
const tabTimeout = 10 * time.Second

// fallbacks are the terminals tried, in order, for a window when the
// detected one gives neither a tab nor a window.
var fallbacks = []string{"kitty", "wezterm", "ghostty", "alacritty", "foot", "konsole", "gnome-terminal", "xfce4-terminal", "terminator", "xterm"}

// envMarks are the environment fingerprints, checked in order.
var envMarks = []struct{ key, term string }{
	{"TMUX", "tmux"},
	{"KITTY_PID", "kitty"},
	{"WEZTERM_PANE", "wezterm"},
	{"KONSOLE_VERSION", "konsole"},
	{"TERMINATOR_UUID", "terminator"},
	{"ALACRITTY_WINDOW_ID", "alacritty"},
	{"GHOSTTY_RESOURCES_DIR", "ghostty"},
	{"GHOSTTY_BIN_DIR", "ghostty"},
}

// procNames maps an ancestor's comm to its terminal.
var procNames = map[string]string{
	"kitty":                 "kitty",
	"wezterm":               "wezterm",
	"wezterm-gui":           "wezterm",
	"konsole":               "konsole",
	"gnome-terminal":        "gnome-terminal",
	"gnome-terminal-server": "gnome-terminal",
	"gnome-terminal-":       "gnome-terminal", // gnome-terminal-server cut to comm's 15 chars
	"xfce4-terminal":        "xfce4-terminal",
	"terminator":            "terminator",
	"tmux":                  "tmux",
	"tmux: server":          "tmux",
	"alacritty":             "alacritty",
	"foot":                  "foot",
	"footclient":            "foot",
	"ghostty":               "ghostty",
	"xterm":                 "xterm",
}

// maxDepth bounds the parent walk.
const maxDepth = 64

// Launcher opens commands in terminals. Env and LookPath are os.Getenv
// and exec.LookPath in production; ProcRoot is /proc; TTY is true when
// stdin and stdout are terminals.
type Launcher struct {
	R        sys.Runner
	Env      func(string) string
	LookPath func(string) (string, error)
	ProcRoot string
	TTY      bool
	Err      io.Writer

	ppid int // the walk's first pid; 0 = os.Getppid() (tests set it)
}

// Detect names the terminal railswitch runs in: environment fingerprints
// first (exact), then the parent chain via <ProcRoot>/<pid>/{comm,stat};
// "unknown" when neither tells.
func (l Launcher) Detect() string {
	for _, m := range envMarks {
		if l.Env(m.key) != "" {
			return m.term
		}
	}
	pid := l.ppid
	if pid == 0 {
		pid = os.Getppid()
	}
	for range maxDepth {
		if pid <= 1 {
			break
		}
		dir := filepath.Join(l.ProcRoot, strconv.Itoa(pid))
		comm, err := os.ReadFile(filepath.Join(dir, "comm"))
		if err != nil {
			break
		}
		name := strings.TrimPrefix(filepath.Base(strings.TrimSpace(string(comm))), "-")
		if t, ok := procNames[name]; ok {
			return t
		}
		if pid = parentOf(filepath.Join(dir, "stat")); pid == 0 {
			break
		}
	}
	return "unknown"
}

// parentOf is the ppid in a /proc/<pid>/stat file, 0 when unreadable. The
// comm in parentheses may hold spaces and ")", so fields are read after
// the last ")".
func parentOf(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:]) // state ppid …
	if len(f) < 2 {
		return 0
	}
	n, err := strconv.Atoi(f[1])
	if err != nil {
		return 0
	}
	return n
}

// ExitError is the non-zero exit code of a command run in the current
// window; its stderr already went to the user.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return "exit status " + strconv.Itoa(e.Code) }

// Launch runs argv in dir in a new tab of the detected terminal, else a
// new window of it, else a window of $TERMINAL or a fallback terminal,
// else (TTY only) in the current window, waiting for it (a non-zero exit
// is an *ExitError). title names the
// tab or window where the terminal takes one.
func (l Launcher) Launch(ctx context.Context, title, dir string, argv []string) error {
	detected := l.Detect()
	if l.tab(ctx, detected, title, dir, argv) || l.window(ctx, detected, title, dir, argv) {
		return nil
	}
	tried := map[string]bool{detected: true}
	candidates := fallbacks
	if t := l.Env("TERMINAL"); t != "" {
		candidates = append([]string{filepath.Base(t)}, fallbacks...)
	}
	for _, t := range candidates {
		if tried[t] {
			continue
		}
		tried[t] = true
		if l.window(ctx, t, title, dir, argv) {
			fmt.Fprintf(l.Err, "railswitch: %s gave no tab or window; opened %s instead\n", detected, t)
			return nil
		}
	}
	if l.TTY {
		fmt.Fprintln(l.Err, "railswitch: no terminal emulator could be launched; running here")
		res, err := l.R.Run(ctx, sys.Cmd{Name: argv[0], Args: argv[1:], Dir: dir, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: l.Err, Timeout: sys.NoTimeout})
		if err != nil && errors.Is(err, sys.ErrExit) && res.ExitCode > 0 {
			return &ExitError{Code: res.ExitCode}
		}
		return err
	}
	return fmt.Errorf("no terminal emulator could be launched (detected: %s)", detected)
}

// tab opens a tab in a running instance of t; false when t has no CLI
// tab support, is not on PATH, or the request failed.
func (l Launcher) tab(ctx context.Context, t, title, dir string, argv []string) bool {
	var args []string
	switch t {
	case "tmux":
		args = append([]string{"new-window", "-c", dir, "-n", title, "--"}, argv...)
	case "kitty":
		args = append([]string{"@", "launch", "--type=tab", "--cwd=" + dir, "--tab-title=" + title, "--copy-env=yes", "--"}, argv...)
	case "wezterm":
		args = append([]string{"cli", "spawn", "--cwd", dir, "--"}, argv...)
	case "konsole":
		args = append([]string{"--new-tab", "--workdir", dir, "-e"}, argv...)
	case "gnome-terminal":
		args = append([]string{"--tab", "--title=" + title, "--working-directory=" + dir, "--"}, argv...)
	case "xfce4-terminal":
		args = []string{"--tab", "--title=" + title, "--working-directory=" + dir, "--command=" + quoteArgv(argv)}
	case "terminator":
		args = append([]string{"--new-tab", "--working-directory=" + dir, "-x"}, argv...)
	default:
		return false
	}
	if _, err := l.LookPath(t); err != nil {
		return false
	}
	_, err := l.R.Run(ctx, sys.Cmd{Name: t, Args: args, Timeout: tabTimeout})
	return err == nil
}

// window starts a new, detached instance of t; false when t is unknown or
// not on PATH, there is no display, or it failed to start.
func (l Launcher) window(ctx context.Context, t, title, dir string, argv []string) bool {
	var args []string
	switch t {
	case "kitty":
		args = append([]string{"--directory", dir, "--"}, argv...)
	case "wezterm":
		args = append([]string{"start", "--cwd", dir, "--"}, argv...)
	case "ghostty":
		args = append([]string{"--working-directory=" + dir, "-e"}, argv...)
	case "alacritty":
		args = append([]string{"--working-directory", dir, "-e"}, argv...)
	case "foot":
		args = append([]string{"-D", dir}, argv...)
	case "konsole":
		args = append([]string{"--workdir", dir, "-e"}, argv...)
	case "gnome-terminal":
		args = append([]string{"--window", "--title=" + title, "--working-directory=" + dir, "--"}, argv...)
	case "xfce4-terminal":
		args = []string{"--title=" + title, "--working-directory=" + dir, "--command=" + quoteArgv(argv)}
	case "terminator":
		args = append([]string{"--working-directory=" + dir, "-x"}, argv...)
	case "xterm":
		args = []string{"-title", title, "-e", "sh", "-c", "cd " + quote(dir) + " && exec " + quoteArgv(argv)}
	default:
		return false
	}
	if l.Env("DISPLAY") == "" && l.Env("WAYLAND_DISPLAY") == "" {
		return false
	}
	if _, err := l.LookPath(t); err != nil {
		return false
	}
	_, err := l.R.Run(ctx, sys.Cmd{Name: t, Args: args, Dir: dir, Detach: true})
	return err == nil
}

// quoteArgv is argv as one POSIX shell command line.
func quoteArgv(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = quote(a)
	}
	return strings.Join(q, " ")
}

// quote single-quotes s for a POSIX shell: ' becomes '\”.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
