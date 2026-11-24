package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

// branchWS is gitWS with a bare origin and branches in every state:
// done (merged, its remote branch deleted: gone), wip (unmerged, never
// pushed), behind (origin has one more commit), squash (unmerged to git,
// its PR merged on GitHub), and feat (the feat worktree's, behind origin).
func branchWS(t *testing.T) (ws, master, origin string) {
	t.Helper()
	ws, _ = gitWS(t)
	master = filepath.Join(ws, "worktrees", "master")
	origin = filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, master, "clone", "-q", "--bare", master, origin)
	gitIn(t, master, "remote", "add", "origin", origin)
	gitIn(t, master, "fetch", "-q", "origin")
	gitIn(t, master, "branch", "-q", "--set-upstream-to=origin/master", "master")
	for _, b := range []string{"done", "behind"} {
		gitIn(t, master, "branch", "-q", b)
		gitIn(t, master, "push", "-q", "-u", "origin", b)
	}
	gitIn(t, master, "push", "-q", "origin", "--delete", "done")
	for _, b := range []string{"wip", "squash"} {
		gitIn(t, master, "switch", "-q", "-c", b)
		writeFile(t, filepath.Join(master, b+".txt"), b)
		gitIn(t, master, "add", b+".txt")
		gitIn(t, master, "commit", "-q", "-m", b)
		gitIn(t, master, "switch", "-q", "master")
	}
	feat := filepath.Join(ws, "worktrees", "feat")
	gitIn(t, feat, "push", "-q", "-u", "origin", "feat")
	// Someone else pushes to behind and feat.
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, master, "clone", "-q", origin, other)
	for _, b := range []string{"behind", "feat"} {
		gitIn(t, other, "switch", "-q", b)
		writeFile(t, filepath.Join(other, b+"-more.txt"), "more")
		gitIn(t, other, "add", ".")
		gitIn(t, other, "commit", "-q", "-m", "more on "+b)
		gitIn(t, other, "push", "-q", "origin", b)
	}
	return ws, master, origin
}

func branchFake(t *testing.T, master string) *systest.Fake {
	t.Helper()
	res, err := (sys.Exec{}).Run(t.Context(), sys.Cmd{Name: "git", Args: []string{"-C", master, "rev-parse", "squash"}})
	if err != nil {
		t.Fatal(err)
	}
	squashed := `[{"number": 9, "title": "Squash", "url": "https://github.com/o/n/pull/9", "headRefName": "squash", "headRefOid": "` + strings.TrimSpace(string(res.Stdout)) + `", "isDraft": false, "state": "MERGED"}]`
	f := fakeHost()
	f.Passthrough = []string{"git"}
	f.On([]string{"gh", "pr", "list"}, sys.Result{Stdout: []byte("[]")}, nil)
	f.On([]string{"gh", "pr", "list", "--repo", "[ORG]/[REPO]", "--state", "all"}, sys.Result{Stdout: []byte(squashed)}, nil)
	f.On([]string{"gh", "pr", "list", "--repo", "[ORG]/[REPO]", "--head=squash"}, sys.Result{Stdout: []byte(squashed)}, nil)
	f.On([]string{"gh", "pr", "list", "--repo", "[ORG]/[REPO]", "--head=behind"}, sys.Result{Stdout: []byte(`[{"number": 11, "headRefName": "behind", "state": "OPEN"}]`)}, nil)
	return f
}

func readBranches(t *testing.T, f *systest.Fake, ws string) map[string]branchRow {
	t.Helper()
	code, out, errs := runWith(t, f, "-C", ws, "--json", "branch", "ls")
	if code != 0 {
		t.Fatalf("branch ls: exit %d: %s", code, errs)
	}
	var doc branchDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if doc.Base != "master" || doc.Fetched == "" {
		t.Errorf("base %q, fetched %q", doc.Base, doc.Fetched)
	}
	m := map[string]branchRow{}
	for _, b := range doc.Branches {
		m[b.Name] = b
	}
	return m
}

// D227: branch ls says where each branch is checked out, its upstream
// and why it could go; a squash-merged branch counts as merged by its PR.
func TestBranchLs(t *testing.T) {
	ws, master, _ := branchWS(t)
	f := branchFake(t, master)
	gitIn(t, master, "fetch", "-q", "--prune", "origin")
	bs := readBranches(t, f, ws)
	if len(bs) != 6 {
		t.Fatalf("branches %v", bs)
	}
	check := func(name string, ok bool) {
		t.Helper()
		if !ok {
			t.Errorf("%s: %+v", name, bs[name])
		}
	}
	m, d, w, b, s, ft := bs["master"], bs["done"], bs["wip"], bs["behind"], bs["squash"], bs["feat"]
	check("master", m.Base && m.Worktree == "master" && m.CheckedOut && !m.CouldGo && len(m.Reasons) == 0 && m.Upstream == "origin/master")
	check("done", d.Gone && d.Merged && slices.Equal(d.Reasons, []string{"upstream-gone", "merged"}) && d.CouldGo && d.Worktree == "")
	check("wip", !w.Merged && w.Upstream == "" && len(w.Reasons) == 0 && !w.CouldGo)
	check("behind", b.Behind == 1 && b.Ahead == 0 && b.Merged && b.CouldGo)
	check("squash", s.Merged && s.PR != nil && s.PR.Number == 9 && s.PR.State == "merged" && slices.Equal(s.Reasons, []string{"pr-merged"}) && s.CouldGo)
	check("feat", ft.Worktree == "feat" && ft.Behind == 1 && !ft.CouldGo)

	code, out, _ := runWith(t, f, "-C", ws, "branch", "ls")
	if code != 0 || !strings.Contains(out, "master (base)") || !strings.Contains(out, "gone") || !strings.Contains(out, "upstream-gone,merged") {
		t.Errorf("text: %s", out)
	}
}

