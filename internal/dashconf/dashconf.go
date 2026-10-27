// Package dashconf holds the dashboard's colours: a preset per light and
// dark mode plus the tokens changed from it, in
// <config>/dashboard.toml (D39).
package dashconf

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/tomlfile"
)

// SchemaVersion is the dashboard.toml schema this railswitch writes and reads.
const SchemaVersion = 1

// Tokens are the editable colour tokens, in editor order.
var Tokens = []string{"bg", "bg2", "text", "muted", "accent", "blue", "green", "yellow"}

// Fixed are the tokens each preset sets and the user can't change.
var Fixed = []string{"sel", "border", "purple", "red"}

// PresetNames are the presets, in editor order.
var PresetNames = []string{"flexoki", "paper", "solar", "contrast"}

const defaultBase = "flexoki"

// Mode is one mode's colours: a preset and the tokens changed from it.
type Mode struct {
	Base      string            `toml:"base" json:"base"`
	Overrides map[string]string `toml:"-" json:"overrides"` // token → #RRGGBB; flattened into the mode table in the file
}

// File is dashboard.toml.
type File struct{ Light, Dark Mode }

// Path is <d.Config>/dashboard.toml.
func Path(d machine.Dirs) string { return filepath.Join(d.Config, "dashboard.toml") }

func defaultMode() Mode { return Mode{Base: defaultBase, Overrides: map[string]string{}} }

// Load reads dashboard.toml; a missing file is flexoki in both modes with no
// overrides.
func Load(d machine.Dirs) (*File, error) {
	f := &File{Light: defaultMode(), Dark: defaultMode()}
	data, err := os.ReadFile(Path(d))
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	var raw struct {
		Schema int `toml:"schema"`
		Colors struct {
			Light map[string]string `toml:"light"`
			Dark  map[string]string `toml:"dark"`
		} `toml:"colors"`
	}
	if err := tomlfile.Decode(data, SchemaVersion, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", Path(d), err)
	}
	if raw.Schema != SchemaVersion {
		return nil, fmt.Errorf("%s: schema = %d, want %d", Path(d), raw.Schema, SchemaVersion)
	}
	for _, m := range []struct {
		name string
		tbl  map[string]string
		dst  *Mode
	}{{"light", raw.Colors.Light, &f.Light}, {"dark", raw.Colors.Dark, &f.Dark}} {
		ov := map[string]string{}
		base := ""
		for k, v := range m.tbl {
			if k == "base" {
				base = v
			} else {
				ov[k] = v
			}
		}
		if err := m.dst.set(m.name == "dark", base, ov); err != nil {
			return nil, fmt.Errorf("%s: colors.%s: %w", Path(d), m.name, err)
		}
	}
	return f, nil
}

// Save writes dashboard.toml atomically with only the changed tokens.
func (f *File) Save(d machine.Dirs) error {
	var b strings.Builder
	fmt.Fprintf(&b, "schema = %d\n", SchemaVersion)
	for i, m := range []struct {
		name string
		mode Mode
	}{{"light", f.Light}, {"dark", f.Dark}} {
		base := m.mode.Base
		if base == "" {
			base = defaultBase
		}
		fmt.Fprintf(&b, "\n[colors.%s]\nbase = %s\n", m.name, strconv.Quote(base))
		for _, tok := range Tokens {
			if v, ok := m.mode.Overrides[tok]; ok && v != presets[base][i][tok] {
				fmt.Fprintf(&b, "%s = %s\n", tok, strconv.Quote(v))
			}
		}
	}
	if err := os.MkdirAll(d.Config, 0o755); err != nil {
		return err
	}
	return tomlfile.WriteAtomic(Path(d), []byte(b.String()), 0o644)
}

func (f *File) mode(dark bool) *Mode {
	if dark {
		return &f.Dark
	}
	return &f.Light
}

// Set changes one mode: base "" keeps the preset; overrides map editable
// tokens to #RRGGBB (stored upper-cased). Nothing changes on an error.
func (f *File) Set(dark bool, base string, overrides map[string]string) error {
	m := f.mode(dark)
	next := Mode{Base: m.Base, Overrides: map[string]string{}}
	for k, v := range m.Overrides {
		next.Overrides[k] = v
	}
	if err := next.set(dark, base, overrides); err != nil {
		return err
	}
	*m = next
	return nil
}

// set applies base and overrides, then drops overrides equal to the base
// preset's value so memory matches what Save writes.
func (m *Mode) set(dark bool, base string, overrides map[string]string) error {
	var errs []error
	if base != "" {
		if _, ok := presets[base]; !ok {
			errs = append(errs, fmt.Errorf("unknown preset %q (presets: %s)", base, strings.Join(PresetNames, ", ")))
		} else {
			m.Base = base
		}
	}
	if m.Base == "" {
		m.Base = defaultBase
	}
	if m.Overrides == nil {
		m.Overrides = map[string]string{}
	}
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, tok := range keys {
		v := overrides[tok]
		switch {
		case slices.Contains(Fixed, tok):
			errs = append(errs, fmt.Errorf("%s is fixed by the preset", tok))
		case !slices.Contains(Tokens, tok):
			errs = append(errs, fmt.Errorf("unknown token %q (tokens: %s)", tok, strings.Join(Tokens, ", ")))
		case !validHex(v):
			errs = append(errs, fmt.Errorf("%s: %q is not #RRGGBB", tok, v))
		default:
			m.Overrides[tok] = strings.ToUpper(v)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	i := 0
	if dark {
		i = 1
	}
	for tok, v := range m.Overrides {
		if presets[m.Base][i][tok] == v {
			delete(m.Overrides, tok)
		}
	}
	return nil
}

func validHex(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	_, err := strconv.ParseUint(s[1:], 16, 32)
	return err == nil
}

// Reset drops one mode's overrides and keeps its preset.
func (f *File) Reset(dark bool) { f.mode(dark).Overrides = map[string]string{} }

// Colors is one mode's preset with its overrides applied: all 12 tokens.
func (f *File) Colors(dark bool) map[string]string {
	m := f.mode(dark)
	base := m.Base
	if _, ok := presets[base]; !ok {
		base = defaultBase
	}
	i := 0
	if dark {
		i = 1
	}
	c := make(map[string]string, len(Tokens)+len(Fixed))
	for k, v := range presets[base][i] {
		c[k] = v
	}
	for k, v := range m.Overrides {
		if slices.Contains(Tokens, k) {
			c[k] = v
		}
	}
	return c
}

func luminance(hex string) float64 {
	n, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil || len(hex) != 7 {
		return 0
	}
	ch := func(v uint64) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(n>>16&0xFF) + 0.7152*ch(n>>8&0xFF) + 0.0722*ch(n&0xFF)
}

// Contrast is the WCAG contrast ratio of two #RRGGBB colours (1 to 21).
func Contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Warnings names each editable token other than bg and bg2 whose contrast
// against bg is below 4.5:1, in editor order.
func Warnings(c map[string]string) []string {
	var w []string
	for _, tok := range Tokens {
		if tok == "bg" || tok == "bg2" {
			continue
		}
		v, ok := c[tok]
		if !ok {
			continue
		}
		if r := Contrast(v, c["bg"]); r < 4.5 {
			w = append(w, fmt.Sprintf("%s: contrast %.1f:1 against bg (below 4.5:1)", tok, r))
		}
	}
	return w
}
