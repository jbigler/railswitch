package lifecycle

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

const probeCtr = "filial-feat-agent-probe"

func (e *tenv) noSession() {
	e.f.On([]string{"podman", "exec", probeCtr, "tmux", "-L", "agent", "has-session"}, sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit))
}

// D144: the screen's lines, made printable, without trailing blanks; a
// stopped agent or one without a session is an empty screen, not an error.
func TestAgentScreen(t *testing.T) {
	e := setup(t)
	sc, err := e.l.AgentScreen(ctx, "feat", "probe")
	if err != nil || sc.Running || sc.Session || sc.Lines == nil || len(sc.Lines) != 0 {
		t.Fatalf("stopped: %+v, %v", sc, err)
	}

	e.agentRunning(probeCtr)
	e.noSession()
	if sc, err = e.l.AgentScreen(ctx, "feat", "probe"); err != nil || !sc.Running || sc.Session || len(sc.Lines) != 0 {
		t.Fatalf("no session: %+v, %v", sc, err)
	}

	// a container just started: its session server not answering yet
	e = setup(t)
	e.agentRunning(probeCtr)
	e.f.On([]string{"podman", "exec", probeCtr, "tmux", "-L", "agent", "list-sessions"}, sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit))
	if sc, err = e.l.AgentScreen(ctx, "feat", "probe"); err != nil || !sc.Running || sc.Session {
		t.Fatalf("no server: %+v, %v", sc, err)
	}

	e = setup(t)
	e.agentRunning(probeCtr)
	e.f.On([]string{"podman", "exec", probeCtr, "tmux", "-L", "agent", "capture-pane", "-p", "-J", "-t", "=agent:"},
		sys.Result{Stdout: []byte("$ ls  \n\x1b]0;x\x07\tdone\n   \n\n")}, nil)
	sc, err = e.l.AgentScreen(ctx, "feat", "probe")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"$ ls", " ]0;x  done"}; !sc.Running || !sc.Session || !slices.Equal(sc.Lines, want) {
		t.Errorf("got %+v; want lines %q", sc, want)
	}

	if _, err := e.l.AgentScreen(ctx, "feat", "claude"); err == nil || !strings.Contains(err.Error(), "agent screen is for a service agent") {
		t.Errorf("on-demand agent: %v", err)
	}
}

// D144: send pastes the text, then presses Enter, in the live session only.
func TestAgentSend(t *testing.T) {
	e := setup(t)
	if err := e.l.AgentSend(ctx, "feat", "probe", "hi"); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("stopped: %v", err)
	}
	e.agentRunning(probeCtr)
	e.noSession()
	if err := e.l.AgentSend(ctx, "feat", "probe", "hi"); err == nil || !strings.Contains(err.Error(), "no session") {
		t.Fatalf("no session: %v", err)
	}
	if n := count(e.calls(), "podman exec "+probeCtr+" tmux -L agent send-keys"); n != 0 {
		t.Errorf("typed into a worktree without a session")
	}

	e = setup(t)
	e.agentRunning(probeCtr)
	if err := e.l.AgentSend(ctx, "feat", "probe", "-a\nb"); err != nil {
		t.Fatal(err)
	}
	pre := "podman exec " + probeCtr + " tmux -L agent "
	var buf string
	for _, c := range e.calls() {
		if j := joined(c); strings.HasPrefix(j, pre+"set-buffer -b railswitch-send-") {
			buf = c[len(c)-3]
		}
	}
	if buf == "" {
		t.Fatalf("no set-buffer; calls:\n%s", dump(e.calls()))
	}
	wantInOrder(t, e.calls(),
		pre+"set-buffer -b "+buf+" -- -a\nb",
		pre+"paste-buffer -p -d -b "+buf+" -t =agent:",
		pre+"send-keys -t =agent: Enter")
	// each send its own buffer: two at once never paste each other's text
	if err := e.l.AgentSend(ctx, "feat", "probe", "c"); err != nil {
		t.Fatal(err)
	}
	if n := count(e.calls(), pre+"set-buffer -b "+buf+" "); n != 1 {
		t.Errorf("buffer %s used by %d sends", buf, n)
	}

	e = setup(t)
	e.agentRunning(probeCtr)
	if err := e.l.AgentSend(ctx, "feat", "probe", ""); err != nil {
		t.Fatal(err)
	}
	if n := count(e.calls(), pre+"set-buffer"); n != 0 {
		t.Errorf("empty text set a buffer")
	}
	wantInOrder(t, e.calls(), pre+"send-keys -t =agent: Enter")
}

func TestCheckSendText(t *testing.T) {
	for text, ok := range map[string]bool{
		"fix the tests\n\tplease": true,
		"bad\x1b[2J":              false,
		"nul\x00":                 false,
		"cr\r":                    false,
		"\xff":                    false,
		strings.Repeat("x", 4096): true,
		strings.Repeat("x", 4097): false,
		"ünïcode ok":              true,
		"del\x7f":                 false,
		"c1\u0085":                false,
	} {
		if err := CheckSendText(text); (err == nil) != ok {
			t.Errorf("%q: %v", text, err)
		}
	}
}

// AgentPrompt types the text once the screen settles: something shown,
// unchanged for settle.Still polls.
func TestAgentPrompt(t *testing.T) {
	old := settle
	t.Cleanup(func() { settle = old })
	settle.Every, settle.Max = time.Millisecond, time.Second
	e := setup(t)
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=active\n", "filial-feat-agent-probe.service")
	e.agentRunning(probeCtr)
	e.f.On([]string{"podman", "exec", "filial-feat-agent-probe", "tmux", "-L", "agent", "capture-pane"}, sys.Result{Stdout: []byte("> ready\n")}, nil)
	if err := e.l.AgentPrompt(ctx, "feat", "probe", "Work on issue #7"); err != nil {
		t.Fatal(err)
	}
	calls := e.calls()
	if n := count(calls, "podman exec filial-feat-agent-probe tmux -L agent capture-pane"); n < settle.Still+1 {
		t.Errorf("typed after %d looks at the screen; want at least %d", n, settle.Still+1)
	}
	wantInOrder(t, calls, "podman exec filial-feat-agent-probe tmux -L agent capture-pane",
		"podman exec filial-feat-agent-probe tmux -L agent set-buffer -b railswitch-send-",
		"podman exec filial-feat-agent-probe tmux -L agent send-keys")
}
