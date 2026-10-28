// Package review compares two versions of a package file by file and flags
// the changes that reach past a firewalled container (D98): hosts the
// firewall lets through, commands a build runs, images, host mounts, ports
// and capabilities. Everything else is shown as a diff, not flagged.
package review

import (
	"bytes"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
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
	Diff   string `json:"diff"`   // unified, 3 lines of context; a NUL or invalid UTF-8 byte as \xNN
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
// ("pack", "option", "tool", "agent", "base", the agent base image, or "template"), sorted by path, each with its
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
	if kind != "template" {
		return compare(kind, "", a, b), nil
	}
	// A template's bundled packages (packs/<name>/, options/<name>/: D116)
	// are each checked as the package they are.
	groups := map[string]string{} // prefix → kind
	for _, m := range []map[string][]byte{a, b} {
		for p := range m {
			if prefix, k := bundled(p); prefix != "" {
				groups[prefix] = k
			}
		}
	}
	out := compare(kind, "", without(a, groups), without(b, groups))
	for prefix, k := range groups {
		out = append(out, compare(k, prefix, within(a, prefix), within(b, prefix))...)
	}
	slices.SortFunc(out, func(x, y File) int { return strings.Compare(x.Path, y.Path) })
	return out, nil
}

// bundled is the package directory p is in within a template, with its
// kind: "packs/<name>/" (pack) or "options/<name>/" (option); "" for
// none.
func bundled(p string) (prefix, kind string) {
	top, rest, ok := strings.Cut(p, "/")
	name, _, nested := strings.Cut(rest, "/")
	if !ok || !nested {
		return "", ""
	}
	switch top {
	case "packs":
		return top + "/" + name + "/", "pack"
	case "options":
		return top + "/" + name + "/", "option"
	}
	return "", ""
}

func without(m map[string][]byte, groups map[string]string) map[string][]byte {
	out := map[string][]byte{}
	for p, d := range m {
		if prefix, _ := bundled(p); groups[prefix] == "" {
			out[p] = d
		}
	}
	return out
}

func within(m map[string][]byte, prefix string) map[string][]byte {
	out := map[string][]byte{}
	for p, d := range m {
		if rel, ok := strings.CutPrefix(p, prefix); ok {
			out[rel] = d
		}
	}
	return out
}

// compare is Compare of a package of kind, by file maps; its files' paths
// are shown under prefix.
func compare(kind, prefix string, a, b map[string][]byte) []File {
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
		f := File{Path: prefix + p, Status: Changed, Flags: []Flag{}}
		aName, bName := "a/"+prefix+p, "b/"+prefix+p
		switch {
		case inOld && inNew && bytes.Equal(x, y):
			continue
		case !inOld:
			f.Status, aName = Added, "/dev/null"
		case !inNew:
			f.Status, bName = Removed, "/dev/null"
		}
		// Every file is diffed and checked as text: a byte that isn't
		// text hides nothing, and is itself flagged.
		xs, ys := textOf(x), textOf(y)
		f.Diff = textdiff.Unified(aName, bName, xs, ys)
		if inNew {
			if !isText(x) || !isText(y) {
				f.Flags = append(f.Flags, Flag{Check: CheckRunsHere, Text: p + `: not text (a NUL or invalid UTF-8 byte, shown as \xNN)`})
			}
			switch {
			case kind == "template" && p == templateManifest:
				f.Flags = append(f.Flags, templateFlags(x, y)...)
			case p == manifest:
				f.Flags = append(f.Flags, manifestFlags(before, after)...)
			default:
				f.Flags = append(f.Flags, lineFlags(p, xs, ys)...)
			}
			f.Flags = append(f.Flags, scriptFlags(p, caps)...)
		}
		out = append(out, f)
	}
	return out
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

func isText(b []byte) bool {
	return bytes.IndexByte(b, 0) < 0 && utf8.Valid(b)
}

// textOf is b as text, each NUL or invalid UTF-8 byte written \xNN.
func textOf(b []byte) string {
	if isText(b) {
		return string(b)
	}
	var out strings.Builder
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if r == 0 || r == utf8.RuneError && n == 1 {
			fmt.Fprintf(&out, `\x%02x`, b[0])
		} else {
			out.Write(b[:n])
		}
		b = b[n:]
	}
	return out.String()
}
