package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/prs"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/workspace"
)

const stackUsage = "usage: railswitch [--json] stack ls"

// stackDoc is `stack ls --json` (docs/json.md).
type stackDoc struct {
	Available bool       `json:"available"` // gh has the gh-stack extension
	Worktrees []stackRow `json:"worktrees"`
}

// stackRow is one worktree's stack: nil when its branch is in none (or it
// has no branch); Error when gh stack couldn't say.
type stackRow struct {
	Slug   string     `json:"slug"`
	Branch string     `json:"branch"`
	Stack  *prs.Stack `json:"stack"`
	Error  string     `json:"error"`
}

// railswitch stack ls: each worktree of the project (-C), in registry
// order, with its branch's stack as GitHub's gh-stack extension sees it.
// Without the extension, available is false and no worktree is asked.
func cmdStack(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 || args[0] != "ls" {
		return e.usageErr("%s", stackUsage)
	}
	fs := e.flags("stack ls")
	if code, ok := parse(fs, args[1:]); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("%s", stackUsage)
	}
	w, err := workspace.Load(ctx, e.dir, "")
	if err != nil {
		return e.fail(err)
	}
	m, err := e.model(ctx, w)
	if err != nil {
		return e.fail(err)
	}
	doc := stackDoc{Available: prs.StackAvailable(ctx, e.run), Worktrees: []stackRow{}}
	for _, wt := range m.Inputs.Worktrees {
		r := stackRow{Slug: wt.Slug, Branch: wt.Branch}
		if doc.Available && wt.Branch != "" {
			if r.Stack, err = prs.ViewStack(ctx, e.run, wt.Dir); err != nil {
				r.Error = err.Error()
			}
		}
		doc.Worktrees = append(doc.Worktrees, r)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	if !doc.Available {
		fmt.Fprintln(e.stdout, "gh stack is not installed: gh extension install github/gh-stack")
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "WORKTREE\tBRANCH\tSTACK")
	for _, r := range doc.Worktrees {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Slug, safefile.Printable(r.Branch), safefile.Printable(stackText(r)))
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	return 0
}

// stackText is a row's stack in a few words: "2/3 on main", with "restack"
// when a branch needs a rebase.
func stackText(r stackRow) string {
	switch {
	case r.Error != "":
		return "? " + r.Error
	case r.Stack == nil:
		return "-"
	}
	pos, n, rebase := 0, 0, false
	for _, b := range r.Stack.Branches {
		if b.Merged && !b.Current {
			continue
		}
		n++
		if b.Current {
			pos = n
		}
		rebase = rebase || b.NeedsRebase
	}
	parts := []string{fmt.Sprintf("%d/%d on %s", pos, n, r.Stack.Trunk)}
	if rebase {
		parts = append(parts, "restack needed")
	}
	return strings.Join(parts, ", ")
}
