package pack

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jbigler/railswitch/internal/placeholder"
)

// Scope resolves placeholder names to values.
type Scope interface {
	Lookup(name string) (string, bool)
}

// MapScope is a Scope backed by a map.
type MapScope map[string]string

// Lookup implements Scope.
func (m MapScope) Lookup(name string) (string, bool) {
	v, ok := m[name]
	return v, ok
}

// ErrUnset is returned (wrapped) by Expand for an env.X placeholder the
// scope has no value for; callers drop the templated value.
var ErrUnset = errors.New("placeholder value is unset")

// UnknownPlaceholderError names a placeholder the scope does not know.
type UnknownPlaceholderError struct {
	Name string
}

func (e *UnknownPlaceholderError) Error() string {
	return fmt.Sprintf("unknown placeholder {%s}", e.Name)
}

// Expand replaces each placeholder in s with its value from sc, using the
// grammar of placeholder.Replace. An optional "{name?}" is "" when sc does
// not know name (an env.X? still needs a value: ErrUnset).
func Expand(s string, sc Scope) (string, error) {
	return placeholder.Replace(s, func(name string) (string, error) {
		name, optional := strings.CutSuffix(name, "?")
		if v, ok := sc.Lookup(name); ok {
			return v, nil
		}
		if strings.HasPrefix(name, "env.") {
			return "", fmt.Errorf("{%s}: %w", name, ErrUnset)
		}
		if optional {
			return "", nil
		}
		return "", &UnknownPlaceholderError{Name: name}
	})
}

// Major returns the leading part of v before the first "." or "-".
func Major(v string) string {
	if i := strings.IndexAny(v, ".-"); i >= 0 {
		return v[:i]
	}
	return v
}
