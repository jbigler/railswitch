package lifecycle

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jbigler/railswitch/internal/agentqueue"
)

// QueueSettle is how long an agent's status must have said idle (or a
// finished turn) before the queue's next item is typed into it (D159).
const QueueSettle = 5 * time.Second

// QueueHold is why the queue's next item must wait for agent a, given the
// last delivery's start; "" when it may go. Only an idle agent, or one
// whose turn ended without asking the user something, takes the next
// item; its status must have changed since the last delivery (so one
// delivery is never typed twice) and be QueueSettle old.
func QueueHold(a *AgentRow, delivered int64, now time.Time) string {
	switch {
	case a == nil:
		return "the worktree has no agent"
	case !a.Running:
		return fmt.Sprintf("agent %s is not running", a.Name)
	case a.State == "done" && a.Reply:
		return fmt.Sprintf("agent %s asks you something", a.Name)
	case a.State != "idle" && a.State != "done":
		return fmt.Sprintf("agent %s is %s", a.Name, a.State)
	case a.TS <= delivered: // seconds: a status from the delivery's own second may predate the prompt
		return fmt.Sprintf("agent %s has not reported since the last delivery", a.Name)
	case now.Sub(time.Unix(a.TS, 0)) < QueueSettle:
		return fmt.Sprintf("agent %s has been idle for less than %s", a.Name, QueueSettle)
	}
	return ""
}

// QueueNext types worktree slug's next queued item into its default agent
// through AgentPrompt (so its refusals apply), and removes the item only
// once it was typed. It returns the item delivered, or nil and why it
// held. One delivery runs at a time per worktree; another is a hold.
// lead (nil: none) is asked once the item may go, for a line to type before
// it (D213: the PR's state now); a line that would take the text past
// MaxSend is left out.
func (l *L) QueueNext(ctx context.Context, slug string, lead func(context.Context) string) (*agentqueue.Item, string, error) {
	ws := l.WS.Root
	unlock, ok, err := agentqueue.TryDeliver(ws, slug)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "another delivery runs", nil
	}
	defer unlock()
	q, err := agentqueue.Read(ws, slug)
	if err != nil {
		return nil, "", err
	}
	if len(q.Items) == 0 {
		return nil, "the queue is empty", nil
	}
	name, err := l.CheckAgent(ctx, "")
	if err != nil {
		return nil, "", err
	}
	rows, err := l.AgentList(ctx, slug)
	if err != nil {
		return nil, "", err
	}
	var a *AgentRow
	if i := slices.IndexFunc(rows, func(r AgentRow) bool { return r.Name == name }); i >= 0 {
		a = &rows[i]
	}
	start := time.Now()
	if why := QueueHold(a, q.Delivered, start); why != "" {
		return nil, why, nil
	}
	it := q.Items[0]
	text := it.Text
	if lead != nil {
		if line := lead(ctx); line != "" && len(line)+2+len(text) <= MaxSend {
			text = line + "\n\n" + text
		}
	}
	if err := l.AgentPrompt(ctx, slug, name, text); err != nil {
		return nil, "", err
	}
	err = agentqueue.Update(ws, slug, func(q *agentqueue.Queue) error {
		q.Delivered = start.Unix()
		return agentqueue.Remove(q, it.ID, true) // removed meanwhile: typed all the same
	})
	return &it, "", err
}
