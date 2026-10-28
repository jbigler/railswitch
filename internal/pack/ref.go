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

// Ref names one version of a package: "<name>@<version>", for a
// workspace-local package "local:<name>@<version>" (Local, D43), and for a
// library's "<alias>:<name>@<version>" (Library, D105). Local and Library
// are exclusive.
type Ref struct {
	Name    string
	Version int
	Local   bool
	Library string // the library's alias on this machine; "" for none
}

// refRe is the ref grammar: a lowercase name, "@", a positive version with no
// leading zero, so every Ref has exactly one spelling. config's ref regex
// (project.toml `from`, `[stack] pack`, `[options]`) must match the same set.
var refRe = regexp.MustCompile(`^([a-z][a-z0-9-]*)@([1-9][0-9]{0,8})$`)

// nameRe is the grammar of a package name on its own (tools, directories).
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ParseRef parses "name@N", "local:name@N" or "<alias>:name@N".
func ParseRef(s string) (Ref, error) {
	rest, local := strings.CutPrefix(s, config.LocalPrefix)
	alias := ""
	if a, r, ok := config.LibraryRef(s); ok {
		alias, rest = a, r
	}
	m := refRe.FindStringSubmatch(rest)
	if m == nil {
		return Ref{}, fmt.Errorf("%q is not a ref name@N, local:name@N or <library>:name@N, e.g. \"postgres@1\"", s)
	}
	v, _ := strconv.Atoi(m[2]) // at most 9 digits: cannot overflow
	return Ref{Name: m[1], Version: v, Local: local, Library: alias}, nil
}

// String returns "name@N", "local:name@N" or "<alias>:name@N"; "name" for
// an unversioned built-in (a tool, the agent base image).
func (r Ref) String() string {
	s := fmt.Sprintf("%s@%d", r.Name, r.Version)
	if r.Version == 0 {
		s = r.Name
	}
	switch {
	case r.Local:
		return config.LocalPrefix + s
	case r.Library != "":
		return r.Library + ":" + s
	}
	return s
}

// Bare is r without its locality or library: the ref as a library's index
// or a built-in names it.
func (r Ref) Bare() Ref { return Ref{Name: r.Name, Version: r.Version} }
