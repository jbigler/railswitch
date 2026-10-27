// Package workspace finds a project's workspace and current worktree, writes
// its stack.lock, and is the single path that renders its units. It is used
// by internal/cli, lifecycle, worktree and doctor, and imports none of them.
package workspace

import (
	"context"
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
func Load(_ context.Context, startDir, slug string) (*Workspace, error) {
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
	cat, err := pack.LoadCatalog(root)
	if err != nil {
		return nil, err
	}
	st, err := cat.Resolve(p)
	if err != nil {
		return nil, err
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
	return &Workspace{Root: root, Project: p, Catalog: cat, Stack: st, Lock: lock, Dirs: dirs, Slug: slug}, nil
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

// Lock loads and validates root's project.toml, resolves its stack and
// writes <root>/stack.lock: every pack and option with its content hash,
// keeping the image digests of an existing lock. It needs no existing lock.
func Lock(_ context.Context, root string) error {
	p, err := loadProject(root)
	if err != nil {
		return err
	}
	cat, err := pack.LoadCatalog(root)
	if err != nil {
		return err
	}
	st, err := cat.Resolve(p)
	if err != nil {
		return err
	}
	want, err := lockEntries(st)
	if err != nil {
		return err
	}
	path := filepath.Join(root, lockFile)
	l, err := config.ReadLock(path)
	if errors.Is(err, config.ErrNoLock) {
		l, err = &config.Lock{}, nil
	}
	if err != nil {
		return err
	}
	l.Packs, l.Options, l.Agents = want.Packs, want.Options, want.Agents
	return l.Write(path)
}

// lockEntries is the lock's pack, option and agent entries for st. An
// agent's key is its ref string ("local:probe@1" for a local package).
func lockEntries(st *pack.Stack) (*config.Lock, error) {
	builtinSrc := "built in (railswitch " + buildinfo.Version + ")"
	entry := func(version int, files fs.FS, src string) (config.LockEntry, error) {
		sum, err := pack.ContentHash(files)
		return config.LockEntry{Version: strconv.Itoa(version), Source: src, SHA256: sum}, err
	}
	l := &config.Lock{Packs: map[string]config.LockEntry{}, Options: map[string]config.LockEntry{}, Agents: map[string]config.LockEntry{}}
	e, err := entry(st.Pack.Version, st.Pack.Files, builtinSrc)
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", st.Pack.Name, err)
	}
	l.Packs[st.Pack.Name] = e
	for _, ch := range st.Options {
		o := ch.Option
		e, err := entry(o.Version, o.Files, builtinSrc)
		if err != nil {
			return nil, fmt.Errorf("option %s: %w", o.Name, err)
		}
		l.Options[o.Name] = e
	}
	for _, a := range st.Agents {
		key := a.Ref.String()
		if _, ok := l.Agents[key]; ok {
			continue
		}
		src := builtinSrc
		if a.Ref.Local {
			src = "local"
		}
		e, err := entry(a.Agent.Version, a.Agent.Files, src)
		if err != nil {
			return nil, fmt.Errorf("agent %s: %w", key, err)
		}
		l.Agents[key] = e
	}
	return l, nil
}

// ErrStaleLock is wrapped by CheckLock's (and so Render's) error when
// stack.lock differs from the resolved stack.
var ErrStaleLock = errors.New("stack.lock is out of date")

// CheckLock refuses a stack.lock whose packs, options or agents differ from
// the resolved stack by version or content hash, or that lacks or has an
// extra one: the hash exists to stop unreviewed package content (HANDOFF §3.11).
func (w *Workspace) CheckLock() error {
	want, err := lockEntries(w.Stack)
	if err != nil {
		return err
	}
	have := w.Lock
	var stale []string
	// label names an entry in the error: name@version for packs and
	// options, the key itself for agents (already a ref string).
	cmp := func(have, want map[string]config.LockEntry, label func(string, config.LockEntry) string) {
		for _, name := range sortedKeys(want) {
			h, ok := have[name]
			if !ok || h.Version != want[name].Version || h.SHA256 != want[name].SHA256 {
				stale = append(stale, label(name, want[name]))
			}
		}
		for _, name := range sortedKeys(have) {
			if _, ok := want[name]; !ok {
				stale = append(stale, label(name, have[name]))
			}
		}
	}
	versioned := func(name string, e config.LockEntry) string { return name + "@" + e.Version }
	cmp(have.Packs, want.Packs, versioned)
	cmp(have.Options, want.Options, versioned)
	cmp(have.Agents, want.Agents, func(key string, _ config.LockEntry) string { return key })
	if len(stale) > 0 {
		return fmt.Errorf("%w for %s: run railswitch lock", ErrStaleLock, strings.Join(stale, ", "))
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
	})
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
		out = append(out, render.MachineMount{Service: m.Service, Source: m.Source, Target: m.Target, RO: m.ReadOnly(), Optional: m.Optional, Index: i})
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
// in the Diff. It is the only path that writes units.
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
	if err != nil || !portsChanged || !check {
		return d, err
	}
	d.Changed = append(d.Changed, "registry.toml")
	d.Text += "--- a/registry.toml\n+++ b/registry.toml\n-ports " + prefix + portList(pe.Ports) + "\n+ports " + prefix + portList(bases) + "\n"
	return d, nil
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

// ReadHost reads what render needs from the host: CPU count, $HOME and the
// host git identity (`git config --global user.name/user.email`; unset or
// no git = "").
func ReadHost(ctx context.Context, r sys.Runner) (render.Host, error) {
	h := render.Host{CPUs: runtime.NumCPU(), Home: os.Getenv("HOME")}
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
