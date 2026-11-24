package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jbigler/railswitch/internal/gitshadow"
	"github.com/jbigler/railswitch/internal/prs"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/worktree"
)

const branchUsage = "usage: railswitch [--json] branch ls | branch fetch | branch rm [--force] [--remote] <branch>... | branch pull <branch>..."

// branchDoc is `branch ls --json` (docs/json.md).
type branchDoc struct {
	Base     string      `json:"base"`    // the project's base branch
	Fetched  string      `json:"fetched"` // when origin was last fetched, RFC 3339; "" never
	Branches []branchRow `json:"branches"`
	Warnings []string    `json:"warnings"`
}

// branchRow is one local branch with what railswitch knows of it.
type branchRow struct {
	worktree.Branch
	Worktree   string     `json:"worktree"`    // the registered worktree it is checked out in; "" none
	CheckedOut bool       `json:"checked_out"` // checked out in any git worktree, registered or not
	Base       bool       `json:"base"`
	GitMerged  bool       `json:"-"` // Merged as git sees it (Merged also counts a merged PR)
	PR         *branchPR  `json:"pr"`
	Stack      *stackMark `json:"stack"` // in a gh stack; null none
	Reasons    []string   `json:"reasons"`
	CouldGo    bool       `json:"could_go"`
}

type branchPR struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	URL    string `json:"url"`
}

type stackMark struct {
	Number int `json:"number"` // GitHub's stack number; 0 before gh stack submit
}

// railswitch branch ...: the project's local branches (D227).
func cmdBranch(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", branchUsage)
	}
	switch args[0] {
	case "ls":
		return cmdBranchLs(ctx, e, args[1:])
	case "fetch":
		return cmdBranchFetch(ctx, e, args[1:])
	case "rm":
		return cmdBranchRm(ctx, e, args[1:])
	case "pull":
		return cmdBranchPull(ctx, e, args[1:])
	}
	return e.usageErr("%s", branchUsage)
}

