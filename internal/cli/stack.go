package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jbigler/railswitch/internal/gitshadow"
	"github.com/jbigler/railswitch/internal/prs"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
)

const stackUsage = "usage: railswitch [--json] stack ls | railswitch stack switch <branch>"

// stackDoc is `stack ls --json` (docs/json.md).
type stackDoc struct {
	Available bool       `json:"available"` // gh has the gh-stack extension
	Worktrees []stackRow `json:"worktrees"`
}

// stackRow is one worktree's stack: nil when its branch is in none (or it
// has no branch); Error when gh stack couldn't say.
type stackRow struct {
	Slug   string     `json:"slug"`
	Branch string     `json:"branch"`
	Stack  *prs.Stack `json:"stack"`
	Error  string     `json:"error"`
}

// railswitch stack ls: each worktree of the project (-C), in registry
// order, with its branch's stack as GitHub's gh-stack extension sees it.
// Without the extension, available is false and no worktree is asked.
func cmdStack(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", stackUsage)
	}
	switch args[0] {
	case "ls":
	case "switch":
		return cmdStackSwitch(ctx, e, args[1:])
	default:
		return e.usageErr("%s", stackUsage)
	}
	fs := e.flags("stack ls")
	if code, ok := parse(fs, args[1:]); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("%s", stackUsage)
	}
	w, err := workspace.Load(ctx, e.dir, "")
	if err != nil {
		return e.fail(err)
	}
	m, err := e.model(ctx, w)
	if err != nil {
		return e.fail(err)
	}
	doc := stackDoc{Available: prs.StackAvailable(ctx, e.run), Worktrees: []stackRow{}}
	// gh-stack writes into the repository's common dir and runs git with
	// its config; both are the containers' to write: it sees the repository
	// only through a private shadow, or runs not at all.
	var env []string
	var shadowErr error
	var gitDir string
	if doc.Available && len(m.Inputs.Worktrees) > 0 {
		var in *render.Instance
		if in, err = m.Instance(m.Inputs.Worktrees[0]); err != nil {
			return e.fail(err)
		}
		gitDir = in.Values["base.git"]
		sh, err := gitshadow.New(ctx, e.run, gitDir)
		if err == nil {
			defer sh.Remove()
			env = sh.Env(os.Environ())
		}
		shadowErr = err
	}
	for _, wt := range m.Inputs.Worktrees {
		r := stackRow{Slug: wt.Slug, Branch: wt.Branch}
		switch {
		case !doc.Available || wt.Branch == "":
		case shadowErr != nil:
			r.Error = shadowErr.Error()
		default:
			if r.Stack, err = prs.ViewStack(ctx, e.run, wt.Dir, env); err != nil {
				r.Error = err.Error()
			} else if r.Stack != nil {
				r.Stack.Number = prs.StackNumber(gitDir, r.Stack.Current)
			}
		}
		doc.Worktrees = append(doc.Worktrees, r)
	}
	// gh-stack knows only open, merged and queued: draft, checks and
	// review come from GitHub. Without them the stacks still show.
	var stacks []*prs.Stack
	for _, r := range doc.Worktrees {
		if r.Stack != nil {
			stacks = append(stacks, r.Stack)
		}
	}
	if repo, err := prs.Repo(w.Project.Project.Repo); err == nil && len(stacks) > 0 {
		if err := prs.Detail(ctx, e.run, repo, stacks); err != nil {
			fmt.Fprintf(e.stderr, "warning: stack PRs' review and checks: %v\n", err)
		}
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	if !doc.Available {
		fmt.Fprintln(e.stdout, "gh stack is not installed: gh extension install github/gh-stack")
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "WORKTREE\tBRANCH\tSTACK")
	for _, r := range doc.Worktrees {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Slug, safefile.Printable(r.Branch), safefile.Printable(stackText(r)))
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	return 0
}

// stackText is a row's stack in gh stack view's words: "#10350, base
// master, 22 branches (8 merged)", with "restack needed" when a branch
// needs a rebase.
func stackText(r stackRow) string {
	switch {
	case r.Error != "":
		return "? " + r.Error
	case r.Stack == nil:
		return "-"
	}
	merged, rebase := 0, false
	for _, b := range r.Stack.Branches {
		if b.Merged {
			merged++
		}
		rebase = rebase || b.NeedsRebase
	}
	var parts []string
	if r.Stack.Number > 0 {
		parts = append(parts, fmt.Sprintf("#%d", r.Stack.Number))
	}
	parts = append(parts, "base "+r.Stack.Trunk, fmt.Sprintf("%d branches (%d merged)", len(r.Stack.Branches), merged))
	if rebase {
		parts = append(parts, "restack needed")
	}
	return strings.Join(parts, ", ")
}

// railswitch stack switch <branch>: check branch out in the worktree (-w,
// or the working directory's) with git switch --no-guess, the dashboard's
// click on a branch of a worktree's stack. Git runs through a private
// shadow of the repository's common dir (gitshadow), as gh stack does for
// stack ls: no hook, filter or fsmonitor a container wrote runs. A switch
// writes only the worktree's own HEAD and index, which are not in it.
func cmdStackSwitch(ctx context.Context, e *env, args []string) int {
	fs := e.flags("stack switch")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 {
		return e.usageErr("usage: railswitch stack switch <branch>")
	}
	branch := fs.Arg(0)
	if err := sys.GitArg("branch", branch); err != nil {
		return e.usageErr("%v", err)
	}
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return e.fail(err)
	}
	if w.Slug == "" {
		return e.usageErr("stack switch: not in a worktree; run it in <workspace>/<worktree>/ or pass -w <worktree>")
	}
	m, err := e.model(ctx, w)
	if err != nil {
		return e.fail(err)
	}
	i := slices.IndexFunc(m.Inputs.Worktrees, func(wt render.Worktree) bool { return wt.Slug == w.Slug })
	if i < 0 {
		return e.fail(fmt.Errorf("worktree %s is not registered", w.Slug))
	}
	in, err := m.Instance(m.Inputs.Worktrees[i])
	if err != nil {
		return e.fail(err)
	}
	sh, err := gitshadow.New(ctx, e.run, in.Values["base.git"])
	if err != nil {
		return e.fail(err)
	}
	defer sh.Remove()
	c := sys.Cmd{Name: "git", Args: []string{"switch", "--no-guess", branch}, Dir: m.Inputs.Worktrees[i].Dir, Env: sh.Env(os.Environ()), Stdout: e.stdout, Stderr: e.stderr, Timeout: 2 * time.Minute}
	if res, err := e.run.Run(ctx, c); err != nil {
		if errors.Is(err, sys.ErrExit) && res.ExitCode > 0 {
			return res.ExitCode
		}
		return e.fail(err)
	}
	return 0
}
