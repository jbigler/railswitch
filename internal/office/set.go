package office

import (
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/jbigler/railswitch/internal/tomlfile"
)

// SetSchema is the set.toml schema this railswitch reads.
const SetSchema = 1

// ReservedNames are the office API's fixed paths under
// /p/{prefix}/virtual-office/, which win over a set's page there.
var ReservedNames = []string{"issues", "prs", "notes", "whiteboard"}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// ValidName refuses a set name that isn't [a-z][a-z0-9-]{0,30} or is one
// of ReservedNames.
func ValidName(s string) error {
	if !nameRe.MatchString(s) {
		return fmt.Errorf("set name %q: want [a-z][a-z0-9-]{0,30}", s)
	}
	if slices.Contains(ReservedNames, s) {
		return fmt.Errorf("set name %q is one of the office's own paths (%v)", s, ReservedNames)
	}
	return nil
}

// Look is a set's atmosphere; a nil field is the cyberpunk value.
type Look struct {
	Background       *string  `toml:"background" json:"background,omitempty"`
	Fog              *string  `toml:"fog" json:"fog,omitempty"`
	FogDensity       *float64 `toml:"fog_density" json:"fog_density,omitempty"`
	Ambient          *string  `toml:"ambient" json:"ambient,omitempty"`
	AmbientIntensity *float64 `toml:"ambient_intensity" json:"ambient_intensity,omitempty"`
	Key              *string  `toml:"key" json:"key,omitempty"`
	KeyIntensity     *float64 `toml:"key_intensity" json:"key_intensity,omitempty"`
	Bloom            *float64 `toml:"bloom" json:"bloom,omitempty"`
	Scanlines        *bool    `toml:"scanlines" json:"scanlines,omitempty"`
}

// Set is set.toml.
type Set struct {
	Schema      int    `toml:"schema" json:"-"`
	Name        string `toml:"name" json:"name"`
	Version     int    `toml:"version" json:"version"`
	Pieces      int    `toml:"pieces" json:"pieces"`
	Description string `toml:"description" json:"description"`
	Look        Look   `toml:"look" json:"look"`
}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ParseSet reads set.toml strictly: unknown keys, a newer schema, a bad
// name, colour or number are refused, every problem in one error.
func ParseSet(data []byte) (*Set, error) {
	var s Set
	if err := tomlfile.Decode(data, SetSchema, &s); err != nil {
		return nil, err
	}
	var errs []error
	if s.Schema != SetSchema {
		errs = append(errs, fmt.Errorf("schema = %d, want %d", s.Schema, SetSchema))
	}
	if err := ValidName(s.Name); err != nil {
		errs = append(errs, err)
	}
	if s.Version < 1 {
		errs = append(errs, errors.New("version: want 1 or more"))
	}
	if s.Pieces < 1 {
		errs = append(errs, errors.New("pieces: the piece list version it was built for, 1 or more"))
	}
	l := s.Look
	for _, c := range []struct {
		key string
		v   *string
	}{{"background", l.Background}, {"fog", l.Fog}, {"ambient", l.Ambient}, {"key", l.Key}} {
		if c.v != nil && !colorRe.MatchString(*c.v) {
			errs = append(errs, fmt.Errorf("look.%s = %q: want #rrggbb", c.key, *c.v))
		}
	}
	for _, c := range []struct {
		key string
		v   *float64
		max float64
	}{{"fog_density", l.FogDensity, 1}, {"ambient_intensity", l.AmbientIntensity, 10}, {"key_intensity", l.KeyIntensity, 10}, {"bloom", l.Bloom, 3}} {
		if c.v != nil && (*c.v < 0 || *c.v > c.max) {
			errs = append(errs, fmt.Errorf("look.%s = %g: want 0..%g", c.key, *c.v, c.max))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &s, nil
}
