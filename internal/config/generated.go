package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
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

// generatedName is a name GeneratedFile holds: an env var name, a bare
// TOML key.
var generatedName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// GeneratedText is GeneratedFile's content for values: the header, then
// [env] with one line per name, sorted. A name that is no env var name
// is left out.
func GeneratedText(values map[string]string) []byte {
	var b strings.Builder
	b.WriteString(generatedHeader + "\n[env]\n")
	for _, k := range slices.Sorted(maps.Keys(values)) {
		if generatedName.MatchString(k) {
			fmt.Fprintf(&b, "%s = %s\n", k, QuoteString(values[k]))
		}
	}
	return []byte(b.String())
}

// CreateGenerated writes values to root's new GeneratedFile (0600;
// .railswitch/ made 0755 when missing): refused when it exists, as a
// new or joined project's values never replace ones already there.
func CreateGenerated(root string, values map[string]string) error {
	p := filepath.Join(root, GeneratedFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(GeneratedText(values))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// MigrateGenerated moves a workspace's generated values from
// LegacyGeneratedFile to GeneratedFile (D132), unchanged, when there is no
// GeneratedFile yet and the legacy file exists. The legacy file is removed
// only when it is exactly what railswitch wrote (its header and [env]
// lines; the values may have been edited); one you added to, or with a
// name GeneratedFile can't hold, is kept, its values copied. When both
// exist (a move interrupted before the removal) a legacy file that is
// exactly railswitch's text for the moved values is removed. It reports
// whether it moved anything; kept says the legacy file stays.
func MigrateGenerated(root string) (moved, kept bool, err error) {
	legacy := filepath.Join(root, LegacyGeneratedFile)
	data, err := os.ReadFile(legacy)
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	values, err := ReadGenerated(legacy)
	if err != nil {
		return false, true, err
	}
	exact := bytes.Equal(data, GeneratedText(values))
	newPath := filepath.Join(root, GeneratedFile)
	switch _, err := os.Lstat(newPath); {
	case err == nil:
		have, err := ReadGenerated(newPath)
		if err != nil || !exact || !maps.Equal(have, values) {
			return false, false, err // not ours to remove: left as it is
		}
		return false, false, removeLegacy(legacy)
	case !errors.Is(err, fs.ErrNotExist):
		return false, false, err
	}
	text := GeneratedText(values)
	// What is written must read back as exactly the values: a name the
	// file can't hold keeps the legacy file.
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return false, true, err
	}
	if err := tomlfile.WriteAtomic(newPath, text, 0o600); err != nil {
		return false, true, err
	}
	back, err := ReadGenerated(newPath)
	if err != nil {
		return true, true, err
	}
	if !exact || !maps.Equal(back, values) {
		return true, true, nil
	}
	return true, false, removeLegacy(legacy)
}

// removeLegacy removes path; another render that removed it first is fine.
func removeLegacy(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
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
