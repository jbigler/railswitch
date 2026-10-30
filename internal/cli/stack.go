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
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
)

const stackUsage = "usage: railswitch [--json] stack ls | railswitch stack switch <branch> | railswitch stack push|up|down | railswitch stack rebase [--new-term] [--continue | --abort]"

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
	case "push", "up", "down":
		return cmdStackMove(ctx, e, args[0], args[1:])
	case "rebase":
		return cmdStackRebase(ctx, e, args[1:])
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
	if doc.Available && len(m.Inputs.Worktrees) > 0 {
		var in *render.Instance
		if in, err = m.Instance(m.Inputs.Worktrees[0]); err != nil {
			return e.fail(err)
		}
		sh, err := gitshadow.New(ctx, e.run, in.Values["base.git"])
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
			}
		}
		doc.Worktrees = append(doc.Worktrees, r)
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

// stackText is a row's stack in a few words: "2/3 on main", with "restack"
// when a branch needs a rebase.
func stackText(r stackRow) string {
	switch {
	case r.Error != "":
		return "? " + r.Error
	case r.Stack == nil:
		return "-"
	}
	pos, n, rebase := 0, 0, false
	for _, b := range r.Stack.Branches {
		if b.Merged && !b.Current {
			continue
		}
		n++
		if b.Current {
			pos = n
		}
		rebase = rebase || b.NeedsRebase
	}
	parts := []string{fmt.Sprintf("%d/%d on %s", pos, n, r.Stack.Trunk)}
	if rebase {
		parts = append(parts, "restack needed")
	}
	return strings.Join(parts, ", ")
}

// stackTimeouts bound the gh stack actions: push reaches GitHub; up, down
// and a switch only check out a branch.
var stackTimeouts = map[string]time.Duration{"push": 10 * time.Minute, "up": 2 * time.Minute, "down": 2 * time.Minute, "switch": 2 * time.Minute}

// stackRun is a stack action's worktree and the private, writable stand-in
// for its repository's common dir that every git and gh call goes
// through (gitshadow); call done when finished: it carries back what the
// action changed there.
type stackRun struct {
	slug, dir, branch string
	env               []string
	sh                *gitshadow.Shadow
}

// stackStart loads the current worktree (-w, or the working directory's)
// and makes its writable shadow; gh stack must be installed.
func (e *env) stackStart(ctx context.Context, cmd string) (*stackRun, int) {
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return nil, e.fail(err)
	}
	if w.Slug == "" {
		return nil, e.usageErr("stack %s: not in a worktree; run it in <workspace>/<worktree>/ or pass -w <worktree>", cmd)
	}
	m, err := e.model(ctx, w)
	if err != nil {
		return nil, e.fail(err)
	}
	i := slices.IndexFunc(m.Inputs.Worktrees, func(wt render.Worktree) bool { return wt.Slug == w.Slug })
	if i < 0 {
		return nil, e.fail(fmt.Errorf("worktree %s is not registered", w.Slug))
	}
	wt := m.Inputs.Worktrees[i]
	if !prs.StackAvailable(ctx, e.run) {
		return nil, e.fail(errors.New("gh stack is not installed: gh extension install github/gh-stack"))
	}
	in, err := m.Instance(wt)
	if err != nil {
		return nil, e.fail(err)
	}
	sh, err := gitshadow.NewWritable(ctx, e.run, in.Values["base.git"])
	if err != nil {
		return nil, e.fail(err)
	}
	env := sh.Env(os.Environ())
	if !e.interactive() {
		env = append(env, "GH_PROMPT_DISABLED=1")
	}
	return &stackRun{slug: wt.Slug, dir: wt.Dir, branch: wt.Branch, env: env, sh: sh}, 0
}

// done carries the shadow's changes back and removes it: code, or 1 when
// carrying back failed.
func (s *stackRun) done(ctx context.Context, e *env, code int) int {
	defer s.sh.Remove()
	if err := s.sh.Commit(ctx, e.run); err != nil {
		if code == 0 {
			return e.fail(err)
		}
		fmt.Fprintln(e.stderr, "railswitch:", err)
	}
	return code
}

// run runs name args in the worktree through the shadow.
func (s *stackRun) run(ctx context.Context, e *env, timeout time.Duration, name string, args ...string) int {
	return e.runAttached(ctx, s.dir, s.env, timeout, name, args...)
}

// runAttached runs name args in dir with env, its output on ours (and the
// terminal's input when there is one): its exit code.
func (e *env) runAttached(ctx context.Context, dir string, env []string, timeout time.Duration, name string, args ...string) int {
	c := sys.Cmd{Name: name, Args: args, Dir: dir, Env: env, Stdout: e.stdout, Stderr: e.stderr, Timeout: timeout, KillGrace: 5 * time.Second}
	if e.interactive() {
		c.Stdin = e.stdin
	}
	res, err := e.run.Run(ctx, c)
	if err != nil {
		if errors.Is(err, sys.ErrExit) && res.ExitCode > 0 {
			return res.ExitCode
		}
		return e.fail(err)
	}
	return 0
}

