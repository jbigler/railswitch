package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/tailscale"
)

// Share serves worktree slug's share ports (every [[ports]] with share =
// true) on the tailnet, each on the HTTPS port of its own loopback port
// number (D46). It refuses a port the serve config already serves to
// another target, records the share in the registry, brings the worktree up
// with env layer 7 and the tailnet AddHost, then turns the ports on. A
// failure after the registry change is rolled back: every share port of the
// worktree served to its own target is turned off (also those an earlier
// share turned on), the share cleared, and the worktree brought up unshared.
func (l *L) Share(ctx context.Context, slug string) error {
	ts := tailscale.T{R: l.R}
	host, ip, err := ts.Self(ctx)
	if err != nil {
		return fmt.Errorf("%w\nshare needs tailscale installed and connected: run `tailscale up`", err)
	}
	_, ports, err := l.shareInfo(ctx, slug)
	if err != nil {
		return err
	}
	if len(ports) == 0 {
		return fmt.Errorf("worktree %s has nothing to share: no [[ports]] of its pack, options or tools sets share = true", slug)
	}
	served, err := ts.Served(ctx)
	if err != nil {
		return err
	}
	for _, p := range ports {
		if t, ok := served[p.Port]; ok && t != tailscale.Target(p.Port) {
			return fmt.Errorf("tailnet port %d (%s %s) is already served to %s, not by railswitch; railswitch never overwrites it", p.Port, p.Service, p.Name, t)
		}
	}
	var on []int
	for _, p := range ports {
		on = append(on, p.Port)
	}
	if err := l.setShare(slug, &machine.ShareEntry{Host: host, IP: ip.String(), Ports: on}); err != nil {
		return err
	}
	err = func() error {
		if err := l.Up(ctx, slug); err != nil { // Up renders first: layer 7 and AddHost
			return err
		}
		for _, p := range ports {
			if err := ts.ServeOn(ctx, p.Port); err != nil {
				return err
			}
		}
		return nil
	}()
	if err != nil {
		return l.rollbackShare(ctx, slug, ports, err)
	}
	fmt.Fprintf(l.Out, "shared: https://%s:%d/", host, ports[0].Port)
	var more []string
	for _, p := range ports[1:] {
		more = append(more, fmt.Sprintf("%s https://%s:%d/", p.Service, host, p.Port))
	}
	if len(more) > 0 {
		fmt.Fprintf(l.Out, " (%s)", strings.Join(more, ", "))
	}
	fmt.Fprintln(l.Out)
	return nil
}

// rollbackShare undoes a failed Share with unserve's sweep: every port of
// ports served to its own target is turned off, and only then is the share
// cleared (a failed ServeOff keeps the record, so unshare can retry). Then
// it brings the worktree up unshared, unless Ctrl-C cancelled ctx. It
// returns shareErr, joined with whatever of the rollback failed. The
// tailscale and registry undo still runs after Ctrl-C, each call bounded by
// its own timeout.
func (l *L) rollbackShare(ctx context.Context, slug string, ports []render.SharePort, shareErr error) error {
	var err error
	if err = l.unserve(context.WithoutCancel(ctx), slug, ports); err == nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("%w: run `railswitch up -w %s` to restart it unshared", ctx.Err(), slug)
		} else {
			err = l.Up(ctx, slug)
		}
	}
	if err != nil {
		return errors.Join(shareErr, fmt.Errorf("rolling back the share of %s: %w", slug, err))
	}
	return shareErr
}

// Unshare turns off worktree slug's own tailnet ports (DropShare) and
// brings it up unshared, its env back to layers 1–6.
func (l *L) Unshare(ctx context.Context, slug string) error {
	wt, ports, err := l.shareInfo(ctx, slug)
	if err != nil {
		return err
	}
	if wt.Share == nil {
		fmt.Fprintf(l.Out, "worktree %s is not shared\n", slug)
		return nil
	}
	if err := l.unserve(ctx, slug, ports); err != nil {
		return err
	}
	if err := l.Up(ctx, slug); err != nil { // Up renders first
		return err
	}
	fmt.Fprintf(l.Out, "unshared %s\n", slug)
	return nil
}

// DropShare turns off worktree slug's own tailnet ports and clears its
// share, with no render and no restart: for a worktree being torn down
// (down, wt rm, project rm). A worktree that is not shared is left alone,
// and tailscale is not run.
func (l *L) DropShare(ctx context.Context, slug string) error {
	m, err := l.model(ctx)
	if err != nil {
		return err
	}
	return l.dropShare(ctx, m, slug)
}

// dropShare is DropShare on the already built model m.
func (l *L) dropShare(ctx context.Context, m *render.Model, slug string) error {
	wt, ports, err := worktreeShare(m, slug)
	if err != nil || wt.Share == nil {
		return err
	}
	return l.unserve(ctx, slug, ports)
}

// unserve turns off each of ports that the serve config serves to its own
// loopback port, as Share set it up, and clears slug's share. A port served
// to anything else is not railswitch's and stays on.
func (l *L) unserve(ctx context.Context, slug string, ports []render.SharePort) error {
	ts := tailscale.T{R: l.R}
	served, err := ts.Served(ctx)
	if err != nil {
		return err
	}
	for _, p := range ports {
		if t, ok := served[p.Port]; ok && t == tailscale.Target(p.Port) {
			if err := ts.ServeOff(ctx, p.Port); err != nil {
				return err
			}
		}
	}
	return l.setShare(slug, nil)
}

// shareInfo is registered worktree slug and its share ports.
func (l *L) shareInfo(ctx context.Context, slug string) (render.Worktree, []render.SharePort, error) {
	m, err := l.model(ctx)
	if err != nil {
		return render.Worktree{}, nil, err
	}
	return worktreeShare(m, slug)
}

// worktreeShare is m's registered worktree slug and its share ports.
func worktreeShare(m *render.Model, slug string) (render.Worktree, []render.SharePort, error) {
	i := slices.IndexFunc(m.Inputs.Worktrees, func(w render.Worktree) bool { return w.Slug == slug })
	if i < 0 {
		return render.Worktree{}, nil, fmt.Errorf("worktree %s is not registered for project %s", slug, m.Prefix)
	}
	wt := m.Inputs.Worktrees[i]
	return wt, m.SharePorts(wt.ID), nil
}

// setShare records (nil: clears) slug's share in the registry. The registry
// is closed before anything renders: the flock is per open file.
func (l *L) setShare(slug string, s *machine.ShareEntry) error {
	reg, err := machine.Open(l.WS.Dirs.Data)
	if err != nil {
		return err
	}
	defer reg.Close()
	if err := reg.SetShare(l.WS.Project.Project.Prefix, slug, s); err != nil {
		return err
	}
	return reg.Save()
}
