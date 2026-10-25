package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
)

// traefikUnit is the shared proxy's unit file and service.
const (
	traefikUnit    = machine.ProxyUnitName
	traefikService = "railswitch-traefik.service"
)

// L runs lifecycle commands for one workspace.
type L struct {
	R        sys.Runner
	WS       *workspace.Workspace
	Out, Err io.Writer
	In       io.Reader
	TTY      bool
}

func (l *L) podman() podman.Podman   { return podman.Podman{R: l.R} }
func (l *L) systemd() podman.Systemd { return podman.Systemd{R: l.R} }

// model builds the workspace's model from the machine registry.
func (l *L) model(ctx context.Context) (*render.Model, error) {
	host, err := workspace.ReadHost(ctx, l.R)
	if err != nil {
		return nil, err
	}
	reg, err := machine.Open(l.WS.Dirs.Data)
	if err != nil {
		return nil, err
	}
	defer reg.Close()
	return l.WS.Model(reg.Registry, host)
}

// instance is worktree slug's instance of m.
func instance(m *render.Model, slug string) (*render.Instance, error) {
	i := slices.IndexFunc(m.Inputs.Worktrees, func(w render.Worktree) bool { return w.Slug == slug })
	if i < 0 {
		return nil, fmt.Errorf("worktree %s is not registered for project %s", slug, m.Prefix)
	}
	return m.Instance(m.Inputs.Worktrees[i])
}

// load is the model and worktree slug's instance.
func (l *L) load(ctx context.Context, slug string) (*render.Model, *render.Instance, error) {
	m, err := l.model(ctx)
	if err != nil {
		return nil, nil, err
	}
	in, err := instance(m, slug)
	if err != nil {
		return nil, nil, err
	}
	return m, in, nil
}

func (l *L) lockPath() string { return filepath.Join(l.WS.Root, "stack.lock") }

// changes accumulates the unit changes of every render of one Up.
type changes struct{ units, traefik bool }

func (c *changes) add(d *render.Diff) {
	for _, n := range slices.Concat(d.Added, d.Changed, d.Removed) {
		if !strings.Contains(n, "/") { // units are bare filenames, workspace files relative paths
			c.units = true
		}
	}
	if slices.Contains(d.Added, traefikUnit) || slices.Contains(d.Changed, traefikUnit) {
		c.traefik = true
	}
}

// Up renders, checks, pulls, builds and seeds what worktree slug needs, then
// starts its app unit. It stops at the first error; nothing is started
// after a failure, and running it again resumes.
func (l *L) Up(ctx context.Context, slug string) error {
	var ch changes
	d, err := l.WS.Render(ctx, l.R, false)
	if err != nil {
		return err
	}
	ch.add(d)
	m, in, err := l.load(ctx, slug)
	if err != nil {
		return err
	}
	if err := l.Preflight(ctx, m, slug); err != nil {
		return err
	}

	pulled, err := l.pullServiceImages(ctx, m, in)
	if err != nil {
		return err
	}
	if pulled {
		if d, err = l.WS.Render(ctx, l.R, false); err != nil {
			return err
		}
		ch.add(d)
		if m, in, err = l.load(ctx, slug); err != nil {
			return err
		}
	}

	if _, err := l.pinBases(ctx, in, false); err != nil {
		return err
	}
	if err := l.build(ctx, in.Builds, func(int) bool { return false }); err != nil {
		return err
	}
	if err := l.seedVolumes(ctx, m, in); err != nil {
		return err
	}

	sd := l.systemd()
	reload := ch.units
	if !reload {
		if reload, err = l.anyUnloaded(ctx, m, slug); err != nil {
			return err
		}
	}
	if reload {
		if err := sd.DaemonReload(ctx); err != nil {
			return err
		}
	}
	if ch.traefik {
		if err := sd.Restart(ctx, traefikService); err != nil {
			return err
		}
	}
	return sd.Start(ctx, unitOf(m, slug, m.Services[0].ID))
}

// anyUnloaded reports whether a unit Up starts (every service of the
// worktree, its dev network and Traefik) is not loaded by systemd: a unit
// written by an earlier render (wt add, render, a failed up) that no
// daemon-reload has generated yet.
func (l *L) anyUnloaded(ctx context.Context, m *render.Model, slug string) (bool, error) {
	names, err := m.NamesFor(slug)
	if err != nil {
		return false, err
	}
	for _, u := range append(names.Units, traefikService) {
		st, err := l.systemd().LoadState(ctx, u)
		if err != nil {
			return false, err
		}
		if st != "loaded" {
			return true, nil
		}
	}
	return false, nil
}

func unitOf(m *render.Model, slug, svc string) string {
	return containerOf(m, slug, svc) + ".service"
}

