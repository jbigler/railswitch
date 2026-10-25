// Package worktree adds, removes and lists a project's git worktrees, each
// registered on the machine with its ID, and tears a removed one's podman
// and systemd objects down by exact name.
package worktree

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
)

// Manager manages one project's worktrees.
type Manager struct {
	R   sys.Runner
	WS  string // the workspace
	Pfx string // the project prefix
	// Data is machine.Dirs.Data. The registry is opened only around each
	// registry change (Open → change → Save → Close), never across Render:
	// Render opens it too, and flock is per open file description, so
	// holding it would deadlock.
	Data      string
	Base      string   // [project] base_branch; the base worktree is <WS>/<Sanitize(Base)> (D22)
	SeedFiles []string // [project] seed_files, relative to a worktree
	In        io.Reader
	Out       io.Writer
	TTY       bool // In and Out are a terminal: Add prompts for the name
	// Render renders the project's units (workspace.Render).
	Render func(ctx context.Context) error
	// Names are a registered worktree's exact object names
	// (render.Model.NamesFor).
	Names func(slug string) (render.Names, error)
	// WorktreeVolumes are the mounts of the pack's [[worktree_volumes]]
	// relative to /app, e.g. "node_modules".
	WorktreeVolumes []string
}

// Add checks out target in a new worktree <WS>/<slug>, registers it,
// seeds it and renders. target is a branch, a PR number (its head branch,
// via gh) or a new branch, created from the base worktree's HEAD. name is
// the worktree name (--name); when empty, Add prompts for it on a terminal
// and otherwise uses the sanitized branch, suffixed with 4 hex digits of
// its sha256 when that slug is taken. Nothing is registered unless
// `git worktree add` succeeded, and any later failure rolls the whole Add
// back (see rollback), so a second run can succeed.
func (m *Manager) Add(ctx context.Context, target, name string) (w render.Worktree, err error) {
	base, err := m.baseDir()
	if err != nil {
		return render.Worktree{}, err
	}
	u := undo{base: base}
	defer func() {
		if err != nil {
			w, err = render.Worktree{}, m.rollback(ctx, u, err)
		}
	}()
	branch, fetched, err := m.branchFor(ctx, base, target)
	if err != nil {
		return render.Worktree{}, err
	}
	if fetched {
		u.branch = branch
	}
	slug, err := m.slugFor(branch, name)
	if err != nil {
		return render.Worktree{}, err
	}
	dir := filepath.Join(m.WS, slug)
	home := filepath.Join(m.WS, ".railswitch", "home", slug)
	if _, err := os.Lstat(home); errors.Is(err, fs.ErrNotExist) {
		u.home = home // a kept home of an earlier worktree of this name is user data
	}
	created, err := m.gitWorktreeAdd(ctx, base, dir, branch)
	if err != nil {
		return render.Worktree{}, err
	}
	u.dir = dir
	if created {
		u.branch = branch
	}
	e, err := m.register(slug)
	if err != nil {
		return render.Worktree{}, err
	}
	u.slug = slug
	for _, p := range m.WorktreeVolumes {
		if !filepath.IsLocal(p) {
			return render.Worktree{}, fmt.Errorf("worktree volume mount %q is not below /app", p)
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			return render.Worktree{}, err
		}
		err = root.MkdirAll(p, 0o755)
		root.Close()
		if err != nil {
			return render.Worktree{}, fmt.Errorf("create %s for its worktree volume: %w", filepath.Join(dir, p), err)
		}
	}
	if err := m.seed(base, dir); err != nil {
		return render.Worktree{}, err
	}
	u.rendered = true
	if err := m.Render(ctx); err != nil {
		return render.Worktree{}, err
	}
	return render.Worktree{Slug: slug, ID: e.ID, Dir: dir, Branch: branch}, nil
}

// undo is what a failed Add has done so far.
type undo struct {
	base     string
	branch   string // the branch Add created (or fetched), to delete
	dir      string // the checkout Add created
	home     string // the slug's home dir, absent before Add
	slug     string // registered by Add
	rendered bool   // Render ran
}

