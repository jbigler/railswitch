package sys

import (
	"context"
	"errors"
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
