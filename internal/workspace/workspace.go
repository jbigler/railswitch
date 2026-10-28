// Package workspace finds a project's workspace and current worktree, writes
// its stack.lock, and is the single path that renders its units. It is used
// by internal/cli, lifecycle, worktree and doctor, and imports none of them.
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/buildinfo"
	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/firewall"
	"github.com/jbigler/railswitch/internal/gitguard"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
)

const (
	projectFile = "project.toml"
	lockFile    = "stack.lock"
)

// Workspace is a loaded project workspace.
type Workspace struct {
	Root    string
	Project *config.Project
	Catalog *pack.Catalog
	Stack   *pack.Stack
	Lock    *config.Lock
	Dirs    machine.Dirs
	Slug    string // current worktree, "" at the root
	// Pending are the packages whose content changed under an unchanged
	// ref and that run from the store's accepted copy (D97), sorted.
	Pending []pack.Ref

	overlay map[string][]byte // WithFile's files, by workspace-relative path (nil: removed)
}

// Root walks up from startDir (the working directory when empty) to the
// first directory holding project.toml.
func Root(startDir string) (string, error) {
	start, err := absDir(startDir)
	if err != nil {
		return "", err
	}
	for dir := start; ; dir = filepath.Dir(dir) {
		fi, err := os.Stat(filepath.Join(dir, projectFile))
		if err == nil && fi.Mode().IsRegular() {
			return dir, nil
		}
		if filepath.Dir(dir) == dir {
			return "", fmt.Errorf("no %s in %s or any parent directory (pass -C <workspace>)", projectFile, start)
		}
	}
}

func absDir(dir string) (string, error) {
	if dir == "" {
		return os.Getwd()
	}
	return filepath.Abs(dir)
}

// Load finds the workspace from startDir and loads it. The worktree is slug
// when set (-w), else the first path element of startDir below the root.
// stack.lock must exist (an error wrapping config.ErrNoLock otherwise); it
// may be out of date (see CheckLock), so teardown and diagnosis still work.
// A package whose content changed under the ref stack.lock pins runs the
// store's copy of what the lock accepted (Pending).
func Load(ctx context.Context, startDir, slug string) (*Workspace, error) {
	return load(startDir, slug, nil, nil)
}

// LoadEdited is Load of the workspace at root, with cat (a LoadCatalog of
// root with edits in memory, Catalog.WithFile) as its catalog: the project
// as an edit of package edited would leave it, written nowhere. edited
// keeps its edited content; any other package runs what the lock accepted.
func LoadEdited(_ context.Context, root string, cat *pack.Catalog, edited pack.Ref) (*Workspace, error) {
	return load(root, "", cat, []pack.Ref{edited})
}

func load(startDir, slug string, cat *pack.Catalog, keep []pack.Ref) (*Workspace, error) {
	root, err := Root(startDir)
	if err != nil {
		return nil, err
	}
	if slug != "" {
		if s, err := naming.Sanitize(slug); err != nil || s != slug {
			return nil, fmt.Errorf("-w %q is not a worktree slug (lowercase letters, digits and -, at most %d chars)", slug, naming.SlugMaxLen)
		}
	} else {
		slug = slugAt(root, startDir)
	}
	p, err := loadProject(root)
	if err != nil {
		return nil, err
	}
	if cat == nil {
		if cat, err = pack.LoadCatalog(root); err != nil {
			return nil, err
		}
	}
	lock, err := config.ReadLock(filepath.Join(root, lockFile))
	if err != nil {
		if errors.Is(err, config.ErrNoLock) {
			return nil, fmt.Errorf("%w: run railswitch lock", err)
		}
		return nil, err
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return nil, err
	}
	// The swap comes before Resolve: an update that no longer resolves
	// (or a local package broken in an editor) still runs what was accepted.
	cat, pending := runAccepted(cat, p, lock, store(dirs), keep)
	st, err := cat.Resolve(p)
	if err != nil {
		return nil, err
	}
	return &Workspace{Root: root, Project: p, Catalog: cat, Stack: st, Lock: lock, Dirs: dirs, Slug: slug, Pending: pending}, nil
}