// pullServiceImages pulls every registry image of the worktree that
// stack.lock has no digest for and records the digest (D15).
func (l *L) pullServiceImages(ctx context.Context, m *render.Model, in *render.Instance) (bool, error) {
	var refs []string
	for _, s := range m.Services {
		if s.Image.Kind != render.ImagePull {
			continue
		}
		ref, err := pack.Expand(s.Image.Ref, in.Scope(s.ID))
		if err != nil {
			return false, fmt.Errorf("%s image: %w", s.ID, err)
		}
		if l.WS.Lock.Digest(ref) == "" && !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	for _, ref := range refs {
		dg, err := l.podman().Pull(ctx, ref)
		if err != nil {
			return false, err
		}
		if dg == "" {
			return false, fmt.Errorf("podman reported no digest for %s", ref)
		}
		l.WS.Lock.SetDigest(ref, dg)
		if err := l.WS.Lock.Write(l.lockPath()); err != nil {
			return false, err
		}
	}
	return len(refs) > 0, nil
}

// fromRefs are the distinct FROM refs of steps, in build order.
func fromRefs(steps []render.BuildStep) []string {
	var refs []string
	for _, s := range steps {
		for _, r := range s.From {
			if !slices.Contains(refs, r) {
				refs = append(refs, r)
			}
		}
	}
	return refs
}

// repo is ref without its tag: docker.io/library/ruby:3.3.6-slim →
// docker.io/library/ruby.
func repo(ref string) string {
	if i := strings.LastIndexByte(ref, ':'); i > strings.LastIndexByte(ref, '/') {
		return ref[:i]
	}
	return ref
}

// pinBases makes every FROM image of in's builds the one stack.lock records
// (D36): a ref with no digest is pulled and recorded; one whose local image
// is missing or differs is pulled by digest and tagged. With repull, every
// ref is pulled by tag and the new digest recorded. It returns the refs
// whose recorded digest changed.
func (l *L) pinBases(ctx context.Context, in *render.Instance, repull bool) ([]string, error) {
	p, lock := l.podman(), l.WS.Lock
	var changed []string
	for _, ref := range fromRefs(in.Builds) {
		old := lock.BaseDigest(ref)
		if old == "" || repull {
			dg, err := p.Pull(ctx, ref)
			if err != nil {
				return nil, err
			}
			if dg == "" {
				return nil, fmt.Errorf("podman reported no digest for %s", ref)
			}
			if dg == old {
				continue
			}
			lock.SetBaseDigest(ref, dg)
			if err := lock.Write(l.lockPath()); err != nil {
				return nil, err
			}
			switch {
			case old != "":
				fmt.Fprintf(l.Out, "%s: %s → %s\n", ref, old, dg)
				changed = append(changed, ref)
			case repull:
				fmt.Fprintf(l.Out, "%s: (none) → %s\n", ref, dg)
			}
			continue
		}
		local, err := p.ImageDigest(ctx, ref)
		if err != nil {
			return nil, err
		}
		if local == old {
			continue
		}
		pinned := repo(ref) + "@" + old
		if err := p.PullOnly(ctx, pinned); err != nil {
			return nil, err
		}
		if err := p.Tag(ctx, pinned, ref); err != nil {
			return nil, err
		}
	}
	return changed, nil
}

// build runs each step, in order, whose tag is missing or for which force
// reports true. Build uses --pull=never: the FROM images are the pinned ones.
func (l *L) build(ctx context.Context, steps []render.BuildStep, force func(i int) bool) error {
	p := l.podman()
	for i, s := range steps {
		if !force(i) {
			ok, err := p.ImageExists(ctx, s.Tag)
			if err != nil {
				return err
			}
			if ok {
				continue
			}
		}
		fmt.Fprintf(l.Out, "building %s (%s)\n", s.Name, s.Tag)
		if err := p.Build(ctx, podman.BuildArgs{File: s.File, Tag: s.Tag, Context: s.Context, Args: s.Args}); err != nil {
			return fmt.Errorf("build %s: %w", s.Name, err)
		}
	}
	return nil
}

// seedVolumes creates every missing volume with a seed and imports the
// seed from <ws>/db-dumps/ when it is there (D18).
func (l *L) seedVolumes(ctx context.Context, m *render.Model, in *render.Instance) error {
	p := l.podman()
	for _, s := range m.Services {
		for _, v := range s.Volumes {
			if v.Seed == "" {
				continue
			}
			name, err := in.VolumeName(s.ID, v)
			if err != nil {
				return err
			}
			ok, err := p.VolumeExists(ctx, name)
			if err != nil {
				return err
			}
			if ok {
				continue
			}
			seed := filepath.Join(m.Inputs.Workspace, "db-dumps", v.Seed)
			fi, err := os.Stat(seed)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				seed = ""
			case err != nil:
				return err
			case !fi.Mode().IsRegular():
				return fmt.Errorf("seed %s: not a regular file", seed)
			}
			if err := p.VolumeCreate(ctx, name); err != nil {
				return err
			}
			if seed == "" {
				continue
			}
			if err := p.VolumeImport(ctx, name, seed); err != nil {
				// An unseeded volume would never be seeded: the next up re-creates it.
				// Ctrl-C cancelled ctx; the cleanup still runs, bounded by its own timeout.
				if rmErr := p.RemoveVolume(context.WithoutCancel(ctx), name); rmErr != nil {
					return errors.Join(err, fmt.Errorf("removing unseeded volume %s: %w", name, rmErr))
				}
				return err
			}
		}
	}
	return nil
}
