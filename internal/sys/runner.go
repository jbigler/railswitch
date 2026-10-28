// Package sys runs external commands (podman, systemctl, git) argv-only,
// never through a shell, and always under a deadline unless explicitly
// interactive.
package sys

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// DefaultTimeout applies when Cmd.Timeout is 0.
const DefaultTimeout = 30 * time.Second

// NoTimeout disables the deadline (logs -f and exec); the call ends only
// with its ctx.
const NoTimeout time.Duration = -1

// ErrExit is wrapped together with the exit code and stderr tail when a
// command exits non-zero (no tail for a KillGrace job whose Stderr is its
// Stdout file; see Cmd.Stderr).
var ErrExit = errors.New("non-zero exit")

// Cmd describes one command invocation.
type Cmd struct {
	Name   string
	Args   []string
	Dir    string
	Env    []string  // nil = inherit
	Stdin  io.Reader // the only channel for secret values
	Stdout io.Writer // nil = captured into Result
	// Stderr nil = captured into Result. With KillGrace > 0 and Stderr the
	// same *os.File as Stdout (a job log), the child writes that file
	// directly and the ErrExit error carries no stderr tail.
	Stderr  io.Writer
	Timeout time.Duration // 0 = DefaultTimeout; NoTimeout = no deadline
	// Detach starts the process in its own session with /dev/null stdio
	// and returns at once, without waiting (a new terminal window that
	// outlives railswitch). Stdin, Stdout, Stderr and Timeout are ignored.
	Detach bool
	// KillGrace > 0 runs the command in its own process group even when
	// untimed; cancellation sends SIGTERM to the group, then SIGKILL to
	// the group after KillGrace.
	KillGrace time.Duration
	// ExtraFiles are the child's fds 3 onward.
	ExtraFiles []*os.File
}

// Result is the outcome of a command.
type Result struct {
	Stdout, Stderr []byte
	ExitCode       int
}

// Runner runs commands.
type Runner interface {
	Run(ctx context.Context, c Cmd) (Result, error)
}

// Exec is the real Runner, backed by os/exec.
type Exec struct{}

const stderrTail = 512

func deadlineFor(c Cmd) (time.Duration, bool) {
	switch {
	case c.Timeout == NoTimeout:
		return 0, false
	case c.Timeout <= 0:
		return DefaultTimeout, true
	default:
		return c.Timeout, true
	}
}

// Run executes c. A timeout or cancellation returns an error wrapping
// ctx.Err(); a non-zero exit returns an error wrapping ErrExit.
func (Exec) Run(ctx context.Context, c Cmd) (Result, error) {
	if c.Detach {
		return Result{}, detach(c)
	}
	d, timed := deadlineFor(c)
	if timed {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	cmd.Stdin = c.Stdin
	cmd.ExtraFiles = c.ExtraFiles
	var stdout, stderr bytes.Buffer
	var tail tailBuffer // the caller's Stderr, kept for the exit error
	cmd.Stdout = c.Stdout
	if c.Stdout == nil {
		cmd.Stdout = &stdout
	}
	cmd.Stderr = &stderr
	if f, ok := c.Stderr.(*os.File); ok && c.KillGrace > 0 && c.Stderr == c.Stdout {
		// One file for both streams of a job: the child writes it
		// directly, with no pipe through this process, so it outlives us.
		cmd.Stderr = f
	} else if c.Stderr != nil {
		cmd.Stderr = io.MultiWriter(c.Stderr, &tail)
	}
	// kill is the SIGKILL that follows a graceful SIGTERM to group pgid.
	// cmd.Cancel has returned before cmd.Run does.
	var kill *time.Timer
	var pgid int
	switch {
	case c.KillGrace > 0:
		// A graceful group: SIGTERM first so the command can clean up, then
		// SIGKILL for whatever is left of the group after the grace.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			pgid = cmd.Process.Pid
			kill = time.AfterFunc(c.KillGrace, func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
			return syscall.Kill(-pgid, syscall.SIGTERM)
		}
		cmd.WaitDelay = c.KillGrace + 2*time.Second
	case timed:
		// A timed call gets its own process group so a timeout kills the
		// whole group. Untimed (interactive) calls stay in the caller's
		// foreground group so tty reads and Ctrl-C keep working.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		cmd.WaitDelay = 2 * time.Second
	default:
		cmd.WaitDelay = 2 * time.Second
	}

	err := cmd.Run()
	// The leader may be reaped while group members that ignore SIGTERM
	// live on: the SIGKILL still has to reach them. Stop it only when the
	// group is gone; a pgid is not reused while any member lives.
	if kill != nil && errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) {
		kill.Stop()
	}
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err == nil {
		return res, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, fmt.Errorf("%s: %w", c.Name, ctxErr)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		errText := res.Stderr
		if c.Stderr != nil {
			errText = tail.b
		}
		tail := strings.TrimSpace(string(errText))
		if len(tail) > stderrTail {
			i := len(tail) - stderrTail
			for i < len(tail) && !utf8.RuneStart(tail[i]) {
				i++
			}
			tail = "…" + tail[i:]
		}
		if tail == "" {
			return res, fmt.Errorf("%s: %w (exit %d)", c.Name, ErrExit, res.ExitCode)
		}
		return res, fmt.Errorf("%s: %w (exit %d): %s", c.Name, ErrExit, res.ExitCode, tail)
	}
	return res, fmt.Errorf("%s: %w", c.Name, err)
}

