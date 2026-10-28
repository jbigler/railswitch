package template

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/safefile"
)

// The import statuses (D113): how a template folder compares with the
// store's template of its name.
const (
	ImportNew    = "new"    // no template of its name on this machine
	ImportUpdate = "update" // a higher version than the store's
	ImportSame   = "same"   // the store's, byte for byte: nothing to do
	ImportOlder  = "older"  // not above the store's version, other content: refused
)

// maxImportFiles bounds a folder's files: a template has stack.toml, a
// README and its Containerfiles.
const maxImportFiles = 64

// Incoming is a template folder someone handed over, judged against this
// machine's store: Stored is the store's template of its name (nil: none).
type Incoming struct {
	T         *Template
	SHA256    string
	Stored    *Template
	StoredSHA string
	Status    string
}

// ReadIncoming reads the template folder dir as hostile input: regular
// files only, each a CheckPath name read through safefile (≤ 1 MiB), at
// most maxImportFiles; a symlink anywhere, dir included, refuses. It is
// decoded strictly and compared with the store's template of its name; one
// there that doesn't read is an error.
func ReadIncoming(dir string, d machine.Dirs) (*Incoming, error) {
	files, err := readFolder(dir)
	if err != nil {
		return nil, err
	}
	t, err := Decode(files)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	sum, err := pack.ContentHash(files)
	if err != nil {
		return nil, err
	}
	in := &Incoming{T: t, SHA256: sum, Status: ImportNew}
	store := LocalDir(d, t.Name)
	switch _, err := os.Lstat(store); {
	case errors.Is(err, fs.ErrNotExist):
		return in, nil
	case err != nil:
		return nil, err
	}
	old, err := ReadDir(store)
	if err == nil {
		in.Stored, err = Decode(old)
	}
	if err == nil {
		in.StoredSHA, err = pack.ContentHash(old)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w (remove it to import again)", store, err)
	}
	switch {
	case in.StoredSHA == sum:
		in.Status = ImportSame
	case t.Version > in.Stored.Version:
		in.Status = ImportUpdate
	default:
		in.Status = ImportOlder
	}
	return in, nil
}

// Install writes in's template into the store as <name>.stack, over the
// older version there. Only a new template or an update installs.
func (in *Incoming) Install(d machine.Dirs) error {
	switch in.Status {
	case ImportNew, ImportUpdate:
		return in.T.Write(LocalDir(d, in.T.Name))
	case ImportSame:
		return nil
	}
	return fmt.Errorf("template %s: this machine has %s; an import must have a higher version", in.T.Ref(), in.Stored.Ref())
}

// readFolder is dir's files in memory, refusing before it reads anything
// a template can't hold.
func readFolder(dir string) (fs.FS, error) {
	fi, err := os.Lstat(dir)
	switch {
	case err != nil:
		return nil, err
	case !fi.IsDir():
		return nil, fmt.Errorf("%s is not a template folder", dir)
	}
	mem := fstest.MapFS{}
	err = filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case e.IsDir():
			if rel != "." && rel != cfDir {
				return fmt.Errorf("%s/ is not a template directory: only %s/", rel, cfDir)
			}
			return nil
		case !e.Type().IsRegular():
			return fmt.Errorf("%s: not a regular file; refused", rel)
		}
		if err := CheckPath(rel); err != nil {
			return err
		}
		if len(mem) == maxImportFiles {
			return fmt.Errorf("more than %d files: not a template", maxImportFiles)
		}
		data, err := safefile.Read(p, safefile.Limit)
		if err != nil {
			return err
		}
		mem[rel] = &fstest.MapFile{Data: data, Mode: 0o644}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.TrimSuffix(dir, "/"), err)
	}
	return mem, nil
}
