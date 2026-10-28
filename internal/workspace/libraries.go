package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/libraries"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/signing"
)

// LoadCatalog is pack.LoadCatalog of root with the machine's library
// checkouts: the catalog every command resolves a workspace's stack on.
func LoadCatalog(root string) (*pack.Catalog, error) {
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return nil, err
	}
	return pack.LoadCatalog(root, Libraries(dirs))
}

// Libraries are d's library checkouts, <data>/libraries/<alias>/current/
// with the fetch record beside it.
func Libraries(d machine.Dirs) pack.Libraries { return machineLibraries{d} }

type machineLibraries struct{ d machine.Dirs }

// Checkout reads alias's fetch record and its checkout's index. The index
// is not verified again (no ssh-keygen): the checkout is in the data dir,
// out of every container's reach, and was verified when it was fetched;
// the catalog hashes each package it loads against it.
func (m machineLibraries) Checkout(alias string) (*pack.LibraryCheckout, error) {
	rec, err := libraries.ReadRecord(m.d, alias)
	if errors.Is(err, fs.ErrNotExist) || err == nil && rec.Signer == "" {
		return nil, fmt.Errorf("library %s has no verified checkout on this machine: railswitch library fetch %s", alias, alias)
	}
	if err != nil {
		return nil, err
	}
	dir := libraries.CurrentDir(m.d, alias)
	data, err := safefile.Read(filepath.Join(dir, signing.IndexFile), safefile.Limit)
	if err != nil {
		return nil, fmt.Errorf("library %s: %w", alias, err)
	}
	idx, err := signing.ParseIndex(data)
	if err != nil {
		return nil, fmt.Errorf("library %s: %w", alias, err)
	}
	if idx.Library != rec.ID {
		return nil, fmt.Errorf("library %s: its checkout is of %s, its fetch record of %s", alias, idx.Library, rec.ID)
	}
	co := &pack.LibraryCheckout{Dir: dir, ID: rec.ID, Commit: rec.Commit, Signer: rec.Signer}
	for _, p := range idx.Packages {
		r, err := pack.ParseRef(p.Ref) // checked by ParseIndex
		if err != nil {
			return nil, err
		}
		co.Packages = append(co.Packages, pack.LibraryPackage{Kind: p.Kind, Ref: r, SHA256: p.SHA256})
	}
	return co, nil
}

// SignerWarning is a locked library package whose signer is no longer
// trusted for its library (D102): distrusted or revoked since it was
// accepted. The project keeps running it.
type SignerWarning struct {
	Ref         string `json:"ref"`
	Library     string `json:"library"`
	Fingerprint string `json:"fingerprint"`
}

// SignerWarnings are w's locked library packages whose lock signer t does
// not trust for their library; never nil. A nil t trusts nothing.
func (w *Workspace) SignerWarnings(t *signing.Trust) []SignerWarning {
	out := []SignerWarning{}
	seen := map[pack.Ref]bool{}
	for _, x := range projectPackages(w.Project) {
		if x.ref.Library == "" || seen[x.ref] {
			continue
		}
		seen[x.ref] = true
		e, ok := lockedEntry(w.Lock, x.kind, x.key, x.ref, w.Catalog.Libraries)
		if !ok {
			continue
		}
		id, _ := LibraryID(e)
		if !trusts(t, id, e.Signer) {
			out = append(out, SignerWarning{Ref: x.ref.String(), Library: id, Fingerprint: e.Signer})
		}
	}
	return out
}

// trusts reports whether t trusts the key fp for library id.
func trusts(t *signing.Trust, id, fp string) bool {
	if t == nil || fp == "" {
		return false
	}
	l := t.Library(id)
	if l == nil || slices.ContainsFunc(l.Revoked, func(r signing.Revocation) bool { return r.Fingerprint == fp }) {
		return false
	}
	return slices.ContainsFunc(l.Keys, func(k signing.Key) bool { return k.Fingerprint == fp })
}

// Alias is the alias whose fetch record names library id.
func (m machineLibraries) Alias(id string) string {
	entries, err := os.ReadDir(filepath.Join(m.d.Data, "libraries"))
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if config.ValidAlias(e.Name()) != nil {
			continue
		}
		if rec, err := libraries.ReadRecord(m.d, e.Name()); err == nil && rec.ID == id {
			return e.Name()
		}
	}
	return ""
}
