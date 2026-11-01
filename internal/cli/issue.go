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

const issueUsage = "usage: railswitch [--json] issue ls"

// railswitch issue ls: the project's open GitHub issues (D144), from the
// host's gh; the repo is project.toml's, as for pr ls.
func cmdIssue(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 || args[0] != "ls" {
		return e.usageErr("%s", issueUsage)
	}
	fs := e.flags("issue ls")
	if code, ok := parse(fs, args[1:]); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("%s", issueUsage)
	}
	w, err := workspace.Load(ctx, e.dir, "")
	if err != nil {
		return e.fail(err)
	}
	repo, err := prs.Repo(w.Project.Project.Repo)
	if err != nil {
		return e.fail(fmt.Errorf("issue ls: %w", err))
	}
	issues, err := prs.Issues(ctx, e.run, repo)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(struct {
			Issues []prs.Issue `json:"issues"`
		}{issues}); err != nil {
			return e.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ISSUE\tAUTHOR\tASSIGNEES\tLABELS\tTITLE")
	dash := func(s []string) string {
		if len(s) == 0 {
			return "-"
		}
		return safefile.Printable(strings.Join(s, ","))
	}
	for _, i := range issues {
		fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\t%s\n", i.Number, safefile.Printable(i.Author), dash(i.Assignees), dash(i.Labels), safefile.Printable(i.Title))
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	return 0
}
