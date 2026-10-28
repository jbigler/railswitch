package version

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func fixture(name string) string { return filepath.Join("testdata", name) }

func TestParsers(t *testing.T) {
	cases := []struct {
		dir, src, want string
		valid          bool // use ValidDBName instead of ValidVersion
	}{
		{"ruby-version", ".ruby-version", "3.3.6", false},
		{"nvmrc", ".nvmrc", "22.11.0", false},
		{"tool-versions", ".tool-versions:nodejs", "22.11.0", false},
		{"gemfile", "Gemfile", "3.3.6", false},
		{"gemfile-lock", "Gemfile.lock", "3.3.6", false},
		{"gemfile-lock", "Gemfile.lock:playwright-ruby-client", "1.54.0", false},
		{"package-json", "package.json", "22.11.0", false},
		{"package-lock", "package-lock.json:playwright", "1.54.0", false},
		{"uv-lock", "uv.lock:playwright", "1.54.0", false},
		{"database-yml", "config/database.yml:development.database", "filial_dev", true},
		{"package-json-pm", "package.json:packageManager.bun", "1.2.3", false},
		{"mise-string", "mise.toml:elixir", "1.18.4", false},
		{"mise-table", "mise.toml:elixir", "1.18.4", false},
		{"mise-array", "mise.toml:elixir", "1.18.4", false},
		{"dot-mise", ".mise.toml:erlang", "27.3.4", false},
		{"bun-version", ".bun-version", "1.2.3", false},
		// Java: the distribution prefix and the +build part go (jenv, asdf, mise).
		{"java-version", ".java-version", "21.0.2", false},
		{"java-version-jenv", ".java-version", "17.0.9", false},
		{"tool-versions-java", ".tool-versions:java", "21.0.2.13.1", false},
		{"mise-java", "mise.toml:java", "25.0.3", false},
	}
	for _, c := range cases {
		t.Run(c.dir+"/"+c.src, func(t *testing.T) {
			v := ValidVersion
			if c.valid {
				v = ValidDBName
			}
			r := Resolve(fixture(c.dir), []string{c.src}, "fb", v)
			if r.Value != c.want || r.Source != c.src || len(r.Warnings) != 0 {
				t.Fatalf("got %+v, want %q from %q", r, c.want, c.src)
			}
		})
	}
}

// Review Focus 5: packageManager names another tool, so the bun chain (no
// fallback) is empty, without a warning.
func TestPackageManagerSource(t *testing.T) {
	for _, dir := range []string{"package-json-yarn", "package-json"} {
		r := Resolve(fixture(dir), []string{".bun-version", "package.json:packageManager.bun"}, "", ValidVersion)
		if r.Value != "" || r.Source != "none" || len(r.Warnings) != 0 {
			t.Errorf("%s: got %+v; want empty, source none, no warning", dir, r)
		}
	}
}

func TestKnownSource(t *testing.T) {
	for _, src := range []string{".ruby-version", "Gemfile.lock", "Gemfile.lock:playwright-ruby-client", "config/database.yml:development.database", ".tool-versions:elixir", "package.json", "package.json:packageManager.bun", "mise.toml:elixir", ".mise.toml:erlang", "package-lock.json:playwright", ".bun-version"} {
		if !KnownSource(src) {
			t.Errorf("KnownSource(%q) = false", src)
		}
	}
	for _, src := range []string{"x.lock", "package.json:packageManager", "package.json:packageManager.", ".tool-versions", "mise.toml", "package-lock.json", ".ruby-version:ruby", ""} {
		if KnownSource(src) {
			t.Errorf("KnownSource(%q) = true", src)
		}
	}
}

func TestSkippedFallsThrough(t *testing.T) {
	cases := []struct{ dir, src string }{
		{"gemfile-file", "Gemfile"},
		{"package-json-range", "package.json"},
		{"database-yml-erb", "config/database.yml:development.database"},
		{"database-yml-env", "config/database.yml:development.database"},
		{"ruby-version", ".nvmrc"}, // missing file
	}
	for _, c := range cases {
		t.Run(c.dir, func(t *testing.T) {
			for _, v := range []Validator{ValidVersion, ValidDBName} {
				r := Resolve(fixture(c.dir), []string{c.src}, "fb", v)
				if r.Value != "fb" || r.Source != "fallback" {
					t.Fatalf("got %+v, want fallback", r)
				}
			}
		})
	}
}

