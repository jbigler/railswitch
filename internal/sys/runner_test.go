package sys

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

func TestExecArgvLiteral(t *testing.T) {
	r, err := Exec{}.Run(context.Background(), Cmd{Name: "printf", Args: []string{"%s", "a;b $(x)"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(r.Stdout); got != "a;b $(x)" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestExecExitCode(t *testing.T) {
	r, err := Exec{}.Run(context.Background(), Cmd{Name: "false"})
	if !errors.Is(err, ErrExit) {
		t.Fatalf("err = %v, want ErrExit", err)
	}
	if r.ExitCode != 1 {
		t.Fatalf("ExitCode = %d", r.ExitCode)
	}
}

func TestExecTimeout(t *testing.T) {
	start := time.Now()
	_, err := Exec{}.Run(context.Background(), Cmd{Name: "sleep", Args: []string{"5"}, Timeout: 100 * time.Millisecond})
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestDeadlineFor(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want time.Duration
		ok   bool
	}{
		{0, 30 * time.Second, true},
		{5 * time.Second, 5 * time.Second, true},
		{NoTimeout, 0, false},
	} {
		d, ok := deadlineFor(Cmd{Timeout: tc.in})
		if d != tc.want || ok != tc.ok {
			t.Errorf("deadlineFor(%v) = %v, %v; want %v, %v", tc.in, d, ok, tc.want, tc.ok)
		}
	}
}

func TestExecNoTimeoutCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err := Exec{}.Run(ctx, Cmd{Name: "sleep", Args: []string{"5"}, Timeout: NoTimeout})
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
}

func childPgrp(t *testing.T, timeout time.Duration) int {
	t.Helper()
	r, err := Exec{}.Run(context.Background(), Cmd{Name: "cat", Args: []string{"/proc/self/stat"}, Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(r.Stdout))
	n, err := strconv.Atoi(f[4])
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestExecProcessGroup(t *testing.T) {
	if got := childPgrp(t, NoTimeout); got != syscall.Getpgrp() {
		t.Errorf("NoTimeout pgrp = %d, want caller's %d", got, syscall.Getpgrp())
	}
	if got := childPgrp(t, 0); got == syscall.Getpgrp() {
		t.Errorf("timed cmd shares caller's pgrp %d", got)
	}
}

func TestExecExitErrorMessage(t *testing.T) {
	_, err := Exec{}.Run(context.Background(), Cmd{Name: "false"})
	if strings.HasSuffix(err.Error(), ": ") || strings.HasSuffix(err.Error(), ":") {
		t.Errorf("empty tail suffix: %q", err)
	}
	long := strings.Repeat("é", 600) + "x"
	_, err = Exec{}.Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", `printf %s "$1" >&2; exit 3`, "sh", long}})
	if !utf8.ValidString(err.Error()) {
		t.Errorf("tail not cut at rune boundary: %q", err)
	}
	if !strings.Contains(err.Error(), "exit 3") {
		t.Errorf("err = %q", err)
	}
}

// TestExecStderrTailWithCallerStderr: a caller-supplied Stderr still gets
// everything, and the exit error still carries the stderr tail.
func TestExecStderrTailWithCallerStderr(t *testing.T) {
	var buf strings.Builder
	_, err := Exec{}.Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo boom >&2; exit 4"}, Stderr: &buf})
	if buf.String() != "boom\n" {
		t.Errorf("caller stderr = %q", buf.String())
	}
	if err == nil || !strings.Contains(err.Error(), "exit 4): boom") {
		t.Errorf("err = %v; want the stderr tail", err)
	}
}

func TestGitBatchEnv(t *testing.T) {
	has := func(env []string, kv string) bool { return slices.Contains(env, kv) }
	env := gitBatchEnv([]string{"HOME=/h"}, "")
	if !has(env, "HOME=/h") || !has(env, "GIT_TERMINAL_PROMPT=0") || !has(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes") {
		t.Errorf("plain env = %q", env)
	}
	for _, user := range []string{"GIT_SSH_COMMAND=my-ssh", "GIT_SSH=/usr/bin/my-ssh"} {
		env := gitBatchEnv([]string{user, "GIT_TERMINAL_PROMPT=1"}, "ssh -i k")
		if !has(env, user) || !has(env, "GIT_TERMINAL_PROMPT=0") || has(env, "GIT_TERMINAL_PROMPT=1") {
			t.Errorf("env with %s = %q", user, env)
		}
		for _, kv := range env {
			if strings.HasPrefix(kv, "GIT_SSH_COMMAND=ssh") {
				t.Errorf("env with %s overrode it: %q", user, env)
			}
		}
	}
}

// The user's core.sshCommand (global or system config) is kept, pinned as
// GIT_SSH_COMMAND so a repo-level value cannot replace it.
func TestGitNetEnvKeepsUserSSHCommand(t *testing.T) {
	const sshCmd = "ssh -i ~/.ssh/work"
	f := &fakeRunner{out: map[string]Result{
		"git -C / config --get core.sshCommand": {Stdout: []byte(sshCmd + "\n")},
	}}
	env, err := GitNetEnv(context.Background(), f, []string{"HOME=/h"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "GIT_SSH_COMMAND="+sshCmd) || slices.Contains(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes") {
		t.Errorf("env = %q, want GIT_SSH_COMMAND=%s and no batch-mode ssh", env, sshCmd)
	}
	if !slices.Contains(env, "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("env = %q, want GIT_TERMINAL_PROMPT=0", env)
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
}

// With no core.sshCommand (git config exits 1) ssh runs in batch mode.
func TestGitNetEnvNoUserSSHCommand(t *testing.T) {
	const k = "git -C / config --get core.sshCommand"
	f := &fakeRunner{out: map[string]Result{k: {ExitCode: 1}}, errs: map[string]error{k: fmt.Errorf("git: %w (exit 1)", ErrExit)}}
	env, err := GitNetEnv(context.Background(), f, []string{"HOME=/h"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes") {
		t.Errorf("env = %q, want batch-mode ssh", env)
	}
}

// Any other failure reading git config is an error, not a guess.
func TestGitNetEnvConfigFails(t *testing.T) {
	const k = "git -C / config --get core.sshCommand"
	f := &fakeRunner{out: map[string]Result{k: {ExitCode: 3}}, errs: map[string]error{k: fmt.Errorf("git: %w (exit 3)", ErrExit)}}
	if _, err := GitNetEnv(context.Background(), f, nil); err == nil {
		t.Fatal("want an error")
	}
}

// fakeRunner answers by space-joined argv (name included) and records calls.
type fakeRunner struct {
	out   map[string]Result
	errs  map[string]error
	calls []Cmd
}

func (f *fakeRunner) Run(_ context.Context, c Cmd) (Result, error) {
	f.calls = append(f.calls, c)
	k := strings.Join(append([]string{c.Name}, c.Args...), " ")
	return f.out[k], f.errs[k]
}

func TestExecDetach(t *testing.T) {
	dir := t.TempDir()
	out := dir + "/out"
	start := time.Now()
	_, err := Exec{}.Run(context.Background(), Cmd{
		Name:   "sh",
		Args:   []string{"-c", `sleep 0.3; { pwd; read -r _ _ _ _ _ sid _ < /proc/$$/stat; echo "$$ $sid"; readlink /proc/$$/fd/0 /proc/$$/fd/2; } > ` + out + `.tmp && mv ` + out + `.tmp ` + out},
		Dir:    dir,
		Detach: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Errorf("Run took %v; want an immediate return", d)
	}
	var b []byte
	for range 100 {
		if b, err = os.ReadFile(out); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(b))
	if len(lines) != 5 || lines[0] != dir || lines[1] != lines[2] || lines[3] != "/dev/null" || lines[4] != "/dev/null" {
		t.Errorf("got %q; want dir, pid == sid (own session), /dev/null stdio", lines)
	}
}
