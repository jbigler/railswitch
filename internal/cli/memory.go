package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/lifecycle"
	"github.com/jbigler/railswitch/internal/safefile"
)

const memoryUsage = "usage: railswitch [--json] memory [--since <seq>] | railswitch memory backup [--to <file>] | railswitch memory restore [--yes] <file> | railswitch memory migrate"

// railswitch memory: the project's shared agent memory (D136), read with
// bd's own --json; backup, restore and migrate act on it.
func cmdMemory(ctx context.Context, e *env, args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "backup":
			fs := e.flags("memory backup")
			to := fs.String("to", "", "write the backup to `file` (default: <workspace>/memory-backups/<prefix>-<time>.jsonl)")
			if code, ok := parse(fs, args[1:]); !ok {
				return code
			}
			if fs.NArg() != 0 {
				return e.usageErr("%s", memoryUsage)
			}
			path := *to
			if path != "" {
				abs, err := filepath.Abs(path)
				if err != nil {
					return e.fail(err)
				}
				path = abs
			}
			l, code := e.projectLifecycle(ctx)
			if code != 0 {
				return code
			}
			if _, err := l.MemoryBackup(ctx, path); err != nil {
				return e.fail(err)
			}
			return 0
		case "restore":
			fs := e.flags("memory restore")
			yes := fs.Bool("yes", false, "restore without asking")
			rest, code, ok := parseMixed(fs, args[1:])
			if !ok {
				return code
			}
			if len(rest) != 1 {
				return e.usageErr("%s", memoryUsage)
			}
			path, err := filepath.Abs(rest[0])
			if err != nil {
				return e.fail(err)
			}
			l, code := e.projectLifecycle(ctx)
			if code != 0 {
				return code
			}
			if err := l.MemoryRestore(ctx, path, *yes); err != nil {
				return e.fail(err)
			}
			return 0
		case "migrate":
			if len(args) != 1 {
				return e.usageErr("%s", memoryUsage)
			}
			l, code := e.projectLifecycle(ctx)
			if code != 0 {
				return code
			}
			if err := l.MemoryMigrate(ctx); err != nil {
				return e.fail(err)
			}
			return 0
		}
	}
	fs := e.flags("memory")
	since := fs.Int64("since", 0, "only activity after journal `seq` (the cursor of the last report)")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 || *since < 0 {
		return e.usageErr("%s", memoryUsage)
	}
	l, code := e.projectLifecycle(ctx)
	if code != 0 {
		return code
	}
	r, err := l.Memory(ctx, *since)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(r); err != nil {
			return e.fail(err)
		}
		return 0
	}
	printMemory(e, r)
	return 0
}

// printMemory is the report as text; everything bd returned is the
// agents' writing, so it goes through safefile.Printable.
func printMemory(e *env, r *lifecycle.MemoryReport) {
	p := safefile.Printable
	state := r.Server.State
	if state == "" {
		state = "not started (the first railswitch agent starts it)"
	}
	fmt.Fprintf(e.stdout, "server  %s  %s\n", r.Server.Container, state)
	if r.Error != "" {
		fmt.Fprintf(e.stdout, "error   %s\n", p(r.Error))
	}
	if c := r.Issues; c != nil {
		fmt.Fprintf(e.stdout, "schema  v%d\nissues  %d (open %d, in progress %d, blocked %d, ready %d, closed %d)\nmemories %d\n",
			r.Schema, c.Total, c.Open, c.InProgress, c.Blocked, c.Ready, c.Closed, r.Memories)
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	for _, w := range r.Worktrees {
		for _, g := range []struct {
			what   string
			issues []lifecycle.MemoryIssue
		}{{"ready", w.Ready}, {"claimed", w.Claimed}, {"blocked", w.Blocked}} {
			for _, is := range g.issues {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", w.Slug, g.what, p(is.ID), p(is.Assignee), p(is.Title))
			}
		}
	}
	tw.Flush()
	for _, ev := range r.Activity {
		fmt.Fprintf(e.stdout, "#%d %s %s %s %s\n", ev.Seq, p(ev.TS), p(ev.Actor), p(ev.Op), p(ev.IssueID))
	}
	if len(r.Activity) > 0 {
		fmt.Fprintf(e.stdout, "next: railswitch memory --since %d\n", r.Cursor)
	}
}
