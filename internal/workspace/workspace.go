// Package workspace finds a project's workspace and current worktree, writes
// its stack.lock, and is the single path that renders its units. It is used
// by internal/cli, lifecycle, worktree and doctor, and imports none of them.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
)

// Version is railswitch's version, recorded in stack.lock for built-in
// packages. internal/cli sets it from cli.Version, the -ldflags target.
var Version = "dev"

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
// stack.lock must exist (an error wrapping config.ErrNoLock otherwise) and
// match the resolved stack.
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
	cat, err := pack.LoadBuiltins()
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
	want, err := lockEntries(st)
	if err != nil {
		return nil, err
	}
	if err := checkLock(lock, want); err != nil {
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
	cat, err := pack.LoadBuiltins()
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
	l.Packs, l.Options = want.Packs, want.Options
	return l.Write(path)
}

// lockEntries is the lock's pack and option entries for st.
func lockEntries(st *pack.Stack) (*config.Lock, error) {
	src := "built in (railswitch " + Version + ")"
	entry := func(version int, files fs.FS) (config.LockEntry, error) {
		sum, err := pack.ContentHash(files)
		return config.LockEntry{Version: strconv.Itoa(version), Source: src, SHA256: sum}, err
	}
	l := &config.Lock{Packs: map[string]config.LockEntry{}, Options: map[string]config.LockEntry{}}
	e, err := entry(st.Pack.Version, st.Pack.Files)
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", st.Pack.Name, err)
	}
	l.Packs[st.Pack.Name] = e
	for _, ch := range st.Options {
		o := ch.Option
		e, err := entry(o.Version, o.Files)
		if err != nil {
			return nil, fmt.Errorf("option %s: %w", o.Name, err)
		}
		l.Options[o.Name] = e
	}
	return l, nil
}

// checkLock refuses a lock whose packs or options differ from want by
// version or content hash, or that lacks or has an extra one.
func checkLock(have, want *config.Lock) error {
	var stale []string
	cmp := func(have, want map[string]config.LockEntry) {
		for _, name := range sortedKeys(want) {
			h, ok := have[name]
			if !ok || h.Version != want[name].Version || h.SHA256 != want[name].SHA256 {
				stale = append(stale, name+"@"+want[name].Version)
			}
		}
		for _, name := range sortedKeys(have) {
			if _, ok := want[name]; !ok {
				stale = append(stale, name+"@"+have[name].Version)
			}
		}
	}
	cmp(have.Packs, want.Packs)
	cmp(have.Options, want.Options)
	if len(stale) > 0 {
		return fmt.Errorf("stack.lock is out of date for %s: run railswitch lock", strings.Join(stale, ", "))
	}
	return nil
}

// Model builds the project's render model from the registry: the project's
// subnet and every registered worktree of it.
func (w *Workspace) Model(reg *machine.Registry, host render.Host) (*render.Model, error) {
	prefix := w.Project.Project.Prefix
	pe, ok := reg.Project(prefix)
	if !ok {
		return nil, fmt.Errorf("project %s is not registered on this machine", prefix)
	}
	if filepath.Clean(pe.Workspace) != w.Root {
		return nil, fmt.Errorf("project prefix %s is registered for workspace %s, not %s", prefix, pe.Workspace, w.Root)
	}
	var wts []render.Worktree
	for _, e := range reg.WorktreesOf(prefix) {
		dir := filepath.Join(w.Root, e.Slug)
		wts = append(wts, render.Worktree{Slug: e.Slug, ID: e.ID, Dir: dir, Branch: branchOf(dir)})
	}
	gen, err := config.ReadGenerated(filepath.Join(w.Root, "mise.local.toml"))
	if err != nil {
		return nil, err
	}
	return render.Build(render.Inputs{
		Workspace: w.Root,
		Project:   w.Project,
		Stack:     w.Stack,
		Lock:      w.Lock,
		Machine:   pe,
		Worktrees: wts,
		Generated: gen,
		Host:      host,
	})
}

// Render opens the registry, builds the Model, merges every source into one
// render.Plan and Applies it; with check it writes nothing. It is the only
// path that writes units.
func (w *Workspace) Render(ctx context.Context, r sys.Runner, check bool) (*render.Diff, error) {
	host, err := ReadHost(ctx, r)
	if err != nil {
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
	p, err := m.Plan()
	if err != nil {
		return nil, err
	}
	return render.Apply(p, w.Root, w.Dirs.Quadlet, w.Project.Project.Prefix, check)
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
