package machine

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"

	"github.com/BurntSushi/toml"
)

// SchemaVersion is the registry.toml schema this railswitch reads (D39).
const SchemaVersion = 1

const maxSubnet = 255 // 10.214.<n>.0/24, n in 0..255

var (
	ErrPrefixTaken = errors.New("project prefix is already registered")
	ErrSlugTaken   = errors.New("worktree slug is already registered for this project")
)

// ProjectEntry is one registered project.
type ProjectEntry struct {
	Prefix    string `toml:"prefix"`
	Workspace string `toml:"workspace"`
	Subnet    int    `toml:"subnet"` // n in 10.214.n.0/24
}

// WorktreeEntry is one registered worktree. ID is unique machine-wide; ports
// are derived from it.
type WorktreeEntry struct {
	Prefix string `toml:"prefix"`
	Slug   string `toml:"slug"`
	ID     int    `toml:"id"`
}

// Registry is registry.toml in the data dir.
type Registry struct {
	Schema    int             `toml:"schema"` // 1 (D39)
	Projects  []ProjectEntry  `toml:"project"`
	Worktrees []WorktreeEntry `toml:"worktree"`
}

// Locked is a Registry read under an exclusive lock on registry.lock, held
// until Close.
type Locked struct {
	*Registry
	path string
	lock *os.File
}

// Open takes an exclusive flock on <dataDir>/registry.lock, blocking until
// it is free, and reads <dataDir>/registry.toml. A missing registry.toml is
// created with schema 1; a higher schema is refused (D39).
func Open(dataDir string) (*Locked, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dataDir, "registry.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("lock registry: %w", err)
	}
	l := &Locked{path: filepath.Join(dataDir, "registry.toml"), lock: f}
	l.Registry, err = readRegistry(l.path)
	if errors.Is(err, fs.ErrNotExist) {
		l.Registry = &Registry{Schema: SchemaVersion}
		err = l.Save()
	}
	if err != nil {
		f.Close() // releases the flock
		return nil, err
	}
	return l, nil
}

// readRegistry checks the schema on its own first, so a newer file is refused
// as such even when it changes a known key's type, then decodes strictly.
func readRegistry(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
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
		return nil, fmt.Errorf("%s: schema: %d was written by a newer railswitch (this one reads schema = %d)", path, head.Schema, SchemaVersion)
	}
	var r Registry
	md, err := toml.Decode(string(data), &r)
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
	return &r, nil
}

// Save writes registry.toml through a temp file and a rename, so a crash
// leaves either the old or the new file.
func (l *Locked) Save() error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(l.Registry); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(l.path), ".registry.toml.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), l.path)
}

// Close releases the lock. Unsaved changes are dropped.
func (l *Locked) Close() error {
	return l.lock.Close()
}

// AddProject registers prefix on the lowest free subnet.
func (r *Registry) AddProject(prefix, workspace string) (ProjectEntry, error) {
	if _, ok := r.Project(prefix); ok {
		return ProjectEntry{}, fmt.Errorf("%s: %w", prefix, ErrPrefixTaken)
	}
	used := make(map[int]bool, len(r.Projects))
	for _, p := range r.Projects {
		used[p.Subnet] = true
	}
	for n := 0; n <= maxSubnet; n++ {
		if !used[n] {
			e := ProjectEntry{Prefix: prefix, Workspace: workspace, Subnet: n}
			r.Projects = append(r.Projects, e)
			return e, nil
		}
	}
	return ProjectEntry{}, fmt.Errorf("%s: no free subnet in 10.214.0.0/16", prefix)
}

// AddWorktree registers slug under a registered project. Its ID is one above
// the highest ID on the machine (1 for the first), so an ID is not reused
// while a higher one exists.
func (r *Registry) AddWorktree(prefix, slug string) (WorktreeEntry, error) {
	if _, ok := r.Project(prefix); !ok {
		return WorktreeEntry{}, fmt.Errorf("project %s is not registered", prefix)
	}
	id := 0
	for _, w := range r.Worktrees {
		if w.Prefix == prefix && w.Slug == slug {
			return WorktreeEntry{}, fmt.Errorf("%s/%s: %w", prefix, slug, ErrSlugTaken)
		}
		id = max(id, w.ID)
	}
	e := WorktreeEntry{Prefix: prefix, Slug: slug, ID: id + 1}
	r.Worktrees = append(r.Worktrees, e)
	return e, nil
}

// RemoveWorktree drops the exact (prefix, slug) entry. An absent entry is an
// error wrapping fs.ErrNotExist.
func (r *Registry) RemoveWorktree(prefix, slug string) error {
	i := slices.IndexFunc(r.Worktrees, func(w WorktreeEntry) bool { return w.Prefix == prefix && w.Slug == slug })
	if i < 0 {
		return fmt.Errorf("worktree %s/%s: %w", prefix, slug, fs.ErrNotExist)
	}
	r.Worktrees = slices.Delete(r.Worktrees, i, i+1)
	return nil
}

// Project returns the entry for prefix.
func (r *Registry) Project(prefix string) (ProjectEntry, bool) {
	i := slices.IndexFunc(r.Projects, func(p ProjectEntry) bool { return p.Prefix == prefix })
	if i < 0 {
		return ProjectEntry{}, false
	}
	return r.Projects[i], true
}

// WorktreesOf returns prefix's worktrees sorted by ID.
func (r *Registry) WorktreesOf(prefix string) []WorktreeEntry {
	var out []WorktreeEntry
	for _, w := range r.Worktrees {
		if w.Prefix == prefix {
			out = append(out, w)
		}
	}
	slices.SortFunc(out, func(a, b WorktreeEntry) int { return a.ID - b.ID })
	return out
}

func addr(n int, host string) string { return "10.214." + strconv.Itoa(n) + host }

// Subnet is project n's network: 10.214.<n>.0/24.
func Subnet(n int) string { return addr(n, ".0/24") }

// Gateway is the network's gateway: 10.214.<n>.1.
func Gateway(n int) string { return addr(n, ".1") }

// TraefikIP is the shared proxy's address on the network: 10.214.<n>.2.
func TraefikIP(n int) string { return addr(n, ".2") }

// IPRange is the network's dynamic range: 10.214.<n>.128/25.
func IPRange(n int) string { return addr(n, ".128/25") }
