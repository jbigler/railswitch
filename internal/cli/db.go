package cli

import "context"

const dbUsage = "usage: railswitch db dump"

// railswitch db dump: dump the worktree's database and its seeded volumes
// into <workspace>/db-dumps/ (C3).
func cmdDB(ctx context.Context, e *env, args []string) int {
	if len(args) != 1 || args[0] != "dump" {
		return e.usageErr("%s", dbUsage)
	}
	l, slug, code := e.lifecycle(ctx, "db dump")
	if code != 0 {
		return code
	}
	if _, err := l.DBDump(ctx, slug); err != nil {
		return e.fail(err)
	}
	return 0
}
