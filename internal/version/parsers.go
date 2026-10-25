package version

import (
	"bufio"
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// parser extracts a value from a source file's contents; key is the optional
// ":<key>" suffix of the source. found is false when the file has no value.
type parser func(data []byte, key string) (value string, found bool)

// parsers is keyed by base filename.
var parsers = map[string]parser{
	".ruby-version":     parseRubyVersion,
	".nvmrc":            parseNvmrc,
	".node-version":     parseNvmrc,
	".tool-versions":    parseToolVersions,
	"Gemfile":           parseGemfile,
	"Gemfile.lock":      parseGemfileLock,
	"package.json":      parsePackageJSON,
	"package-lock.json": parsePackageLock,
	"uv.lock":           parseUvLock,
	"database.yml":      parseDatabaseYML,
}

// firstLine returns the first non-empty trimmed line.
func firstLine(data []byte) string {
	for _, l := range strings.Split(string(data), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

func parseRubyVersion(data []byte, _ string) (string, bool) {
	v := strings.TrimPrefix(firstLine(data), "ruby-")
	return v, v != ""
}

func parseNvmrc(data []byte, _ string) (string, bool) {
	v := strings.TrimPrefix(firstLine(data), "v")
	return v, v != ""
}

// parseToolVersions reads "<tool> <version>" lines; key names the tool.
func parseToolVersions(data []byte, key string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, len(data)+1)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == key {
			return f[1], true
		}
	}
	return "", false
}

var gemfileRuby = regexp.MustCompile(`^\s*ruby\s+["']([^"']*)["']`)

// parseGemfile reads `ruby "x.y.z"`; `ruby file: ...` has no static value.
func parseGemfile(data []byte, _ string) (string, bool) {
	for _, l := range strings.Split(string(data), "\n") {
		if m := gemfileRuby.FindStringSubmatch(l); m != nil {
			return m[1], true
		}
	}
	return "", false
}

var (
	lockRuby = regexp.MustCompile(`^\s+ruby\s+(\S+?)(p\d+)?\s*$`)
	lockSpec = regexp.MustCompile(`^    (\S+) \(([^)]*)\)\s*$`)
)

// parseGemfileLock reads the RUBY VERSION section without a key, or the
// resolved version of gem key from a spec line (four-space indent).
func parseGemfileLock(data []byte, key string) (string, bool) {
	lines := strings.Split(string(data), "\n")
	if key == "" {
		for i, l := range lines {
			if strings.TrimSpace(l) == "RUBY VERSION" && i+1 < len(lines) {
				if m := lockRuby.FindStringSubmatch(lines[i+1]); m != nil {
					return m[1], true
				}
				return "", false
			}
		}
		return "", false
	}
	for _, l := range lines {
		if m := lockSpec.FindStringSubmatch(l); m != nil && m[1] == key {
			return m[2], true
		}
	}
	return "", false
}

// parsePackageJSON reads engines.node (or engines.<key>).
func parsePackageJSON(data []byte, key string) (string, bool) {
	if key == "" {
		key = "node"
	}
	var p struct {
		Engines map[string]string `json:"engines"`
	}
	if json.Unmarshal(data, &p) != nil {
		return "", false
	}
	v, ok := p.Engines[key]
	return strings.TrimPrefix(v, "v"), ok && v != ""
}

// parsePackageLock reads packages["node_modules/<key>"].version.
func parsePackageLock(data []byte, key string) (string, bool) {
	var p struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	if key == "" || json.Unmarshal(data, &p) != nil {
		return "", false
	}
	pkg, ok := p.Packages["node_modules/"+key]
	return pkg.Version, ok && pkg.Version != ""
}

// parseUvLock reads the version of [[package]] name = key.
func parseUvLock(data []byte, key string) (string, bool) {
	var l struct {
		Package []struct {
			Name    string `toml:"name"`
			Version string `toml:"version"`
		} `toml:"package"`
	}
	if key == "" || toml.Unmarshal(data, &l) != nil {
		return "", false
	}
	for _, p := range l.Package {
		if p.Name == key {
			return p.Version, p.Version != ""
		}
	}
	return "", false
}

// parseDatabaseYML ports dev-db-name.sh: it reads `database:` from the
// top-level block named by the key's first segment (default "development"),
// stopping at the next top-level key. Dynamic (ERB/ENV) values are skipped.
func parseDatabaseYML(data []byte, key string) (string, bool) {
	env, _, _ := strings.Cut(key, ".")
	if env == "" {
		env = "development"
	}
	in := false
	for _, l := range strings.Split(string(data), "\n") {
		if !in {
			if strings.TrimSpace(l) == env+":" {
				in = true
			}
			continue
		}
		if l != "" && l[0] != ' ' && l[0] != '\t' {
			break
		}
		_, v, ok := strings.Cut(l, "database:")
		if !ok {
			continue
		}
		v = strings.Map(func(r rune) rune {
			if r == '"' || r == '\'' || r == ' ' || r == '\t' || r == '\r' {
				return -1
			}
			return r
		}, v)
		if v == "" || strings.Contains(v, "<%") || strings.Contains(v, "ENV") {
			return "", false
		}
		return v, true
	}
	return "", false
}
