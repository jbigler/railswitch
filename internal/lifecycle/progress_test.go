package lifecycle

import (
	"bytes"
	"context"
	"slices"
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
	if strings.Count(got, "starting feat: deps, db, cache, storage, browser, nvim, rails\n") != 1 {
		t.Errorf("start line not printed exactly once:\n%q", got)
	}
	if !strings.Contains(got, "\r⠋ 0s\x1b[K") {
		t.Errorf("no short spinner frame for the start:\n%q", got)
	}
	if !strings.Contains(got, "\r\x1b[Kstarted feat (") {
		t.Errorf("spinner line not replaced by a summary:\n%q", got)
	}
}

// On a terminal a failed start is reported as failed, never as started.
func TestUpStartFailureTTY(t *testing.T) {
	e := setup(t)
	out := &lockedBuf{}
	e.l.Out = out
	e.l.TTY = true
	e.l.SpinEvery = time.Millisecond
	failStart(e)
	if err := e.l.Up(ctx, "feat"); err == nil {
		t.Fatal("want error")
	}
	got := out.String()
	if !strings.Contains(got, "\r\x1b[Kstarting feat: deps, db, cache, storage, browser, nvim, rails failed (") {
		t.Errorf("no failure line:\n%q", got)
	}
	if strings.Contains(got, "started feat") {
		t.Errorf("failed start reported as started:\n%q", got)
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

// up starts every long-running service of the worktree itself, not only
// the app: a service added to a running stack (e.g. [tools.nvim] after the
// first up) starts on the next up, although the app is already active and
// `systemctl start` of an active unit does not start its new Wants=.
// Oneshots (deps) are left to the app's Requires=: starting one explicitly
// would rerun it on every up. Agents are never started by up (D62).
func TestUpStartsEveryService(t *testing.T) {
	e := setup(t)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	var start []string
	for _, c := range e.calls() {
		if len(c) > 3 && c[0] == "systemctl" && c[2] == "start" && c[3] != "filial-proxy-network.service" {
			start = append(start, c[3:]...)
		}
	}
	for _, want := range []string{"filial-feat-db.service", "filial-feat-storage.service", "filial-feat-browser.service", "filial-feat-nvim.service"} {
		if !slices.Contains(start, want) {
			t.Errorf("start %q lacks %s", start, want)
		}
	}
	for _, not := range []string{"filial-feat-deps.service", "filial-feat-agent-probe.service", "filial-feat-agent-claude.service"} {
		if slices.Contains(start, not) {
			t.Errorf("start %q includes %s", start, not)
		}
	}
}
