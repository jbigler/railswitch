package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/jbigler/railswitch/internal/tomlfile"
)

// LockSchemaVersion is the stack.lock schema this railswitch reads and
// writes (D39). It moves independently of project.toml's SchemaVersion.
const LockSchemaVersion = 1

// ErrNoLock is returned by ReadLock when stack.lock does not exist.
var ErrNoLock = errors.New("no stack.lock")

const lockHeader = "# Written by railswitch. Do not edit; review updates in the dashboard.\n"

// Lock is stack.lock: every pack and option by commit and sha256, every image
// by digest (HANDOFF §3.11).
type Lock struct {
	Schema     int                  `toml:"schema"` // 1 (D39)
	Packs      map[string]LockEntry `toml:"pack"`   // key = name
	Options    map[string]LockEntry `toml:"option"`
	Agents     map[string]LockEntry `toml:"agent"`      // key = ref string: "myagent@1", "local:probe@1"
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

// ReadLock reads stack.lock strictly. A newer schema is refused as such, and
// a missing or older one is an error too (D39). A missing file is ErrNoLock.
func ReadLock(path string) (*Lock, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", path, ErrNoLock)
	}
	if err != nil {
		return nil, err
	}
	var l Lock
	if err := tomlfile.Decode(data, LockSchemaVersion, &l); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if l.Schema != LockSchemaVersion {
		return nil, fmt.Errorf("%s: schema: %d is not %d; a railswitch-written stack.lock always has `schema = %d`", path, l.Schema, LockSchemaVersion, LockSchemaVersion)
	}
	// Digest lookups binary-search; a hand-edited file may be out of order.
	byRef := func(a, b LockImage) int { return strings.Compare(a.Ref, b.Ref) }
	slices.SortFunc(l.Images, byRef)
	slices.SortFunc(l.BaseImages, byRef)
	return &l, nil
}

// Write stamps the lock with LockSchemaVersion and replaces path atomically
// with it, under its header. Map keys and image entries are sorted, so the
// same lock always gives the same bytes.
func (l *Lock) Write(path string) error {
	data, err := l.Encode()
	if err != nil {
		return err
	}
	return tomlfile.WriteAtomic(path, data, 0o644)
}

// Encode stamps the lock with LockSchemaVersion and returns the bytes Write
// writes.
func (l *Lock) Encode() ([]byte, error) {
	l.Schema = LockSchemaVersion
	var buf bytes.Buffer
	buf.WriteString(lockHeader)
	if err := toml.NewEncoder(&buf).Encode(l); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
