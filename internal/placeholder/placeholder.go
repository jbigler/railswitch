// Package placeholder is the placeholder grammar shared by project.toml
// (links) and packs: "{name}" or "{dotted.name}", with "{{" as an escaped
// literal "{". It imports nothing of railswitch.
package placeholder

import (
	"regexp"
	"strings"
)

// Pattern matches one placeholder. "{{" is a literal "{" and never starts a
// placeholder; Replace applies that rule.
var Pattern = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_.]*\}`)

// anchored matches Pattern at the start of a string only.
var anchored = regexp.MustCompile(`^` + Pattern.String())

// LinkNames are the placeholders a [[links]] url may use (HANDOFF §3.10).
var LinkNames = map[string]bool{"worktree.host": true, "worktree.slug": true, "branch": true}

// Replace returns s with each placeholder replaced by f(name). "{{" becomes
// a literal "{"; a "{" that does not start a placeholder (e.g. "{0..3}") is
// kept. The first error from f is returned.
func Replace(s string, f func(name string) (string, error)) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '{' {
			b.WriteByte(s[i])
			i++
			continue
		}
		if i+1 < len(s) && s[i+1] == '{' {
			b.WriteByte('{')
			i += 2
			continue
		}
		m := anchored.FindString(s[i:])
		if m == "" {
			b.WriteByte('{')
			i++
			continue
		}
		v, err := f(m[1 : len(m)-1])
		if err != nil {
			return "", err
		}
		b.WriteString(v)
		i += len(m)
	}
	return b.String(), nil
}

// Names returns the placeholder names in s, in order, without braces.
func Names(s string) []string {
	var names []string
	_, _ = Replace(s, func(name string) (string, error) {
		names = append(names, name)
		return "", nil
	})
	return names
}
