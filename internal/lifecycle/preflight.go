// Package lifecycle runs a worktree's containers: up, stop, down, restart,
// status, logs, exec, build and deps, and the checks before them.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/jbigler/railswitch/internal/firewall"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/render"
)

// CheckSecrets refuses to start worktree slug while a secret one of its
// services receives has no podman secret (D33). Every missing one is named.
func CheckSecrets(ctx context.Context, p podman.Podman, m *render.Model, slug string) error {
	i := slices.IndexFunc(m.Inputs.Worktrees, func(w render.Worktree) bool { return w.Slug == slug })
	if i < 0 {
		return fmt.Errorf("worktree %s is not registered for project %s", slug, m.Prefix)
	}
	in, err := m.Instance(m.Inputs.Worktrees[i])
	if err != nil {
		return err
	}
	secrets := m.Inputs.Project.Secrets
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(secrets)) {
		received := slices.ContainsFunc(m.Services, func(s render.Service) bool {
			return slices.Contains(secrets[name].Services, s.ID)
		})
		if !received {
			continue
		}
		ok, err := p.SecretExists(ctx, in.Values["secret."+name])
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("secret %s: %w", name, err))
		case !ok:
			errs = append(errs, fmt.Errorf("secret %s is not set: railswitch secret set %s", name, name))
		}
	}
	return errors.Join(errs...)
}

// requireDescriptor refuses when the firewall hook descriptor is missing.
func requireDescriptor(ws, prefix string) error {
	d := firewall.DescriptorPath(ws, prefix)
	if _, err := os.Stat(d); err != nil {
		return fmt.Errorf("no firewall hook descriptor %s: containers would start unfirewalled (run railswitch render)", d)
	}
	return nil
}

// Preflight checks what worktree slug's containers need before anything
// starts: every absolute bind-mount source exists, the firewall hook
// descriptor exists, the worktree dir exists and every secret its services
// receive is set.
func (l *L) Preflight(ctx context.Context, m *render.Model, slug string) error {
	in, err := instance(m, slug)
	if err != nil {
		return err
	}
	for _, s := range m.Services {
		for _, mt := range s.Mounts {
			src, err := pack.Expand(mt.Source, in.Scope(s.ID))
			if err != nil {
				return fmt.Errorf("%s: mount %s: %w", s.ID, mt.Source, err)
			}
			if !filepath.IsAbs(src) {
				continue
			}
			if _, err := os.Stat(src); err != nil {
				return fmt.Errorf("%s: bind mount source %s is missing: %w", s.ID, src, err)
			}
		}
	}
	if err := requireDescriptor(m.Inputs.Workspace, m.Prefix); err != nil {
		return err
	}
	if fi, err := os.Stat(in.Worktree.Dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("worktree dir %s is missing", in.Worktree.Dir)
	}
	return CheckSecrets(ctx, l.podman(), m, slug)
}
