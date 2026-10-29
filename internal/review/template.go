package review

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// templateManifest is a template's manifest file, stack.toml (D108).
const templateManifest = "stack.toml"

// templateFlags are the flagged changes from old to new stack.toml: the
// lines a template writes into a new project that widen what it may reach
// or hand on (D98's checks, applied to project.toml lines). Values are
// compared by their path, so reordering or reformatting adds no flag.
// Tasks, health checks, env and the rest run inside the project's
// firewalled containers and are shown as diffs, as a package's are; hooks
// too, but they run without being asked (D126), so each is flagged.
func templateFlags(old, new []byte) []Flag {
	var out []Flag
	add := func(check, format string, args ...any) {
		out = append(out, Flag{Check: check, Text: fmt.Sprintf(format, args...)})
	}
	var a, b map[string]any
	if _, err := toml.Decode(string(old), &a); err != nil {
		a = nil
	}
	if _, err := toml.Decode(string(new), &b); err != nil {
		add(CheckRunsHere, "does not load: %v", err)
		return out
	}
	before, after := flatten(a), flatten(b)
	for _, k := range slices.Sorted(maps.Keys(before)) {
		if _, ok := after[k]; !ok && strings.HasPrefix(k, "hooks.") {
			add(CheckRunsHere, "%s: %s -> (none)", k, before[k])
		}
	}
	for _, k := range slices.Sorted(maps.Keys(after)) {
		v := after[k]
		if before[k] == v {
			continue
		}
		var check string
		switch {
		case strings.HasPrefix(k, "firewall."):
			check = CheckFirewall // the project's egress mode and allow lists
		case strings.HasPrefix(k, "secret."):
			check = CheckPrivilege // who receives a secret, and where it may go
		case strings.HasPrefix(k, "routes."):
			check = CheckPort // a host Traefik routes to a service
		case strings.HasPrefix(k, "hooks."):
			check = CheckRunsHere // runs on every start (deps: with the registries) or once per worktree
		default:
			continue
		}
		was := before[k]
		if was == "" {
			was = "(none)"
		}
		add(check, "%s: %s -> %s", k, was, v)
	}
	return out
}

// flatten is m's leaf values by dotted path; an array of tables is keyed
// by each table's name (or its index), so a reordered list changes nothing.
func flatten(m map[string]any) map[string]string {
	out := map[string]string{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, x := range v {
				walk(prefix+"."+k, x)
			}
		case []map[string]any:
			for i, t := range v {
				key := fmt.Sprint(i)
				if n, ok := t["name"].(string); ok && n != "" {
					key = n
				}
				walk(prefix+"."+key, t)
			}
		case []any:
			s := make([]string, len(v))
			for i, x := range v {
				s[i] = fmt.Sprint(x)
			}
			if strings.HasPrefix(prefix, ".hooks.") {
				// Commands: their order matters and each is its own shell.
				for i := range s {
					s[i] = strconv.Quote(s[i])
				}
				out[prefix[1:]] = strings.Join(s, " ")
				break
			}
			slices.Sort(s)
			out[prefix[1:]] = strings.Join(s, ", ")
		default:
			out[prefix[1:]] = fmt.Sprint(v)
		}
	}
	walk("", m)
	return out
}
