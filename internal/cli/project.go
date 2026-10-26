package cli

import (
	"context"
	"os"

	"github.com/jbigler/railswitch/internal/lifecycle"
	"github.com/jbigler/railswitch/internal/machine"
)

const projectUsage = "usage: railswitch project rm <prefix> [--yes] [--purge]"

// railswitch project rm <prefix> [--yes] [--purge]: unregister a project
// and tear down its worktrees, units and networks (lifecycle.ProjectRemove).
func cmdProject(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 || args[0] != "rm" {
		return e.usageErr("%s", projectUsage)
	}
	fs := e.flags("project rm")
	yes := fs.Bool("yes", false, "remove without asking")
	purge := fs.Bool("purge", false, "also delete the project's shared volumes and <workspace>/.railswitch")
	prefix, code, ok := parseNamed(e, fs, args[1:], projectUsage)
	if !ok {
		return code
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	tty := false
	if f, ok := e.stdin.(*os.File); ok && isTerminal(f) {
		tty = true
	}
	if err := lifecycle.ProjectRemove(ctx, e.run, dirs, e.stdout, e.stdin, tty, prefix, *yes, *purge); err != nil {
		return e.fail(err)
	}
	return 0
}
