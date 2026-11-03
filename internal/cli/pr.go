package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/jbigler/railswitch/internal/agentqueue"
	"github.com/jbigler/railswitch/internal/lifecycle"
	"github.com/jbigler/railswitch/internal/prs"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
)

const prUsage = "usage: railswitch [--json] pr ls [--all] | [--json] pr show [<number>] | pr merge [<number>] [--method merge|squash|rebase] [--auto] | pr ready|rerun [<number>] | pr comment [<number>] | pr review [<number>] --approve|--request-changes|--comment | pr reply <thread> | pr resolve|unresolve <thread> | pr agent [<number>] [--thread <thread>]... [--queue]  (comment, review and reply read the text on stdin)"

// prRow is one worktree's row of pr ls.
type prRow struct {
	Slug   string  `json:"slug"`
	Branch string  `json:"branch"`
	PR     *prs.PR `json:"pr"`
}

// railswitch pr ls: each worktree of the project (-C), in registry order,
// with its branch's pull request from the host's gh (D75); the repo is
// project.toml's, never a worktree's git config.
func cmdPr(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", prUsage)
	}
	switch args[0] {
	case "show", "merge", "ready", "rerun", "comment", "review", "agent":
		return prAct(ctx, e, args)
	case "reply", "resolve", "unresolve":
		return prThread(ctx, e, args)
	case "ls":
	default:
		return e.usageErr("%s", prUsage)
	}
	fs := e.flags("pr ls")
	all := fs.Bool("all", false, "every open PR of the repo, with its author, assignees and reviewers, and who gh signs in as")
	if code, ok := parse(fs, args[1:]); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("%s", prUsage)
	}
	w, err := workspace.Load(ctx, e.dir, "")
	if err != nil {
		return e.fail(err)
	}
	repo, err := prs.Repo(w.Project.Project.Repo)
	if err != nil {
		return e.fail(fmt.Errorf("pr ls: %w", err))
	}
	if *all {
		return prAll(ctx, e, repo)
	}
	m, err := e.model(ctx, w)
	if err != nil {
		return e.fail(err)
	}
	var branches []string
	for _, wt := range m.Inputs.Worktrees {
		branches = append(branches, wt.Branch)
	}
	list, err := prs.List(ctx, e.run, repo, branches)
	if err != nil {
		return e.fail(err)
	}
	rows := make([]prRow, 0, len(m.Inputs.Worktrees))
	for _, wt := range m.Inputs.Worktrees {
		r := prRow{Slug: wt.Slug, Branch: wt.Branch}
		if wt.Branch != "" {
			r.PR = prs.ForBranch(list, wt.Branch, e.headCommit(ctx, wt.Dir))
		}
		rows = append(rows, r)
	}
	var found []*prs.PR
	for _, r := range rows {
		found = append(found, r.PR)
	}
	if err := prs.ReviewApps(ctx, e.run, repo, found); err != nil {
		fmt.Fprintf(e.stderr, "warning: review apps: %v\n", err)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(rows); err != nil {
			return e.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "WORKTREE\tBRANCH\tPR\tSTATE\tCHECKS\tREVIEW\tTITLE")
	for _, r := range rows {
		branch := safefile.Printable(r.Branch) // from the worktree's git files
		if branch == "" {
			branch = "-"
		}
		if r.PR == nil {
			fmt.Fprintf(tw, "%s\t%s\t-\t-\t-\t-\t-\n", r.Slug, branch)
			continue
		}
		p := r.PR
		fmt.Fprintf(tw, "%s\t%s\t#%s\t%s\t%s\t%s\t%s\n", r.Slug, branch, strconv.Itoa(p.Number), p.State, p.Checks, p.Review, safefile.Printable(p.Title))
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	return 0
}

// headCommit is the commit checked out in worktree dir, "" when git can't
// say (the PR match then takes only open ones). Hooks and fsmonitor off: a
// worktree's git config is the project's, not trusted.
func (e *env) headCommit(ctx context.Context, dir string) string {
	res, err := e.run.Run(ctx, sys.Cmd{Name: "git", Args: []string{"-C", dir, "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "rev-parse", "--verify", "-q", "HEAD^{commit}"}})
	if err != nil {
		return ""
	}
	h := strings.TrimSpace(string(res.Stdout))
	if len(h) < 40 || strings.Trim(h, "0123456789abcdef") != "" {
		return ""
	}
	return h
}

// prAll is pr ls --all: the repo's open PRs and who they are for, and the
// login gh signs in as ("" when it can't tell: a warning, not a failure).
func prAll(ctx context.Context, e *env, repo string) int {
	list, err := prs.Open(ctx, e.run, repo)
	if err != nil {
		return e.fail(err)
	}
	me := whoami(ctx, e)
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(struct {
			Me  string       `json:"me"`
			PRs []prs.OpenPR `json:"prs"`
		}{me, list}); err != nil {
			return e.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PR\tAUTHOR\tASSIGNEES\tREVIEWERS\tREVIEW\tTITLE")
	for _, p := range list {
		title := safefile.Printable(p.Title)
		if p.Draft {
			title = "[draft] " + title
		}
		fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\t%s\t%s\n", p.Number, safefile.Printable(p.Author), logins(p.Assignees), logins(p.Reviewers), p.Review, title)
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	return 0
}

// whoami is the login gh signs in as, "" with a warning when it can't tell.
func whoami(ctx context.Context, e *env) string {
	me, err := prs.Me(ctx, e.run)
	if err != nil {
		fmt.Fprintf(e.stderr, "warning: who gh signs in as: %v\n", err)
	}
	return me
}

// logins is a list of logins for a table cell.
func logins(s []string) string {
	if len(s) == 0 {
		return "-"
	}
	return safefile.Printable(strings.Join(s, ","))
}

// prAct is pr show|merge|ready|rerun (D152): one pull request, the number
// given or the current worktree's (-w) branch's, as pr ls finds it.
func prAct(ctx context.Context, e *env, args []string) int {
	var fs *flag.FlagSet
	var method *string
	var auto, approve, changes, comment, queue *bool
	var threads stringList
	switch args[0] {
	case "show":
		fs = e.flags("pr show")
	case "merge":
		fs = e.flags("pr merge")
		method = fs.String("method", "", "merge, squash or rebase (default: the first the repo allows)")
		auto = fs.Bool("auto", false, "merge once the PR's requirements pass (with a merge queue on the base: join it)")
	case "ready":
		fs = e.flags("pr ready")
	case "rerun":
		fs = e.flags("pr rerun")
	case "comment":
		fs = e.flags("pr comment")
	case "review":
		fs = e.flags("pr review")
		approve = fs.Bool("approve", false, "approve the PR")
		changes = fs.Bool("request-changes", false, "ask for changes (the text says which)")
		comment = fs.Bool("comment", false, "review with a comment only")
	case "agent":
		fs = e.flags("pr agent")
		fs.Var(&threads, "thread", "a review `thread` to send (repeatable; default: every unresolved one)")
		queue = fs.Bool("queue", false, "add the text to the worktree's agent queue (D159) instead of typing it now")
	}
	pos, code, ok := parseMixed(fs, args[1:])
	if !ok {
		return code
	}
	if len(pos) > 1 {
		return e.usageErr("%s", prUsage)
	}
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return e.fail(err)
	}
	repo, err := prs.Repo(w.Project.Project.Repo)
	if err != nil {
		return e.fail(fmt.Errorf("pr %s: %w", args[0], err))
	}
	n := 0
	if len(pos) == 1 {
		if n, err = strconv.Atoi(strings.TrimPrefix(pos[0], "#")); err != nil || n <= 0 {
			return e.usageErr("pr %s: %q is not a PR number", args[0], pos[0])
		}
	} else if n, err = e.worktreePR(ctx, w, repo); err != nil {
		return e.fail(fmt.Errorf("pr %s: %w", args[0], err))
	}
	if args[0] == "agent" && len(pos) == 1 {
		// the agent works on the worktree's branch: another PR's review is not its work
		own, err := e.worktreePR(ctx, w, repo)
		if err != nil {
			return e.fail(fmt.Errorf("pr agent: %w", err))
		}
		if own != n {
			return e.fail(fmt.Errorf("pr agent: #%d is not worktree %s's PR (#%d)", n, w.Slug, own))
		}
	}
	v, err := prs.ShowPR(ctx, e.run, repo, n)
	if err != nil {
		return e.fail(err)
	}
	switch args[0] {
	case "show":
		return prShow(e, v)
	case "merge":
		m := *method
		if m == "" && len(v.Methods) > 0 {
			m = v.Methods[0]
		}
		if !slices.Contains(v.Methods, m) {
			return e.fail(fmt.Errorf("pr merge: %s does not allow %q merges (it allows %s)", repo, m, strings.Join(v.Methods, ", ")))
		}
		if *auto && !v.Queue && !v.AutoMergeAllowed {
			return e.fail(fmt.Errorf("pr merge --auto: %s does not allow auto-merge (Settings → General → Allow auto-merge); merge it once its checks pass", repo))
		}
		if err := prs.Merge(ctx, e.run, repo, n, m, *auto); err != nil {
			return e.fail(err)
		}
		switch {
		case v.Queue:
			fmt.Fprintf(e.stdout, "#%d joins %s's merge queue\n", n, safefile.Printable(v.Base))
		case *auto:
			fmt.Fprintf(e.stdout, "#%d merges (%s) once its requirements pass\n", n, m)
		default:
			fmt.Fprintf(e.stdout, "merged #%d (%s)\n", n, m)
		}
	case "ready":
		if v.State != "draft" {
			return e.fail(fmt.Errorf("pr ready: #%d is %s, not a draft", n, v.State))
		}
		if err := prs.Ready(ctx, e.run, repo, n); err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "#%d is ready for review\n", n)
	case "comment":
		body, err := e.prBody(true)
		if err != nil {
			return e.fail(err)
		}
		if err := prs.AddComment(ctx, e.run, repo, n, body); err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "commented on #%d\n", n)
	case "review":
		var verdict string
		switch {
		case *approve && !*changes && !*comment:
			verdict = "approve"
		case *changes && !*approve && !*comment:
			verdict = "request-changes"
		case *comment && !*approve && !*changes:
			verdict = "comment"
		default:
			return e.usageErr("pr review: one of --approve, --request-changes, --comment")
		}
		body, err := e.prBody(verdict != "approve")
		if err != nil {
			return e.fail(err)
		}
		if err := prs.SubmitReview(ctx, e.run, repo, n, verdict, body); err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "reviewed #%d: %s\n", n, verdict)
	case "agent":
		return e.prAgent(ctx, v, threads, *queue)
	case "rerun":
		runs, err := prs.Rerun(ctx, e.run, repo, v)
		if err != nil {
			return e.fail(err)
		}
		if len(runs) == 0 {
			fmt.Fprintf(e.stdout, "#%d has no failed Actions run to rerun\n", n)
			return 0
		}
		fmt.Fprintf(e.stdout, "reran the failed jobs of %d run(s) of #%d\n", len(runs), n)
	}
	return 0
}

// worktreePR is the PR of the current worktree's branch (pr ls's pick).
func (e *env) worktreePR(ctx context.Context, w *workspace.Workspace, repo string) (int, error) {
	if w.Slug == "" {
		return 0, errors.New("not in a worktree: name a PR number, run it in <workspace>/worktrees/<worktree>/, or pass -w <worktree>")
	}
	m, err := e.model(ctx, w)
	if err != nil {
		return 0, err
	}
	i := slices.IndexFunc(m.Inputs.Worktrees, func(wt render.Worktree) bool { return wt.Slug == w.Slug })
	if i < 0 || m.Inputs.Worktrees[i].Branch == "" {
		return 0, fmt.Errorf("worktree %s is on no branch", w.Slug)
	}
	wt := m.Inputs.Worktrees[i]
	list, err := prs.List(ctx, e.run, repo, []string{wt.Branch})
	if err != nil {
		return 0, err
	}
	p := prs.ForBranch(list, wt.Branch, e.headCommit(ctx, wt.Dir))
	if p == nil {
		return 0, fmt.Errorf("branch %s of worktree %s has no pull request", safefile.Printable(wt.Branch), w.Slug)
	}
	return p.Number, nil
}

// prShow prints v: with --json as is, else a summary with its checks.
func prShow(e *env, v *prs.View) int {
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(v); err != nil {
			return e.fail(err)
		}
		return 0
	}
	p := safefile.Printable
	fmt.Fprintf(e.stdout, "#%d %s\n%s · %s → %s · by %s · +%d −%d\n", v.Number, p(v.Title), v.State, p(v.Head), p(v.Base), p(v.Author), v.Additions, v.Deletions)
	fmt.Fprintf(e.stdout, "checks %s · review %s · merge %s (%s)", v.Checks, v.Review, v.MergeState, v.Mergeable)
	if v.AutoMerge != "" {
		fmt.Fprintf(e.stdout, " · auto-merge %s", v.AutoMerge)
	}
	fmt.Fprintln(e.stdout)
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	for _, c := range v.CheckRuns {
		fmt.Fprintf(tw, "  %s\t%s\n", c.State, p(c.Name))
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "%d review(s), %d comment(s), %d file(s): %s\n", len(v.Reviews), len(v.Comments), len(v.Files), v.URL)
	return 0
}

// maxPRText is GitHub's limit on a comment's length.
const maxPRText = 65536

// prBody reads a comment's text from stdin: UTF-8, at most maxPRText bytes,
// and (when needed) not blank.
func (e *env) prBody(needed bool) ([]byte, error) {
	body, err := e.readStdin()
	if err != nil {
		return nil, err
	}
	switch {
	case len(body) > maxPRText:
		return nil, fmt.Errorf("the text is over %d bytes", maxPRText)
	case !utf8.Valid(body):
		return nil, errors.New("the text is not UTF-8")
	case needed && strings.TrimSpace(string(body)) == "":
		return nil, errors.New("no text on stdin")
	}
	return body, nil
}

// prThread is pr reply|resolve|unresolve <thread>: one review thread, by
// its GraphQL ID (pr show --json's threads[].id).
func prThread(ctx context.Context, e *env, args []string) int {
	var fs *flag.FlagSet
	switch args[0] {
	case "reply":
		fs = e.flags("pr reply")
	case "resolve":
		fs = e.flags("pr resolve")
	case "unresolve":
		fs = e.flags("pr unresolve")
	}
	pos, code, ok := parseMixed(fs, args[1:])
	if !ok {
		return code
	}
	if len(pos) != 1 || !prs.ThreadID.MatchString(pos[0]) {
		return e.usageErr("pr %s: name one review thread (pr show --json: threads[].id)", args[0])
	}
	id := pos[0]
	switch args[0] {
	case "reply":
		body, err := e.prBody(true)
		if err != nil {
			return e.fail(err)
		}
		if err := prs.Reply(ctx, e.run, id, body); err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "replied to %s\n", id)
	default:
		if err := prs.Resolve(ctx, e.run, id, args[0] == "unresolve"); err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "%sd %s\n", args[0], id)
	}
	return 0
}

// prAgent types PR v's review threads (the named ones, else every
// unresolved one) into the worktree's default agent: its session is
// started when it has none (agent prompt), then typed into once it settles.
// With queue the text joins the worktree's agent queue instead (D159).
func (e *env) prAgent(ctx context.Context, v *prs.View, threads []string, queue bool) int {
	text, n, err := prs.AgentText(v, threads, lifecycle.MaxSend)
	if err != nil {
		return e.fail(err)
	}
	l, slug, code := e.lifecycle(ctx, "pr agent")
	if code != 0 {
		return code
	}
	if queue {
		it, err := agentqueue.Add(l.WS.Root, slug, text, time.Now())
		if err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "queued %d review thread(s) of #%d as #%d for the agent in %s\n", n, v.Number, it.ID, slug)
		return 0
	}
	if err := l.AgentPrompt(ctx, slug, "", text); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "sent %d review thread(s) of #%d to the agent in %s: railswitch -w %s agent joins it\n", n, v.Number, slug, slug)
	return 0
}
