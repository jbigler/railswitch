package lifecycle

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/sys"
)

// dumpTimeout bounds the database option's [dump] command.
const dumpTimeout = 30 * time.Minute

// DBDump dumps worktree slug's database (C3): the database option's [dump]
// command runs in its running container and its stdout becomes
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
	sc := in.Scope(ch.Service)
	argv := make([]string, len(o.Dump.Command))
	for i, a := range o.Dump.Command {
		if argv[i], err = pack.Expand(a, sc); err != nil {
			return nil, fmt.Errorf("%s [dump] command: %w", o.Name, err)
		}
	}
	file, err := pack.Expand(o.Dump.File, sc)
	if err != nil {
		return nil, fmt.Errorf("%s [dump] file: %w", o.Name, err)
	}
	if file == "" || strings.Contains(file, "/") || file == "." || file == ".." {
		return nil, fmt.Errorf("%s [dump] file %q is not a bare file name", o.Name, file)
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	if err := write(file, false, sys.Cmd{Name: "podman", Args: append([]string{"exec", ctr}, argv...), Timeout: dumpTimeout}); err != nil {
		return paths, fmt.Errorf("dump %s: %w", ctr, err)
	}
	for _, s := range m.Services {
		for _, v := range s.Volumes {
			if v.Seed == "" || v.Shared {
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

// writeStream runs c with its stdout (gzipped when gz) going to a 0600 temp
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
