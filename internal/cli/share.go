package cli

import (
	"context"

	"github.com/jbigler/railswitch/internal/lifecycle"
)

// railswitch share [<worktree>]: serve the worktree's share ports on the
// tailnet, each on its own HTTPS port (D46).
// railswitch unshare [<worktree>]: turn them off again.
// The worktree is the argument, -w, or the one the working directory is in.
var (
	cmdShare   = shareCommand("share", (*lifecycle.L).Share)
	cmdUnshare = shareCommand("unshare", (*lifecycle.L).Unshare)
)

func shareCommand(name string, run func(*lifecycle.L, context.Context, string) error) command {
	return func(ctx context.Context, e *env, args []string) int {
		fs := e.flags(name)
		if code, ok := parse(fs, args); !ok {
			return code
		}
		usage := "usage: railswitch " + name + " [<worktree>]"
		switch {
		case fs.NArg() > 1:
			return e.usageErr("%s", usage)
		case fs.NArg() == 1:
			if e.slug != "" && e.slug != fs.Arg(0) {
				return e.usageErr("%s: -w %s and the argument %s name different worktrees", name, e.slug, fs.Arg(0))
			}
			e.slug = fs.Arg(0)
		}
		l, slug, code := e.lifecycle(ctx, name)
		if code != 0 {
			return code
		}
		if err := run(l, ctx, slug); err != nil {
			return e.fail(err)
		}
		return 0
	}
}
