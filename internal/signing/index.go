package signing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing/fstest"

	"github.com/BurntSushi/toml"

	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/tomlfile"
)

// Schema is the library.toml and signers.toml schema this railswitch
// reads and writes (D39).
const Schema = 1

// IndexFile and SigFile are the signed index and its signature at a
// library's top.
const (
	IndexFile = "library.toml"
	SigFile   = "library.toml.sig"
)

// Signature namespaces (ssh-keygen -Y -n): the index, and a rotation.
const (
	NamespaceLibrary = "railswitch-library"
	NamespaceRotate  = "railswitch-rotate"
)

const maxSerial = 1 << 53

var libraryRe = regexp.MustCompile(`^[a-z0-9][a-z0-9./_-]{0,199}$`)

// CheckLibrary refuses a library id outside the grammar, or one holding "..".
func CheckLibrary(id string) error {
	if !libraryRe.MatchString(id) || strings.Contains(id, "..") {
		return fmt.Errorf("library %q must match %s with no \"..\"", id, libraryRe)
	}
	return nil
}

// Index is library.toml: the library's id, its serial, and the content hash
// of every package it publishes.
type Index struct {
	Schema   int            `toml:"schema"`
	Library  string         `toml:"library"`
	Serial   int64          `toml:"serial"`
	Packages []IndexPackage `toml:"package"`
	Rotate   []Rotation     `toml:"rotate,omitempty"`
	Revoke   []Revocation   `toml:"revoke,omitempty"`
}

// IndexPackage is one published package: its ref, its kind (pack.KindPack,
// KindOption or KindAgent) and its pack.ContentHash.
type IndexPackage struct {
	Ref    string `toml:"ref"`
	Kind   string `toml:"kind"`
	SHA256 string `toml:"sha256"`
}

// Rotation is an old key vouching for a new one: Signature is the From
// key's signature, namespace NamespaceRotate, over RotationMessage.
type Rotation struct {
	From      string `toml:"from"`
	To        string `toml:"to"`
	Signature string `toml:"signature"`
}

// Revocation names a key never trusted again for the library.
type Revocation struct {
	Fingerprint string `toml:"fingerprint"`
}

// RotationMessage is the message a rotation's signature covers.
func RotationMessage(library, to string) []byte {
	return []byte("railswitch-rotate\n" + library + "\n" + to + "\n")
}

var kindTop = map[string]string{pack.KindPack: "packs", pack.KindOption: "options", pack.KindAgent: "agents"}

// kinds is the order packages are listed in.
var kinds = []string{pack.KindPack, pack.KindOption, pack.KindAgent}

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ParseIndex decodes library.toml strictly and checks every field.
func ParseIndex(data []byte) (*Index, error) {
	var idx Index
	if err := tomlfile.Decode(data, Schema, &idx); err != nil {
		return nil, fmt.Errorf("%s: %w", IndexFile, err)
	}
	if err := idx.check(); err != nil {
		return nil, fmt.Errorf("%s: %w", IndexFile, err)
	}
	return &idx, nil
}

func (idx *Index) check() error {
	var errs []error
	if idx.Schema != Schema {
		errs = append(errs, fmt.Errorf("schema = %d, want %d", idx.Schema, Schema))
	}
	if err := CheckLibrary(idx.Library); err != nil {
		errs = append(errs, err)
	}
	if idx.Serial < 1 || idx.Serial > maxSerial {
		errs = append(errs, fmt.Errorf("serial %d is not in 1 … 2^53", idx.Serial))
	}
	dirs := map[string]bool{}
	for i, p := range idx.Packages {
		r, err := pack.ParseRef(p.Ref)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("package[%d]: %w", i, err))
			continue
		case r.Local:
			errs = append(errs, fmt.Errorf("package[%d]: %s: a library publishes no local: refs", i, p.Ref))
			continue
		}
		top, ok := kindTop[p.Kind]
		if !ok {
			errs = append(errs, fmt.Errorf("package[%d]: kind %q is not pack, option or agent", i, p.Kind))
			continue
		}
		if !sha256Re.MatchString(p.SHA256) {
			errs = append(errs, fmt.Errorf("package[%d]: sha256 %q is not 64 lowercase hex digits", i, p.SHA256))
		}
		// One directory holds one version: a second ref of the same kind
		// and name could never match.
		if dir := top + "/" + r.Name; dirs[dir] {
			errs = append(errs, fmt.Errorf("package[%d]: %s: %s is listed twice", i, p.Ref, dir))
		} else {
			dirs[dir] = true
		}
	}
	for i, r := range idx.Rotate {
		if !fingerprintRe.MatchString(r.From) {
			errs = append(errs, fmt.Errorf("rotate[%d]: from %q is not a SHA256: fingerprint", i, r.From))
		}
		if k, err := ParseKey(r.To); err != nil {
			errs = append(errs, fmt.Errorf("rotate[%d]: to: %w", i, err))
		} else if k != r.To {
			errs = append(errs, fmt.Errorf("rotate[%d]: to must be \"<type> <base64>\" with no comment", i))
		}
		if _, err := SignerOf([]byte(r.Signature)); err != nil {
			errs = append(errs, fmt.Errorf("rotate[%d]: %w", i, err))
		}
	}
	for i, r := range idx.Revoke {
		if !fingerprintRe.MatchString(r.Fingerprint) {
			errs = append(errs, fmt.Errorf("revoke[%d]: %q is not a SHA256: fingerprint", i, r.Fingerprint))
		}
	}
	return errors.Join(errs...)
}

