package render

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// Finding is one lint failure in a rendered unit file.
type Finding struct {
	File      string
	Line      int
	Rule, Msg string
}

func (f Finding) Error() string { return fmt.Sprintf("%s:%d: %s: %s", f.File, f.Line, f.Rule, f.Msg) }

// Lint checks rendered unit files, keyed by filename, for the mistakes that
// render cleanly yet break at run time: a $ systemd would expand, a value
// Quadlet would split, a network without its service dependency, a
// non-loopback publish, firewall annotations that disagree with the
// networks, a volume outside the project's names, a deps unit that stays
// active. Findings are ordered by file, then line.
func Lint(files map[string][]byte) []Finding {
	units := map[string]*lintUnit{}
	for name, data := range files {
		units[name] = parseUnit(name, data)
	}
	var out []Finding
	for _, name := range slices.Sorted(maps.Keys(units)) {
		out = append(out, units[name].lint(units)...)
	}
	return out
}

// lintEntry is one key=value with its continuation lines joined; Line is
// where it starts.
type lintEntry struct {
	Section, Key, Value string
	Line                int
}

type lintUnit struct {
	name    string
	lines   []string
	entries []lintEntry
	machine bool
	prefix  string // from the marker; "" for a machine unit
	slug    string // from the marker; "" for a project-wide or machine unit
	role    string // from "# role: <role>" on line 2
}

// markerScope is the last parenthesised group of the marker line.
var markerScope = regexp.MustCompile(`\(([^()]*)\)[^()]*$`)

