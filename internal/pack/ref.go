// Package pack reads stack packs, options and tools (D12): their file
// format, the built-in catalog and the content hash that locks them (D14).
package pack

import (
	"fmt"
	"regexp"
	"strconv"
)

// Ref names one version of a pack or option: "<name>@<version>".
type Ref struct {
	Name    string
	Version int
}

// refRe is the ref grammar: a lowercase name, "@", a positive version with no
// leading zero, so every Ref has exactly one spelling. config's ref regex
// (project.toml `from`, `[stack] pack`, `[options]`) must match the same set.
var refRe = regexp.MustCompile(`^([a-z][a-z0-9-]*)@([1-9][0-9]{0,8})$`)

// nameRe is the grammar of a package name on its own (tools, directories).
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ParseRef parses "name@N".
func ParseRef(s string) (Ref, error) {
	m := refRe.FindStringSubmatch(s)
	if m == nil {
		return Ref{}, fmt.Errorf("%q is not a ref name@N, e.g. \"postgres@1\"", s)
	}
	v, _ := strconv.Atoi(m[2]) // at most 9 digits: cannot overflow
	return Ref{Name: m[1], Version: v}, nil
}

// String returns "name@N".
func (r Ref) String() string { return fmt.Sprintf("%s@%d", r.Name, r.Version) }
