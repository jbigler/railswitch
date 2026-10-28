// Package lockstore keeps the package content a stack.lock accepted (D96):
// <data>/locked/<sha256>/, one directory per pack.ContentHash, outside
// every workspace, so no container can reach it. A copy is checked against
// its hash on every read.
package lockstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/safefile"
)

// The limits of one stored package.
const (
	maxFile  = 1 << 20
	maxFiles = 256
)

// ErrMissing is Get's error for a copy that is not stored, can't be read,
// or no longer has the content its name hashes.
var ErrMissing = errors.New("no stored copy of the accepted content")

var shaRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Dir is the machine's store: <data>/locked.
func Dir(d machine.Dirs) string { return filepath.Join(d.Data, "locked") }

// Store is the store at Dir.
type Store struct{ Dir string }

// Put stores files under their content hash and returns it: the hash of
// the bytes it read, once, so a file changing meanwhile can't be stored
// under another content's name. A copy already there that Get accepts is
// left as it is; one it doesn't is replaced. Files are copied to a temp
// dir beside the target, then renamed into place: dirs 0755, files 0644,
// .sh 0755.
func (s Store) Put(files fs.FS) (string, error) {
	mem, err := Read(files)
	if err != nil {
		return "", err
	}
	sha, err := pack.ContentHash(mem)
	if err != nil {
		return "", err
	}
	target := filepath.Join(s.Dir, sha)
	if _, err := os.Lstat(target); err == nil {
		if _, err := s.Get(sha); err == nil {
			return sha, nil
		}
		if err := os.RemoveAll(target); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(s.Dir, "."+sha+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", err
	}
	for p, f := range mem {
		dst := filepath.Join(tmp, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, f.Data, f.Mode); err != nil {
			return "", err
		}
		if err := os.Chmod(dst, f.Mode); err != nil { // past the umask
			return "", err
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		if _, serr := s.Get(sha); serr == nil { // stored meanwhile
			return sha, nil
		}
		return "", err
	}
	return sha, nil
}

// Get is the stored copy named sha, read into memory, or an error wrapping
// ErrMissing when it is not stored, holds anything but directories and
// regular files within the limits, or no longer hashes to sha.
func (s Store) Get(sha string) (fs.FS, error) {
	if !shaRe.MatchString(sha) {
		return nil, fmt.Errorf("%w: %q is not a sha256", ErrMissing, sha)
	}
	dir := filepath.Join(s.Dir, sha)
	mem := fstest.MapFS{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("%s: not a regular file", p)
		case len(mem) == maxFiles:
			return fmt.Errorf("more than %d files", maxFiles)
		}
		data, err := safefile.Read(p, maxFile)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		mem[filepath.ToSlash(rel)] = &fstest.MapFile{Data: data, Mode: mode(rel)}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrMissing, sha, err)
	}
	if got, err := pack.ContentHash(mem); err != nil || got != sha {
		return nil, fmt.Errorf("%w: %s: its content has changed", ErrMissing, sha)
	}
	return mem, nil
}

// Read is files in memory, within the limits, each with its stored mode.
func Read(files fs.FS) (fstest.MapFS, error) {
	mem := fstest.MapFS{}
	err := fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil || d.IsDir():
			return err
		case !d.Type().IsRegular():
			return fmt.Errorf("%s: not a regular file (%s)", p, d.Type())
		}
		if len(mem) == maxFiles {
			return fmt.Errorf("a package of more than %d files can't be stored", maxFiles)
		}
		data, err := fs.ReadFile(files, p)
		if err != nil {
			return err
		}
		if len(data) > maxFile {
			return fmt.Errorf("%s: larger than 1 MiB, can't be stored", p)
		}
		mem[p] = &fstest.MapFile{Data: data, Mode: mode(p)}
		return nil
	})
	return mem, err
}

func mode(p string) os.FileMode {
	if path.Ext(filepath.ToSlash(p)) == ".sh" {
		return 0o755
	}
	return 0o644
}
