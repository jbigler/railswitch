package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/safefile"
)

// kindDirs is each package kind's top-level directory in a catalog.
var kindDirs = map[string]string{KindPack: "packs", KindOption: "options", KindAgent: "agents", KindTool: "tools", KindBase: "agentbase"}

// LibraryDirs is each kind's top-level directory in a library, in the
// order its index lists them.
var LibraryDirs = map[string]string{KindPack: "packs", KindOption: "options", KindAgent: "agents", KindTemplate: "templates"}

// Libraries finds the library checkouts of this machine (D103–D105).
type Libraries interface {
	// Checkout is the verified checkout of the library alias names.
	Checkout(alias string) (*LibraryCheckout, error)
	// Alias is the alias of the library with id on this machine; "" when
	// none has it.
	Alias(id string) string
}

// LibraryCheckout is a library's verified checkout: the directory its
// packages are read from, its signed index's packages, and where it came
// from.
type LibraryCheckout struct {
	Dir      string // holds packs/, options/, agents/ and templates/
	ID       string // the library's id, from its index
	Commit   string // the commit it was fetched at
	Signer   string // the fingerprint of the key that signed its index
	Packages []LibraryPackage
}

// LibraryPackage is one package a library's signed index lists.
type LibraryPackage struct {
	Kind   string // KindPack, KindOption, KindAgent or KindTemplate
	Ref    Ref    // as the index names it: no Library, no Local
	SHA256 string // its ContentHash
}

// LibraryOrigin is where a library package was loaded from, as stack.lock
// records it.
type LibraryOrigin struct {
	ID, Commit, Signer string
}

// library is a loader over the library package of kind r names, read into
// memory from its checkout (libraryTree), and its directory there.
func (c *Catalog) library(r Ref, kind string) (*loader, string, *LibraryOrigin, error) {
	if c.Libraries == nil {
		return nil, "", nil, fmt.Errorf("%s: library packages need the machine's libraries", r)
	}
	mem, dir, _, origin, err := libraryTree(c.Libraries, kind, r)
	if err != nil {
		return nil, "", nil, err
	}
	l := &loader{fsys: mem, bases: map[int]string{}, names: map[string]string{}}
	return l, dir, origin, nil
}

// LibraryTree is the package of kind (a template too) at the library ref
// r, read into memory from its library's checkout, with its content hash
// and where it came from.
func LibraryTree(libs Libraries, kind string, r Ref) (fs.FS, string, *LibraryOrigin, error) {
	mem, dir, sum, origin, err := libraryTree(libs, kind, r)
	if err != nil {
		return nil, "", nil, err
	}
	sub, err := fs.Sub(mem, dir)
	return sub, sum, origin, err
}

// libraryTree reads r's directory of a checkout into memory under its
// library path, only when the checkout's index lists r with the content
// hash the directory has now: a file changed since the fetch, by anything,
// refuses.
func libraryTree(libs Libraries, kind string, r Ref) (fstest.MapFS, string, string, *LibraryOrigin, error) {
	co, err := libs.Checkout(r.Library)
	if err != nil {
		return nil, "", "", nil, fmt.Errorf("%s: %w", r, err)
	}
	i := slices.IndexFunc(co.Packages, func(p LibraryPackage) bool { return p.Kind == kind && p.Ref == r.Bare() })
	if i < 0 {
		return nil, "", "", nil, fmt.Errorf("%s: library %s publishes no %s %s", r, r.Library, kind, r.Bare())
	}
	dir := LibraryDirs[kind] + "/" + r.Name
	mem, err := ReadTree(filepath.Join(co.Dir, filepath.FromSlash(dir)), dir)
	if err != nil {
		return nil, "", "", nil, fmt.Errorf("%s: %w", r, err)
	}
	sub, err := fs.Sub(mem, dir)
	if err != nil {
		return nil, "", "", nil, err
	}
	sum, err := ContentHash(sub)
	if err != nil {
		return nil, "", "", nil, fmt.Errorf("%s: %w", r, err)
	}
	if sum != co.Packages[i].SHA256 {
		return nil, "", "", nil, fmt.Errorf("%s: %s of library %s no longer matches its signed index: fetch it again (railswitch library fetch %s)", r, dir, r.Library, r.Library)
	}
	return mem, dir, sum, &LibraryOrigin{ID: co.ID, Commit: co.Commit, Signer: co.Signer}, nil
}

// ReadTree reads the directory root into memory under prefix: regular
// files only, each through safefile; a symlink anywhere, root included,
// refuses.
func ReadTree(root, prefix string) (fstest.MapFS, error) {
	fi, err := os.Lstat(root)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("%s is missing from the checkout", prefix)
	case err != nil:
		return nil, err
	case !fi.IsDir():
		return nil, fmt.Errorf("%s is not a directory", prefix)
	}
	mem := fstest.MapFS{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		name := path.Join(prefix, filepath.ToSlash(rel))
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: not a regular file", name)
		}
		data, err := safefile.Read(p, safefile.Limit)
		if err != nil {
			return err
		}
		mem[name] = &fstest.MapFile{Data: data, Mode: 0o644}
		return nil
	})
	return mem, err
}
