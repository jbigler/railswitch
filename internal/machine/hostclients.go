package machine

import "strings"

// hostClientClause is built once: it depends on nothing but the pinned pool.
var hostClientClause = buildHostClientClause()

// HostClientClause is the Traefik v3 rule fragment that admits requests from
// the host (D50). It is static and never reads the registry, so adding a
// project never changes any app's labels.
//
// Where a host request's RemoteAddr comes from depends on the rootless
// network stack, so the clause admits both possible sources:
//   - (a) an address outside the pool 10.214.0.0/16 (127.0.0.1, a
//     rootless-netns range): !ClientIP(`10.214.0.0/16`);
//   - (b) Traefik's own address 10.214.<n>.2 on the receiving network
//     (rootlessport dials from inside Traefik's netns): one
//     ClientIP(`10.214.<n>.2/32`) per n in 0..255, the machine network
//     included.
//
// Neither admits another container, provided every railswitch network is
// isolate=strict and Traefik is the only .2 on any network that can reach it:
// strict blocks traffic to and from every other bridge network, so a
// container reaches Traefik only over its own railswitch network, from its
// subnet with a host part of 3 or more.
// Traefik v3's ClientIP takes exactly one argument, hence the || chain.
func HostClientClause() string { return hostClientClause }

func buildHostClientClause() string {
	var b strings.Builder
	b.WriteString("!ClientIP(`10.214.0.0/16`)")
	for n := range MachineSubnet + 1 {
		b.WriteString(" || ClientIP(`" + TraefikIP(n) + "/32`)")
	}
	return b.String()
}

// RouterRule is the rule of a router of project subnet n serving host h: it
// matches only h, and only clients in 10.214.<n>.0/24 or the host
// (HostClientClause).
func RouterRule(h string, n int) string {
	return "Host(`" + h + "`) && (ClientIP(`" + Subnet(n) + "`) || " + hostClientClause + ")"
}
