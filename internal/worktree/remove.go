package worktree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/podman"
)

// Remove tears down the worktree target names (see resolve) and removes
// it: its tailnet share is turned off (Unshare), its units are stopped, its
// containers, per-worktree volumes and dev network removed by exact name,
// its env and agent status dirs, registry entry and checkout deleted, and the project
// re-rendered (D28). The base worktree is refused, and so is a dirty one
// unless force. force also deletes the worktree's home dirs (homeDirs:
// nvim's and its agents'), which are kept otherwise: they hold logins.
// Nothing changes when a registered worktree's directory is missing.
func (m *Manager) Remove(ctx context.Context, target string, force bool) error {
	entries, err := m.registered()
	if err != nil {
		return err
	}
	if err := m.present(entries); err != nil {
		return err
	}
	base, err := m.baseDir()
	if err != nil {
		return err
	}
	wts, err := m.worktrees(ctx, base, entries)
	if err != nil {
		return err
	}
	w, err := resolve(wts, target)
	if err != nil {
		return err
	}
	if realPath(w.Dir) == realPath(base) {
		return fmt.Errorf("%s is the base worktree; it is never removed", w.Dir)
	}
	if w.Git {
		dirty, err := m.dirty(ctx, w.Dir)
		if err != nil {
			return err
		}
		if dirty && !force {
			return fmt.Errorf("worktree %s has uncommitted changes or untracked files; commit them, or pass --force to discard them", w.Slug)
		}
	}

	if w.ID != 0 {
		if err := m.Unshare(ctx, w.Slug); err != nil {
			return err
		}
		if err := m.teardown(ctx, w.Slug); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(m.Out, "worktree %s is not registered: no containers to remove\n", w.Slug)
	}
	for _, d := range []string{"env", "status", "restore-pending"} {
		if err := os.RemoveAll(filepath.Join(m.WS, ".railswitch", d, w.Slug)); err != nil {
			return err
		}
	}
	for _, d := range homeDirs {
		home := filepath.Join(m.WS, ".railswitch", d, w.Slug)
		if force {
			if err := os.RemoveAll(home); err != nil {
				return err
			}
		} else if _, err := os.Lstat(home); err == nil {
			fmt.Fprintf(m.Out, "kept %s (--force deletes it)\n", home)
		}
	}
	if err := m.unregister(w.Slug); err != nil {
		return err
	}
	if w.Git {
		if _, err := m.gitT(ctx, gitLong, base, "worktree", "remove", "--force", w.Dir); err != nil {
			return fmt.Errorf("git worktree remove %s: %w", w.Dir, err)
		}
	} else {
		fmt.Fprintf(m.Out, "%s is not a git worktree; left in place\n", w.Dir)
	}
	if err := m.Render(ctx); err != nil {
		return err
	}
	return podman.Systemd{R: m.R}.DaemonReload(ctx)
}

// homeDirs are the .railswitch dirs that hold a worktree's homes, each in
// <dir>/<slug> (render's homeDir and agentHomeDir).
var homeDirs = []string{"home", "agent-home"}

// teardown stops slug's units and removes its containers, per-worktree
// volumes and dev network, each by its exact name. The units are reloaded
// first, so that every rendered unit is loaded and stop finds it.
func (m *Manager) teardown(ctx context.Context, slug string) error {
	names, err := m.Names(slug)
	if err != nil {
		return err
	}
	sd, pm := podman.Systemd{R: m.R}, podman.Podman{R: m.R}
	if len(names.Units) > 0 {
		if err := sd.DaemonReload(ctx); err != nil {
			return err
		}
		if err := sd.StopLoaded(ctx, names.Units...); err != nil {
			return err
		}
	}
	for _, c := range names.Containers {
		if err := pm.RemoveContainer(ctx, c); err != nil {
			return err
		}
	}
	for _, v := range names.Volumes {
		if err := pm.RemoveVolume(ctx, v); err != nil {
			return err
		}
	}
	for _, n := range names.Networks {
		if err := pm.RemoveNetwork(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// unregister drops slug from the registry; an absent entry is fine.
func (m *Manager) unregister(slug string) error {
	reg, err := machine.Open(m.Data)
	if err != nil {
		return err
	}
	defer reg.Close()
	if err := reg.RemoveWorktree(m.Pfx, slug); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return reg.Save()
}
