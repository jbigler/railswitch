package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/sys"
)

// gitLong bounds a git call that checks out or fetches a tree.
const gitLong = 10 * time.Minute

// git runs git in dir with a worktree's hostile config defused: no
// fsmonitor command and no hooks.
func (m *Manager) git(ctx context.Context, dir string, args ...string) (string, error) {
	return m.gitT(ctx, 0, dir, args...)
}

func (m *Manager) gitT(ctx context.Context, timeout time.Duration, dir string, args ...string) (string, error) {
	res, err := m.gitRun(ctx, timeout, dir, args...)
	return string(res.Stdout), err
}

// gitRun is the one place a git argv is built.
func (m *Manager) gitRun(ctx context.Context, timeout time.Duration, dir string, args ...string) (sys.Result, error) {
	return m.R.Run(ctx, m.gitCmd(timeout, dir, args...))
}

func (m *Manager) gitCmd(timeout time.Duration, dir string, args ...string) sys.Cmd {
	argv := append([]string{"-C", dir, "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null"}, args...)
	return sys.Cmd{Name: "git", Args: argv, Timeout: timeout}
}

// fetch fetches refspec from origin into the repo at dir, unable to prompt
// and with the ssh the user chose, never one from the repo's config
// (sys.GitNetEnv).
func (m *Manager) fetch(ctx context.Context, dir, refspec string) error {
	env, err := sys.GitNetEnv(ctx, m.R, os.Environ())
	if err != nil {
		return err
	}
	c := m.gitCmd(gitLong, dir, "fetch", "origin", refspec)
	c.Env = env
	_, err = m.R.Run(ctx, c)
	return err
}

// hasRef reports whether ref exists in the repo at dir.
func (m *Manager) hasRef(ctx context.Context, dir, ref string) (bool, error) {
	res, err := m.gitRun(ctx, 0, dir, "show-ref", "--verify", "--quiet", ref)
	if err != nil && errors.Is(err, sys.ErrExit) && res.ExitCode == 1 {
		return false, nil
	}
	return err == nil, err
}

// gitWorktree is one entry of `git worktree list --porcelain`.
type gitWorktree struct{ Dir, Branch string }