// slugAt is the worktree startDir is in: its first path element below root,
// or "" at the root, in a dot dir or in a dir whose name is not a slug.
func slugAt(root, startDir string) string {
	start, err := absDir(startDir)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, start)
	if err != nil || rel == "." {
		return ""
	}
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	if s, err := naming.Sanitize(first); err != nil || s != first {
		return ""
	}
	return first
}

func loadProject(root string) (*config.Project, error) {
	path := filepath.Join(root, projectFile)
	p, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// WithProject is w with project p in place of its own: p's stack, and a
// lock of that stack's packages that keeps w's image digests, in memory
// only, so p renders without writing stack.lock.
func (w *Workspace) WithProject(p *config.Project) (*Workspace, error) {
	st, err := w.Catalog.Resolve(p)
	if err != nil {
		return nil, err
	}
	want, err := lockEntries(st)
	if err != nil {
		return nil, err
	}
	l := *w.Lock
	l.Packs, l.Options, l.Agents = want.Packs, want.Options, want.Agents
	c := *w
	c.Project, c.Stack, c.Lock = p, st, &l
	return &c, nil
}

// WithFile is a copy of w whose renders read data at rel, a
// workspace-relative slash path (nil: rel is removed), in memory only.
func (w *Workspace) WithFile(rel string, data []byte) *Workspace {
	c := *w
	c.overlay = maps.Clone(w.overlay)
	if c.overlay == nil {
		c.overlay = map[string][]byte{}
	}
	c.overlay[rel] = data
	return &c
}

// Hash is the content hash of a project.toml: "sha256:<hex>".
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// lockEntries is the lock's pack, option and agent entries for st. Packs and
// options are keyed by name, agents by ref string ("local:probe@1"); a
// workspace-local package's source is "local" (D43).
func lockEntries(st *pack.Stack) (*config.Lock, error) {
	l := &config.Lock{Packs: map[string]config.LockEntry{}, Options: map[string]config.LockEntry{}, Agents: map[string]config.LockEntry{}}
	for _, sp := range stackPackages(st) {
		e, err := sp.entry()
		if err != nil {
			return nil, err
		}
		table(l, sp.kind)[sp.key] = e
	}
	return l, nil
}

// stackPkg is one package of a resolved stack, as stack.lock keys it.
type stackPkg struct {
	kind, key string // pack.KindPack etc.; the lock table's key
	ref       pack.Ref
	files     fs.FS
}

// stackPackages are st's pack, options and agent packages, one per key.
func stackPackages(st *pack.Stack) []stackPkg {
	out := []stackPkg{{pack.KindPack, st.Pack.Name, st.PackRef, st.Pack.Files}}
	for _, ch := range st.Options {
		out = append(out, stackPkg{pack.KindOption, ch.Option.Name, ch.Ref, ch.Option.Files})
	}
	seen := map[string]bool{}
	for _, a := range st.Agents {
		if key := a.Ref.String(); !seen[key] {
			seen[key] = true
			out = append(out, stackPkg{pack.KindAgent, key, a.Ref, a.Agent.Files})
		}
	}
	return out
}

// entry is sp's lock entry, its content hashed.
func (sp stackPkg) entry() (config.LockEntry, error) {
	src := "built in (railswitch " + buildinfo.Version + ")"
	if sp.ref.Local {
		src = localSource
	}
	sum, err := pack.ContentHash(sp.files)
	if err != nil {
		return config.LockEntry{}, fmt.Errorf("%s %s: %w", sp.kind, sp.ref, err)
	}
	return config.LockEntry{Version: strconv.Itoa(sp.ref.Version), Source: src, SHA256: sum}, nil
}

// table is l's table of kind, made when nil.
func table(l *config.Lock, kind string) map[string]config.LockEntry {
	t := map[string]*map[string]config.LockEntry{pack.KindPack: &l.Packs, pack.KindOption: &l.Options, pack.KindAgent: &l.Agents}[kind]
	if *t == nil {
		*t = map[string]config.LockEntry{}
	}
	return *t
}

// pins reports whether e locks ref r: its version and locality.
func pins(e config.LockEntry, r pack.Ref) bool {
	return e.Version == strconv.Itoa(r.Version) && (e.Source == localSource) == r.Local
}

// localSource is a workspace-local package's lock source.
const localSource = "local"

// ErrStaleLock is wrapped by CheckLock's (and so Render's) error when
// stack.lock differs from the resolved stack.
var ErrStaleLock = errors.New("stack.lock is out of date")

// CheckLock refuses a stack.lock whose packs, options or agents differ from
// the resolved stack by version, content hash or locality (a built-in and a
// local copy of it are different packages, even byte for byte), or that
// lacks or has an extra one: the hash exists to stop unreviewed package
// content (HANDOFF §3.11). The built-in source text, which carries the
// railswitch version, is not compared.
func (w *Workspace) CheckLock() error {
	want, err := lockEntries(w.Stack)
	if err != nil {
		return err
	}
	have := w.Lock
	var stale []string
	// label names an entry in the error: [local:]name@version for packs and
	// options, the key itself for agents (already a ref string).
	cmp := func(have, want map[string]config.LockEntry, label func(string, config.LockEntry) string) {
		for _, name := range sortedKeys(want) {
			h, ok := have[name]
			x := want[name]
			if !ok || h.Version != x.Version || h.SHA256 != x.SHA256 || (h.Source == localSource) != (x.Source == localSource) {
				stale = append(stale, label(name, x))
			}
		}
		for _, name := range sortedKeys(have) {
			if _, ok := want[name]; !ok {
				stale = append(stale, label(name, have[name]))
			}
		}
	}
	versioned := func(name string, e config.LockEntry) string {
		if e.Source == localSource {
			return config.LocalPrefix + name + "@" + e.Version
		}
		return name + "@" + e.Version
	}
	cmp(have.Packs, want.Packs, versioned)
	cmp(have.Options, want.Options, versioned)
	cmp(have.Agents, want.Agents, func(key string, _ config.LockEntry) string { return key })
	if len(stale) > 0 {
		return fmt.Errorf("%w for %s: %s", ErrStaleLock, strings.Join(stale, ", "), reviewHint)
	}
	return nil
}

// Model builds the project's render model from the registry: the project's
// subnet and every registered worktree of it. It sets w.Root to the
// registered workspace path, which Root may reach through a symlink.
func (w *Workspace) Model(reg *machine.Registry, host render.Host) (*render.Model, error) {
	prefix := w.Project.Project.Prefix
	pe, ok := reg.Project(prefix)
	if !ok {
		return nil, fmt.Errorf("project %s is not registered on this machine", prefix)
	}
	if !samePath(pe.Workspace, w.Root) {
		return nil, fmt.Errorf("project prefix %s is registered for workspace %s, not %s", prefix, pe.Workspace, w.Root)
	}
	// Reached through another path (a symlink): every rendered path, marker
	// and Apply target uses the registered one, so renders agree byte for byte.
	w.Root = filepath.Clean(pe.Workspace)
	var wts []render.Worktree
	for _, e := range reg.WorktreesOf(prefix) {
		dir := filepath.Join(w.Root, e.Slug)
		wts = append(wts, render.Worktree{Slug: e.Slug, ID: e.ID, Dir: dir, Branch: branchOf(dir), Share: e.Share})
	}
	gen, err := config.ReadGenerated(filepath.Join(w.Root, "mise.local.toml"))
	if err != nil {
		return nil, err
	}
	cfg, err := machine.LoadConfig(w.Dirs)
	if err != nil {
		return nil, err
	}
	mounts, missing := machineMounts(cfg, prefix)
	host.Missing = missing
	return render.Build(render.Inputs{
		Workspace: w.Root,
		Project:   w.Project,
		Stack:     w.Stack,
		Lock:      w.Lock,
		Machine:   pe,
		Worktrees: wts,
		Generated: gen,
		Host:      host,
		Mounts:    mounts,
		Overlay:   w.overlay,
	})
}

// GitGuard runs git-guard's check (D60) on the base worktree, with the
// project's other registered worktrees as the linked ones (none without a
// registry). A workspace reached through another path than the registered
// one (a symlink) is checked at the registered path, the one git recorded.
func (w *Workspace) GitGuard(ctx context.Context, r sys.Runner) ([]gitguard.Finding, error) {
	base, err := w.baseSlug()
	if err != nil {
		return nil, err
	}
	root := w.Root
	var linked []string
	switch reg, err := machine.Read(w.Dirs.Data); {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		prefix := w.Project.Project.Prefix
		if pe, ok := reg.Project(prefix); ok && samePath(pe.Workspace, w.Root) {
			root = filepath.Clean(pe.Workspace)
		}
		for _, e := range reg.WorktreesOf(prefix) {
			if e.Slug != base {
				linked = append(linked, e.Slug)
			}
		}
	}
	return gitguard.Check(ctx, r, root, filepath.Join(root, base), linked)
}

// BaseDir is the base worktree's directory, <root>/<sanitize(base_branch)>.
func (w *Workspace) BaseDir() (string, error) {
	base, err := w.baseSlug()
	if err != nil {
		return "", err
	}
	return filepath.Join(w.Root, base), nil
}

func (w *Workspace) baseSlug() (string, error) {
	b := w.Project.Project.BaseBranch
	slug, err := naming.Sanitize(b)
	if err != nil {
		return "", fmt.Errorf("[project] base_branch %q: %w", b, err)
	}
	return slug, nil
}

// machineMounts are cfg's [[mount]]s of project prefix (D57), and which of
// their optional sources are missing now (render leaves those out; a
// missing non-optional source fails Preflight).
func machineMounts(cfg *machine.Config, prefix string) ([]render.MachineMount, map[string]bool) {
	var out []render.MachineMount
	var missing map[string]bool
	for i, m := range cfg.Mounts {
		if len(m.Projects) > 0 && !slices.Contains(m.Projects, prefix) {
			continue
		}
		out = append(out, render.MachineMount{Service: m.Service, Source: m.Source, Target: m.Target, RO: m.ReadOnly(), Relabel: m.Relabels(), Optional: m.Optional, Index: i})
		if m.Optional {
			if _, err := os.Stat(m.Source); err != nil {
				if missing == nil {
					missing = map[string]bool{}
				}
				missing[m.Source] = true
			}
		}
	}
	return out, missing
}

// samePath reports whether a and b name the same directory, through
// symlinks when both resolve.
func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// Render checks stack.lock (CheckLock), opens the registry, builds the
// Model, records the Model's port bases in the registry when they differ
// (SetPorts), merges every source into one render.Plan (the units and files
// of the Model, the firewall's files, the machine's proxy units) and Applies
// it; with check it writes nothing, and differing port bases are a change
// in the Diff. It is the only path that writes units. Without check it also
// writes the dashboard's unit; failing that is a Diff warning, not an error.
func (w *Workspace) Render(ctx context.Context, r sys.Runner, check bool) (*render.Diff, error) {
	if err := w.CheckLock(); err != nil {
		return nil, err
	}
	host, err := ReadHost(ctx, r)
	if err != nil {
		return nil, err
	}
	if host.NetworkDir, err = NetworkDir(ctx, r); err != nil {
		return nil, err
	}
	reg, err := machine.Open(w.Dirs.Data)
	if err != nil {
		return nil, err
	}
	defer reg.Close() // held through Apply: renders of the machine are serial
	m, err := w.Model(reg.Registry, host)
	if err != nil {
		return nil, err
	}
	prefix := w.Project.Project.Prefix
	pe, _ := reg.Project(prefix) // Model found it
	bases := m.PortBases()
	portsChanged := !maps.Equal(pe.Ports, bases)
	if portsChanged && !check {
		if err := reg.SetPorts(prefix, bases); err != nil {
			return nil, err
		}
		if err := reg.Save(); err != nil {
			return nil, err
		}
	}
	p, err := m.Plan()
	if err != nil {
		return nil, err
	}
	fw, err := firewall.Files(prefix, w.Root, host.NetworkDir, w.Stack, w.Project.Firewall)
	if err != nil {
		return nil, err
	}
	maps.Copy(p.Files, fw)
	maps.Copy(p.Units, machine.ProxyUnits(reg.Registry))
	d, err := render.Apply(p, w.Root, w.Dirs.Quadlet, prefix, check)
	if err == nil && !check {
		// A container whose unit render removed (a service no longer in
		// the stack, D28) keeps running, and holds its volume, until it
		// is stopped: stop it while systemd still has the unit loaded.
		var gone []string
		for _, n := range d.Removed {
			if base, ok := strings.CutSuffix(n, ".container"); ok {
				gone = append(gone, base+".service")
			}
		}
		if err := (podman.Systemd{R: r}).StopLoaded(ctx, gone...); err != nil {
			return d, fmt.Errorf("stop a removed unit: %w", err)
		}
		// The dashboard is a convenience: its unit failing to be written
		// must not stop an up.
		if werr := writeDashboardUnit(w.Dirs); werr != nil {
			d.Warnings = append(d.Warnings, machine.DashboardService+": "+werr.Error())
		}
	}
	if err != nil || !portsChanged || !check {
		return d, err
	}
	d.Changed = append(d.Changed, "registry.toml")
	d.Text += "--- a/registry.toml\n+++ b/registry.toml\n-ports " + prefix + portList(pe.Ports) + "\n+ports " + prefix + portList(bases) + "\n"
	return d, nil
}

// executable is the running railswitch, symlinks resolved: the dashboard
// unit's ExecStart. Tests replace it.
var executable = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// writeDashboardUnit rewrites the dashboard's unit for this railswitch
// (machine.WriteDashboardUnit); `up` restarts a running dashboard whose
// unit file is newer than its start.
func writeDashboardUnit(d machine.Dirs) error {
	exe, err := executable()
	if err != nil {
		return fmt.Errorf("the dashboard unit needs railswitch's path: %w", err)
	}
	_, err = machine.WriteDashboardUnit(d, exe)
	return err
}

// portList is " name=base" for each port, sorted by name.
func portList(ports map[string]int) string {
	var b strings.Builder
	for _, name := range sortedKeys(ports) {
		fmt.Fprintf(&b, " %s=%d", name, ports[name])
	}
	return b.String()
}

// NetworkDir is rootless podman's netavark network config dir,
// <GraphRoot>/networks, where the firewall hook reads each network's subnets.
func NetworkDir(ctx context.Context, r sys.Runner) (string, error) {
	g, err := podman.Podman{R: r}.GraphRoot(ctx)
	if err != nil {
		return "", fmt.Errorf("podman info (the firewall hook needs the graph root): %w", err)
	}
	if g == "" {
		return "", errors.New("podman info reported no graph root (the firewall hook reads <graph root>/networks)")
	}
	return filepath.Join(g, "networks"), nil
}

// LocalHost is what render needs from the host without running anything:
// CPU count and $HOME, no git identity.
func LocalHost() render.Host {
	return render.Host{CPUs: runtime.NumCPU(), Home: os.Getenv("HOME")}
}

// ReadHost reads what render needs from the host: LocalHost and the host
// git identity (`git config --global user.name/user.email`; unset or no
// git = "").
func ReadHost(ctx context.Context, r sys.Runner) (render.Host, error) {
	h := LocalHost()
	for _, f := range []struct {
		key string
		dst *string
	}{{"user.name", &h.GitName}, {"user.email", &h.GitEmail}} {
		res, err := r.Run(ctx, sys.Cmd{Name: "git", Args: []string{"config", "--global", f.key}})
		if err != nil {
			if ctx.Err() != nil {
				return render.Host{}, ctx.Err()
			}
			continue
		}
		*f.dst = strings.TrimSpace(string(res.Stdout))
	}
	return h, nil
}

// branchOf is the branch checked out in worktree dir, read from its git
// files (a worktree's files are hostile: safefile only); "" when detached or
// unreadable.
func branchOf(dir string) string {
	gitPath := filepath.Join(dir, ".git")
	head := filepath.Join(gitPath, "HEAD")
	if data, err := safefile.Read(gitPath, safefile.Limit); err == nil {
		// A linked worktree: .git is a file "gitdir: <path>".
		gd, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
		if !ok {
			return ""
		}
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(dir, gd)
		}
		head = filepath.Join(gd, "HEAD")
	}
	data, err := safefile.Read(head, safefile.Limit)
	if err != nil {
		return ""
	}
	branch, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "ref: refs/heads/")
	if !ok || strings.ContainsAny(branch, "\r\n\x00") {
		return ""
	}
	return branch
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
