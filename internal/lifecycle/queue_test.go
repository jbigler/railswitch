package lifecycle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/agentqueue"
	"github.com/jbigler/railswitch/internal/sys"
)

// D159: only a running agent that is idle, or whose turn ended without
// asking anything, takes the next item, once its status is QueueSettle old
// and newer than the last delivery.
func TestQueueHold(t *testing.T) {
	now := time.Unix(1000, 0)
	row := func(state string, ts int64) *AgentRow {
		return &AgentRow{Name: "a", State: state, TS: ts, Running: true}
	}
	asks := row("done", 900)
	asks.Reply = true
	stopped := row("idle", 900)
	stopped.Running = false
	for _, c := range []struct {
		name      string
		a         *AgentRow
		delivered int64
		want      string // "" delivers; else a word of the hold
	}{
		{"idle", row("idle", 900), 0, ""},
		{"turn over", row("done", 900), 0, ""},
		{"asks", asks, 0, "asks you"},
		{"permission", row("permission", 900), 0, "permission"},
		{"question", row("question", 900), 0, "question"},
		{"working", row("working", 900), 0, "working"},
		{"off", row("off", 900), 0, "off"},
		{"stopped", stopped, 0, "not running"},
		{"none", nil, 0, "no agent"},
		{"settling", row("idle", 998), 0, "less than"},
		{"since delivery", row("idle", 900), 901, "not reported"},
		{"reported at delivery", row("done", 901), 901, ""},
	} {
		got := QueueHold(c.a, c.delivered, now)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: hold %q; want %q", c.name, got, c.want)
		}
	}
}

// D159: next types the first item through AgentPrompt and removes it; a
// second next before the agent reports again holds; a failed delivery
// keeps the item; permission holds.
func TestQueueNext(t *testing.T) {
	old := settle
	t.Cleanup(func() { settle = old })
	settle.Every, settle.Max = time.Millisecond, time.Second
	e := setup(t)
	e.editProject(t, func(s string) string {
		return strings.Replace(s, "use = \"local:probe@1\"\nrun = \"service\"", "use = \"local:probe@1\"\nrun = \"service\"\ndefault = true", 1)
	})
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=active\n", "filial-feat-agent-probe.service")
	e.agentRunning(probeCtr)
	e.f.On([]string{"podman", "exec", probeCtr, "tmux", "-L", "agent", "capture-pane"}, sys.Result{Stdout: []byte("> ready\n")}, nil)
	dir := filepath.Join(e.ws, ".railswitch", "status", "feat")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	status := func(state string, ts int64) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "probe.json"), fmt.Appendf(nil, `{"v":1,"state":%q,"message":"","ts":%d}`, state, ts), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sent := func() int { return count(e.calls(), "podman exec "+probeCtr+" tmux -L agent set-buffer") }
	ws := e.l.WS.Root
	if it, why, err := e.l.QueueNext(ctx, "feat"); err != nil || it != nil || !strings.Contains(why, "empty") {
		t.Fatalf("empty queue: %v %q %v", it, why, err)
	}
	for _, text := range []string{"first", "second"} {
		if _, err := agentqueue.Add(ws, "feat", text, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	old10 := time.Now().Add(-10 * time.Second).Unix()
	status("idle", old10)
	it, why, err := e.l.QueueNext(ctx, "feat")
	if err != nil || it == nil || it.Text != "first" || sent() != 1 {
		t.Fatalf("idle: %+v %q %v, sent %d", it, why, err, sent())
	}
	q, _ := agentqueue.Read(ws, "feat")
	if len(q.Items) != 1 || q.Items[0].Text != "second" || q.Delivered < old10 {
		t.Fatalf("after delivery: %+v", q)
	}
	// the status still is the one delivered on: never typed twice
	if it, why, err := e.l.QueueNext(ctx, "feat"); err != nil || it != nil || !strings.Contains(why, "not reported") || sent() != 1 {
		t.Fatalf("again: %v %q %v, sent %d", it, why, err, sent())
	}
	status("permission", time.Now().Add(time.Minute).Unix())
	if it, why, err := e.l.QueueNext(ctx, "feat"); err != nil || it != nil || !strings.Contains(why, "permission") || sent() != 1 {
		t.Fatalf("permission: %v %q %v, sent %d", it, why, err, sent())
	}
	// a delivery that fails keeps its item
	e.f.On([]string{"podman", "exec", probeCtr, "tmux", "-L", "agent", "set-buffer"}, sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit))
	if err := agentqueue.Update(ws, "feat", func(q *agentqueue.Queue) error { q.Delivered = 0; return nil }); err != nil {
		t.Fatal(err)
	}
	status("done", old10)
	if _, _, err := e.l.QueueNext(ctx, "feat"); err == nil {
		t.Fatal("failed delivery: no error")
	}
	if q, _ := agentqueue.Read(ws, "feat"); len(q.Items) != 1 || q.Items[0].Text != "second" {
		t.Fatalf("failed delivery dropped the item: %+v", q)
	}
	// another delivery running: a hold, nothing typed
	unlock, ok, err := agentqueue.TryDeliver(ws, "feat")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	defer unlock()
	if it, why, err := e.l.QueueNext(ctx, "feat"); err != nil || it != nil || !strings.Contains(why, "another delivery") {
		t.Fatalf("racing: %v %q %v", it, why, err)
	}
}
