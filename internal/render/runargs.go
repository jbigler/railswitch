package render

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
)

// The settings of a service's container, shared by its Quadlet unit and a
// transient `podman run` of it (exec, deps), so the two cannot drift.

// Worktree containers run as the host user mapped to uid/gid 1000.
const (
	WorktreeUserNS = "keep-id:uid=1000,gid=1000"
	WorktreeUser   = "1000:1000"
)

// HooksDir is the --hooks-dir holding the firewall hook descriptor of
// workspace ws.
func HooksDir(ws string) string {
	return filepath.Join(ws, ".railswitch", "firewall", "hooks.d")
}

// Firewalled reports whether s's container runs the firewall hook.
func (s *Service) Firewalled() bool {
	return !(len(s.Firewall) == 1 && s.Firewall[0] == "self")
}

// DropsAllCaps reports whether s drops every capability (D34): data images
// switch users in their entrypoints and keep podman's defaults; a service
// that adds capabilities (the browser) drops none.
func (s *Service) DropsAllCaps() bool {
	return s.Role != RoleData && len(s.Unit.AddCapability) == 0
}

// Networks are the networks s's container joins in this worktree: the dev
// network, then the proxy network for the app and a routed service.
func (in *Instance) Networks(s *Service) []string {
	n := []string{naming.DevNetwork(in.m.Prefix, in.Worktree.Slug)}
	if s.onProxy() {
		n = append(n, naming.ProxyNetwork(in.m.Prefix))
	}
	return n
}

func (s *Service) onProxy() bool {
	for _, n := range s.Networks {
		if n == "proxy" {
			return true
		}
	}
	return false
}

// FirewallAnnotations are the annotations the firewall hook reads for a
// container of s on networks; nil when s is not firewalled. The mode is
// s.FirewallMode when set (an agent's: D55), else the project's.
func (in *Instance) FirewallAnnotations(s *Service, networks []string) []string {
	if !s.Firewalled() {
		return nil
	}
	p := in.m.Prefix
	mode := s.FirewallMode
	if mode == "" {
		mode = in.m.Inputs.Project.Firewall.Mode
	}
	if mode == "" {
		mode = "log"
	}
	return []string{
		p + ".firewall=" + strings.Join(s.Firewall, ","),
		p + ".firewall.networks=" + strings.Join(networks, ","),
		p + ".firewall.mode=" + mode,
	}
}

// VolumeSpecs are s's bind mounts, then its named volumes, as podman -v
// (Quadlet Volume=) values: "<source>:<target>[:<options>]".
func (in *Instance) VolumeSpecs(s *Service) ([]string, error) {
	sc := in.Scope(s.ID)
	var errs []error
	x := func(t string) string {
		v, err := pack.Expand(t, sc)
		errs = append(errs, err)
		return v
	}
	var out []string
	for _, mt := range s.Mounts {
		v := x(mt.Source) + ":" + x(mt.Target)
		if mt.Options != "" {
			v += ":" + mt.Options
		}
		out = append(out, v)
	}
	for _, vol := range s.Volumes {
		name, err := in.VolumeName(s.ID, vol)
		if err != nil {
			return nil, err
		}
		v := name + ":" + x(vol.Mount)
		if vol.Chown {
			v += ":U"
		}
		out = append(out, v)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}
