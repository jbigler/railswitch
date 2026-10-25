package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// ErrNoLock is returned by ReadLock when stack.lock does not exist.
var ErrNoLock = errors.New("no stack.lock")

const lockHeader = "# Written by railswitch. Do not edit; review updates in the dashboard.\n"

// Lock is stack.lock: every pack and option by commit and sha256, every image
// by digest (HANDOFF §3.11).
type Lock struct {
	Schema     int                  `toml:"schema"` // 1 (D39)
	Packs      map[string]LockEntry `toml:"pack"`   // key = name
	Options    map[string]LockEntry `toml:"option"`
	Images     []LockImage          `toml:"image"`      // pulled service images (D15)
	BaseImages []LockImage          `toml:"base_image"` // FROM images of built steps, by first pull (D36)
}

// LockEntry pins one pack or option.
type LockEntry struct {
	Version string `toml:"version"`
	Source  string `toml:"source"`
	Commit  string `toml:"commit"`
	SHA256  string `toml:"sha256"`
}

// LockImage pins one image ref to a digest.
type LockImage struct {
	Ref    string `toml:"ref"`
	Digest string `toml:"digest"`
}

// ReadLock reads stack.lock. The schema is checked on its own first, so a file
// from a newer railswitch is refused as such (D39); then the file is decoded
// strictly. A missing file is ErrNoLock.
func ReadLock(path string) (*Lock, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", path, ErrNoLock)
	}
	if err != nil {
		return nil, err
	}
	var head struct {
		Schema int `toml:"schema"`
	}
	if _, err := toml.Decode(string(data), &head); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if head.Schema > SchemaVersion {
		return nil, fmt.Errorf("%s: %w", path, newerSchema(head.Schema))
	}
	var l Lock
	md, err := toml.Decode(string(data), &l)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		errs := make([]error, len(und))
		for i, k := range und {
			errs[i] = fmt.Errorf("%s: unknown key %s", path, k)
		}
		return nil, errors.Join(errs...)
	}
	// Digest lookups binary-search; a hand-edited file may be out of order.
	byRef := func(a, b LockImage) int { return strings.Compare(a.Ref, b.Ref) }
	slices.SortFunc(l.Images, byRef)
	slices.SortFunc(l.BaseImages, byRef)
	return &l, nil
}

// Write writes the lock with its header through a temp file and a rename.
// Map keys and image entries are sorted, so the same lock always gives the
// same bytes.
func (l *Lock) Write(path string) error {
	var buf bytes.Buffer
	buf.WriteString(lockHeader)
	if err := toml.NewEncoder(&buf).Encode(l); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Digest returns the recorded digest of a pulled service image, or "".
func (l *Lock) Digest(ref string) string { return digest(l.Images, ref) }

// SetDigest records ref's digest, replacing an existing entry.
func (l *Lock) SetDigest(ref, digest string) { l.Images = setDigest(l.Images, ref, digest) }

// BaseDigest returns the recorded digest of a FROM image, or "".
func (l *Lock) BaseDigest(ref string) string { return digest(l.BaseImages, ref) }

// SetBaseDigest records a FROM image's digest, replacing an existing entry.
func (l *Lock) SetBaseDigest(ref, digest string) {
	l.BaseImages = setDigest(l.BaseImages, ref, digest)
}

func cmpRef(img LockImage, ref string) int { return strings.Compare(img.Ref, ref) }

func digest(imgs []LockImage, ref string) string {
	if i, ok := slices.BinarySearchFunc(imgs, ref, cmpRef); ok {
		return imgs[i].Digest
	}
	return ""
}

// setDigest keeps imgs sorted by ref, one entry per ref.
func setDigest(imgs []LockImage, ref, digest string) []LockImage {
	i, ok := slices.BinarySearchFunc(imgs, ref, cmpRef)
	if ok {
		imgs[i].Digest = digest
		return imgs
	}
	return slices.Insert(imgs, i, LockImage{Ref: ref, Digest: digest})
}