// rollback undoes a failed Add in reverse order: the registry entry, the
// slug's env dir and (new) home dir, the checkout, the branch Add created;
// then, when Render ran, it renders again, which deletes the slug's units
// and env files (D28). It returns cause joined with every rollback error.
// It stops at a failed unregister: a registered worktree whose checkout is
// gone would block every later change (the partial-view abort).
func (m *Manager) rollback(ctx context.Context, u undo, cause error) error {
	ctx = context.WithoutCancel(ctx) // an interrupted Add still rolls back (each call keeps its timeout)
	errs := []error{cause}
	fail := func(what string, err error) {
		errs = append(errs, fmt.Errorf("rolling back: %s: %w", what, err))
	}
	if u.slug != "" {
		if err := m.unregister(u.slug); err != nil {
			fail("unregister "+u.slug, err)
			return errors.Join(errs...)
		}
		if err := os.RemoveAll(filepath.Join(m.WS, ".railswitch", "env", u.slug)); err != nil {
			fail("remove its env dir", err)
		}
	}
	if u.home != "" && u.rendered { // only Render creates it
		if err := os.RemoveAll(u.home); err != nil {
			fail("remove its home dir", err)
		}
	}
	if u.dir != "" {
		if _, err := m.gitT(ctx, gitLong, u.base, "worktree", "remove", "--force", u.dir); err != nil {
			fail("git worktree remove "+u.dir, err)
		}
	}
	if u.branch != "" {
		if _, err := m.git(ctx, u.base, "branch", "-D", u.branch); err != nil {
			fail("delete branch "+u.branch, err)
		}
	}
	if u.rendered {
		if err := m.Render(ctx); err != nil {
			fail("re-render without "+u.slug+" (run railswitch render)", err)
		}
	}
	return errors.Join(errs...)
}

// baseDir is the base worktree, <WS>/<Sanitize(Base)> (D22).
func (m *Manager) baseDir() (string, error) {
	slug, err := m.baseSlug()
	if err != nil {
		return "", err
	}
	return filepath.Join(m.WS, slug), nil
}

func (m *Manager) baseSlug() (string, error) {
	slug, err := naming.Sanitize(m.Base)
	if err != nil {
		return "", fmt.Errorf("project.base_branch %q: %w", m.Base, err)
	}
	return slug, nil
}

// branchFor is target's branch: a PR number's head branch, else target.
// fetched: a PR head that was neither local nor on origin was fetched
// into a new local branch.
func (m *Manager) branchFor(ctx context.Context, base, target string) (branch string, fetched bool, err error) {
	branch = target
	if isPR(target) {
		res, err := m.R.Run(ctx, sys.Cmd{Name: "gh", Args: []string{"pr", "view", target, "--json", "headRefName", "-q", ".headRefName"}, Dir: base})
		if err != nil {
			return "", false, fmt.Errorf("PR %s: %w", target, err)
		}
		branch = strings.TrimSpace(string(res.Stdout))
	}
	if branch == "" || strings.HasPrefix(branch, "-") {
		return "", false, fmt.Errorf("%q is not a branch name", branch)
	}
	if _, err := m.git(ctx, base, "check-ref-format", "--branch", branch); err != nil {
		return "", false, fmt.Errorf("%q is not a branch name: %w", branch, err)
	}
	if !isPR(target) {
		return branch, false, nil
	}
	for _, ref := range []string{"refs/heads/" + branch, "refs/remotes/origin/" + branch} {
		ok, err := m.hasRef(ctx, base, ref)
		if err != nil || ok {
			return branch, false, err // gitWorktreeAdd tracks origin/<branch>
		}
	}
	// A fork's PR: its head, fetched.
	if _, err := m.gitT(ctx, gitLong, base, "fetch", "origin", "refs/pull/"+target+"/head:refs/heads/"+branch); err != nil {
		return "", false, fmt.Errorf("fetch PR %s: %w", target, err)
	}
	return branch, true, nil
}

func isPR(target string) bool {
	return target != "" && strings.Trim(target, "0123456789") == ""
}

