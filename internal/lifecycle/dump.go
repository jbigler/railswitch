package lifecycle

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
)

// dumpTimeout bounds the database option's [dump] command.
const dumpTimeout = 30 * time.Minute

// DBDump dumps worktree slug's databases (C3): the database option's [dump]
// command runs in its running container, once per name its [dump] list
// prints when it has one, and each stdout becomes
// <workspace>/db-dumps/<file>; then every worktree volume with a seed
// (D18) is exported to <workspace>/db-dumps/<seed>, gzipped when the seed
// ends in .gz. Each file is written to a temp file and renamed over the
// old one, so a failure leaves the old file as it was. It prints and
// returns the written paths.
func (l *L) DBDump(ctx context.Context, slug string) ([]string, error) {
	m, in, err := l.load(ctx, slug)
	if err != nil {
		return nil, err
	}
	ch := m.Inputs.Stack.Options["database"]
	if ch == nil {
		return nil, fmt.Errorf("project %s has no database option: project.toml sets database = \"none\"", m.Prefix)
	}
	o := ch.Option
	if o.Dump == nil {
		return nil, fmt.Errorf("the database option %s declares no [dump]", pack.Ref{Name: o.Name, Version: o.Version})
	}
	ctr := containerOf(m, slug, ch.Service)
	p := l.podman()
	ok, err := p.Running(ctx, ctr)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%s is not running: railswitch up", ctr)
	}
	dir := in.Values["dumps"]
	// 0755 dir and 0644 files: the database container restores the dumps
	// at initdb as the image's own non-root user (no keep-id), to which the
	// user's files appear as another owner's. Dumps hold dev data and the
	// workspace dir is the user's own.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return nil, err
	}
	var paths []string
	write := func(name string, gz bool, c sys.Cmd) error {
		path := filepath.Join(dir, name)
		n, err := writeStream(ctx, l.R, path, gz, c)
		if err != nil {
			return err
		}
		fmt.Fprintf(l.Out, "wrote %s (%d bytes)\n", path, n)
		paths = append(paths, path)
		return nil
	}
	sc := in.Scope(ch.Service)
	dbs := []string{""} // one dump, no {dump.db}
	if o.Dump.List != nil {
		if dbs, err = l.dumpList(ctx, ctr, o, sc); err != nil {
			return nil, err
		}
	}
	for _, db := range dbs {
		argv, file, err := dumpCommand(o, dumpScope{sc, db})
		if err != nil {
			return paths, err
		}
		if err := write(file, false, sys.Cmd{Name: "podman", Args: append([]string{"exec", ctr}, argv...), Timeout: dumpTimeout}); err != nil {
			return paths, fmt.Errorf("dump %s: %w", ctr, err)
		}
	}
	for _, s := range m.Services {
		for _, v := range s.Volumes {
			if v.Seed == "" {
				continue
			}
			vol, err := in.VolumeName(s.ID, v)
			if err != nil {
				return paths, err
			}
			c := sys.Cmd{Name: "podman", Args: []string{"volume", "export", vol}, Timeout: podman.ImportTimeout}
			if err := write(v.Seed, strings.HasSuffix(v.Seed, ".gz"), c); err != nil {
				return paths, fmt.Errorf("export volume %s: %w", vol, err)
			}
		}
	}
	return paths, nil
}

// dumpScope is a [dump] scope: {dump.db} is db when db is set.
type dumpScope struct {
	pack.Scope
	db string
}

func (s dumpScope) Lookup(name string) (string, bool) {
	if name == pack.DumpDB && s.db != "" {
		return s.db, true
	}
	return s.Scope.Lookup(name)
}

// dumpDBName is a database name that is safe as a file name and an argv
// word: the list's output comes from the app's own database server.
var dumpDBName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,62}$`)

// dumpList runs o's [dump] list in ctr: the databases to dump, one per
// line. A name unsafe as a file name is skipped with a warning.
func (l *L) dumpList(ctx context.Context, ctr string, o *pack.Option, sc pack.Scope) ([]string, error) {
	argv := make([]string, len(o.Dump.List))
	for i, a := range o.Dump.List {
		var err error
		if argv[i], err = pack.Expand(a, sc); err != nil {
			return nil, fmt.Errorf("%s [dump] list: %w", o.Name, err)
		}
	}
	res, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: append([]string{"exec", ctr}, argv...)})
	if err != nil {
		return nil, fmt.Errorf("list databases in %s: %w", ctr, err)
	}
	var dbs []string
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		switch name := strings.TrimSpace(line); {
		case name == "":
		case !dumpDBName.MatchString(name) || name == "." || name == "..":
			fmt.Fprintf(l.Err, "warning: %s: database %q skipped: not a safe file name\n", ctr, safefile.Printable(name))
		default:
			dbs = append(dbs, name)
		}
	}
	if len(dbs) == 0 {
		return nil, fmt.Errorf("%s: %s [dump] list printed no database", ctr, o.Name)
	}
	return dbs, nil
}

// dumpCommand is o's [dump] command and file name expanded in sc.
func dumpCommand(o *pack.Option, sc pack.Scope) ([]string, string, error) {
	argv := make([]string, len(o.Dump.Command))
	for i, a := range o.Dump.Command {
		var err error
		if argv[i], err = pack.Expand(a, sc); err != nil {
			return nil, "", fmt.Errorf("%s [dump] command: %w", o.Name, err)
		}
	}
	file, err := pack.Expand(o.Dump.File, sc)
	if err != nil {
		return nil, "", fmt.Errorf("%s [dump] file: %w", o.Name, err)
	}
	if file == "" || strings.Contains(file, "/") || file == "." || file == ".." {
		return nil, "", fmt.Errorf("%s [dump] file %q is not a bare file name", o.Name, file)
	}
	return argv, file, nil
}

// writeStream runs c with its stdout (gzipped when gz) going to a 0644 temp
// file next to path, which it renames over path once c succeeded; on any
// failure the temp file is removed. It returns path's size.
func writeStream(ctx context.Context, r sys.Runner, path string, gz bool, c sys.Cmd) (int64, error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return 0, err
	}
	tmp := f.Name()
	err = func() error {
		var w io.Writer = f
		var zw *gzip.Writer
		if gz {
			zw = gzip.NewWriter(f)
			w = zw
		}
		c.Stdout = w
		if _, err := r.Run(ctx, c); err != nil {
			return err
		}
		if zw != nil {
			if err := zw.Close(); err != nil {
				return err
			}
		}
		return f.Sync()
	}()
	if err == nil {
		err = f.Chmod(0o644)
	}
	err = errors.Join(err, f.Close())
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return 0, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}
