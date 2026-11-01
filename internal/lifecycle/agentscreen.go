package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
)

// AgentScreen is `railswitch agent screen` (and its --json, docs/json.md):
// a service agent's session as it shows now (D144).
type AgentScreen struct {
	Agent   string   `json:"agent"`
	Running bool     `json:"running"` // its container runs in the worktree
	Session bool     `json:"session"` // its session server holds the agent's session
	Lines   []string `json:"lines"`   // the visible screen, each line Printable
}

// MaxSend is the most bytes AgentSend types at once.
const MaxSend = 4096

// sessionTarget is the agent session's active pane: "=" matches the
// session name exactly, the ":" makes it a window target (its current one).
const sessionTarget = "=" + render.AgentSession + ":"

// AgentScreen captures service agent name's screen in worktree slug: the
// visible pane, wrapped lines joined, trailing spaces and trailing blank
// lines cut. A stopped container or a server without a session is no
// error: Running or Session is false and Lines empty.
func (l *L) AgentScreen(ctx context.Context, slug, name string) (*AgentScreen, error) {
	name, ctr, err := l.serviceAgent(ctx, slug, name, "agent screen")
	if err != nil {
		return nil, err
	}
	sc := &AgentScreen{Agent: name, Lines: []string{}}
	if sc.Running, err = l.podman().Running(ctx, ctr); err != nil || !sc.Running {
		return sc, err
	}
	if sc.Session, err = l.agentSession(ctx, ctr); err != nil || !sc.Session {
		return sc, err
	}
	res, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: sessionCmd(ctr, "capture-pane", "-p", "-J", "-t", sessionTarget)})
	if err != nil {
		return nil, fmt.Errorf("%s: capture its screen: %w", ctr, err)
	}
	for line := range strings.Lines(string(res.Stdout)) {
		sc.Lines = append(sc.Lines, strings.TrimRight(safefile.Printable(line), " "))
	}
	for len(sc.Lines) > 0 && sc.Lines[len(sc.Lines)-1] == "" {
		sc.Lines = sc.Lines[:len(sc.Lines)-1]
	}
	return sc, nil
}

// AgentSend types text into service agent name's session in worktree slug,
// then Enter: pasted (bracketed when the agent asks for it), so a
// multi-line text arrives as one input. Empty text presses Enter alone. It
// never starts a session: a stopped agent or one without a session is an
// error.
func (l *L) AgentSend(ctx context.Context, slug, name, text string) error {
	if err := CheckSendText(text); err != nil {
		return err
	}
	name, ctr, err := l.serviceAgent(ctx, slug, name, "agent send")
	if err != nil {
		return err
	}
	if ok, err := l.podman().Running(ctx, ctr); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("agent %s is not running in %s", name, slug)
	}
	if live, err := l.agentSession(ctx, ctr); err != nil {
		return err
	} else if !live {
		return fmt.Errorf("agent %s has no session in %s to type into: railswitch agent %s starts one", name, slug, name)
	}
	const buf = "railswitch-send"
	var steps [][]string
	if text != "" {
		steps = append(steps,
			[]string{"set-buffer", "-b", buf, "--", text},
			[]string{"paste-buffer", "-p", "-d", "-b", buf, "-t", sessionTarget})
	}
	steps = append(steps, []string{"send-keys", "-t", sessionTarget, "Enter"})
	for _, s := range steps {
		if _, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: sessionCmd(ctr, s...)}); err != nil {
			return fmt.Errorf("%s: %s: %w", ctr, s[0], err)
		}
	}
	return nil
}

// CheckSendText refuses text AgentSend must not type: over MaxSend bytes,
// not UTF-8, or holding a control character other than a line break or a
// tab (an escape sequence would drive the agent's terminal).
func CheckSendText(text string) error {
	if len(text) > MaxSend {
		return fmt.Errorf("agent send: more than %d bytes", MaxSend)
	}
	if !utf8.ValidString(text) {
		return errors.New("agent send: the text is not UTF-8")
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return fmt.Errorf("agent send: control character %U in the text", r)
		}
	}
	return nil
}

// sessionCmd is the podman exec of args on service agent ctr's session server.
func sessionCmd(ctr string, args ...string) []string {
	return append(append([]string{"exec", ctr}, render.AgentTmux...), args...)
}

// settle is how AgentPrompt waits for an agent to be ready for input: its
// screen polled every Every, ready once it shows something and stays the
// same for Still polls in a row, given up after Max.
var settle = struct {
	Every time.Duration
	Still int
	Max   time.Duration
}{time.Second, 3, 90 * time.Second}

// AgentPrompt starts service agent name's session in worktree slug in the
// background (AgentDetached) and types text into it (AgentSend) once its
// screen settles, so any agent's first input is the text, whatever its
// command line takes. A session already running is typed into as it is.
func (l *L) AgentPrompt(ctx context.Context, slug, name, text string) error {
	if err := CheckSendText(text); err != nil {
		return err
	}
	name, err := l.CheckAgent(ctx, name)
	if err != nil {
		return err
	}
	if err := l.AgentDetached(ctx, slug, name, nil); err != nil {
		return err
	}
	deadline := time.Now().Add(settle.Max)
	var last string
	still := 0
	for still < settle.Still {
		if time.Now().After(deadline) {
			return fmt.Errorf("agent %s in %s: its screen did not settle in %s; type to it with railswitch agent send", name, slug, settle.Max)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(settle.Every):
		}
		sc, err := l.AgentScreen(ctx, slug, name)
		if err != nil {
			return err
		}
		now := strings.Join(sc.Lines, "\n")
		switch {
		case !sc.Session:
			return fmt.Errorf("agent %s in %s: its session ended before it took the text", name, slug)
		case now == "" || now != last:
			still = 0
		default:
			still++
		}
		last = now
	}
	return l.AgentSend(ctx, slug, name, text)
}
