// Package placeholder is the placeholder grammar shared by project.toml
// (links) and packs: "{name}" or "{dotted.name}", with "{{" as an escaped
// literal "{". It imports nothing of railswitch.
package placeholder

import "regexp"

// Pattern matches one placeholder. "{{" is a literal "{" and never starts a
// placeholder; Names applies that rule.
var Pattern = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_.]*\}`)

// anchored matches Pattern at the start of a string only.
var anchored = regexp.MustCompile(`^` + Pattern.String())

// LinkNames are the placeholders a [[links]] url may use (HANDOFF §3.10).
var LinkNames = map[string]bool{"worktree.host": true, "worktree.slug": true, "branch": true}

// Names returns the placeholder names in s, in order, without braces. A "{{"
// escape is skipped, as is a "{" that does not start a placeholder
// (e.g. "{0..3}").
func Names(s string) []string {
	var names []string
	for i := 0; i < len(s); {
		if s[i] != '{' {
			i++
			continue
		}
		if i+1 < len(s) && s[i+1] == '{' {
			i += 2
			continue
		}
		if m := anchored.FindString(s[i:]); m != "" {
			names = append(names, m[1:len(m)-1])
			i += len(m)
			continue
		}
		i++
	}
	return names
}
