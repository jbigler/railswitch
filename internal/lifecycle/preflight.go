// Package lifecycle runs a worktree's containers: up, stop, down, restart,
// status, logs, exec, build and deps, and the checks before them.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/firewall"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
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

// checkProxySubnet runs before up renders. While the project's proxy
// network does not exist yet and another podman network overlaps the
// registered subnet (netavark refuses it, and the network unit fails),
// it moves the project to the lowest free subnet, which the render then
// writes. An existing proxy network on another subnet than the registered
// one is refused: units and the live network would disagree.
func (l *L) checkProxySubnet(ctx context.Context) error {
	nets, err := l.podman().NetworkSubnets(ctx)
	if err != nil {
		return fmt.Errorf("listing podman networks: %w", err)
	}
	prefix := l.WS.Project.Project.Prefix
	own := naming.ProxyNetwork(prefix)
	reg, err := machine.Open(l.WS.Dirs.Data)
	if err != nil {
		return err
	}
	defer reg.Close()
	pe, ok := reg.Project(prefix)
	if !ok {
		return nil // the render names the missing registration
	}
	registered := machine.SubnetPrefix(pe.Subnet)
	if subnets, exists := nets[own]; exists {
		if slices.Contains(subnets, registered) {
			return nil
		}
		have := "no subnet"
		if len(subnets) > 0 {
			strs := make([]string, len(subnets))
			for i, s := range subnets {
				strs[i] = s.String()
			}
			have = strings.Join(strs, ", ")
		}
		return fmt.Errorf("podman network %s has %s, but project %s is registered on %s: run railswitch down in every worktree of the project, then podman network rm %s, then railswitch up", own, have, prefix, registered, own)
	}
	users := machine.SubnetUsers(nets, own, pe.Subnet)
	if len(users) == 0 {
		return nil
	}
	var taken []netip.Prefix
	for name, subnets := range nets {
		if name != own {
			taken = append(taken, subnets...)
		}
	}
	_, moved, err := reg.MoveSubnet(prefix, taken)
	if err != nil {
		return fmt.Errorf("proxy subnet %s is used by podman network %s: %w", registered, strings.Join(users, ", "), err)
	}
	if err := reg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(l.Out, "proxy subnet %s is used by podman network %s; project %s moved to %s\n", registered, strings.Join(users, ", "), prefix, machine.Subnet(moved))
	return nil
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
