// Package version resolves per-worktree versions (runtimes, gem/package pins,
// database names) from a chain of sources, falling back to a default.
//
// Every source file lives inside a worktree and is hostile input: it is read
// only through safefile.Read, and every value must match a validator before
// it can reach a tag, build arg or unit.
package version

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jbigler/railswitch/internal/safefile"
)

// Validator accepts a value read from a version source; String describes
// what it accepts, for error messages.
type Validator interface {
	MatchString(string) bool
	String() string
}

// ValidVersion matches runtime versions and pins: no range or wildcard
// (`20.x`, `^20`, `>=20`, `20 || 22`).
var ValidVersion Validator = versionValidator{}

var (
	versionRe  = regexp.MustCompile(`^[0-9][0-9A-Za-z._-]{0,39}$`)
	wildcardRe = regexp.MustCompile(`(^|\.)[xX](\.|$)`)
)

type versionValidator struct{}

func (versionValidator) MatchString(s string) bool {
	return versionRe.MatchString(s) && !wildcardRe.MatchString(s)
}

func (versionValidator) String() string { return versionRe.String() + " with no x wildcard part" }

// ValidDBName matches a [db_name] value (D16).
var ValidDBName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

// Resolved is the outcome of a version chain.
type Resolved struct {
	Value string
	// Source is "<file>", "<file>:<key>" or "fallback".
	Source   string
	Warnings []string
}

// Resolve walks the sources in from (each "<file>" or "<file>:<key>",
// relative to dir) and returns the first value that a parser finds and valid
// accepts. A missing file or a file without a value is skipped silently; a
// refused or unreadable file, an unknown source or an invalid value is
// skipped with a warning. When every source is skipped the result is
// fallback with Source "fallback".
func Resolve(dir string, from []string, fallback string, valid Validator) Resolved {
	var r Resolved
	for _, src := range from {
		file, key, _ := strings.Cut(src, ":")
		parse, ok := parsers[filepath.Base(file)]
		if !ok {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s: unknown version source", src))
			continue
		}
		data, err := safefile.Read(filepath.Join(dir, file), safefile.Limit)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s: skipped: %v", src, err))
			continue
		}
		v, found := parse(data, key)
		if !found {
			continue
		}
		if !valid.MatchString(v) {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s: skipped: invalid value %q", src, truncate(v)))
			continue
		}
		r.Value, r.Source = v, src
		return r
	}
	r.Value, r.Source = fallback, "fallback"
	return r
}

// truncate keeps hostile values short in warnings.
func truncate(s string) string {
	const max = 64
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
