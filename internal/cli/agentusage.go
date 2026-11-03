package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/agentusage"
	"github.com/jbigler/railswitch/internal/lifecycle"
)

// railswitch [--json] agent usage [--days N] [--all]: the agents' tokens
// (and cost, when their package reports it) per day of the worktree, or
// with --all of every worktree of the project (D162).
func cmdAgentUsage(ctx context.Context, e *env, args []string) int {
	fs := e.flags("agent usage")
	days := fs.Int("days", 14, fmt.Sprintf("days to report, today included (1 to %d)", agentusage.Keep))
	all := fs.Bool("all", false, "every worktree of the project")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("agent usage takes no arguments")
	}
	if *days < 1 || *days > agentusage.Keep {
		return e.usageErr("agent usage: --days must be 1 to %d", agentusage.Keep)
	}
	var l *lifecycle.L
	slug, code := "", 0
	if *all {
		l, code = e.projectLifecycle(ctx)
	} else {
		l, slug, code = e.lifecycle(ctx, "agent usage")
	}
	if code != 0 {
		return code
	}
	rep, err := l.AgentUsage(ctx, slug, *days)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(rep); err != nil {
			return e.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "DAY\tWORKTREE\tAGENT\tTOTAL\tINPUT\tOUTPUT\tCACHE READ\tCACHE WRITE\tCOST")
	for _, r := range rep.Rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%s\n", r.Day, r.Worktree, r.Agent, r.Total(), r.Input, r.Output, r.CacheRead, r.CacheWrite, costText(r.Cost))
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	for _, w := range rep.Worktrees {
		if w.Runaway {
			fmt.Fprintf(e.stdout, "runaway: %s used %d tokens today, over 3 times its median day (%d)\n", w.Worktree, w.Today, w.Median)
		}
	}
	return 0
}

func costText(c *float64) string {
	if c == nil {
		return "-"
	}
	return fmt.Sprintf("$%.2f", *c)
}
