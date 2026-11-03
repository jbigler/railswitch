package office

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"time"

	"github.com/jbigler/railswitch/internal/safefile"
)

// DefaultSet is the built-in set the office uses when none is chosen.
const DefaultSet = "cyberpunk"

// setsDir is where built-in sets live in the dashboard's static files.
const setsDir = "office/sets"

// Index is an installed set's index.json: every file it holds, the
// pieces it has, and when it was added.
type Index struct {
	Files  map[string]IndexFile `json:"files"`
	Pieces []string             `json:"pieces"`
	Added  time.Time            `json:"added"`
}

// IndexFile is one file of a set as added: its sha256, and the size and
// modification time ls compares (a stat per file, not a hash of the set).
type IndexFile struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	MTime  int64  `json:"mtime"` // Unix nanoseconds
}

// Entry is one set as office set ls lists it.
type Entry struct {
	Name        string   `json:"name"`
	Source      string   `json:"source"` // builtin | installed
	Version     int      `json:"version"`
	Pieces      int      `json:"pieces"`
	Present     int      `json:"present"`
	Missing     int      `json:"missing"`
	Status      string   `json:"status"` // ok | stale | broken
	Reason      string   `json:"reason,omitempty"`
	Description string   `json:"description"`
	Look        Look     `json:"look"`
	Have        []string `json:"have"` // the pieces of the list it has
}

// Store is the built-in sets (in static, the dashboard's static files) and
// the installed ones under <Data>/office/, checked against List.
type Store struct {
	Data   string
	Static fs.FS
	List   *Pieces
}

// Base is the folder of installed sets.
func (s *Store) Base() string { return filepath.Join(s.Data, "office") }

// Builtin reports whether name is a built-in set.
func (s *Store) Builtin(name string) bool {
	fi, err := fs.Stat(s.Static, path.Join(setsDir, name, "set.toml"))
	return ValidName(name) == nil && err == nil && fi.Mode().IsRegular()
}

// Add checks the folder dir and installs it as <Base>/<its name>/,
// replacing an installed set of that name. A refused check, or a built-in
// name, adds nothing; the report says why.
func (s *Store) Add(dir string) (*Report, error) {
	r, err := Check(dir, s.List)
	if err != nil || !r.OK() {
		return r, err
	}
	if s.Builtin(r.Name) {
		return r, fmt.Errorf("%s is a built-in set; give yours another name in set.toml", r.Name)
	}
	if err := os.MkdirAll(s.Base(), 0o700); err != nil {
		return r, err
	}
	tmp, err := os.MkdirTemp(s.Base(), ".add-*")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(tmp) // gone once renamed; a failed add leaves nothing
	src, _ := filepath.Abs(dir)
	idx := Index{Files: map[string]IndexFile{}, Pieces: r.Have(), Added: time.Now().UTC().Truncate(time.Second)}
	for _, rel := range r.Files {
		data, err := safefile.Read(filepath.Join(src, filepath.FromSlash(rel)), MaxFileSize)
		if err != nil {
			return r, err
		}
		dst := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return r, err
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return r, err
		}
		fi, err := os.Lstat(dst)
		if err != nil {
			return r, err
		}
		sum := sha256.Sum256(data)
		idx.Files[rel] = IndexFile{SHA256: hex.EncodeToString(sum[:]), Size: fi.Size(), MTime: fi.ModTime().UnixNano()}
	}
	// The copy is what gets installed: check it again, as the folder may
	// have changed since the first check.
	again, err := Check(tmp, s.List)
	if err != nil {
		return r, err
	}
	if !again.OK() || again.Name != r.Name {
		return again, errors.New("the folder changed while it was copied; nothing added")
	}
	b, err := json.MarshalIndent(idx, "", " ")
	if err != nil {
		return r, err
	}
	if err := os.WriteFile(filepath.Join(tmp, "index.json"), append(b, '\n'), 0o600); err != nil {
		return r, err
	}
	dst := filepath.Join(s.Base(), r.Name)
	if _, err := os.Lstat(dst); err != nil {
		return r, rename(tmp, dst)
	}
	old, err := os.MkdirTemp(s.Base(), ".old-*")
	if err != nil {
		return r, err
	}
	moved := filepath.Join(old, r.Name)
	if err := os.Rename(dst, moved); err != nil {
		os.Remove(old)
		return r, err
	}
	if err := rename(tmp, dst); err != nil {
		// put the old set back rather than leave none
		if back := os.Rename(moved, dst); back != nil {
			return r, fmt.Errorf("%w; the old set is kept in %s (%v)", err, moved, back)
		}
		os.Remove(old)
		return r, err
	}
	return r, os.RemoveAll(old)
}

// rename is os.Rename; a test makes the install's last step fail.
var rename = os.Rename

