package worktree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

// The project's local branches (branch ls, rm, pull and fetch; D227). The
// repository is shared by every worktree, so these are the project's, read
// and changed in the base worktree with the worktree's hostile config
// defused (gitCmd). Branch names come from a repository containers write:
// print them through safefile.Printable.

// fetchTimeout bounds a fetch of origin.
const fetchTimeout = 2 * time.Minute

// Branch is one local branch.
type Branch struct {
	Name string `json:"name"`
	// Dir is the worktree directory it is checked out in, as git lists it;
	// "" when none. A registered worktree's slug is the caller's to find.
	Dir string `json:"-"`
	// Upstream is its upstream as git names it ("origin/jb/x"), "" for
	// none; Remote and RemoteRef split it ("origin", "refs/heads/jb/x").
	Upstream  string `json:"upstream"`
	Remote    string `json:"-"`
	RemoteRef string `json:"-"`
	Gone      bool   `json:"gone"` // its upstream no longer exists (after a fetch --prune)
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`   // the tip's committer date, RFC 3339
	Merged    bool   `json:"merged"` // reachable from the base branch (origin's, else the local one)
}

// branchFormat is for-each-ref's line per branch, NUL-separated.
const branchFormat = "%(refname:lstrip=2)%00%(upstream:lstrip=2)%00%(upstream:remotename)%00%(upstream:remoteref)%00%(upstream:track,nobracket)%00%(objectname)%00%(committerdate:iso-strict)%00%(worktreepath)"

// Branches lists the repository's local branches, by name.
func (m *Manager) Branches(ctx context.Context) ([]Branch, error) {
	base, err := m.baseDir()
	if err != nil {
		return nil, err
	}
	out, err := m.git(ctx, base, "for-each-ref", "--format="+branchFormat, "refs/heads/")
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	merged := map[string]bool{}
	if target := m.mergeTarget(ctx, base); target != "" {
		mo, err := m.git(ctx, base, "for-each-ref", "--format=%(refname:lstrip=2)", "--merged="+target, "refs/heads/")
		if err != nil {
			return nil, fmt.Errorf("git for-each-ref --merged: %w", err)
		}
		for _, n := range strings.Split(strings.TrimSpace(mo), "\n") {
			merged[n] = n != ""
		}
	}
	bs := []Branch{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 8 || f[0] == "" {
			continue
		}
		b := Branch{Name: f[0], Upstream: f[1], Remote: f[2], RemoteRef: f[3], Commit: f[5], Dir: f[7], Merged: merged[f[0]]}
		if t, err := time.Parse(time.RFC3339, f[6]); err == nil {
			b.Date = t.UTC().Format(time.RFC3339)
		}
		b.Gone, b.Ahead, b.Behind = parseTrack(f[4])
		bs = append(bs, b)
	}
	slices.SortFunc(bs, func(a, b Branch) int { return strings.Compare(a.Name, b.Name) })
	return bs, nil
}

// parseTrack reads %(upstream:track,nobracket): "gone", "ahead 1",
// "behind 2", "ahead 1, behind 2" or "".
func parseTrack(s string) (gone bool, ahead, behind int) {
	if s == "gone" {
		return true, 0, 0
	}
	for _, part := range strings.Split(s, ", ") {
		k, v, ok := strings.Cut(part, " ")
		n, err := strconv.Atoi(v)
		if !ok || err != nil {
			continue
		}
		switch k {
		case "ahead":
			ahead = n
		case "behind":
			behind = n
		}
	}
	return gone, ahead, behind
}

// mergeTarget is what a merged branch is reachable from: origin's base
// branch when it has been fetched, else the local one; "" for neither.
func (m *Manager) mergeTarget(ctx context.Context, base string) string {
	for _, ref := range []string{"refs/remotes/origin/" + m.Base, "refs/heads/" + m.Base} {
		if ok, err := m.hasRef(ctx, base, ref); err == nil && ok {
			return ref
		}
	}
	return ""
}

// BaseBranch is the project's base branch ([project] base_branch).
func (m *Manager) BaseBranch() string { return m.Base }

// FetchAll fetches origin with --prune, so a branch whose remote branch
// went reads as gone. It can't prompt, and uses the ssh the user chose
// (sys.GitNetEnv).
func (m *Manager) FetchAll(ctx context.Context, out io.Writer) error {
	base, err := m.baseDir()
	if err != nil {
		return err
	}
	env, err := sys.GitNetEnv(ctx, m.R, os.Environ())
	if err != nil {
		return err
	}
	c := m.gitCmd(fetchTimeout, base, append(slices.Clone(sys.GitProtocols), "fetch", "--prune", "origin")...)
	c.Env = env
	if out != nil {
		c.Stdout, c.Stderr = out, out
	}
	if _, err := m.R.Run(ctx, c); err != nil {
		return fmt.Errorf("git fetch --prune origin: %w", err)
	}
	return nil
}

// LastFetch is when origin was last fetched (FETCH_HEAD's time); zero when
// never. Only the time of a file in the container-writable .git is read.
func (m *Manager) LastFetch() time.Time {
	base, err := m.baseDir()
	if err != nil {
		return time.Time{}
	}
	fi, err := os.Lstat(filepath.Join(base, ".git", "FETCH_HEAD"))
	if err != nil || !fi.Mode().IsRegular() {
		return time.Time{}
	}
	return fi.ModTime()
}

// DeleteBranch deletes local branch name: git branch -d, or -D with force
// (a branch squash-merged on GitHub is never merged to git).
func (m *Manager) DeleteBranch(ctx context.Context, name string, force bool) error {
	if err := sys.GitArg("branch", name); err != nil {
		return err
	}
	base, err := m.baseDir()
	if err != nil {
		return err
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	if res, err := m.gitRun(ctx, 0, base, "branch", flag, "--", name); err != nil {
		return fmt.Errorf("git branch %s %s: %s", flag, name, gitMsg(res, err))
	}
	return nil
}

// DeleteRemote deletes b's upstream branch on origin (git push origin
// --delete): only origin, and only the branch b tracks.
func (m *Manager) DeleteRemote(ctx context.Context, b Branch) error {
	ref, ok := strings.CutPrefix(b.RemoteRef, "refs/heads/")
	if b.Remote != "origin" || !ok || ref == "" {
		return fmt.Errorf("%s: its upstream %q is not a branch on origin", b.Name, b.Upstream)
	}
	if err := sys.GitArg("branch", ref); err != nil {
		return err
	}
	base, err := m.baseDir()
	if err != nil {
		return err
	}
	env, err := sys.GitNetEnv(ctx, m.R, os.Environ())
	if err != nil {
		return err
	}
	c := m.gitCmd(fetchTimeout, base, append(slices.Clone(sys.GitProtocols), "push", "--no-verify", "origin", "--delete", "refs/heads/"+ref)...)
	c.Env = env
	if res, err := m.R.Run(ctx, c); err != nil {
		return fmt.Errorf("git push origin --delete %s: %s", ref, gitMsg(res, err))
	}
	return nil
}

// FastForwardRef moves b, checked out nowhere, to its upstream as last
// fetched: git update-ref with b's old commit, so a branch that moved since
// it was read is left alone. The caller checked it is behind and not
// ahead, a fast forward; no file changes.
func (m *Manager) FastForwardRef(ctx context.Context, b Branch) error {
	if b.Upstream == "" || b.Gone {
		return fmt.Errorf("%s has no upstream to pull", b.Name)
	}
	if err := sys.GitArg("branch", b.Name); err != nil {
		return err
	}
	base, err := m.baseDir()
	if err != nil {
		return err
	}
	if res, err := m.gitRun(ctx, 0, base, "update-ref", "-m", "railswitch branch pull", "refs/heads/"+b.Name, "refs/remotes/"+b.Upstream, b.Commit); err != nil {
		return fmt.Errorf("git update-ref %s: %s", b.Name, gitMsg(res, err))
	}
	return nil
}

// GitDir is the repository's common git dir: the base worktree's .git.
func (m *Manager) GitDir() (string, error) {
	base, err := m.baseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, ".git"), nil
}

// gitMsg is a failed git call's stderr, else its error.
func gitMsg(res sys.Result, err error) string {
	if msg := strings.TrimSpace(string(res.Stderr)); msg != "" && errors.Is(err, sys.ErrExit) {
		return msg
	}
	return err.Error()
}
