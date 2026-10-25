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
	"os/exec"
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
// command exits non-zero.
var ErrExit = errors.New("non-zero exit")

// Cmd describes one command invocation.
type Cmd struct {
	Name    string
	Args    []string
	Dir     string
	Env     []string      // nil = inherit
	Stdin   io.Reader     // the only channel for secret values
	Stdout  io.Writer     // nil = captured into Result
	Stderr  io.Writer     // nil = captured into Result
	Timeout time.Duration // 0 = DefaultTimeout; NoTimeout = no deadline
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
	var stdout, stderr bytes.Buffer
	var tail tailBuffer // the caller's Stderr, kept for the exit error
	cmd.Stdout = c.Stdout
	if c.Stdout == nil {
		cmd.Stdout = &stdout
	}
	cmd.Stderr = &stderr
	if c.Stderr != nil {
		cmd.Stderr = io.MultiWriter(c.Stderr, &tail)
	}
	if timed {
		// A timed call gets its own process group so a timeout kills the
		// whole group. Untimed (interactive) calls stay in the caller's
		// foreground group so tty reads and Ctrl-C keep working.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	cmd.WaitDelay = 2 * time.Second

	err := cmd.Run()
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

// GitBatchEnv is environ for a git call that may touch the network, made
// non-interactive: a timed call runs in its own process group, where a
// credential, passphrase or host-key prompt would stop git silently until
// the deadline. GIT_TERMINAL_PROMPT=0, and ssh in batch mode unless the
// user's environment already chooses an ssh (GIT_SSH_COMMAND or GIT_SSH).
func GitBatchEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+2)
	userSSH := false
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "GIT_TERMINAL_PROMPT":
			continue
		case "GIT_SSH_COMMAND", "GIT_SSH":
			userSSH = true
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	if !userSSH {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	return env
}
