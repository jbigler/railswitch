package lifecycle

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/sys"
)

// DBRestore puts dumps back into worktree slug's running database (D121):
// each file in from ("": <workspace>/db-dumps/) that the database option's
// [dump] file names (one per database with a [dump] list) replaces its
// database: [dump] prepare empties it, then [dump] restore reads the file
// on its stdin, both in the database's container. only, when set, limits it
// to those databases. It asks first (each database by name) unless yes; no
// terminal and no yes refuses. It returns the databases restored.
func (l *L) DBRestore(ctx context.Context, slug, from string, only []string, yes bool) ([]string, error) {
	m, in, err := l.load(ctx, slug)
	if err != nil {
		return nil, err
	}
	ch := m.Inputs.Stack.Option("database")
	if ch == nil {
		return nil, fmt.Errorf("project %s has no database option: project.toml sets database = \"none\"", m.Prefix)
	}
	o := ch.Option
	if o.Dump == nil || o.Dump.Restore == nil {
		err := fmt.Errorf("the database option %s declares no [dump] restore", ch.Ref)
		if slices.Contains(l.WS.Pending, ch.Ref) {
			// The project runs its accepted copy (D97); the update may add it.
			err = fmt.Errorf("%w in the copy this project accepted; an update of it waits for review: railswitch lock --review", err)
		}
		return nil, err
	}
	ctr := containerOf(m, slug, ch.Service)
	ok, err := l.podman().Running(ctx, ctr)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%s is not running: railswitch up", ctr)
	}
	dir := from
	if dir == "" {
		dir = in.Values["dumps"]
	}
	sc := in.Scope(ch.Service)
	files, err := restoreFiles(dir, o, sc)
	if err != nil {
		return nil, err
	}
	if only != nil {
		for _, db := range only {
			if _, ok := files[db]; !ok {
				return nil, fmt.Errorf("no dump of %s in %s", db, dir)
			}
		}
		for db := range files {
			if !slices.Contains(only, db) {
				delete(files, db)
			}
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no dumps in %s (railswitch db dump writes them)", dir)
	}
	dbs := make([]string, 0, len(files))
	for db := range files {
		dbs = append(dbs, db)
	}
	slices.Sort(dbs)
	label := func(db string) string {
		if db == "" {
			return "the " + ch.Service + " database"
		}
		return db
	}
	if !yes {
		names := make([]string, len(dbs))
		for i, db := range dbs {
			names[i] = label(db)
		}
		what := fmt.Sprintf("replace %s of worktree %s with the dumps in %s", strings.Join(names, ", "), slug, dir)
		if !l.TTY {
			return nil, fmt.Errorf("db restore would %s: pass --yes (no terminal to confirm on)", what)
		}
		fmt.Fprintf(l.Out, "This will %s; what they hold now is lost. Continue? [y/N] ", what)
		line, _ := bufio.NewReader(l.In).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(l.Out, "aborted")
			return nil, nil
		}
	}
	var done []string
	for _, db := range dbs {
		dsc := dumpScope{sc, db}
		if o.Dump.List == nil {
			dsc.db = ""
		}
		if o.Dump.Prepare != nil {
			argv, err := expandArgv(o.Dump.Prepare, dsc, o.Name+" [dump] prepare")
			if err != nil {
				return done, err
			}
			if _, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: append([]string{"exec", ctr}, argv...), Stdout: l.Out, Stderr: l.Err, Timeout: dumpTimeout}); err != nil {
				return done, fmt.Errorf("prepare %s in %s: %w", db, ctr, err)
			}
		}
		argv, err := expandArgv(o.Dump.Restore, dsc, o.Name+" [dump] restore")
		if err != nil {
			return done, err
		}
		f, err := os.Open(files[db])
		if err != nil {
			return done, err
		}
		_, err = l.R.Run(ctx, sys.Cmd{Name: "podman", Args: append([]string{"exec", "-i", ctr}, argv...), Stdin: f, Stdout: l.Out, Stderr: l.Err, Timeout: dumpTimeout})
		f.Close()
		if err != nil {
			return done, fmt.Errorf("restore %s into %s: %w", files[db], ctr, err)
		}
		fmt.Fprintf(l.Out, "restored %s from %s\n", label(db), files[db])
		done = append(done, db)
	}
	return done, nil
}

// restoreFiles are the dumps in dir that o's [dump] file names, by the
// database each is for ("" for an option that dumps one database): regular
// files only, a name a database can have.
func restoreFiles(dir string, o *pack.Option, sc pack.Scope) (map[string]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("dumps: %w", err)
	}
	ph := "{" + pack.DumpDB + "}"
	var re *regexp.Regexp
	single := ""
	if o.Dump.List != nil {
		before, after, _ := strings.Cut(o.Dump.File, ph)
		b, err := pack.Expand(before, sc)
		if err != nil {
			return nil, err
		}
		a, err := pack.Expand(after, sc)
		if err != nil {
			return nil, err
		}
		re = regexp.MustCompile("^" + regexp.QuoteMeta(b) + "(.+)" + regexp.QuoteMeta(a) + "$")
	} else if single, err = pack.Expand(o.Dump.File, sc); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, ent := range ents {
		if !ent.Type().IsRegular() {
			continue
		}
		name := ent.Name()
		switch {
		case re != nil:
			if m := re.FindStringSubmatch(name); m != nil && dumpDBName.MatchString(m[1]) && m[1] != "." && m[1] != ".." {
				out[m[1]] = filepath.Join(dir, name)
			}
		case name == single:
			out[""] = filepath.Join(dir, name)
		}
	}
	return out, nil
}

// expandArgv is argv with every placeholder expanded in sc.
func expandArgv(argv []string, sc pack.Scope, what string) ([]string, error) {
	out := make([]string, len(argv))
	for i, a := range argv {
		var err error
		if out[i], err = pack.Expand(a, sc); err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
	}
	return out, nil
}
