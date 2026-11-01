package lifecycle

import (
	"fmt"
	"slices"
	"strings"
	"testing"

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
	wantInOrder(t, e.calls(),
		pre+"set-buffer -b railswitch-send -- -a\nb",
		pre+"paste-buffer -p -d -b railswitch-send -t =agent:",
		pre+"send-keys -t =agent: Enter")

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
