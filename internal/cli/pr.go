package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/prs"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/workspace"
)

const prUsage = "usage: railswitch [--json] pr ls"

// prRow is one worktree's row of pr ls.
type prRow struct {
	Slug   string  `json:"slug"`
	Branch string  `json:"branch"`
	PR     *prs.PR `json:"pr"`
}

// railswitch pr ls: each worktree of the project (-C), in registry order,
// with its branch's pull request from the host's gh (D75); the repo is
// project.toml's, never a worktree's git config.
func cmdPr(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 || args[0] != "ls" {
		return e.usageErr("%s", prUsage)
	}
	fs := e.flags("pr ls")
	if code, ok := parse(fs, args[1:]); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("%s", prUsage)
	}
	w, err := workspace.Load(ctx, e.dir, "")
	if err != nil {
		return e.fail(err)
	}
	repo, err := prs.Repo(w.Project.Project.Repo)
	if err != nil {
		return e.fail(fmt.Errorf("pr ls: %w", err))
	}
	m, err := e.model(ctx, w)
	if err != nil {
		return e.fail(err)
	}
	var branches []string
	for _, wt := range m.Inputs.Worktrees {
		branches = append(branches, wt.Branch)
	}
	list, err := prs.List(ctx, e.run, repo, branches)
	if err != nil {
		return e.fail(err)
	}
	rows := make([]prRow, 0, len(m.Inputs.Worktrees))
	for _, wt := range m.Inputs.Worktrees {
		r := prRow{Slug: wt.Slug, Branch: wt.Branch}
		if wt.Branch != "" {
			r.PR = prs.ForBranch(list, wt.Branch)
		}
		rows = append(rows, r)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(rows); err != nil {
			return e.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "WORKTREE\tBRANCH\tPR\tSTATE\tCHECKS\tREVIEW\tTITLE")
	for _, r := range rows {
		branch := safefile.Printable(r.Branch) // from the worktree's git files
		if branch == "" {
			branch = "-"
		}
		if r.PR == nil {
			fmt.Fprintf(tw, "%s\t%s\t-\t-\t-\t-\t-\n", r.Slug, branch)
			continue
		}
		p := r.PR
		fmt.Fprintf(tw, "%s\t%s\t#%s\t%s\t%s\t%s\t%s\n", r.Slug, branch, strconv.Itoa(p.Number), p.State, p.Checks, p.Review, safefile.Printable(p.Title))
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	return 0
}
