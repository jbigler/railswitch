package cli

import (
	"context"
	"os/exec"
)

const nvimUsage = "usage: railswitch nvim | railswitch nvim update | railswitch nvim reset-config [--yes]"

// lookPath finds a host program for lifecycle (the host's nvim); tests
// replace it.
var lookPath = exec.LookPath

// railswitch nvim: attach to the worktree's nvim server (C4); nvim update:
// update the shared config from [tools.nvim] config_repo; nvim
// reset-config [--yes]: empty the shared config volume (D54).
func cmdNvim(ctx context.Context, e *env, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "":
		l, slug, code := e.lifecycle(ctx, "nvim")
		if code != 0 {
			return code
		}
		if err := l.Nvim(ctx, slug); err != nil {
			return e.failCommand(err)
		}
		return 0
	case "update":
		if len(args) != 0 {
			return e.usageErr("%s", nvimUsage)
		}
		l, slug, code := e.lifecycle(ctx, "nvim update")
		if code != 0 {
			return code
		}
		if err := l.NvimUpdate(ctx, slug); err != nil {
			return e.fail(err)
		}
		return 0
	case "reset-config":
		fs := e.flags("nvim reset-config")
		yes := fs.Bool("yes", false, "delete the config volume without asking")
		if code, ok := parse(fs, args); !ok {
			return code
		}
		if fs.NArg() != 0 {
			return e.usageErr("%s", nvimUsage)
		}
		l, code := e.projectLifecycle(ctx)
		if code != 0 {
			return code
		}
		if err := l.NvimResetConfig(ctx, *yes); err != nil {
			return e.fail(err)
		}
		return 0
	}
	return e.usageErr("%s", nvimUsage)
}
