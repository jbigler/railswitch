package cli

import "context"

// railswitch overview: every worktree of the project (-C), its stack,
// agent and links; --json is the dashboard's poll (docs/json.md).
func cmdOverview(ctx context.Context, e *env, args []string) int {
	fs := e.flags("overview")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("overview takes no arguments")
	}
	l, code := e.projectLifecycle(ctx)
	if code != 0 {
		return code
	}
	ov, err := l.Overview(ctx)
	if err != nil {
		return e.fail(err)
	}
	ov.DefaultsChanged = staleEjects(l.WS.Root, l.WS.Stack)
	if err := l.WriteOverview(ov, e.json); err != nil {
		return e.fail(err)
	}
	return 0
}