// railswitch branch ls: every local branch of the project's repository,
// with the worktree it is checked out in, its upstream (gone after a fetch
// --prune removed it), ahead and behind, its tip's date, whether it is
// merged into the base branch or its PR merged, its PR and its gh stack;
// reasons say why it could go. It reads; it fetches nothing.
func cmdBranchLs(ctx context.Context, e *env, args []string) int {
	fs := e.flags("branch ls")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("%s", branchUsage)
	}
	doc, _, err := e.branches(ctx, true)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "BRANCH\tWORKTREE\tPR\tUPSTREAM\tLAST COMMIT\tCOULD GO")
	for _, b := range doc.Branches {
		name := safefile.Printable(b.Name)
		if b.Base {
			name += " (base)"
		}
		if b.Stack != nil {
			name += " ≡"
		}
		pr := "-"
		if b.PR != nil {
			pr = fmt.Sprintf("#%d %s", b.PR.Number, b.PR.State)
		}
		up := "-"
		switch {
		case b.Gone:
			up = "gone"
		case b.Upstream != "":
			up = safefile.Printable(b.Upstream)
			if b.Ahead > 0 || b.Behind > 0 {
				up += fmt.Sprintf(" +%d -%d", b.Ahead, b.Behind)
			}
		}
		wt := b.Worktree
		if wt == "" && b.CheckedOut {
			wt = "(elsewhere)"
		}
		if wt == "" {
			wt = "-"
		}
		date := b.Date
		if len(date) >= 10 {
			date = date[:10]
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", name, wt, pr, up, date, strings.Join(b.Reasons, ","))
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	if doc.Fetched == "" {
		fmt.Fprintln(e.stdout, "origin was never fetched: railswitch branch fetch")
	}
	for _, w := range doc.Warnings {
		fmt.Fprintf(e.stderr, "warning: %s\n", w)
	}
	return 0
}

// branches reads the project's branches; withPRs asks GitHub for PRs (one
// list of the open ones and the newest; no PR state is a warning).
func (e *env) branches(ctx context.Context, withPRs bool) (branchDoc, *worktree.Manager, error) {
	doc := branchDoc{Branches: []branchRow{}, Warnings: []string{}}
	mgr, err := e.manager(ctx)
	if err != nil {
		return doc, nil, err
	}
	doc.Base = mgr.BaseBranch()
	if t := mgr.LastFetch(); !t.IsZero() {
		doc.Fetched = t.UTC().Format(time.RFC3339)
	}
	bs, err := mgr.Branches(ctx)
	if err != nil {
		return doc, nil, err
	}
	rows, err := mgr.List(ctx)
	if err != nil {
		return doc, nil, err
	}
	gitDir, err := mgr.GitDir()
	if err != nil {
		return doc, nil, err
	}
	stacks := prs.StackBranches(gitDir)
	var list []prs.PR
	if withPRs {
		if repo, err := prs.Repo(mgr.Repo); err != nil {
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("no PR states: %v", err))
		} else if list, err = prs.Recent(ctx, e.run, repo); err != nil {
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("no PR states: %v", err))
		}
	}
	for _, b := range bs {
		r := branchRow{Branch: b, Base: b.Name == doc.Base, GitMerged: b.Merged, CheckedOut: b.Dir != "", Reasons: []string{}}
		if b.Dir != "" {
			if i := slices.IndexFunc(rows, func(w worktree.Row) bool { return sameDir(w.Dir, b.Dir) }); i >= 0 {
				r.Worktree = rows[i].Slug
			}
		}
		if p := prs.ForBranch(list, b.Name, b.Commit); p != nil {
			r.PR = &branchPR{Number: p.Number, State: p.State, URL: p.URL}
			r.Merged = r.Merged || p.State == "merged"
		}
		if n, ok := stacks[b.Name]; ok {
			r.Stack = &stackMark{Number: n}
		}
		if !r.Base {
			if b.Gone {
				r.Reasons = append(r.Reasons, "upstream-gone")
			}
			if r.PR != nil && (r.PR.State == "merged" || r.PR.State == "closed") {
				r.Reasons = append(r.Reasons, "pr-"+r.PR.State)
			}
			if b.Merged {
				r.Reasons = append(r.Reasons, "merged")
			}
		}
		r.CouldGo = len(r.Reasons) > 0 && !r.CheckedOut && !r.Base
		doc.Branches = append(doc.Branches, r)
	}
	slices.SortStableFunc(doc.Branches, func(a, b branchRow) int { return strings.Compare(b.Date, a.Date) })
	return doc, mgr, nil
}

// sameDir reports whether a and b name one directory.
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// railswitch branch fetch: git fetch --prune origin, for the whole
// repository (every worktree shares it).
func cmdBranchFetch(ctx context.Context, e *env, args []string) int {
	fs := e.flags("branch fetch")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("%s", branchUsage)
	}
	mgr, err := e.manager(ctx)
	if err != nil {
		return e.fail(err)
	}
	if err := mgr.FetchAll(ctx, e.stdout); err != nil {
		return e.fail(err)
	}
	fmt.Fprintln(e.stdout, "fetched origin")
	return 0
}

