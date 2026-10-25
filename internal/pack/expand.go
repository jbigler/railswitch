package pack

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jbigler/railswitch/internal/placeholder"
)

// Scope resolves placeholder names to values.
type Scope interface {
	Lookup(name string) (string, bool)
}

// UnsetScope is optionally implemented by a Scope that knows a name is
// deliberately unset (an env.X with no value); Expand then returns ErrUnset.
type UnsetScope interface {
	Unset(name string) bool
}

// MapScope is a Scope backed by a map.
type MapScope map[string]string

// Lookup implements Scope.
func (m MapScope) Lookup(name string) (string, bool) {
	v, ok := m[name]
	return v, ok
}

// ErrUnset is returned by Expand for an env.X placeholder the scope reports
// as unset; callers drop the templated value.
var ErrUnset = errors.New("placeholder value is unset")

// UnknownPlaceholderError names a placeholder the scope does not know.
type UnknownPlaceholderError struct {
	Name string
}

func (e *UnknownPlaceholderError) Error() string {
	return fmt.Sprintf("unknown placeholder {%s}", e.Name)
}

// anchored matches placeholder.Pattern at the start of a string only.
var anchored = regexp.MustCompile(`^(?:` + placeholder.Pattern.String() + `)`)

// Expand replaces each placeholder in s with its value from sc. "{{" is a
// literal "{"; a "{" that does not start a placeholder is kept as is.
func Expand(s string, sc Scope) (string, error) {
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
		name := m[1 : len(m)-1]
		if strings.HasPrefix(name, "env.") {
			if u, ok := sc.(UnsetScope); ok && u.Unset(name) {
				return "", fmt.Errorf("{%s}: %w", name, ErrUnset)
			}
		}
		v, ok := sc.Lookup(name)
		if !ok {
			return "", &UnknownPlaceholderError{Name: name}
		}
		b.WriteString(v)
		i += len(m)
	}
	return b.String(), nil
}

// Major returns the leading part of v before the first "." or "-".
func Major(v string) string {
	if i := strings.IndexAny(v, ".-"); i >= 0 {
		return v[:i]
	}
	return v
}