// detach starts c in a new session, stdio on /dev/null (os/exec's nil
// streams), and releases it: it is never waited for.
func detach(c Cmd) error {
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", c.Name, err)
	}
	return cmd.Process.Release()
}

// tailBuffer keeps the last few KiB written to it.
type tailBuffer struct{ b []byte }

const tailKeep = 4 * stderrTail // room to cut at a rune start and trim

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > tailKeep {
		t.b = append(t.b[:0], t.b[len(t.b)-tailKeep:]...)
	}
	return len(p), nil
}

// GitProtocols are the -c flags that limit a git call touching a remote to
// the https, ssh, git and file transports: never ext::, fd:: or another
// remote helper a URL could name.
var GitProtocols = []string{
	"-c", "protocol.allow=never",
	"-c", "protocol.https.allow=always",
	"-c", "protocol.ssh.allow=always",
	"-c", "protocol.git.allow=always",
	"-c", "protocol.file.allow=always",
}

// gitHelperURL is a URL naming a remote helper: <transport>::<address>.
var gitHelperURL = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*::`)

// GitArg refuses a value git would read as an option, a <transport>::
// URL, or one holding a line break.
func GitArg(what, v string) error {
	if v == "" || strings.HasPrefix(v, "-") || strings.ContainsAny(v, "\r\n\x00") {
		return fmt.Errorf("%s %q is not a usable git value", what, v)
	}
	if gitHelperURL.MatchString(v) {
		return fmt.Errorf("%s %q names a git remote helper (<transport>::): use an https, ssh, git or file URL", what, v)
	}
	return nil
}

// GitNetEnv is environ for a git call that may touch the network, made
// non-interactive: a timed call runs in its own process group, where a
// credential, passphrase or host-key prompt would stop git silently until
// the deadline. It sets GIT_TERMINAL_PROMPT=0 and chooses the ssh git runs:
//   - GIT_SSH_COMMAND or GIT_SSH already in environ: the user's, unchanged;
//   - core.sshCommand in the user's global or system git config: that
//     command, as GIT_SSH_COMMAND (the user's key or agent wrapper keeps
//     working);
//   - otherwise `ssh -o BatchMode=yes`.
//
// The repo's own config is never read for it, and GIT_SSH_COMMAND overrides
// it: containers can write the base repo's .git/config, and a
// core.sshCommand set there would run on the host.
func GitNetEnv(ctx context.Context, r Runner, environ []string) ([]string, error) {
	// -C /: outside any repo, so only global and system config count.
	res, err := r.Run(ctx, Cmd{Name: "git", Args: []string{"-C", "/", "config", "--get", "core.sshCommand"}})
	switch {
	case err == nil:
	case errors.Is(err, ErrExit) && res.ExitCode == 1: // not set
	default:
		return nil, fmt.Errorf("git config --get core.sshCommand: %w", err)
	}
	return gitBatchEnv(environ, strings.TrimSpace(string(res.Stdout))), nil
}

// gitBatchEnv is environ with GIT_TERMINAL_PROMPT=0 and, unless environ
// names an ssh, GIT_SSH_COMMAND set to userSSH, or to
// `ssh -o BatchMode=yes` when userSSH is "".
func gitBatchEnv(environ []string, userSSH string) []string {
	env := make([]string, 0, len(environ)+2)
	chosen := false
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "GIT_TERMINAL_PROMPT":
			continue
		case "GIT_SSH_COMMAND", "GIT_SSH":
			chosen = true
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	switch {
	case chosen:
	case userSSH != "":
		env = append(env, "GIT_SSH_COMMAND="+userSSH)
	default:
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	return env
}