// Remove removes the installed set name: <Base>/<name>, by exact path.
func (s *Store) Remove(name string) error {
	if err := ValidName(name); err != nil {
		return err
	}
	if s.Builtin(name) {
		return fmt.Errorf("%s is built in; it can't be removed", name)
	}
	dst := filepath.Join(s.Base(), name)
	fi, err := os.Lstat(dst)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no installed set %s", name)
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return os.Remove(dst)
	}
	return os.RemoveAll(dst)
}

// Ls lists the built-in sets, then the installed ones, each by name.
func (s *Store) Ls() ([]Entry, error) {
	var out []Entry
	builtins, err := fs.ReadDir(s.Static, setsDir)
	if err != nil {
		return nil, err
	}
	for _, d := range builtins {
		if s.Builtin(d.Name()) {
			out = append(out, s.builtin(d.Name()))
		}
	}
	installed, err := os.ReadDir(s.Base())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, d := range installed {
		if d.IsDir() && ValidName(d.Name()) == nil && !s.Builtin(d.Name()) {
			out = append(out, s.installed(d.Name()))
		}
	}
	return out, nil
}

// Get is the set name as Ls lists it, with its pieces; false when there
// is none.
func (s *Store) Get(name string) (Entry, bool) {
	if s.Builtin(name) {
		return s.builtin(name), true
	}
	if ValidName(name) != nil {
		return Entry{}, false
	}
	if fi, err := os.Lstat(filepath.Join(s.Base(), name)); err != nil || !fi.IsDir() {
		return Entry{}, false
	}
	return s.installed(name), true
}

func (s *Store) builtin(name string) Entry {
	e := Entry{Name: name, Source: "builtin", Status: "ok", Have: []string{}}
	data, err := fs.ReadFile(s.Static, path.Join(setsDir, name, "set.toml"))
	if err == nil {
		err = e.fill(data)
	}
	if err != nil {
		e.Status, e.Reason = "broken", "set.toml: "+err.Error()
		return e
	}
	for p := range s.List.Pieces {
		if _, err := fs.Stat(s.Static, path.Join(setsDir, name, p+".gltf")); err == nil {
			e.Have = append(e.Have, p)
		}
	}
	s.count(&e)
	return e
}

func (s *Store) installed(name string) Entry {
	e := Entry{Name: name, Source: "installed", Status: "ok", Have: []string{}}
	dir := filepath.Join(s.Base(), name)
	broken := func(why string) Entry {
		e.Status, e.Reason, e.Missing = "broken", why, len(s.List.Pieces)
		return e
	}
	raw, err := safefile.Read(filepath.Join(dir, "index.json"), 1<<20)
	if err != nil {
		return broken("index.json: " + err.Error())
	}
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return broken("index.json: " + err.Error())
	}
	// A stat per file, not a hash of the whole set: the dashboard asks for
	// this on every office page it opens. set.toml alone is read.
	for rel, f := range idx.Files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		fi, err := os.Lstat(path)
		switch {
		case err != nil:
			return broken(rel + ": " + err.Error())
		case !fi.Mode().IsRegular():
			return broken(rel + ": not a regular file")
		case fi.Size() != f.Size || fi.ModTime().UnixNano() != f.MTime:
			return broken(rel + ": changed since it was added")
		}
		if rel == "set.toml" {
			data, err := safefile.Read(path, MaxFileSize)
			if err != nil {
				return broken("set.toml: " + err.Error())
			}
			if got := sha256.Sum256(data); hex.EncodeToString(got[:]) != f.SHA256 {
				return broken("set.toml: changed since it was added")
			}
			if err := e.fill(data); err != nil {
				return broken("set.toml: " + err.Error())
			}
		}
	}
	if e.Pieces == 0 {
		return broken("index.json lists no set.toml")
	}
	for _, p := range idx.Pieces {
		if _, ok := s.List.Pieces[p]; ok && idx.Files[p+".gltf"].SHA256 != "" {
			e.Have = append(e.Have, p)
		}
	}
	s.count(&e)
	return e
}

func (e *Entry) fill(setToml []byte) error {
	set, err := ParseSet(setToml)
	if err != nil {
		return err
	}
	e.Version, e.Pieces, e.Description, e.Look = set.Version, set.Pieces, set.Description, set.Look
	return nil
}

func (s *Store) count(e *Entry) {
	slices.Sort(e.Have)
	e.Present, e.Missing = len(e.Have), len(s.List.Pieces)-len(e.Have)
	if e.Status == "ok" && e.Pieces != s.List.Version {
		e.Status, e.Reason = "stale", fmt.Sprintf("built for piece list %d; this railswitch has %d", e.Pieces, s.List.Version)
	}
}
