package sys

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/pty"
)

// ttyOut collects what a pseudo-terminal's master reads.
type ttyOut struct {
	mu sync.Mutex
	b  strings.Builder
}

func (o *ttyOut) String() string { o.mu.Lock(); defer o.mu.Unlock(); return o.b.String() }

func openTTY(t *testing.T) (*os.File, *os.File, *ttyOut) {
	t.Helper()
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	m, s, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(); s.Close() })
	out := &ttyOut{}
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := m.Read(buf)
			out.mu.Lock()
			out.b.Write(buf[:n])
			out.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return m, s, out
}

func waitFor(t *testing.T, out *ttyOut, re *regexp.Regexp) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m := re.FindStringSubmatch(out.String()); m != nil {
			return m
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %v in %q", re, out.String())
	return nil
}

// TestTTY: the command runs on the terminal, at its size, leading its own
// session.
func TestTTY(t *testing.T) {
	m, s, out := openTTY(t)
	if err := pty.Resize(m, 30, 100); err != nil {
		t.Fatal(err)
	}
	_, err := Exec{}.Run(context.Background(), Cmd{Name: "sh", Args: []string{"-c", `tty; stty size; echo "sid=$(ps -o sid= -p $$ | tr -d ' ') pid=$$"`}, TTY: s, Timeout: NoTimeout})
	if err != nil {
		t.Fatal(err)
	}
	m2 := waitFor(t, out, regexp.MustCompile(`sid=(\d+) pid=(\d+)`))
	if m2[1] != m2[2] {
		t.Errorf("sid %s, pid %s: not a session leader", m2[1], m2[2])
	}
	if got := out.String(); !strings.Contains(got, "/dev/pts/") || !strings.Contains(got, "30 100") {
		t.Errorf("output %q: want a /dev/pts/ path and 30 100", got)
	}
}

// TestTTYCancel: cancelling ends the whole session's group, a child that
// ignores SIGTERM included.
func TestTTYCancel(t *testing.T) {
	_, s, out := openTTY(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Exec{}.Run(ctx, Cmd{Name: "sh", Args: []string{"-c", `trap "" TERM; echo pid=$$; sleep 30 & wait`}, TTY: s, Timeout: NoTimeout, KillGrace: 200 * time.Millisecond})
		done <- err
	}()
	pid, _ := strconv.Atoi(waitFor(t, out, regexp.MustCompile(`pid=(\d+)`))[1])
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run didn't return")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
		if time.Now().After(deadline) {
			t.Fatal("the group outlived the cancel")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
