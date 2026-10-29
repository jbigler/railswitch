package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/jbigler/railswitch/internal/tomlfile"
)

// GeneratedFile is where a workspace keeps the values railswitch generated
// for its services (D17, D132): passwords and keys, never committed (the
// workspace ignores .railswitch/), 0600.
const GeneratedFile = ".railswitch/generated.toml"

// LegacyGeneratedFile is where they were before D132: a mise config file,
// which a mise-shimmed tool run in the workspace refuses as untrusted.
const LegacyGeneratedFile = "mise.local.toml"

// generatedHeader is line 1 of GeneratedFile (and of the legacy file).
const generatedHeader = "# Local values railswitch generated for the services (D17). Never commit this file."

// LoadGenerated returns workspace root's generated values: GeneratedFile's,
// else (not migrated yet) LegacyGeneratedFile's, else none.
func LoadGenerated(root string) (map[string]string, error) {
	p := filepath.Join(root, GeneratedFile)
	if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
		p = filepath.Join(root, LegacyGeneratedFile)
	}
	return ReadGenerated(p)
}

// GeneratedText is GeneratedFile's content for values: the header, then
// [env] with one line per name, sorted.
func GeneratedText(values map[string]string) []byte {
	var b strings.Builder
	b.WriteString(generatedHeader + "\n[env]\n")
	for _, k := range slices.Sorted(maps.Keys(values)) {
		fmt.Fprintf(&b, "%s = %s\n", k, QuoteString(values[k]))
	}
	return []byte(b.String())
}

// WriteGenerated writes values to root's GeneratedFile (0600, atomically;
// .railswitch/ made 0755 when missing).
func WriteGenerated(root string, values map[string]string) error {
	p := filepath.Join(root, GeneratedFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return tomlfile.WriteAtomic(p, GeneratedText(values), 0o600)
}

// MigrateGenerated moves a workspace's generated values from
// LegacyGeneratedFile to GeneratedFile (D132), unchanged: when there is no
// GeneratedFile yet and the legacy file exists. The legacy file is removed
// only when it is exactly what railswitch wrote (its header and [env]
// lines, the values may have been edited); one you added to is kept, its
// values copied. It reports whether it moved anything.
func MigrateGenerated(root string) (bool, error) {
	if _, err := os.Lstat(filepath.Join(root, GeneratedFile)); !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	legacy := filepath.Join(root, LegacyGeneratedFile)
	data, err := os.ReadFile(legacy)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	values, err := ReadGenerated(legacy)
	if err != nil {
		return false, err
	}
	if err := WriteGenerated(root, values); err != nil {
		return false, err
	}
	if bytes.Equal(data, GeneratedText(values)) {
		if err := os.Remove(legacy); err != nil {
			return true, err
		}
	}
	return true, nil
}

// ReadGenerated returns the string values of [env] in path, a generated
// values file (GeneratedFile, or LegacyGeneratedFile before D132). Other
// tables and non-string values are ignored. A missing file gives an empty
// map: render reports each missing name.
func ReadGenerated(path string) (map[string]string, error) {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Env map[string]any `toml:"env"`
	}
	if _, err := toml.Decode(string(data), &doc); err != nil {
		// The file also holds user secret values: name the line, never echo content.
		var perr toml.ParseError
		if errors.As(err, &perr) {
			return nil, fmt.Errorf("%s: invalid TOML at line %d", path, perr.Position.Line)
		}
		return nil, fmt.Errorf("%s: invalid TOML", path)
	}
	for k, v := range doc.Env {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out, nil
}
