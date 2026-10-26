package machine

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"

	"github.com/jbigler/railswitch/internal/tomlfile"
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

// readRegistry decodes registry.toml strictly. A newer schema is refused as
// such; a missing or older one is an error too, since an empty registry
// would hand out subnets and IDs that live networks still use (D39).
func readRegistry(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Registry
	if err := tomlfile.Decode(data, SchemaVersion, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if r.Schema != SchemaVersion {
		return nil, fmt.Errorf("%s: schema: %d is not %d; a railswitch-written registry.toml always has `schema = %d`", path, r.Schema, SchemaVersion, SchemaVersion)
	}
	return &r, nil
}

// Save stamps the registry with SchemaVersion and replaces registry.toml
// atomically.
func (l *Locked) Save() error {
	l.Schema = SchemaVersion
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(l.Registry); err != nil {
		return err
	}
	return tomlfile.WriteAtomic(l.path, buf.Bytes(), 0o600)
}

// Close releases the lock. Unsaved changes are dropped.
func (l *Locked) Close() error {
	return l.lock.Close()
}

// AddProject registers prefix on the lowest free subnet: one no other
// project uses and that overlaps none of taken (the subnets of existing
// podman networks).
func (r *Registry) AddProject(prefix, workspace string, taken []netip.Prefix) (ProjectEntry, error) {
	if _, ok := r.Project(prefix); ok {
		return ProjectEntry{}, fmt.Errorf("%s: %w", prefix, ErrPrefixTaken)
	}
	n, err := r.freeSubnet(prefix, taken)
	if err != nil {
		return ProjectEntry{}, err
	}
	e := ProjectEntry{Prefix: prefix, Workspace: workspace, Subnet: n}
	r.Projects = append(r.Projects, e)
	return e, nil
}

// MoveSubnet moves prefix to the lowest subnet no other project uses that
// overlaps none of taken, returning the old and new subnet indices (equal
// when the current subnet is that one). An absent project is an error
// wrapping fs.ErrNotExist.
func (r *Registry) MoveSubnet(prefix string, taken []netip.Prefix) (old, moved int, err error) {
	i := slices.IndexFunc(r.Projects, func(p ProjectEntry) bool { return p.Prefix == prefix })
	if i < 0 {
		return 0, 0, fmt.Errorf("project %s: %w", prefix, fs.ErrNotExist)
	}
	n, err := r.freeSubnet(prefix, taken)
	if err != nil {
		return 0, 0, err
	}
	old = r.Projects[i].Subnet
	r.Projects[i].Subnet = n
	return old, n, nil
}

// freeSubnet returns the lowest n whose 10.214.n.0/24 no project but prefix
// uses and that overlaps none of taken.
func (r *Registry) freeSubnet(prefix string, taken []netip.Prefix) (int, error) {
	used := make(map[int]bool, len(r.Projects))
	for _, p := range r.Projects {
		if p.Prefix != prefix {
			used[p.Subnet] = true
		}
	}
	for n := 0; n <= maxSubnet; n++ {
		if !used[n] && !Overlaps(SubnetPrefix(n), taken) {
			return n, nil
		}
	}
	var clash []string
	pool := netip.MustParsePrefix("10.214.0.0/16")
	for _, t := range taken {
		if t.Overlaps(pool) {
			clash = append(clash, t.String())
		}
	}
	if len(clash) > 0 {
		return 0, fmt.Errorf("%s: no free subnet in 10.214.0.0/16: %d used by other railswitch projects, the rest overlaps existing podman network subnets %s", prefix, len(used), strings.Join(clash, ", "))
	}
	return 0, fmt.Errorf("%s: no free subnet in 10.214.0.0/16", prefix)
}

// Overlaps reports whether p overlaps any of taken.
func Overlaps(p netip.Prefix, taken []netip.Prefix) bool {
	return slices.ContainsFunc(taken, p.Overlaps)
}

// SubnetUsers names, sorted, the podman networks in nets other than own
// whose subnets overlap 10.214.n.0/24.
func SubnetUsers(nets map[string][]netip.Prefix, own string, n int) []string {
	var users []string
	for name, subnets := range nets {
		if name != own && Overlaps(SubnetPrefix(n), subnets) {
			users = append(users, name)
		}
	}
	slices.Sort(users)
	return users
}

// MaxWorktreeID caps worktree IDs so the per-worktree port ranges
// (base+ID) of different services never overlap (playwright 18888+ID vs
// storage 19000+ID).
const MaxWorktreeID = 99

// AddWorktree registers slug under a registered project on the lowest free
// ID in 1..MaxWorktreeID, machine-wide.
func (r *Registry) AddWorktree(prefix, slug string) (WorktreeEntry, error) {
	if _, ok := r.Project(prefix); !ok {
		return WorktreeEntry{}, fmt.Errorf("project %s is not registered", prefix)
	}
	used := make(map[int]bool, len(r.Worktrees))
	for _, w := range r.Worktrees {
		if w.Prefix == prefix && w.Slug == slug {
			return WorktreeEntry{}, fmt.Errorf("%s/%s: %w", prefix, slug, ErrSlugTaken)
		}
		used[w.ID] = true
	}
	for id := 1; id <= MaxWorktreeID; id++ {
		if !used[id] {
			e := WorktreeEntry{Prefix: prefix, Slug: slug, ID: id}
			r.Worktrees = append(r.Worktrees, e)
			return e, nil
		}
	}
	return WorktreeEntry{}, fmt.Errorf("%s/%s: at most %d worktrees per machine; remove one with railswitch wt rm", prefix, slug, MaxWorktreeID)
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

// RemoveProject drops prefix's entry and every worktree entry of it. An
// absent project is an error wrapping fs.ErrNotExist.
func (r *Registry) RemoveProject(prefix string) error {
	i := slices.IndexFunc(r.Projects, func(p ProjectEntry) bool { return p.Prefix == prefix })
	if i < 0 {
		return fmt.Errorf("project %s: %w", prefix, fs.ErrNotExist)
	}
	r.Projects = slices.Delete(r.Projects, i, i+1)
	r.Worktrees = slices.DeleteFunc(r.Worktrees, func(w WorktreeEntry) bool { return w.Prefix == prefix })
	return nil
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

// SubnetPrefix is Subnet(n) as a netip.Prefix.
func SubnetPrefix(n int) netip.Prefix {
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 214, byte(n), 0}), 24)
}

// Gateway is the network's gateway: 10.214.<n>.1.
func Gateway(n int) string { return addr(n, ".1") }

// TraefikIP is the shared proxy's address on the network: 10.214.<n>.2.
func TraefikIP(n int) string { return addr(n, ".2") }

// IPRange is the network's dynamic range: 10.214.<n>.128/25.
func IPRange(n int) string { return addr(n, ".128/25") }
