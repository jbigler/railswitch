// Package lifecycle runs a worktree's containers: up, stop, down, status,
// logs, exec and build, and the checks before them.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

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