// Encode is the index as library.toml.
func (idx *Index) Encode() []byte {
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(idx); err != nil {
		panic(err) // plain strings and ints always encode
	}
	return b.Bytes()
}

// BuildIndex is an index of lib at serial listing every package under dir,
// keeping keep's rotations and revocations (keep may be nil). A package
// that does not load is an error.
func BuildIndex(dir, lib string, serial int64, keep *Index) (*Index, error) {
	idx := &Index{Schema: Schema, Library: lib, Serial: serial, Packages: []IndexPackage{}}
	if keep != nil {
		idx.Rotate, idx.Revoke = keep.Rotate, keep.Revoke
	}
	found, err := packageDirs(dir)
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, d := range found {
		ref, sum, err := loadPackage(dir, d.kind, d.name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		idx.Packages = append(idx.Packages, IndexPackage{Ref: ref.String(), Kind: d.kind, SHA256: sum})
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if err := idx.check(); err != nil {
		return nil, err
	}
	return idx, nil
}

// pkgDir is one directory under a library's packs/, options/ or agents/.
type pkgDir struct{ kind, name string }

func (d pkgDir) rel() string { return kindTop[d.kind] + "/" + d.name }

// packageDirs lists the entries of dir's packs/, options/ and agents/ in
// kind then name order. A top that is missing is empty; one that is a
// symlink or not a directory is an error.
func packageDirs(dir string) ([]pkgDir, error) {
	var out []pkgDir
	for _, kind := range kinds {
		top := filepath.Join(dir, kindTop[kind])
		fi, err := os.Lstat(top)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("%s: not a directory", top)
		}
		entries, err := os.ReadDir(top)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			out = append(out, pkgDir{kind, e.Name()})
		}
	}
	return out, nil
}

// loadPackage loads dir/<kind>s/<name>/ on its own, every file read through
// safefile, and returns the ref it declares and its pack.ContentHash.
func loadPackage(dir, kind, name string) (pack.Ref, string, error) {
	rel := kindTop[kind] + "/" + name
	root := filepath.Join(dir, kindTop[kind], name)
	fi, err := os.Lstat(root)
	if err != nil {
		return pack.Ref{}, "", fmt.Errorf("%s: %w", rel, err)
	}
	if !fi.IsDir() {
		return pack.Ref{}, "", fmt.Errorf("%s: not a package directory", rel)
	}
	mem := fstest.MapFS{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		sub, _ := filepath.Rel(root, p)
		name := path.Join(rel, filepath.ToSlash(sub))
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
	if err != nil {
		return pack.Ref{}, "", err
	}
	cat, err := pack.Load(mem)
	if err != nil {
		return pack.Ref{}, "", err
	}
	var refs []pack.Ref
	switch kind {
	case pack.KindPack:
		for r := range cat.Packs {
			refs = append(refs, r)
		}
	case pack.KindOption:
		for r := range cat.Options {
			refs = append(refs, r)
		}
	default:
		for r := range cat.Agents {
			refs = append(refs, r)
		}
	}
	if len(refs) != 1 {
		return pack.Ref{}, "", fmt.Errorf("%s: no %s manifest", rel, kind)
	}
	sub, err := fs.Sub(mem, rel)
	if err != nil {
		return pack.Ref{}, "", err
	}
	sum, err := pack.ContentHash(sub)
	if err != nil {
		return pack.Ref{}, "", fmt.Errorf("%s: %w", rel, err)
	}
	return refs[0], sum, nil
}
