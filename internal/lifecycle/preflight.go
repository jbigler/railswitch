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
// An on-demand agent's secrets are checked when it runs (L.Agent), not here:
// up starts no on-demand service.
func CheckSecrets(ctx context.Context, p podman.Podman, m *render.Model, slug string) error {
	return checkSecrets(ctx, p, m, slug, func(s *render.Service) bool { return !s.OnDemand })
}

// checkSecrets is CheckSecrets for the services keep reports.
func checkSecrets(ctx context.Context, p podman.Podman, m *render.Model, slug string, keep func(*render.Service) bool) error {
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
			return keep(&s) && slices.Contains(secrets[name].Services, s.ID)
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

// warnNotIsolated prints a WARNING naming this project's networks that
// exist without isolate=strict (D50: created before Stage 3, podman never
// changes them) and how to recreate them. It never refuses: the networks
// work, only the isolation between projects is missing. A failed check is
// a warning too.
func (l *L) warnNotIsolated(ctx context.Context) {
	reg, err := machine.Read(l.WS.Dirs.Data)
	if err != nil {
		return // checkProxySubnet and the render report the registry
	}
	nets := reg.Networks(l.WS.Project.Project.Prefix)
	iso, err := l.podman().Isolation(ctx, nets)
	if err != nil {
		fmt.Fprintf(l.Err, "WARNING: could not check that the networks are isolate=strict: %v\n", err)
		return
	}
	var not []string
	for _, n := range nets {
		if ok, exists := iso[n]; exists && !ok {
			not = append(not, n)
		}
	}
	if len(not) > 0 {
		fmt.Fprintf(l.Err, "WARNING: podman networks %s are not isolate=strict (created before Stage 3): other projects' containers reach them directly; %s\n", strings.Join(not, ", "), machine.RecreateNetworks)
	}
}

// warnGitGuard prints a WARNING when git-guard finds something to review
// (D60): a hook without a backup or unlike it, an unusual .git/config key,
// a worktree's .git pointing elsewhere. It never refuses; a failed check is
// a warning too.
func (l *L) warnGitGuard(ctx context.Context) {
	fs, err := l.WS.GitGuard(ctx, l.R)
	if err != nil {
		fmt.Fprintf(l.Err, "WARNING: git-guard: could not check: %v\n", err)
		return
	}
	if len(fs) == 0 {
		return
	}
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = f.Kind + " " + f.Path
	}
	fmt.Fprintf(l.Err, "WARNING: git-guard: %d finding(s): %s; railswitch hooks check\n", len(fs), strings.Join(parts, ", "))
}

// mkHooks creates <base>/.git/hooks (0755) when git is a directory without
// one: it is the source of git-guard's read-only mounts. A missing git is
// left to the mount check.
func mkHooks(git string) error {
	hooks := filepath.Join(git, "hooks")
	err := os.Mkdir(hooks, 0o755)
	switch {
	case err == nil:
		return os.Chmod(hooks, 0o755) // whatever the umask
	case errors.Is(err, os.ErrExist), errors.Is(err, os.ErrNotExist):
		return nil
	}
	return fmt.Errorf("git hooks dir: %w", err)
}

// checkProxySubnet runs before up renders. It refuses while another network
// uses the machine network's subnet (checkMachineSubnet). While the project's proxy
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
	if err := checkMachineSubnet(nets); err != nil {
		return err
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

// checkMachineSubnet refuses while another podman network than the
// machine network uses its reserved 10.214.255.0/24: the machine network
// unit would fail, and Traefik and Dozzle with it.
func checkMachineSubnet(nets map[string][]netip.Prefix) error {
	if users := machine.SubnetUsers(nets, machine.MachineNetwork, machine.MachineSubnet); len(users) > 0 {
		return fmt.Errorf("the machine network subnet %s is used by podman network %s: railswitch does not start Traefik and Dozzle on it; remove or move that network", machine.Subnet(machine.MachineSubnet), strings.Join(users, ", "))
	}
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
// receive is set. It first creates the worktree's agent status dir (0700,
// D58) when the project has an agent and the dir is missing, and the base
// worktree's .git/hooks (0755), the source of git-guard's read-only mounts
// (D60), when its .git exists without one.
func (l *L) Preflight(ctx context.Context, m *render.Model, slug string) error {
	in, err := instance(m, slug)
	if err != nil {
		return err
	}
	if len(m.Agents()) > 0 {
		dir := in.Values["status.dir"]
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("agent status dir: %w", err)
		}
	}
	if err := mkHooks(in.Values["base.git"]); err != nil {
		return err
	}
	for _, s := range m.Services {
		for _, mt := range in.Mounts(&s) {
			src, err := pack.Expand(mt.Source, in.Scope(s.ID))
			if err != nil {
				return fmt.Errorf("%s: mount %s: %w", s.ID, mt.Source, err)
			}
			if !filepath.IsAbs(src) {
				continue
			}
			if _, err := os.Stat(src); err != nil {
				if mt.Origin != "" {
					return fmt.Errorf("%s: bind mount source %s of %s in config.toml is missing: %w", s.ID, src, mt.Origin, err)
				}
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
