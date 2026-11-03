package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jbigler/railswitch/internal/agentqueue"
	"github.com/jbigler/railswitch/internal/lifecycle"
	"github.com/jbigler/railswitch/internal/prs"
	"github.com/jbigler/railswitch/internal/safefile"
)

const agentQueueUsage = "usage: railswitch agent queue add [--issue <n>] [<text>...] (else the text on stdin) | railswitch [--json] agent queue ls | railswitch agent queue rm <id> | railswitch agent queue clear | railswitch agent queue next"

// queueDoc is agent queue ls --json.
type queueDoc struct {
	Slug      string            `json:"slug"`
	Delivered int64             `json:"delivered"`
	Items     []agentqueue.Item `json:"items"`
}

// railswitch agent queue add|ls|rm|clear|next: the worktree's agent work
// queue (D159), typed into its default agent one item at a time as it goes
// idle (next, which the dashboard runs).
func cmdAgentQueue(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", agentQueueUsage)
	}
	var fs *flag.FlagSet
	issue := new(int)
	switch args[0] {
	case "add":
		fs = e.flags("agent queue add")
		issue = fs.Int("issue", 0, "queue GitHub issue `n` as wt add --issue --agent words it")
	case "ls":
		fs = e.flags("agent queue ls")
	case "rm":
		fs = e.flags("agent queue rm")
	case "clear":
		fs = e.flags("agent queue clear")
	case "next":
		fs = e.flags("agent queue next")
	default:
		return e.usageErr("agent queue: unknown subcommand %q\n%s", args[0], agentQueueUsage)
	}
	pos, code, ok := parseMixed(fs, args[1:])
	if !ok {
		return code
	}
	switch {
	case args[0] == "rm" && len(pos) != 1,
		args[0] == "add" && (*issue < 0 || *issue > 0 && len(pos) > 0),
		args[0] != "rm" && args[0] != "add" && len(pos) > 0:
		return e.usageErr("%s", agentQueueUsage)
	}
	l, slug, code := e.lifecycle(ctx, "agent queue")
	if code != 0 {
		return code
	}
	ws := l.WS.Root
	switch args[0] {
	case "add":
		text, err := e.queueText(ctx, l.WS.Project.Project.Repo, *issue, pos)
		if err != nil {
			return e.fail(err)
		}
		it, err := agentqueue.Add(ws, slug, text, time.Now())
		if err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "queued #%d for the agent in %s\n", it.ID, slug)
	case "ls":
		q, err := agentqueue.Read(ws, slug)
		if err != nil {
			return e.fail(err)
		}
		if e.json {
			if err := json.NewEncoder(e.stdout).Encode(queueDoc{Slug: slug, Delivered: q.Delivered, Items: q.Items}); err != nil {
				return e.fail(err)
			}
			return 0
		}
		tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tTEXT")
		for _, it := range q.Items {
			first, _, _ := strings.Cut(it.Text, "\n")
			fmt.Fprintf(tw, "%d\t%s\n", it.ID, safefile.Printable(first))
		}
		if err := tw.Flush(); err != nil {
			return e.fail(err)
		}
	case "rm":
		id, err := strconv.Atoi(strings.TrimPrefix(pos[0], "#"))
		if err != nil {
			return e.usageErr("agent queue rm: %q is not an item id (agent queue ls)", pos[0])
		}
		if err := agentqueue.Update(ws, slug, func(q *agentqueue.Queue) error { return agentqueue.Remove(q, id, false) }); err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "removed #%d from %s's queue\n", id, slug)
	case "clear":
		if err := agentqueue.Update(ws, slug, func(q *agentqueue.Queue) error { q.Items = []agentqueue.Item{}; return nil }); err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "cleared %s's queue\n", slug)
	case "next":
		it, why, err := l.QueueNext(ctx, slug)
		if err != nil {
			return e.fail(err)
		}
		if it == nil {
			fmt.Fprintf(e.stdout, "held: %s\n", why)
			return 0
		}
		fmt.Fprintf(e.stdout, "delivered #%d to the agent in %s\n", it.ID, slug)
	}
	return 0
}

// queueText is what agent queue add queues: issue n's prompt, the words
// given, else stdin; checked as agent send checks its text.
func (e *env) queueText(ctx context.Context, repoURL string, n int, words []string) (string, error) {
	var text string
	switch {
	case n > 0:
		repo, err := prs.Repo(repoURL)
		if err != nil {
			return "", fmt.Errorf("agent queue add --issue: %w", err)
		}
		title, err := prs.IssueTitle(ctx, e.run, repo, n)
		if err != nil {
			return "", err
		}
		text = issuePrompt(n, safefile.Printable(title))
	case len(words) > 0:
		text = strings.Join(words, " ")
	default:
		data, err := e.readStdin()
		if err != nil {
			return "", err
		}
		text = strings.TrimSuffix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("agent queue add: no text")
	}
	return text, lifecycle.CheckSendText(text)
}
