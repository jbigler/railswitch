package version

import (
	"os"
	"path/filepath"
	"regexp"
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
			for _, v := range []*regexp.Regexp{ValidVersion, ValidDBName} {
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
