package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"
)

const dbUsage = "usage: railswitch db dump | railswitch db restore [--from <dir>] [--yes] [<database>|<service>...] | railswitch db snapshot [<name>] | railswitch [--json] db snapshot ls | railswitch db snapshot restore [--yes] <name> | railswitch db snapshot rm <name>"

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
		// Flags may follow the database names (db restore mydb --yes).
		only, code, ok := parseMixed(fs, args[1:])
		if !ok {
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
		if _, err := l.DBRestore(ctx, slug, dir, only, *yes); err != nil {
			return e.fail(err)
		}
		return 0
	case "snapshot":
		return cmdDBSnapshot(ctx, e, args[1:])
	}
	return e.usageErr("%s", dbUsage)
}

// railswitch db snapshot [<name>] | ls | restore [--yes] <name> | rm
// <name>: named dumps of the worktree kept in
// <workspace>/.railswitch/snapshots/<worktree>/ (D157).
func cmdDBSnapshot(ctx context.Context, e *env, args []string) int {
	verb := ""
	if len(args) > 0 {
		verb = args[0]
	}
	switch verb {
	case "ls":
		if len(args) != 1 {
			return e.usageErr("%s", dbUsage)
		}
		l, slug, code := e.lifecycle(ctx, "db snapshot ls")
		if code != 0 {
			return code
		}
		snaps, err := l.Snapshots(slug)
		if err != nil {
			return e.fail(err)
		}
		if e.json {
			if err := json.NewEncoder(e.stdout).Encode(snaps); err != nil {
				return e.fail(err)
			}
			return 0
		}
		tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tCREATED\tSIZE")
		for _, s := range snaps {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, s.Created.Local().Format("2006-01-02 15:04:05"), humanBytes(s.Bytes))
		}
		if err := tw.Flush(); err != nil {
			return e.fail(err)
		}
		return 0
	case "restore":
		fs := e.flags("db snapshot restore")
		yes := fs.Bool("yes", false, "replace the databases without asking")
		pos, code, ok := parseMixed(fs, args[1:])
		if !ok {
			return code
		}
		if len(pos) != 1 {
			return e.usageErr("%s", dbUsage)
		}
		l, slug, code := e.lifecycle(ctx, "db snapshot restore")
		if code != 0 {
			return code
		}
		if _, err := l.DBSnapshotRestore(ctx, slug, pos[0], *yes); err != nil {
			return e.fail(err)
		}
		return 0
	case "rm":
		if len(args) != 2 {
			return e.usageErr("%s", dbUsage)
		}
		l, slug, code := e.lifecycle(ctx, "db snapshot rm")
		if code != 0 {
			return code
		}
		if err := l.DBSnapshotRemove(slug, args[1]); err != nil {
			return e.fail(err)
		}
		return 0
	}
	if len(args) > 1 {
		return e.usageErr("%s", dbUsage)
	}
	name := verb
	if name == "" {
		name = time.Now().Format("20060102-150405")
	}
	l, slug, code := e.lifecycle(ctx, "db snapshot")
	if code != 0 {
		return code
	}
	if _, err := l.DBSnapshot(ctx, slug, name); err != nil {
		return e.fail(err)
	}
	return 0
}