func parseUnit(name string, data []byte) *lintUnit {
	u := &lintUnit{name: name, lines: strings.Split(string(data), "\n")}
	if len(u.lines) > 0 && strings.HasPrefix(u.lines[0], Marker) {
		if m := markerScope.FindStringSubmatch(u.lines[0]); m != nil {
			if m[1] == "machine" {
				u.machine = true
			} else {
				for part := range strings.SplitSeq(m[1], ", ") {
					if v, ok := strings.CutPrefix(part, "prefix "); ok {
						u.prefix = v
					} else if v, ok := strings.CutPrefix(part, "worktree "); ok {
						u.slug = v
					}
				}
			}
		}
	}
	if len(u.lines) > 1 {
		u.role, _ = strings.CutPrefix(u.lines[1], "# role: ")
	}
	section := ""
	for i := 0; i < len(u.lines); i++ {
		l := strings.TrimSpace(u.lines[i])
		if l == "" || l[0] == '#' || l[0] == ';' {
			continue
		}
		if l[0] == '[' && l[len(l)-1] == ']' {
			section = l[1 : len(l)-1]
			continue
		}
		start := i + 1
		// systemd joins a line ending in \ with the next, the \ becoming a space.
		for strings.HasSuffix(l, `\`) && i+1 < len(u.lines) {
			i++
			l = l[:len(l)-1] + " " + strings.TrimSpace(u.lines[i])
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		u.entries = append(u.entries, lintEntry{section, strings.TrimSpace(k), strings.TrimSpace(v), start})
	}
	return u
}

func (u *lintUnit) values(section, key string) []lintEntry {
	var out []lintEntry
	for _, e := range u.entries {
		if e.Key == key && (section == "" || e.Section == section) {
			out = append(out, e)
		}
	}
	return out
}

func (u *lintUnit) lint(units map[string]*lintUnit) []Finding {
	var out []Finding
	add := func(line int, rule, format string, args ...any) {
		out = append(out, Finding{u.name, line, rule, fmt.Sprintf(format, args...)})
	}
	u.lintDollar(add)
	for _, e := range u.entries {
		switch e.Key {
		case "Label", "Environment":
			if strings.Contains(e.Value, " ") && !(len(e.Value) >= 2 && e.Value[0] == '"' && e.Value[len(e.Value)-1] == '"') {
				add(e.Line, "unquoted-space", "%s=%s holds a space but is not wrapped in \"", e.Key, e.Value)
			}
		case "PublishPort":
			if !strings.HasPrefix(e.Value, "127.0.0.1:") {
				add(e.Line, "loopback-publish", "PublishPort=%s does not bind 127.0.0.1", e.Value)
			}
		case "RemainAfterExit":
			if u.role == RoleDeps {
				add(e.Line, "deps-remain", "a deps unit must not set RemainAfterExit=")
			}
		case "Volume":
			if src, _, _ := strings.Cut(e.Value, ":"); !u.volumeOK(src) {
				add(e.Line, "shared-underscore", "Volume= source %q is not %s", src, u.volumeForms())
			}
		}
	}
	u.lintNetworkDeps(add)
	u.lintFirewall(units, add)
	return out
}

// lintDollar flags every run of $ of odd length outside comments.
func (u *lintUnit) lintDollar(add func(int, string, string, ...any)) {
	for i, l := range u.lines {
		t := strings.TrimSpace(l)
		if t == "" || t[0] == '#' || t[0] == ';' {
			continue
		}
		for j := 0; j < len(l); {
			if l[j] != '$' {
				j++
				continue
			}
			n := 0
			for j < len(l) && l[j] == '$' {
				n++
				j++
			}
			if n%2 == 1 {
				add(i+1, "single-dollar", "a $ not written $$: systemd would expand it")
				break
			}
		}
	}
}

// specifierPath is a path starting with a systemd specifier, e.g. %t/ or %h/.
var specifierPath = regexp.MustCompile(`^%[A-Za-z]/`)

func (u *lintUnit) volumeOK(src string) bool {
	switch {
	case strings.HasPrefix(src, "/"), specifierPath.MatchString(src):
		return true
	case u.machine:
		return strings.HasPrefix(src, "railswitch_")
	case u.prefix == "":
		return false
	case strings.HasPrefix(src, u.prefix+"_"):
		return true
	default:
		return u.slug != "" && strings.HasPrefix(src, u.prefix+"-"+u.slug+"-")
	}
}

func (u *lintUnit) volumeForms() string {
	forms := "an absolute path, a %t/ or %h/ path"
	switch {
	case u.machine:
		return forms + " or railswitch_…"
	case u.slug != "":
		return fmt.Sprintf("%s, %s-%s-… or %s_…", forms, u.prefix, u.slug, u.prefix)
	default:
		return fmt.Sprintf("%s or %s_…", forms, u.prefix)
	}
}

// lintNetworkDeps: a Network= that is not a .network unit gets no automatic
// dependency from Quadlet, so the unit must name its network service itself.
func (u *lintUnit) lintNetworkDeps(add func(int, string, string, ...any)) {
	var requires, after []string
	for _, e := range u.values("Unit", "Requires") {
		requires = append(requires, splitWords(e.Value)...)
	}
	for _, e := range u.values("Unit", "After") {
		after = append(after, splitWords(e.Value)...)
	}
	for _, e := range u.values("Container", "Network") {
		x, _, _ := strings.Cut(e.Value, ":")
		if strings.HasSuffix(x, ".network") {
			continue
		}
		svc := x + "-network.service"
		if !slices.Contains(requires, svc) || !slices.Contains(after, svc) {
			add(e.Line, "network-dependency", "Network=%s is not a .network unit and Requires=/After= do not both name %s", x, svc)
		}
	}
}

// lintFirewall checks the firewall hook's inputs: --hooks-dir and the profile
// annotation come together, and the networks annotation lists exactly the
// unit's network names.
func (u *lintUnit) lintFirewall(units map[string]*lintUnit, add func(int, string, string, ...any)) {
	prefix := cmp.Or(u.prefix, "railswitch")
	hooksLine, profileLine, netsLine := 0, 0, 0
	var annotated []string
	for _, e := range u.values("Container", "GlobalArgs") {
		for _, w := range splitWords(e.Value) {
			if w == "--hooks-dir" || strings.HasPrefix(w, "--hooks-dir=") {
				hooksLine = e.Line
			}
		}
	}
	for _, e := range u.values("Container", "Annotation") {
		for _, w := range splitWords(e.Value) {
			k, v, _ := strings.Cut(w, "=")
			switch k {
			case prefix + ".firewall":
				profileLine = e.Line
			case prefix + ".firewall.networks":
				netsLine = e.Line
				for n := range strings.SplitSeq(v, ",") {
					if n != "" {
						annotated = append(annotated, n)
					}
				}
			}
		}
	}
	if hooksLine == 0 && profileLine == 0 && netsLine == 0 {
		return
	}
	if (hooksLine == 0) != (profileLine == 0) {
		add(max(hooksLine, profileLine), "firewall-networks", "GlobalArgs=--hooks-dir and the %s.firewall annotation must come together", prefix)
	}
	var names []string
	first := 0
	for _, e := range u.values("Container", "Network") {
		if first == 0 {
			first = e.Line
		}
		names = append(names, networkName(units, e.Value))
	}
	slices.Sort(annotated)
	annotated = slices.Compact(annotated)
	slices.Sort(names)
	names = slices.Compact(names)
	if !slices.Equal(annotated, names) {
		add(cmp.Or(netsLine, hooksLine, profileLine, first), "firewall-networks",
			"%s.firewall.networks lists [%s], the unit's networks are [%s]", prefix, strings.Join(annotated, ","), strings.Join(names, ","))
	}
}

// networkName is the podman network a Network= value joins: a .network
// unit's NetworkName= (Quadlet's default systemd-<name> when unset or the
// unit is not among the files), else the value itself.
func networkName(units map[string]*lintUnit, value string) string {
	x, _, _ := strings.Cut(value, ":")
	base, ok := strings.CutSuffix(x, ".network")
	if !ok {
		return x
	}
	if n, ok := units[x]; ok {
		if es := n.values("Network", "NetworkName"); len(es) > 0 {
			return es[len(es)-1].Value
		}
	}
	return "systemd-" + base
}

// splitWords splits a value into words the way systemd does: whitespace
// separates, "…" and '…' quote, \ escapes inside double quotes.
func splitWords(s string) []string {
	var words []string
	var w strings.Builder
	in, quote := false, byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(s) {
				i++
				w.WriteByte(s[i])
			} else {
				w.WriteByte(c)
			}
		case c == '"' || c == '\'':
			quote, in = c, true
		case c == ' ' || c == '\t':
			if in {
				words = append(words, w.String())
				w.Reset()
				in = false
			}
		default:
			w.WriteByte(c)
			in = true
		}
	}
	if in {
		words = append(words, w.String())
	}
	return words
}
