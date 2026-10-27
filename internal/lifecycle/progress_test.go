package lifecycle

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

// lockedBuf is a bytes.Buffer safe for the spinner's goroutine and the test.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *lockedBuf) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *lockedBuf) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// slowStart holds `systemctl --user start <unit>` until the spinner has
// drawn a frame, the way a real start blocks while units come up.
type slowStart struct {
	sys.Runner
	unit string
	out  *lockedBuf
}

func (r slowStart) Run(ctx context.Context, c sys.Cmd) (sys.Result, error) {
	if c.Name == "systemctl" && len(c.Args) == 3 && c.Args[1] == "start" && c.Args[2] == r.unit {
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(r.out.String(), "\r") && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}
	return r.Runner.Run(ctx, c)
}

// On a terminal, up animates a spinner with the elapsed time while the
// worktree's units start, then leaves one line saying how long it took.
func TestUpSpinsWhileStarting(t *testing.T) {
	e := setup(t)
	out := &lockedBuf{}
	e.l.Out = out
	e.l.TTY = true
	e.l.SpinEvery = time.Millisecond
	e.l.R = slowStart{Runner: e.f, unit: "filial-feat-rails.service", out: out}
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "\r⠋ starting feat: deps, db, cache, storage, browser, nvim, rails (0s)") {
		t.Errorf("no spinner frame for the start:\n%q", got)
	}
	if !strings.Contains(got, "\r\x1b[Kstarted feat (") {
		t.Errorf("spinner line not replaced by a summary:\n%q", got)
	}
}

// Without a terminal there is no animation: one line per phase.
func TestUpPhaseLinesNoTTY(t *testing.T) {
	e := setup(t)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	got := e.out.String()
	if !strings.Contains(got, "starting feat: deps, db, cache, storage, browser, nvim, rails\n") {
		t.Errorf("no start line:\n%s", got)
	}
	if strings.Contains(got, "\r") || strings.Contains(got, "\x1b[") {
		t.Errorf("terminal control codes without a terminal:\n%q", got)
	}
}