// gitWorktrees lists the worktrees of the repo at base.
func (m *Manager) gitWorktrees(ctx context.Context, base string) ([]gitWorktree, error) {
	out, err := m.git(ctx, base, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	var wts []gitWorktree
	for _, line := range strings.Split(out, "\x00") {
		if dir, ok := strings.CutPrefix(line, "worktree "); ok {
			wts = append(wts, gitWorktree{Dir: dir})
		} else if b, ok := strings.CutPrefix(line, "branch refs/heads/"); ok && len(wts) > 0 {
			wts[len(wts)-1].Branch = b
		}
	}
	return wts, nil
}

// worktree is one of the project's worktrees.
type worktree struct {
	Slug, Dir, Branch string
	ID                int                 // 0 when not registered
	Git               bool                // listed by git
	Share             *machine.ShareEntry // the registry's: set while shared on the tailnet
}

// registered returns the project's registry entries.
func (m *Manager) registered() ([]machine.WorktreeEntry, error) {
	reg, err := machine.Open(m.Data)
	if err != nil {
		return nil, err
	}
	defer reg.Close()
	return reg.WorktreesOf(m.Pfx), nil
}

// present returns an error naming every entry whose directory is missing:
// with part of the project out of view, nothing may be changed.
func (m *Manager) present(entries []machine.WorktreeEntry) error {
	var missing []string
	for _, e := range entries {
		if _, err := os.Lstat(filepath.Join(m.WS, e.Slug)); err != nil {
			missing = append(missing, e.Slug)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("registered worktree(s) %s missing from %s: refusing to change anything with part of the project out of view; restore them first", sortedSlugs(missing), m.WS)
	}
	return nil
}

// worktrees are the registered worktrees and every git worktree directly
// in the workspace, merged by slug.
func (m *Manager) worktrees(ctx context.Context, base string, entries []machine.WorktreeEntry) ([]worktree, error) {
	gits, err := m.gitWorktrees(ctx, base)
	if err != nil {
		return nil, err
	}
	ws := realPath(m.WS)
	var wts []worktree
	for _, e := range entries {
		wts = append(wts, worktree{Slug: e.Slug, ID: e.ID, Dir: filepath.Join(m.WS, e.Slug), Share: e.Share})
	}
	for _, g := range gits {
		rel, err := filepath.Rel(ws, realPath(g.Dir))
		if err != nil || rel != filepath.Base(rel) {
			continue // not directly in the workspace
		}
		if s, err := naming.Sanitize(rel); err != nil || s != rel {
			continue
		}
		i := indexSlug(wts, rel)
		if i < 0 {
			wts = append(wts, worktree{Slug: rel, Dir: filepath.Join(m.WS, rel)})
			i = len(wts) - 1
		}
		wts[i].Branch, wts[i].Git = g.Branch, true
	}
	return wts, nil
}

func indexSlug(wts []worktree, slug string) int {
	for i, w := range wts {
		if w.Slug == slug {
			return i
		}
	}
	return -1
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// resolve finds target: an absolute path names the worktree there;
// otherwise target is the branch checked out in a worktree or a worktree's
// name, and is refused when those are two different worktrees.
func resolve(wts []worktree, target string) (worktree, error) {
	if filepath.IsAbs(target) {
		p := realPath(target)
		for _, w := range wts {
			if realPath(w.Dir) == p {
				return w, nil
			}
		}
		return worktree{}, fmt.Errorf("%s is not a worktree of this project", target)
	}
	byBranch, byName := -1, indexSlug(wts, target)
	for i, w := range wts {
		if w.Branch == target {
			byBranch = i
		}
	}
	switch {
	case byBranch >= 0 && byName >= 0 && byBranch != byName:
		return worktree{}, fmt.Errorf("%q is ambiguous: branch %s is checked out in worktree %s, and worktree %s is another one; pass its path", target, target, wts[byBranch].Slug, target)
	case byBranch >= 0:
		return wts[byBranch], nil
	case byName >= 0:
		return wts[byName], nil
	}
	return worktree{}, fmt.Errorf("no worktree of this project has the branch or name %q", target)
}

// dirty reports whether the worktree at dir has uncommitted changes or
// untracked files.
func (m *Manager) dirty(ctx context.Context, dir string) (bool, error) {
	out, err := m.git(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("git status in %s: %w", dir, err)
	}
	return strings.TrimSpace(out) != "", nil
}

// Row is one line of `wt ls`.
type Row struct {
	Slug   string              `json:"slug"`
	ID     int                 `json:"id"`
	Branch string              `json:"branch"` // "" when detached or not a git worktree
	Dir    string              `json:"dir"`
	Dirty  bool                `json:"dirty"`
	Share  *machine.ShareEntry `json:"share"` // null unless shared on the tailnet
}

// List is the project's registered worktrees, by ID.
func (m *Manager) List(ctx context.Context) ([]Row, error) {
	base, err := m.baseDir()
	if err != nil {
		return nil, err
	}
	entries, err := m.registered()
	if err != nil {
		return nil, err
	}
	wts, err := m.worktrees(ctx, base, entries)
	if err != nil {
		return nil, err
	}
	rows := []Row{}
	for _, w := range wts {
		if w.ID == 0 {
			continue
		}
		r := Row{Slug: w.Slug, ID: w.ID, Branch: w.Branch, Dir: w.Dir, Share: w.Share}
		if _, err := os.Lstat(w.Dir); err != nil {
			r.Branch = "" // git still lists a worktree whose directory is gone
		} else if w.Git {
			if r.Dirty, err = m.dirty(ctx, w.Dir); err != nil {
				return nil, err
			}
		}
		rows = append(rows, r)
	}
	return rows, nil
}
