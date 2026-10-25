// Package naming holds the rules for worktree slugs, prefixes and service ids,
// and builds every podman/systemd object name (D6, D7). Names are only ever
// built, never parsed back into their parts.
package naming

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// SlugMaxLen caps a worktree slug: it keeps DNS labels well under 63 chars.
const SlugMaxLen = 40

// ErrEmptyName is returned by Sanitize when nothing of the name is left.
var ErrEmptyName = errors.New("name has no letters or digits")

var (
	prefixRe  = regexp.MustCompile(`^[a-z][a-z0-9]{1,15}$`)
	serviceRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
)

// Sanitize turns a branch or worktree name into a slug: lowercase, every
// character other than an ASCII letter or digit becomes '-', runs of '-'
// collapse, leading and trailing '-' go, the result is capped at SlugMaxLen
// and any '-' the cap left at the end goes too. A port of rails-dev-podman's
// lib.sh sanitize_worktree_name.
func Sanitize(name string) (string, error) {
	var b strings.Builder
	dash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			dash = false
		default:
			if !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > SlugMaxLen {
		s = strings.TrimRight(s[:SlugMaxLen], "-")
	}
	if s == "" {
		return "", ErrEmptyName
	}
	return s, nil
}

// ValidPrefix checks a project prefix (D7): no '-' or '_', since both
// separate the parts of object names.
func ValidPrefix(p string) error {
	if !prefixRe.MatchString(p) {
		return fmt.Errorf("prefix %q must match %s (lowercase letters and digits, 2-16 chars, no - or _)", p, prefixRe)
	}
	return nil
}

// ValidService checks a service id: ^[a-z][a-z0-9-]{0,30}$ and not an
// HSTS-preloaded TLD.
func ValidService(id string) error {
	if !serviceRe.MatchString(id) {
		return fmt.Errorf("service id %q must match %s", id, serviceRe)
	}
	if hstsTLDs[id] {
		return fmt.Errorf("service id %q is an HSTS-preloaded TLD: browsers force HTTPS on the host %q; pick another id", id, id)
	}
	return nil
}

// Container is the name of a worktree's service container, its .container
// file and its .service unit: "filial-feat-rails".
func Container(prefix, slug, svc string) string {
	return prefix + "-" + slug + "-" + svc
}

// DevNetworkUnit is a worktree's dev network unit file: "filial-feat-dev.network".
func DevNetworkUnit(prefix, slug string) string {
	return DevNetwork(prefix, slug) + ".network"
}

// WorktreeVolume is a per-worktree volume: "filial-feat-db-data-pg16".
func WorktreeVolume(prefix, slug, vol string) string {
	return prefix + "-" + slug + "-" + vol
}

// Shared is a project-wide object shared by every worktree: "filial_npm_cache".
func Shared(prefix, what string) string {
	return prefix + "_" + what
}

// DevNetwork is a worktree's dev network (NetworkName=): "filial-feat-dev".
func DevNetwork(prefix, slug string) string {
	return prefix + "-" + slug + "-dev"
}

// ProxyNetwork is the project's proxy network (NetworkName=): "filial_proxy".
func ProxyNetwork(prefix string) string {
	return Shared(prefix, "proxy")
}

// ProxyNetworkUnit is the project's proxy network unit file: "filial-proxy.network".
func ProxyNetworkUnit(prefix string) string {
	return prefix + "-proxy.network"
}
