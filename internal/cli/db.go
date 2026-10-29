package cli

import (
	"context"
	"os"
	"path/filepath"
)

const dbUsage = "usage: railswitch db dump | railswitch db restore [--from <dir>] [--yes] [<database>...]"

// railswitch db dump: dump the worktree's database and its seeded volumes
// into <workspace>/db-dumps/ (C3). railswitch db restore: put the dumps
// back into the running database (D121).
func cmdDB(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", dbUsage)
	}
	switch args[0] {
	case "dump":
		if len(args) != 1 {
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
	case "restore":
		fs := e.flags("db restore")
		from := fs.String("from", "", "restore the dumps in `dir` (default: <workspace>/db-dumps)")
		yes := fs.Bool("yes", false, "replace the databases without asking")
		if code, ok := parse(fs, args[1:]); !ok {
			return code
		}
		l, slug, code := e.lifecycle(ctx, "db restore")
		if code != 0 {
			return code
		}
		dir := *from
		if dir != "" {
			abs, err := filepath.Abs(dir)
			if err != nil {
				return e.fail(err)
			}
			if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
				return e.usageErr("db restore: --from %s is not a directory", dir)
			}
			dir = abs
		}
		var only []string
		if fs.NArg() > 0 {
			only = fs.Args()
		}
		if _, err := l.DBRestore(ctx, slug, dir, only, *yes); err != nil {
			return e.fail(err)
		}
		return 0
	}
	return e.usageErr("%s", dbUsage)
}