func TestChainOrder(t *testing.T) {
	r := Resolve(fixture("gemfile-lock"), []string{".ruby-version", "Gemfile.lock"}, "fb", ValidVersion)
	if r.Value != "3.3.6" || r.Source != "Gemfile.lock" {
		t.Fatalf("got %+v", r)
	}
}

func hasWarning(r Resolved, s string) bool {
	for _, w := range r.Warnings {
		if strings.Contains(w, s) {
			return true
		}
	}
	return false
}

func TestHostileSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("3.3.6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ".ruby-version")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("22.11.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Resolve(dir, []string{".ruby-version", ".nvmrc"}, "fb", ValidVersion)
	if r.Value != "22.11.0" || r.Source != ".nvmrc" || !hasWarning(r, "refused") {
		t.Fatalf("got %+v", r)
	}
}

func TestHostileInjection(t *testing.T) {
	r := Resolve(fixture("ruby-version-hostile"), []string{".ruby-version"}, "3.4.1", ValidVersion)
	if r.Value != "3.4.1" || r.Source != "fallback" || !hasWarning(r, "invalid") {
		t.Fatalf("got %+v", r)
	}
}

func TestHostileHuge(t *testing.T) {
	dir := t.TempDir()
	junk := []byte("22.11.0\n" + strings.Repeat("x", 2<<20))
	if err := os.WriteFile(filepath.Join(dir, ".nvmrc"), junk, 0o644); err != nil {
		t.Fatal(err)
	}
	r := Resolve(dir, []string{".nvmrc"}, "fb", ValidVersion)
	if r.Value != "fb" || r.Source != "fallback" || !hasWarning(r, "refused") {
		t.Fatalf("got %+v", r)
	}
}

func TestHostileFIFO(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, ".ruby-version"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Resolve(dir, []string{".ruby-version"}, "fb", ValidVersion)
	if r.Value != "fb" || r.Source != "fallback" || !hasWarning(r, "refused") {
		t.Fatalf("got %+v", r)
	}
}

func TestAllSkippedFallback(t *testing.T) {
	r := Resolve(fixture("ruby-version-hostile"), []string{".ruby-version", ".nvmrc", "Gemfile", "nosuch.txt"}, "3.4.1", ValidVersion)
	if r.Value != "3.4.1" || r.Source != "fallback" {
		t.Fatalf("got %+v", r)
	}
}

func TestDBNameValidator(t *testing.T) {
	src := []string{"config/database.yml:development.database"}
	if r := Resolve(fixture("database-yml"), src, "fb", ValidDBName); r.Value != "filial_dev" {
		t.Fatalf("ValidDBName: got %+v", r)
	}
	if r := Resolve(fixture("database-yml"), src, "fb", ValidVersion); r.Source != "fallback" || !hasWarning(r, "invalid") {
		t.Fatalf("ValidVersion: got %+v", r)
	}
	if r := Resolve(fixture("database-yml-hostile"), src, "fb", ValidDBName); r.Source != "fallback" || !hasWarning(r, "invalid") {
		t.Fatalf("hostile: got %+v", r)
	}
}

// TestWildcardVersionFallsThrough: a range or wildcard ("20.x", the common
// engines value) is not a version: the chain moves on to the next source.
func TestWildcardVersionFallsThrough(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{"package.json": `{"engines": {"node": "20.x"}}`, ".nvmrc": "22.1.0\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := Resolve(dir, []string{"package.json", ".nvmrc"}, "fb", ValidVersion)
	if r.Value != "22.1.0" || r.Source != ".nvmrc" || !hasWarning(r, "invalid") {
		t.Fatalf("got %+v", r)
	}
	for _, v := range []string{"20.x", "20.X", "20.x.1", "x", "20.*", "^20.1", "~20.1", ">=20", "<21", "20 || 22", "20.1 - 20.4"} {
		if ValidVersion.MatchString(v) {
			t.Errorf("ValidVersion accepts %q", v)
		}
	}
	for _, v := range []string{"20", "3.3.6", "22.11.0", "3.4.0-preview1", "1.2.x86", "16"} {
		if !ValidVersion.MatchString(v) {
			t.Errorf("ValidVersion refuses %q", v)
		}
	}
}