// interactive: stdin and stdout are both a terminal.
func (e *env) interactive() bool {
	in, ok := e.stdin.(*os.File)
	out, ok2 := e.stdout.(*os.File)
	return ok && ok2 && isTerminal(in) && isTerminal(out)
}

// railswitch stack switch <branch>: check out another open branch of the
// worktree's stack (git switch --no-guess).
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
	s, code := e.stackStart(ctx, "switch")
	if code != 0 {
		return code
	}
	st, err := prs.ViewStack(ctx, e.run, s.dir, s.env)
	if err != nil {
		return s.done(ctx, e, e.fail(err))
	}
	if st == nil {
		return s.done(ctx, e, e.fail(fmt.Errorf("%s: branch %s is in no gh stack", s.slug, safefile.Printable(s.branch))))
	}
	if !slices.ContainsFunc(st.Branches, func(b prs.StackBranch) bool { return b.Name == branch && !b.Merged && !b.Current }) {
		return s.done(ctx, e, e.usageErr("%s is not another open branch of %s's stack", safefile.Printable(branch), s.slug))
	}
	return s.done(ctx, e, s.run(ctx, e, stackTimeouts["switch"], "git", "switch", "--no-guess", branch))
}

// railswitch stack push|up|down: gh stack's own, in the worktree.
func cmdStackMove(ctx context.Context, e *env, verb string, args []string) int {
	fs := e.flags("stack " + verb)
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("stack %s takes no arguments", verb)
	}
	s, code := e.stackStart(ctx, verb)
	if code != 0 {
		return code
	}
	return s.done(ctx, e, s.run(ctx, e, stackTimeouts[verb], "gh", "stack", verb))
}

// railswitch stack rebase [--new-term] [--continue | --abort]: gh stack
// rebase in the worktree, which stops at a conflict. --new-term re-runs
// it in a new terminal with --shell-on-stop: when it stops, the user's
// shell starts there to resolve the conflict.
//
// gh stack rebase runs git in each worktree without GIT_COMMON_DIR, so it
// can't go through the shadow: it runs on the repository itself, hooks and
// fsmonitor off, and only while git-guard finds nothing but hook changes
// (no config key, worktree link or submodule a container could have
// planted).
func cmdStackRebase(ctx context.Context, e *env, args []string) int {
	fs := e.flags("stack rebase")
	newTerm := fs.Bool("new-term", false, "run it in a new terminal tab or window")
	cont := fs.Bool("continue", false, "continue after resolving a conflict")
	abort := fs.Bool("abort", false, "abort the rebase")
	shellOnStop := fs.Bool("shell-on-stop", false, "start your shell in the worktree when the rebase stops")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 || *cont && *abort {
		return e.usageErr("usage: railswitch stack rebase [--new-term] [--continue | --abort]")
	}
	var flags []string
	switch {
	case *cont:
		flags = []string{"--continue"}
	case *abort:
		flags = []string{"--abort"}
	}
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return e.fail(err)
	}
	if w.Slug == "" {
		return e.usageErr("stack rebase: not in a worktree; run it in <workspace>/<worktree>/ or pass -w <worktree>")
	}
	dir := filepath.Join(w.Root, w.Slug)
	if *newTerm {
		exe, err := executable()
		if err != nil {
			return e.fail(err)
		}
		argv := append([]string{exe, "-C", w.Root, "-w", w.Slug, "stack", "rebase", "--shell-on-stop"}, flags...)
		return e.launchIn(ctx, w.Slug+" restack", dir, argv)
	}
	if !prs.StackAvailable(ctx, e.run) {
		return e.fail(errors.New("gh stack is not installed: gh extension install github/gh-stack"))
	}
	findings, err := w.GitGuard(ctx, e.run)
	if err != nil {
		return e.fail(err)
	}
	var bad []string
	for _, f := range findings {
		if !strings.HasPrefix(f.Kind, "hook-") { // hooks don't run
			bad = append(bad, "  "+f.String())
		}
	}
	if len(bad) > 0 {
		return e.fail(fmt.Errorf("stack rebase runs git on the repository itself; refused while git-guard finds (railswitch doctor):\n%s", strings.Join(bad, "\n")))
	}
	env := gitshadow.Defused(os.Environ())
	if !e.interactive() {
		env = append(env, "GH_PROMPT_DISABLED=1")
	}
	code := e.runAttached(ctx, dir, env, sys.NoTimeout, "gh", append([]string{"stack", "rebase"}, flags...)...)
	if code != 0 && *shellOnStop {
		fmt.Fprintln(e.stderr, "\nThe rebase stopped. Resolve it here, then run: railswitch stack rebase --continue (or --abort). Exit the shell to close.")
		return runShell(ctx, e, dir)
	}
	return code
}
