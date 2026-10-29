package lifecycle

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/render"
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
	files, err := dumpFiles(m, dir, o, sc)
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
	label := restoreLabel(ch.Service)
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
	done, err := l.runRestore(ctx, ctr, o, sc, files, dbs, label)
	if err == nil {
		// Whatever you restored is the database now: up never restores
		// over it on a marker an earlier failure left.
		os.Remove(restorePending(m.Inputs.Workspace, slug))
	}
	return done, err
}

// restoreLabel names database db of the option's service: "" (an option
// with one dump) is "the <service> database".
func restoreLabel(service string) func(string) string {
	return func(db string) string {
		if db == "" {
			return "the " + service + " database"
		}
		return db
	}
}

// dumpFiles are the dumps in dir the database option o names (restoreFiles),
// without the volume seeds db dump also writes there.
func dumpFiles(m *render.Model, dir string, o *pack.Option, sc pack.Scope) (map[string]string, error) {
	files, err := restoreFiles(dir, o, sc)
	if err != nil {
		return nil, err
	}
	// A dump of a database the pack leaves out (an older dump of Rails'
	// test databases, say) is never restored.
	for db := range files {
		if db != "" && skipDump(m.Inputs.Stack.Pack.DumpSkip, db) {
			delete(files, db)
		}
	}
	for _, s := range m.Services {
		for _, v := range s.Volumes {
			for db, path := range files {
				if v.Seed != "" && filepath.Base(path) == v.Seed {
					delete(files, db)
				}
			}
		}
	}
	return files, nil
}

// runRestore restores each of dbs from files into the running container
// ctr: o's [dump] prepare, then its restore with the file on stdin.
func (l *L) runRestore(ctx context.Context, ctr string, o *pack.Option, sc pack.Scope, files map[string]string, dbs []string, label func(string) string) ([]string, error) {
	var err error
	// Every command is expanded and every file opened before anything
	// runs: an argument that doesn't expand or a file that went missing (or
	// became a symlink) never leaves a database dropped and empty.
	type step struct {
		db               string
		prepare, restore []string
		f                *os.File
	}
	var steps []step
	defer func() {
		for _, st := range steps {
			st.f.Close()
		}
	}()
	for _, db := range dbs {
		dsc := dumpScope{sc, db} // "" for an option with one dump: no {dump.db}
		st := step{db: db}
		if o.Dump.Prepare != nil {
			if st.prepare, err = expandArgv(o.Dump.Prepare, dsc, o.Name+" [dump] prepare"); err != nil {
				return nil, err
			}
		}
		if st.restore, err = expandArgv(o.Dump.Restore, dsc, o.Name+" [dump] restore"); err != nil {
			return nil, err
		}
		if st.f, err = openRegular(files[db]); err != nil {
			return nil, err
		}
		steps = append(steps, st)
	}
	var done []string
	for i, st := range steps {
		fail := func(err error) ([]string, error) {
			var left []string
			for _, rest := range steps[i+1:] {
				left = append(left, label(rest.db))
			}
			msg := fmt.Sprintf("%s: %v", label(st.db), err)
			if len(done) > 0 {
				msg += "; restored before it: " + strings.Join(done, ", ")
			}
			msg += "; " + label(st.db) + " may be empty or partly restored"
			if len(left) > 0 {
				msg += "; not touched: " + strings.Join(left, ", ")
			}
			return done, errors.New(msg)
		}
		if st.prepare != nil {
			if _, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: append([]string{"exec", ctr}, st.prepare...), Stdout: l.Out, Stderr: l.Err, Timeout: dumpTimeout}); err != nil {
				return fail(fmt.Errorf("prepare in %s: %w", ctr, err))
			}
		}
		if _, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: append([]string{"exec", "-i", ctr}, st.restore...), Stdin: st.f, Stdout: l.Out, Stderr: l.Err, Timeout: dumpTimeout}); err != nil {
			return fail(fmt.Errorf("restore %s into %s: %w", files[st.db], ctr, err))
		}
		fmt.Fprintf(l.Out, "restored %s from %s\n", label(st.db), files[st.db])
		done = append(done, label(st.db))
	}
	return done, nil
}

// openRegular opens path without following a symlink, and refuses
// anything but a regular file: what is read is what was listed.
func openRegular(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("dump %s: %w", path, err)
	}
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("dump %s: not a regular file", path)
	}
	return f, nil
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

// freshRestore is a new database's restore that up runs (D122).
type freshRestore struct {
	unit, ctr string
	o         *pack.Option
	sc        pack.Scope
	files     map[string]string
	dbs       []string
	label     func(string) string
	pending   string // its marker (restorePending)
}

// freshDatabase is the restore up runs into worktree slug's database: when
// the database option declares a [dump] restore, its data volume doesn't
// exist yet (a new worktree, or one whose volumes down deleted) and the
// workspace's db-dumps/ holds dumps it names. nil otherwise.
func (l *L) freshDatabase(ctx context.Context, m *render.Model, in *render.Instance, slug string) (*freshRestore, error) {
	ch := m.Inputs.Stack.Option("database")
	if ch == nil || ch.Option.Dump == nil || ch.Option.Dump.Restore == nil {
		return nil, nil
	}
	i := slices.IndexFunc(m.Services, func(s render.Service) bool { return s.ID == ch.Service })
	if i < 0 {
		return nil, nil
	}
	s := m.Services[i]
	var data []string
	for _, v := range s.Volumes {
		if v.Cache || v.Shared || v.Seed != "" {
			continue
		}
		name, err := in.VolumeName(s.ID, v)
		if err != nil {
			return nil, err
		}
		data = append(data, name)
	}
	if len(data) == 0 {
		return nil, nil
	}
	pending := restorePending(m.Inputs.Workspace, slug)
	if _, err := os.Stat(pending); err != nil {
		// No restore left unfinished: only a new database is restored.
		for _, name := range data {
			ok, err := l.podman().VolumeExists(ctx, name)
			if err != nil || ok {
				return nil, err
			}
		}
	}
	sc := in.Scope(ch.Service)
	files, err := dumpFiles(m, in.Values["dumps"], ch.Option, sc)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil // no db-dumps/: an empty database
	case err != nil:
		return nil, err
	case len(files) == 0:
		os.Remove(pending) // nothing left to restore
		return nil, nil
	}
	dbs := make([]string, 0, len(files))
	for db := range files {
		dbs = append(dbs, db)
	}
	slices.Sort(dbs)
	return &freshRestore{
		unit: unitOf(m, slug, ch.Service), ctr: containerOf(m, slug, ch.Service),
		o: ch.Option, sc: sc, files: files, dbs: dbs, label: restoreLabel(ch.Service),
		pending: pending,
	}, nil
}

// restorePending marks worktree slug's new database as not yet restored:
// written before up starts it, removed once a restore finished, so an up
// or a db restore after a failure or a crash restores it then (D122).
func restorePending(ws, slug string) string {
	return filepath.Join(ws, ".railswitch", "restore-pending", slug)
}

// markRestorePending writes f's marker.
func (f *freshRestore) markPending() error {
	if err := os.MkdirAll(filepath.Dir(f.pending), 0o700); err != nil {
		return err
	}
	return os.WriteFile(f.pending, []byte("the dumps are not restored into this worktree's new database yet\n"), 0o600)
}
