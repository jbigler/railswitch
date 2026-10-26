package machine

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
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

// MachineSubnet is the reserved 10.214.255.0/24 of the machine network
// railswitch_machine; projects get 10.214.<n>.0/24 with n in 0..254.
const MachineSubnet = 255

var (
	ErrPrefixTaken = errors.New("project prefix is already registered")
	ErrSlugTaken   = errors.New("worktree slug is already registered for this project")
)

// ProjectEntry is one registered project.
type ProjectEntry struct {
	Prefix    string         `toml:"prefix"`
	Workspace string         `toml:"workspace"`
	Subnet    int            `toml:"subnet"` // n in 10.214.n.0/24
	Ports     map[string]int `toml:"ports"`  // port name → base: every [[ports]] base of its pack, options and tools
}

// WorktreeEntry is one registered worktree. ID is unique machine-wide; ports
// are derived from it. Share is set while it is shared on the tailnet.
type WorktreeEntry struct {
	Prefix string      `toml:"prefix"`
	Slug   string      `toml:"slug"`
	ID     int         `toml:"id"`
	Share  *ShareEntry `toml:"share,omitempty"`
}

// ShareEntry is a worktree's tailnet share (D46): this machine's tailnet
// host name and IPv4 address when it was shared.
type ShareEntry struct {
	Host string `toml:"host" json:"host"` // e.g. "box.tail1234.ts.net"
	IP   string `toml:"ip" json:"ip"`     // e.g. "100.64.0.7"
	// Ports are the tailnet HTTPS ports share turned on, so a teardown
	// without the workspace turns off exactly those. Empty in an entry
	// written before they were recorded.
	Ports []int `toml:"ports,omitempty" json:"-"`
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

// Read reads <dataDir>/registry.toml without taking the lock or creating
// anything: for read-only callers such as shell completion. Save replaces
// the file atomically, so Read sees one whole version of it.
func Read(dataDir string) (*Registry, error) {
	return readRegistry(filepath.Join(dataDir, "registry.toml"))
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
	for n := range MachineSubnet {
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

// SetPorts records prefix's port bases (port name → base). Two different
// bases on the machine must be more than MaxWorktreeID apart, or their
// base+ID ranges overlap: a base within MaxWorktreeID of another project's
// different base, or two of prefix's own, is refused and nothing changes.
func (r *Registry) SetPorts(prefix string, ports map[string]int) error {
	i := slices.IndexFunc(r.Projects, func(p ProjectEntry) bool { return p.Prefix == prefix })
	if i < 0 {
		return fmt.Errorf("project %s: %w", prefix, fs.ErrNotExist)
	}
	names := slices.Sorted(maps.Keys(ports))
	for j, a := range names {
		for _, b := range names[j+1:] {
			if tooClose(ports[a], ports[b]) {
				return fmt.Errorf("project %s: port %s base %d and port %s base %d are less than %d apart, so their base+ID ranges overlap", prefix, a, ports[a], b, ports[b], MaxWorktreeID+1)
			}
		}
	}
	for _, p := range r.Projects {
		if p.Prefix == prefix {
			continue
		}
		for _, a := range names {
			for _, b := range slices.Sorted(maps.Keys(p.Ports)) {
				if tooClose(ports[a], p.Ports[b]) {
					return fmt.Errorf("project %s: port %s base %d is less than %d from project %s's port %s base %d, so their base+ID ranges overlap", prefix, a, ports[a], MaxWorktreeID+1, p.Prefix, b, p.Ports[b])
				}
			}
		}
	}
	r.Projects[i].Ports = maps.Clone(ports)
	return nil
}

// tooClose reports whether a and b are different bases whose base+ID
// ranges (ID 1..MaxWorktreeID) overlap.
func tooClose(a, b int) bool {
	return a != b && max(a-b, b-a) <= MaxWorktreeID
}

// AddWorktree registers slug under a registered project on the lowest free
// ID in 1..MaxWorktreeID, machine-wide, for which no base+ID of any base
// recorded on the machine is in busy (ports in use, see Listening).
func (r *Registry) AddWorktree(prefix, slug string, busy map[int]bool) (WorktreeEntry, error) {
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
	var bases []int
	for _, p := range r.Projects {
		for _, b := range p.Ports {
			bases = append(bases, b)
		}
	}
	slices.Sort(bases)
	bases = slices.Compact(bases)
	blocked := 0 // the first port in use that kept a free ID from being handed out
	for id := 1; id <= MaxWorktreeID; id++ {
		if used[id] {
			continue
		}
		i := slices.IndexFunc(bases, func(b int) bool { return busy[b+id] })
		if i < 0 {
			e := WorktreeEntry{Prefix: prefix, Slug: slug, ID: id}
			r.Worktrees = append(r.Worktrees, e)
			return e, nil
		}
		if blocked == 0 {
			blocked = bases[i] + id
		}
	}
	if blocked != 0 {
		return WorktreeEntry{}, fmt.Errorf("%s/%s: no free worktree ID: every free ID has a port another program listens on (the first: port %d); stop it or remove a worktree with railswitch wt rm", prefix, slug, blocked)
	}
	return WorktreeEntry{}, fmt.Errorf("%s/%s: at most %d worktrees per machine; remove one with railswitch wt rm", prefix, slug, MaxWorktreeID)
}

// SetShare sets (or, with nil, clears) the exact (prefix, slug) entry's
// Share. An absent entry is an error wrapping fs.ErrNotExist.
func (r *Registry) SetShare(prefix, slug string, s *ShareEntry) error {
	i := slices.IndexFunc(r.Worktrees, func(w WorktreeEntry) bool { return w.Prefix == prefix && w.Slug == slug })
	if i < 0 {
		return fmt.Errorf("worktree %s/%s: %w", prefix, slug, fs.ErrNotExist)
	}
	r.Worktrees[i].Share = s
	return nil
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