// D227: rm refuses the base branch, a checked-out one and an unmerged one
// without --force; deletes a merged one, a squash-merged one (its PR) and
// with --remote also origin's branch unless an open PR has it.
func TestBranchRm(t *testing.T) {
	ws, master, origin := branchWS(t)
	f := branchFake(t, master)
	code, _, errs := runWith(t, f, "-C", ws, "branch", "rm", "master", "feat", "wip", "nope")
	if code != 1 || !strings.Contains(errs, "master: it is the project's base branch") ||
		!strings.Contains(errs, "feat: it is checked out in worktree feat: remove that with railswitch wt rm feat") ||
		!strings.Contains(errs, "wip: it is not merged: --force deletes it anyway") || !strings.Contains(errs, "nope: no such local branch") {
		t.Errorf("exit %d: %s", code, errs)
	}
	code, out, errs := runWith(t, f, "-C", ws, "branch", "rm", "done", "squash", "--force", "wip")
	if code != 0 || !strings.Contains(out, "deleted done") || !strings.Contains(out, "deleted squash") || !strings.Contains(out, "deleted wip") {
		t.Errorf("exit %d: %s %s", code, out, errs)
	}
	// behind: an open PR keeps origin's branch and the local one.
	code, _, errs = runWith(t, f, "-C", ws, "branch", "rm", "--remote", "behind")
	if code != 1 || !strings.Contains(errs, "behind: PR #11 is open on it; the local branch stays") {
		t.Errorf("exit %d: %s", code, errs)
	}
	f.On([]string{"gh", "pr", "list", "--repo", "[ORG]/[REPO]", "--head=behind"}, sys.Result{Stdout: []byte("[]")}, nil)
	code, out, errs = runWith(t, f, "-C", ws, "branch", "rm", "--remote", "behind")
	if code != 0 || !strings.Contains(out, "deleted behind on origin") || !strings.Contains(out, "deleted behind") {
		t.Errorf("exit %d: %s %s", code, out, errs)
	}
	refs := gitOut(t, master, "for-each-ref", "--format=%(refname:lstrip=2)", "refs/heads/")
	if refs != "feat\nmaster\n" {
		t.Errorf("local branches left:\n%s", refs)
	}
	if remote := gitOut(t, origin, "for-each-ref", "--format=%(refname:lstrip=2)", "refs/heads/"); strings.Contains(remote, "behind") {
		t.Errorf("origin still has behind:\n%s", remote)
	}
	for _, c := range f.Calls {
		if c[0] == "git" && slices.Contains(c, "push") && !slices.Contains(c, "--no-verify") {
			t.Errorf("push without --no-verify: %q", c)
		}
	}
}

// D227: pull fetches, then fast-forwards a branch checked out nowhere by
// moving it and the feat worktree's with merge --ff-only there; a
// diverged branch is refused.
func TestBranchPull(t *testing.T) {
	ws, master, _ := branchWS(t)
	f := branchFake(t, master)
	gitIn(t, master, "switch", "-q", "-c", "ahead", "--track", "origin/behind")
	writeFile(t, filepath.Join(master, "mine.txt"), "mine")
	gitIn(t, master, "add", "mine.txt")
	gitIn(t, master, "commit", "-q", "-m", "mine")
	gitIn(t, master, "switch", "-q", "master")
	gitIn(t, master, "reset", "-q", "--hard", "HEAD") // nothing else changed
	code, out, errs := runWith(t, f, "-C", ws, "branch", "pull", "behind", "feat", "ahead", "wip")
	if code != 1 || !strings.Contains(out, "pulled behind: 1 new commit(s)") || !strings.Contains(out, "pulled feat: 1 new commit(s)") ||
		!strings.Contains(errs, "ahead: it has diverged (1 ahead, 1 behind): rebase it in a terminal") || !strings.Contains(errs, "wip: it has no upstream") {
		t.Errorf("exit %d: %s %s", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(ws, "worktrees", "feat", "feat-more.txt")); err != nil {
		t.Errorf("feat's worktree didn't get the new file: %v", err)
	}
	if st := gitOut(t, filepath.Join(ws, "worktrees", "feat"), "status", "--porcelain"); st != "" {
		t.Errorf("feat is not clean after the pull:\n%s", st)
	}
	bs := readBranches(t, f, ws)
	if bs["behind"].Behind != 0 || bs["feat"].Behind != 0 {
		t.Errorf("still behind: %+v %+v", bs["behind"], bs["feat"])
	}
	code, out, _ = runWith(t, f, "-C", ws, "branch", "pull", "behind")
	if code != 0 || !strings.Contains(out, "behind is up to date") {
		t.Errorf("exit %d: %s", code, out)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	res, err := (sys.Exec{}).Run(t.Context(), sys.Cmd{Name: "git", Args: append([]string{"-C", dir}, args...)})
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, res.Stderr)
	}
	return string(res.Stdout)
}