// slugFor picks the worktree's slug; see Add.
func (m *Manager) slugFor(branch, name string) (string, error) {
	entries, err := m.registered()
	if err != nil {
		return "", err
	}
	isTaken := func(slug string) bool {
		_, err := os.Lstat(filepath.Join(m.WS, slug))
		return slices.ContainsFunc(entries, func(e machine.WorktreeEntry) bool { return e.Slug == slug }) || !errors.Is(err, fs.ErrNotExist)
	}
	def, err := naming.Sanitize(branch)
	if err != nil {
		return "", fmt.Errorf("branch %q: %w", branch, err)
	}
	if isTaken(def) {
		sum := sha256.Sum256([]byte(branch))
		def = strings.TrimRight(def[:min(len(def), naming.SlugMaxLen-5)], "-") + "-" + hex.EncodeToString(sum[:2])
	}
	if name == "" && m.TTY {
		fmt.Fprintf(m.Out, "Worktree name [%s]: ", def)
		line, err := bufio.NewReader(m.In).ReadString('\n')
		if err != nil && (!errors.Is(err, io.EOF) || line == "") {
			return "", fmt.Errorf("read the worktree name: %w", err)
		}
		name = strings.TrimSpace(line)
	}
	slug := def
	if name != "" {
		if slug, err = naming.Sanitize(name); err != nil {
			return "", fmt.Errorf("worktree name %q: %w", name, err)
		}
	}
	if isTaken(slug) {
		return "", fmt.Errorf("worktree name %s is taken: it is registered or %s exists", slug, filepath.Join(m.WS, slug))
	}
	return slug, nil
}

// gitWorktreeAdd checks branch out at dir: an existing local branch, a
// new tracking branch for origin/<branch>, else a new branch at the base
// worktree's HEAD. created: it created the local branch.
func (m *Manager) gitWorktreeAdd(ctx context.Context, base, dir, branch string) (created bool, err error) {
	args := []string{"worktree", "add", dir, branch}
	local, err := m.hasRef(ctx, base, "refs/heads/"+branch)
	if err != nil {
		return false, err
	}
	if !local {
		remote, err := m.hasRef(ctx, base, "refs/remotes/origin/"+branch)
		if err != nil {
			return false, err
		}
		if remote {
			args = []string{"worktree", "add", "--track", "-b", branch, dir, "origin/" + branch}
		} else {
			args = []string{"worktree", "add", "-b", branch, dir, "HEAD"}
		}
	}
	if _, err := m.gitT(ctx, gitLong, base, args...); err != nil {
		return false, fmt.Errorf("git worktree add %s: %w", branch, err)
	}
	return !local, nil
}

// register adds slug to the registry.
func (m *Manager) register(slug string) (machine.WorktreeEntry, error) {
	reg, err := machine.Open(m.Data)
	if err != nil {
		return machine.WorktreeEntry{}, err
	}
	defer reg.Close()
	e, err := reg.AddWorktree(m.Pfx, slug)
	if err != nil {
		return machine.WorktreeEntry{}, err
	}
	return e, reg.Save()
}

// seed copies each seed file present in the base worktree and absent in
// dir, at mode 0600. A file safefile refuses is skipped with a warning.
func (m *Manager) seed(base, dir string) error {
	if len(m.SeedFiles) == 0 {
		return nil
	}
	root, err := os.OpenRoot(dir) // a checked-out symlink can't redirect a write
	if err != nil {
		return err
	}
	defer root.Close()
	for _, p := range m.SeedFiles {
		if !filepath.IsLocal(p) {
			fmt.Fprintf(m.Out, "warning: seed file %s skipped: not a path inside the worktree\n", p)
			continue
		}
		_, lerr := root.Lstat(p)
		if lerr == nil {
			continue // present in the new worktree: keep it
		}
		data, err := safefile.Read(filepath.Join(base, p), safefile.Limit)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			fmt.Fprintf(m.Out, "warning: seed file %s skipped: %v\n", p, err)
			continue
		}
		if !errors.Is(lerr, fs.ErrNotExist) {
			return fmt.Errorf("seed file %s: %w", p, lerr) // e.g. a parent is a file in this branch
		}
		if d := filepath.Dir(p); d != "." {
			if err := root.MkdirAll(d, 0o755); err != nil {
				return fmt.Errorf("seed file %s: %w", p, err)
			}
		}
		f, err := root.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("seed file %s: %w", p, err)
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return fmt.Errorf("seed file %s: %w", p, werr)
		}
	}
	return nil
}

// sortedSlugs is used in error messages.
func sortedSlugs(s []string) string {
	s = slices.Clone(s)
	slices.Sort(s)
	return strings.Join(s, ", ")
}
