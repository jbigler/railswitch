// Package review compares two versions of a package file by file and flags
// the changes that reach past a firewalled container (D98): hosts the
// firewall lets through, commands a build runs, images, host mounts, ports
// and capabilities. Everything else is shown as a diff, not flagged.
package review

import (
	"bytes"
	"io/fs"
	"maps"
	"slices"
	"unicode/utf8"

	"github.com/jbigler/railswitch/internal/textdiff"
)

// File statuses.
const (
	Added   = "added"
	Removed = "removed"
	Changed = "changed"
)

// File is one file that differs between the two versions.
type File struct {
	Path   string `json:"path"`
	Status string `json:"status"` // Added, Removed or Changed
	Diff   string `json:"diff"`   // unified, 3 lines of context; "" for a binary file
	Flags  []Flag `json:"flags"`  // never nil
}

// Flag is one flagged change: Line is its line in the new file, 0 for a
// manifest field (listed above the diff).
type Flag struct {
	Check string `json:"check"`
	Line  int    `json:"line"`
	Text  string `json:"text"`
}

// The checks.
const (
	CheckFirewall  = "firewall"
	CheckRunsHere  = "runs-here"
	CheckImage     = "image"
	CheckHostMount = "host-mount"
	CheckPort      = "port"
	CheckPrivilege = "privilege"
)

// Compare is every file that differs from old to new, a package of kind
// ("pack", "option", "tool" or "agent"), sorted by path, each with its
// flags. A nil old is an empty package: every file is added; a nil new
// removes every file.
func Compare(kind string, old, new fs.FS) ([]File, error) {
	a, err := readAll(old)
	if err != nil {
		return nil, err
	}
	b, err := readAll(new)
	if err != nil {
		return nil, err
	}
	manifest := kind + ".toml"
	before, after := decode(kind, a[manifest]), decode(kind, b[manifest])
	caps := after.caps
	if after.err != nil {
		caps = before.caps
	}
	paths := slices.Sorted(maps.Keys(a))
	for p := range b {
		if _, ok := a[p]; !ok {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	out := []File{}
	for _, p := range paths {
		x, inOld := a[p]
		y, inNew := b[p]
		f := File{Path: p, Status: Changed, Flags: []Flag{}}
		aName, bName := "a/"+p, "b/"+p
		switch {
		case inOld && inNew && bytes.Equal(x, y):
			continue
		case !inOld:
			f.Status, aName = Added, "/dev/null"
		case !inNew:
			f.Status, bName = Removed, "/dev/null"
		}
		binary := isBinary(x) || isBinary(y)
		if !binary {
			f.Diff = textdiff.Unified(aName, bName, string(x), string(y))
		}
		if inNew {
			switch {
			case p == manifest:
				f.Flags = append(f.Flags, manifestFlags(before, after)...)
			case !binary:
				f.Flags = append(f.Flags, lineFlags(p, string(x), string(y))...)
			}
			f.Flags = append(f.Flags, scriptFlags(p, caps)...)
		}
		out = append(out, f)
	}
	return out, nil
}

// readAll is every regular file of fsys by slash path; nil for a nil fsys.
func readAll(fsys fs.FS) (map[string][]byte, error) {
	out := map[string][]byte{}
	if fsys == nil {
		return out, nil
	}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		out[p] = data
		return err
	})
	return out, err
}

func isBinary(b []byte) bool {
	return bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b)
}
