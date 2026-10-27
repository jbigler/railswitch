// Package pack reads stack packs, options, tools and agent packages (D12):
// their file format, the catalog and the content hash that locks them (D14).
package pack

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
)

// Ref names one version of a package: "<name>@<version>", or for a
// workspace-local package "local:<name>@<version>" (Local, D43).
type Ref struct {
	Name    string
	Version int
	Local   bool
}

// refRe is the ref grammar: a lowercase name, "@", a positive version with no
// leading zero, so every Ref has exactly one spelling. config's ref regex
// (project.toml `from`, `[stack] pack`, `[options]`) must match the same set.
var refRe = regexp.MustCompile(`^([a-z][a-z0-9-]*)@([1-9][0-9]{0,8})$`)

// nameRe is the grammar of a package name on its own (tools, directories).
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ParseRef parses "name@N" or "local:name@N".
func ParseRef(s string) (Ref, error) {
	rest, local := strings.CutPrefix(s, config.LocalPrefix)
	m := refRe.FindStringSubmatch(rest)
	if m == nil {
		return Ref{}, fmt.Errorf("%q is not a ref name@N or local:name@N, e.g. \"postgres@1\"", s)
	}
	v, _ := strconv.Atoi(m[2]) // at most 9 digits: cannot overflow
	return Ref{Name: m[1], Version: v, Local: local}, nil
}

// String returns "name@N", or "local:name@N" for a local ref.
func (r Ref) String() string {
	s := fmt.Sprintf("%s@%d", r.Name, r.Version)
	if r.Local {
		return config.LocalPrefix + s
	}
	return s
}