// railswitch branch rm [--force] [--remote] <branch>...: delete local
// branches, each judged first (rmRefusal); --remote also deletes the
// branch it tracks on origin, first, when its PR is merged or closed or it
// has none. Each named branch is tried; the exit code is 1 when any
// failed.
func cmdBranchRm(ctx context.Context, e *env, args []string) int {
	fs := e.flags("branch rm")
	force := fs.Bool("force", false, "also delete a branch that isn't merged")
	remote := fs.Bool("remote", false, "also delete the branch it tracks on origin")
	names, code, ok := parseMixed(fs, args)
	if !ok {
		return code
	}
	if len(names) == 0 {
		return e.usageErr("%s", branchUsage)
	}
	for _, n := range names {
		if err := sys.GitArg("branch", n); err != nil {
			return e.usageErr("%v", err)
		}
	}
	doc, mgr, err := e.branches(ctx, false)
	if err != nil {
		return e.fail(err)
	}
	repo, repoErr := prs.Repo(mgr.Repo)
	failed := false
	for _, n := range names {
		i := slices.IndexFunc(doc.Branches, func(b branchRow) bool { return b.Name == n })
		if i < 0 {
			fmt.Fprintf(e.stderr, "%s: no such local branch\n", safefile.Printable(n))
			failed = true
			continue
		}
		b := doc.Branches[i]
		// A squash merge on GitHub never merges to git: the PR says.
		if repoErr == nil && !b.Merged {
			if list, err := prs.List(ctx, e.run, repo, []string{n}); err == nil {
				if p := prs.ForBranch(list, n, b.Commit); p != nil {
					b.PR = &branchPR{Number: p.Number, State: p.State, URL: p.URL}
					b.Merged = p.State == "merged"
				}
			}
		}
		if why := rmRefusal(b, *force); why != "" {
			fmt.Fprintf(e.stderr, "%s: %s\n", safefile.Printable(n), why)
			failed = true
			continue
		}
		if *remote {
			if err := e.removeRemote(ctx, mgr, b, repo, repoErr); err != nil {
				fmt.Fprintf(e.stderr, "%s: %v; the local branch stays\n", safefile.Printable(n), err)
				failed = true
				continue
			}
		}
		if err := mgr.DeleteBranch(ctx, n, *force || !b.GitMerged); err != nil {
			fmt.Fprintf(e.stderr, "%s: %v\n", safefile.Printable(n), err)
			failed = true
			continue
		}
		fmt.Fprintf(e.stdout, "deleted %s\n", safefile.Printable(n))
	}
	if failed {
		return 1
	}
	return 0
}

// rmRefusal says why branch b can't go, "" when it can: never the base
// branch or one checked out in a worktree (wt rm removes the worktree), an
// unmerged branch of a gh stack never (its stack would point at a missing
// branch), and an unmerged one only with --force.
func rmRefusal(b branchRow, force bool) string {
	switch {
	case b.Base:
		return "it is the project's base branch"
	case b.Worktree != "":
		return "it is checked out in worktree " + b.Worktree + ": remove that with railswitch wt rm " + b.Worktree
	case b.CheckedOut:
		return "it is checked out in " + safefile.Printable(b.Dir)
	case b.Stack != nil && !b.Merged:
		return "it is an unmerged branch of a gh stack: change the stack with gh stack"
	case !b.Merged && !force:
		return "it is not merged: --force deletes it anyway"
	}
	return ""
}

// removeRemote deletes b's upstream branch on origin, after checking with
// GitHub that no open PR has it as its head (deleting it would close the
// PR). A branch with no upstream, or one already gone, has nothing there.
func (e *env) removeRemote(ctx context.Context, mgr *worktree.Manager, b branchRow, repo string, repoErr error) error {
	if b.Upstream == "" || b.Gone {
		fmt.Fprintf(e.stdout, "%s: nothing on origin to delete\n", safefile.Printable(b.Name))
		return nil
	}
	if repoErr != nil {
		return fmt.Errorf("can't check its PR: %w", repoErr)
	}
	ref := strings.TrimPrefix(b.RemoteRef, "refs/heads/")
	list, err := prs.List(ctx, e.run, repo, []string{ref})
	if err != nil {
		return fmt.Errorf("can't check its PR: %w", err)
	}
	for _, p := range list {
		if p.Branch == ref && (p.State == "open" || p.State == "draft") {
			return fmt.Errorf("PR #%d is open on it", p.Number)
		}
	}
	if err := mgr.DeleteRemote(ctx, b.Branch); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "deleted %s on origin\n", safefile.Printable(ref))
	return nil
}

