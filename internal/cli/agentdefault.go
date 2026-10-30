package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/jbigler/railswitch/internal/lifecycle"
	"github.com/jbigler/railswitch/internal/safefile"
)

const agentDefaultUsage = "usage: railswitch agent default promote|apply [<name>] [--all]"

// railswitch agent default promote [<name>] [--all]: the worktree's agent
// config ([config] paths of its home) becomes the project's default for
// that agent; --all also has every other worktree's home take it.
// railswitch agent default apply [<name>] [--all]: the worktree's home
// (--all: every worktree's) takes the default. A home takes it at its
// agent's next start; a new home always does (D135).
func cmdAgentDefault(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 || args[0] != "promote" && args[0] != "apply" {
		return e.usageErr("%s", agentDefaultUsage)
	}
	verb := args[0]
	var fs *flag.FlagSet
	var all *bool
	if verb == "promote" {
		fs = e.flags("agent default promote")
		all = fs.Bool("all", false, "every other worktree's home takes it too")
	} else {
		fs = e.flags("agent default apply")
		all = fs.Bool("all", false, "every worktree's home")
	}
	pos, code, ok := parseMixed(fs, args[1:])
	if !ok {
		return code
	}
	if len(pos) > 1 {
		return e.usageErr("%s", agentDefaultUsage)
	}
	agent := ""
	if len(pos) == 1 {
		agent = pos[0]
	}
	l, slug, code := e.lifecycle(ctx, "agent default")
	if code != 0 {
		return code
	}
	var name string
	var requested []string
	var err error
	if verb == "promote" {
		var r *lifecycle.PromoteResult
		r, err = l.PromoteAgentDefault(ctx, slug, agent, *all)
		if r != nil {
			name, requested = r.Agent, r.Requested
			for _, p := range r.Skipped {
				fmt.Fprintf(e.stderr, "railswitch: skipped %s (not a regular file or directory)\n", safefile.Printable(p))
			}
		}
		if err == nil {
			fmt.Fprintf(e.stdout, "%s's %s config is the project's default for %s; new worktrees start from it\n", slug, name, name)
		}
	} else {
		name, requested, err = l.ApplyAgentDefault(ctx, slug, agent, *all)
	}
	if err != nil {
		var u *lifecycle.UsageError
		if errors.As(err, &u) {
			return e.usageErr("%s", u.Msg)
		}
		return e.fail(err)
	}
	switch {
	case len(requested) > 0:
		fmt.Fprintf(e.stdout, "%s takes it at the next start of %s in: %s (a running one: railswitch agent stop %s, then start it again)\n", name, name, strings.Join(requested, ", "), name)
	case verb == "apply":
		fmt.Fprintf(e.stdout, "%s has not run here yet: it starts from the default\n", name)
	}
	return 0
}
