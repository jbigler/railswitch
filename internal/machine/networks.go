package machine

import (
	"cmp"
	"slices"

	"github.com/jbigler/railswitch/internal/naming"
)

// RecreateNetworks is how to give a network created before Stage 3 its
// isolate=strict (D50): podman creates a network once (`network create
// --ignore`) and never changes its options. docs/stage3-acceptance.md
// ("Upgrading from Stage 2") has the full procedure.
const RecreateNetworks = "recreate them (docs/stage3-acceptance.md, \"Upgrading from Stage 2\"): " +
	"railswitch down in every worktree of the project, " +
	"podman network disconnect <network> " + ProxyContainer + ", " +
	"systemctl --user stop the network services, " +
	"podman network rm <network>…, then railswitch up in every worktree; " +
	"Quadlet makes Traefik require each network service, so stopping them stops Traefik and every project's routes drop until railswitch up starts it again"

// Networks names the railswitch networks of project prefix: its proxy
// network, then each worktree's dev network, by slug. With prefix "", it
// is the machine network, then every project's, by prefix.
func (r *Registry) Networks(prefix string) []string {
	var out []string
	prefixes := []string{prefix}
	if prefix == "" {
		out = append(out, MachineNetwork)
		prefixes = prefixes[:0]
		for _, p := range r.Projects {
			prefixes = append(prefixes, p.Prefix)
		}
		slices.Sort(prefixes)
	}
	for _, p := range prefixes {
		out = append(out, naming.ProxyNetwork(p))
		wts := r.WorktreesOf(p)
		slices.SortFunc(wts, func(a, b WorktreeEntry) int { return cmp.Compare(a.Slug, b.Slug) })
		for _, w := range wts {
			out = append(out, naming.DevNetwork(p, w.Slug))
		}
	}
	return out
}
