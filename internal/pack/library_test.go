package pack

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
)

func TestParseLibraryRef(t *testing.T) {
	r, err := ParseRef("acme:django@1")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Ref{Name: "django", Version: 1, Library: "acme"}); r != want {
		t.Fatalf("ParseRef = %+v, want %+v", r, want)
	}
	if r.String() != "acme:django@1" || r.Bare() != (Ref{Name: "django", Version: 1}) {
		t.Errorf("String = %q, Bare = %+v", r.String(), r.Bare())
	}
	// local: is the local prefix, never an alias.
	if r, err := ParseRef("local:django@1"); err != nil || !r.Local || r.Library != "" {
		t.Errorf("local:django@1 = %+v, %v", r, err)
	}
	for _, bad := range []string{"Acme:django@1", "acme:local:django@1", "acme:Django@1", ":django@1", "acme:", "a-very-long-alias-name-x:d@1", "1acme:d@1"} {
		if r, err := ParseRef(bad); err == nil {
			t.Errorf("ParseRef(%q) = %+v, want an error", bad, r)
		}
	}
	if err := config.ValidAlias("local"); err == nil {
		t.Error(`ValidAlias("local") accepted`)
	}
}

// fakeLibraries serves one checkout per alias.
type fakeLibraries map[string]*LibraryCheckout

func (f fakeLibraries) Checkout(alias string) (*LibraryCheckout, error) {
	if co, ok := f[alias]; ok {
		return co, nil
	}
	return nil, errors.New("library " + alias + " is not fetched")
}

func (f fakeLibraries) Alias(id string) string {
	for a, co := range f {
		if co.ID == id {
			return a
		}
	}
	return ""
}

// libraryCheckout is a checkout dir holding the minimal option as
// options/o/, its index listing it with its hash (as ref, when set).
func libraryCheckout(t *testing.T, ref string) (*LibraryCheckout, string) {
	t.Helper()
	dir := t.TempDir()
	writeLocal(t, dir, "options", "o", minimalFiles("options/o"))
	cat, err := LoadCatalog(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	o, err := cat.Option(Ref{Name: "o", Version: 1, Local: true})
	if err != nil {
		t.Fatal(err)
	}
	sum, err := ContentHash(o.Files)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	co := &LibraryCheckout{Dir: dir, ID: "example.org/lib", Commit: "c0ffee", Signer: "SHA256:k", Packages: []LibraryPackage{{Kind: KindOption, Ref: r, SHA256: sum}}}
	return co, dir
}

func TestCatalogLibrary(t *testing.T) {
	co, _ := libraryCheckout(t, "o@1")
	cat, err := LoadCatalog(t.TempDir(), fakeLibraries{"acme": co})
	if err != nil {
		t.Fatal(err)
	}
	r := Ref{Name: "o", Version: 1, Library: "acme"}
	o, err := cat.Option(r)
	if err != nil || o == nil {
		t.Fatalf("Option(%s) = %v, %v", r, o, err)
	}
	if want := (LibraryOrigin{ID: "example.org/lib", Commit: "c0ffee", Signer: "SHA256:k"}); o.Origin == nil || *o.Origin != want {
		t.Errorf("Origin = %+v, want %+v", o.Origin, want)
	}
	if _, err := cat.Resolve(localProject("rails@1", map[string]string{"cache": "acme:o@1"})); err != nil {
		t.Errorf("a project naming acme:o@1 does not resolve: %v", err)
	}
	// No such alias, and no Libraries at all.
	if _, err := cat.Option(Ref{Name: "o", Version: 1, Library: "other"}); err == nil {
		t.Error("an unknown alias resolved")
	}
	bare, _ := LoadCatalog(t.TempDir(), nil)
	if _, err := bare.Option(r); err == nil {
		t.Error("a library ref resolved with no libraries")
	}
}

// A file under the checkout changed after the fetch, by anything: the ref
// does not resolve.
func TestCatalogLibraryTampered(t *testing.T) {
	co, dir := libraryCheckout(t, "o@1")
	f := filepath.Join(dir, "options", "o", "option.toml")
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, append(data, "\n# changed\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, _ := LoadCatalog(t.TempDir(), fakeLibraries{"acme": co})
	if _, err := cat.Option(Ref{Name: "o", Version: 1, Library: "acme"}); err == nil || !strings.Contains(err.Error(), "no longer matches its signed index") {
		t.Fatalf("tampered option: %v", err)
	}
	// A file added, and a symlink.
	co, dir = libraryCheckout(t, "o@1")
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "options", "o", "e.sh")); err != nil {
		t.Fatal(err)
	}
	cat, _ = LoadCatalog(t.TempDir(), fakeLibraries{"acme": co})
	if _, err := cat.Option(Ref{Name: "o", Version: 1, Library: "acme"}); err == nil {
		t.Fatal("a checkout with a symlink resolved")
	}
}

func TestCatalogLibraryUnlisted(t *testing.T) {
	// The index lists o@2; the directory holds o@1.
	co, _ := libraryCheckout(t, "o@2")
	cat, _ := LoadCatalog(t.TempDir(), fakeLibraries{"acme": co})
	if _, err := cat.Option(Ref{Name: "o", Version: 1, Library: "acme"}); err == nil || !strings.Contains(err.Error(), "publishes no option o@1") {
		t.Fatalf("unlisted o@1: %v", err)
	}
	// Listed as o@2, but the manifest says 1.
	if _, err := cat.Option(Ref{Name: "o", Version: 2, Library: "acme"}); err == nil || !strings.Contains(err.Error(), "does not match the ref") {
		t.Fatalf("o@2 with a version 1 manifest: %v", err)
	}
	// Listed as an option, asked for as a pack.
	if _, err := cat.Pack(Ref{Name: "o", Version: 2, Library: "acme"}); err == nil {
		t.Fatal("an option resolved as a pack")
	}
}