// railswitch branch pull <branch>...: fetch origin, then fast-forward
// each branch to its upstream: one checked out nowhere by moving the
// branch (no file changes), one checked out in a registered worktree with
// git merge --ff-only there, through a private shadow of the common dir
// (gitshadow: no hook, filter or fsmonitor a container wrote runs). A
// branch that is ahead (diverged) is refused: rebase it in a terminal.
func cmdBranchPull(ctx context.Context, e *env, args []string) int {
	fs := e.flags("branch pull")
	names, code, ok := parseMixed(fs, args)
	if !ok {
		return code
	}
	if len(names) == 0 {
		return e.usageErr("%s", branchUsage)
	}
	for _, n := range names {
		if err := sys.GitArg("branch", n); err != nil {
			return e.usageErr("%v", err)
		}
	}
	mgr, err := e.manager(ctx)
	if err != nil {
		return e.fail(err)
	}
	if err := mgr.FetchAll(ctx, e.stdout); err != nil {
		return e.fail(err)
	}
	doc, _, err := e.branches(ctx, false)
	if err != nil {
		return e.fail(err)
	}
	rows, err := mgr.List(ctx)
	if err != nil {
		return e.fail(err)
	}
	failed := false
	for _, n := range names {
		i := slices.IndexFunc(doc.Branches, func(b branchRow) bool { return b.Name == n })
		if i < 0 {
			fmt.Fprintf(e.stderr, "%s: no such local branch\n", safefile.Printable(n))
			failed = true
			continue
		}
		b := doc.Branches[i]
		if why := pullRefusal(b); why != "" {
			fmt.Fprintf(e.stderr, "%s: %s\n", safefile.Printable(n), why)
			failed = true
			continue
		}
		if b.Behind == 0 {
			fmt.Fprintf(e.stdout, "%s is up to date\n", safefile.Printable(n))
			continue
		}
		if b.Worktree != "" {
			j := slices.IndexFunc(rows, func(w worktree.Row) bool { return w.Slug == b.Worktree })
			err = e.mergeFF(ctx, mgr, rows[j].Dir)
		} else {
			err = mgr.FastForwardRef(ctx, b.Branch)
		}
		if err != nil {
			fmt.Fprintf(e.stderr, "%s: %v\n", safefile.Printable(n), err)
			failed = true
			continue
		}
		fmt.Fprintf(e.stdout, "pulled %s: %d new commit(s)\n", safefile.Printable(n), b.Behind)
	}
	if failed {
		return 1
	}
	return 0
}

// pullRefusal says why b can't be fast-forwarded, "" when it can.
func pullRefusal(b branchRow) string {
	switch {
	case b.Upstream == "":
		return "it has no upstream"
	case b.Gone:
		return "its upstream is gone"
	case b.Remote != "origin":
		return "its upstream is not on origin"
	case b.Ahead > 0 && b.Behind > 0:
		return fmt.Sprintf("it has diverged (%d ahead, %d behind): rebase it in a terminal", b.Ahead, b.Behind)
	case b.Ahead > 0:
		return fmt.Sprintf("it is %d commit(s) ahead of its upstream: nothing to pull", b.Ahead)
	case b.CheckedOut && b.Worktree == "":
		return "it is checked out in " + safefile.Printable(b.Dir) + ", not a worktree of this project"
	}
	return ""
}

// mergeFF runs git merge --ff-only @{upstream} in worktree dir through a
// shadow of the common dir. Git refuses when uncommitted changes would be
// overwritten, and changes nothing.
func (e *env) mergeFF(ctx context.Context, mgr *worktree.Manager, dir string) error {
	gitDir, err := mgr.GitDir()
	if err != nil {
		return err
	}
	sh, err := gitshadow.New(ctx, e.run, gitDir)
	if err != nil {
		return err
	}
	defer sh.Remove()
	c := sys.Cmd{Name: "git", Args: []string{"merge", "--ff-only", "@{upstream}"}, Dir: dir, Env: sh.Env(os.Environ()), Timeout: 2 * time.Minute}
	res, err := e.run.Run(ctx, c)
	if err != nil {
		if msg := strings.TrimSpace(string(res.Stderr)); msg != "" && errors.Is(err, sys.ErrExit) {
			return errors.New(safefile.Printable(msg))
		}
		return err
	}
	return nil
}
