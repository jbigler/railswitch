package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

const ghFixture = `[
  {"number": 4, "title": "Old feat", "url": "https://github.com/o/n/pull/4", "headRefName": "feat", "isDraft": false, "state": "CLOSED", "reviewDecision": "", "statusCheckRollup": []},
  {"number": 7, "title": "Feat", "url": "https://github.com/o/n/pull/7", "headRefName": "feat", "isDraft": false, "state": "OPEN", "reviewDecision": "APPROVED",
   "statusCheckRollup": [{"__typename": "CheckRun", "name": "ci", "status": "COMPLETED", "conclusion": "SUCCESS"}]},
  {"number": 9, "title": "Other", "url": "https://github.com/o/n/pull/9", "headRefName": "other", "isDraft": true, "state": "OPEN", "reviewDecision": "", "statusCheckRollup": []},
  {"number": 11, "title": "Fork's master", "url": "https://github.com/o/n/pull/11", "headRefName": "master", "isCrossRepository": true, "isDraft": false, "state": "OPEN", "reviewDecision": "", "statusCheckRollup": []}
]`

func TestPrLsJSON(t *testing.T) {
	ws, _ := gitWS(t)
	f := fakeHost()
	f.Passthrough = []string{"git"}
	f.On([]string{"gh", "pr", "list"}, sys.Result{Stdout: []byte(ghFixture)}, nil)
	f.On([]string{"gh", "api", "graphql"}, sys.Result{Stdout: []byte(`{"data":{"repository":{"p7":{"commits":{"nodes":[{"commit":{"deployments":{"nodes":[{"state":"ACTIVE","latestStatus":{"state":"SUCCESS","environmentUrl":"https://app-pr-7.example.dev"}}]}}}]}}}}}`)}, nil)
	code, out, errs := runWith(t, f, "-C", ws, "--json", "pr", "ls")
	if code != 0 {
		t.Fatalf("pr ls: exit %d: %s", code, errs)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	want := []map[string]any{
		{"slug": "master", "branch": "master", "pr": nil},
		{"slug": "feat", "branch": "feat", "pr": map[string]any{
			"number": float64(7), "title": "Feat", "url": "https://github.com/o/n/pull/7",
			"state": "open", "checks": "pass", "review": "approved", "review_app": "https://app-pr-7.example.dev"}},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows %v; want %v", rows, want)
	}
	// One query per worktree branch, never in the workspace (prs.NeutralDir).
	var heads []string
	for i, c := range f.Calls {
		if c[0] != "gh" || c[1] == "api" {
			continue
		}
		heads = append(heads, c[5])
		if !slices.Equal(c[:5], []string{"gh", "pr", "list", "--repo", "[ORG]/[REPO]"}) || f.Cmds[i].Dir != "/" {
			t.Errorf("gh argv %q dir %q", c, f.Cmds[i].Dir)
		}
	}
	if !slices.Equal(heads, []string{"--head=master", "--head=feat"}) {
		t.Errorf("gh --head args %q; want master, feat", heads)
	}

	code, out, _ = runWith(t, f, "-C", ws, "pr", "ls")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if code != 0 || len(lines) != 3 || strings.Join(strings.Fields(lines[0]), " ") != "WORKTREE BRANCH PR STATE CHECKS REVIEW TITLE" ||
		strings.Join(strings.Fields(lines[1]), " ") != "master master - - - - -" ||
		strings.Join(strings.Fields(lines[2]), " ") != "feat feat #7 open pass approved Feat" {
		t.Errorf("text:\n%s", out)
	}
}

func TestPrLsNotGitHub(t *testing.T) {
	ws, _ := lockedWS(t)
	path := filepath.Join(ws, "project.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), `"git@github.com:[ORG]/[REPO].git"`, `"https://gitlab.com/o/n.git"`, 1)
	if edited == string(data) {
		t.Fatal("fixture has no repo line")
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	f := fakeHost()
	code, _, errs := runWith(t, f, "-C", ws, "pr", "ls")
	if code != 1 || !strings.Contains(errs, "pr ls: https://gitlab.com/o/n.git is not a GitHub repo") {
		t.Errorf("exit %d stderr %q; want 1, not a GitHub repo", code, errs)
	}
	if slices.ContainsFunc(f.Calls, func(c []string) bool { return c[0] == "gh" }) {
		t.Error("ran gh")
	}
}

func TestPrLsGhFails(t *testing.T) {
	ws, _ := gitWS(t)
	f := fakeHost()
	f.Passthrough = []string{"git"}
	f.On([]string{"gh"}, sys.Result{ExitCode: 4}, errors.New("gh: non-zero exit (exit 4): To get started with GitHub CLI, please run:  gh auth login"))
	code, out, errs := runWith(t, f, "-C", ws, "pr", "ls")
	if code != 1 || out != "" || !strings.Contains(errs, "gh auth login") {
		t.Errorf("exit %d stdout %q stderr %q; want 1 with gh's message", code, out, errs)
	}
}

// TestPrLsTextPrintable: a branch a container wrote into the worktree's
// git files prints without its control characters.
func TestPrLsTextPrintable(t *testing.T) {
	ws, _ := gitWS(t)
	head := filepath.Join(ws, "worktrees", "master", ".git", "worktrees", "feat", "HEAD")
	if err := os.WriteFile(head, []byte("ref: refs/heads/jb/\x1b[31mred\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := fakeHost()
	f.Passthrough = []string{"git"}
	f.On([]string{"gh", "pr", "list"}, sys.Result{Stdout: []byte(`[{"number": 3, "title": "T\u001b]0;x\u0007", "url": "u", "headRefName": "jb/\u001b[31mred", "isDraft": false, "isCrossRepository": false, "state": "OPEN", "reviewDecision": "", "statusCheckRollup": []}]`)}, nil)
	code, out, errs := runWith(t, f, "-C", ws, "pr", "ls")
	if code != 0 || strings.Contains(out, "\x1b") || strings.Contains(out, "\x07") || !strings.Contains(out, "jb/ [31mred") || !strings.Contains(out, "#3") {
		t.Errorf("exit %d %s\n%q", code, errs, out)
	}
}

// TestPrLsOldPRFromBaseBranch: a merged PR someone once opened from master
// is not master's PR once master has moved past it; while master is still
// at that PR's head commit it is.
func TestPrLsOldPRFromBaseBranch(t *testing.T) {
	ws, _ := gitWS(t)
	res, err := (sys.Exec{}).Run(t.Context(), sys.Cmd{Name: "git", Args: []string{"-C", filepath.Join(ws, "worktrees", "master"), "rev-parse", "HEAD"}})
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(res.Stdout))
	for _, c := range []struct {
		oid  string
		want bool
	}{{"0123456789abcdef0123456789abcdef01234567", false}, {head, true}} {
		f := fakeHost()
		f.Passthrough = []string{"git"}
		f.On([]string{"gh", "pr", "list"}, sys.Result{Stdout: []byte(`[{"number": 3, "title": "Release", "url": "https://github.com/o/n/pull/3", "headRefName": "master", "headRefOid": "` + c.oid + `", "isDraft": false, "state": "MERGED", "reviewDecision": "", "statusCheckRollup": []}]`)}, nil)
		code, out, errs := runWith(t, f, "-C", ws, "--json", "pr", "ls")
		if code != 0 {
			t.Fatalf("pr ls: exit %d: %s", code, errs)
		}
		var rows []struct {
			Slug string          `json:"slug"`
			PR   json.RawMessage `json:"pr"`
		}
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatal(err)
		}
		if got := string(rows[0].PR) != "null"; rows[0].Slug != "master" || got != c.want {
			t.Errorf("head %s: master's pr %s; want shown %v", c.oid[:7], rows[0].PR, c.want)
		}
		// The worktree's git config is the project's: hooks and fsmonitor off.
		if !slices.ContainsFunc(f.Calls, func(a []string) bool {
			return slices.Equal(a, []string{"git", "-C", filepath.Join(ws, "worktrees", "master"), "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "rev-parse", "--verify", "-q", "HEAD^{commit}"})
		}) {
			t.Errorf("no safe rev-parse of master: %q", f.Calls)
		}
	}
}

const ghViewFixture = `{"number": 7, "title": "Feat\u001b[2J", "url": "https://github.com/o/n/pull/7", "state": "OPEN", "isDraft": true,
  "author": {"login": "ann"}, "body": "## Why\nBecause.", "baseRefName": "main", "headRefName": "feat", "labels": [{"name": "ui"}],
  "mergeable": "MERGEABLE", "mergeStateStatus": "BLOCKED", "reviewDecision": "CHANGES_REQUESTED",
  "statusCheckRollup": [
    {"__typename": "CheckRun", "name": "test", "workflowName": "ci", "status": "COMPLETED", "conclusion": "FAILURE", "detailsUrl": "https://github.com/o/n/actions/runs/41/job/5"},
    {"__typename": "CheckRun", "name": "lint", "workflowName": "ci", "status": "COMPLETED", "conclusion": "FAILURE", "detailsUrl": "https://github.com/o/n/actions/runs/41/job/6"},
    {"__typename": "CheckRun", "name": "docs", "status": "COMPLETED", "conclusion": "SKIPPED", "detailsUrl": "javascript:alert(1)"},
    {"__typename": "StatusContext", "context": "deploy", "state": "PENDING", "targetUrl": "https://ci.example.dev/9"}],
  "reviews": [{"author": {"login": "bo"}, "state": "CHANGES_REQUESTED", "body": "Rename it", "submittedAt": "2026-11-01T10:00:00Z"},
              {"author": {"login": "bo"}, "state": "PENDING", "body": "draft", "submittedAt": ""}],
  "comments": [{"author": {"login": "cy"}, "body": "LGTM", "createdAt": "2026-11-01T11:00:00Z", "url": "https://github.com/o/n/pull/7#issuecomment-1"}],
  "files": [{"path": "a.go", "additions": 3, "deletions": 1, "changeType": "MODIFIED"}],
  "additions": 3, "deletions": 1, "createdAt": "2026-10-30T09:00:00Z", "updatedAt": "2026-11-01T11:00:00Z", "autoMergeRequest": null}`

const ghMergeFixture = `{"data":{"repository":{"mergeCommitAllowed":false,"squashMergeAllowed":true,"rebaseMergeAllowed":true,"autoMergeAllowed":true,"mergeQueue":null}}}`

// D152: pr show reads one PR (the worktree's by default) with its checks,
// reviews, comments, files and the repo's merge settings; links that are
// not GitHub's web URLs are dropped.
func TestPrShow(t *testing.T) {
	ws, _ := gitWS(t)
	f := fakeHost()
	f.Passthrough = []string{"git"}
	f.On([]string{"gh", "pr", "list"}, sys.Result{Stdout: []byte(ghFixture)}, nil)
	f.On([]string{"gh", "pr", "view", "7"}, sys.Result{Stdout: []byte(ghViewFixture)}, nil)
	f.On([]string{"gh", "api", "graphql"}, sys.Result{Stdout: []byte(ghMergeFixture)}, nil)
	code, out, errs := runWith(t, f, "-C", ws, "-w", "feat", "--json", "pr", "show")
	if code != 0 {
		t.Fatalf("pr show: exit %d: %s", code, errs)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{"number": float64(7), "state": "draft", "author": "ann", "review": "changes_requested",
		"checks": "fail", "merge_state": "blocked", "mergeable": "mergeable", "queue": false, "auto_merge": ""} {
		if v[k] != want {
			t.Errorf("%s = %v; want %v", k, v[k], want)
		}
	}
	if m := v["methods"].([]any); len(m) != 2 || m[0] != "squash" {
		t.Errorf("methods %v; want squash, rebase", m)
	}
	runs := v["check_runs"].([]any)
	if len(runs) != 4 || runs[0].(map[string]any)["run"] != float64(41) || runs[2].(map[string]any)["state"] != "skipped" ||
		runs[2].(map[string]any)["url"] != "" || runs[3].(map[string]any)["name"] != "deploy" || runs[3].(map[string]any)["state"] != "pending" {
		t.Errorf("check_runs %v", runs)
	}
	if r := v["reviews"].([]any); len(r) != 1 {
		t.Errorf("reviews %v: the pending one is a draft", r)
	}
	// the text form makes GitHub's text printable
	_, out, _ = runWith(t, f, "-C", ws, "-w", "feat", "pr", "show")
	if strings.ContainsRune(out, 0x1b) || !strings.Contains(out, "#7 Feat") {
		t.Errorf("text form:\n%s", out)
	}
	// a number names any PR, without a worktree
	if code, _, errs := runWith(t, f, "-C", ws, "pr", "show", "#7"); code != 0 {
		t.Errorf("pr show #7: exit %d %s", code, errs)
	}
	if code, _, _ := runWith(t, f, "-C", ws, "pr", "show", "-3"); code != 2 {
		t.Errorf("pr show -3: exit %d; want 2", code)
	}
}

// D152: merge takes one of the repo's methods (the first by default), with
// --auto; ready refuses a PR that is no draft; rerun reruns each failed
// Actions run once.
func TestPrActions(t *testing.T) {
	ws, _ := gitWS(t)
	f := fakeHost()
	f.Passthrough = []string{"git"}
	f.On([]string{"gh", "pr", "view", "7"}, sys.Result{Stdout: []byte(ghViewFixture)}, nil)
	f.On([]string{"gh", "api", "graphql"}, sys.Result{Stdout: []byte(ghMergeFixture)}, nil)
	calls := func(prefix ...string) [][]string {
		var out [][]string
		for _, c := range f.Calls {
			if len(c) >= len(prefix) && slices.Equal(c[:len(prefix)], prefix) {
				out = append(out, c)
			}
		}
		return out
	}
	if code, _, errs := runWith(t, f, "-C", ws, "pr", "merge", "7", "--auto"); code != 0 {
		t.Fatalf("merge: exit %d %s", code, errs)
	}
	if c := calls("gh", "pr", "merge"); len(c) != 1 || !slices.Equal(c[0], []string{"gh", "pr", "merge", "7", "--repo", "[ORG]/[REPO]", "--squash", "--auto"}) {
		t.Errorf("merge argv %q", c)
	}
	// a repo that doesn't allow auto-merge: --auto is refused before gh merges
	f.On([]string{"gh", "api", "graphql"}, sys.Result{Stdout: []byte(strings.Replace(ghMergeFixture, `"autoMergeAllowed":true`, `"autoMergeAllowed":false`, 1))}, nil)
	if code, _, errs := runWith(t, f, "-C", ws, "pr", "merge", "7", "--auto"); code != 1 || !strings.Contains(errs, "does not allow auto-merge") {
		t.Errorf("--auto without auto-merge: exit %d %s", code, errs)
	}
	if c := calls("gh", "pr", "merge"); len(c) != 1 {
		t.Errorf("merged anyway: %q", c)
	}
	f.On([]string{"gh", "api", "graphql"}, sys.Result{Stdout: []byte(ghMergeFixture)}, nil)
	if code, _, errs := runWith(t, f, "-C", ws, "pr", "merge", "7", "--method", "merge"); code != 1 || !strings.Contains(errs, "does not allow") {
		t.Errorf("a refused method: exit %d %s", code, errs)
	}
	if code, _, errs := runWith(t, f, "-C", ws, "pr", "ready", "7"); code != 0 {
		t.Errorf("ready: exit %d %s", code, errs)
	}
	if code, out, errs := runWith(t, f, "-C", ws, "pr", "rerun", "7"); code != 0 || !strings.Contains(out, "1 run(s)") {
		t.Errorf("rerun: exit %d %s%s", code, out, errs)
	}
	if c := calls("gh", "run", "rerun"); len(c) != 1 || !slices.Equal(c[0], []string{"gh", "run", "rerun", "41", "--failed", "--repo", "[ORG]/[REPO]"}) {
		t.Errorf("rerun argv %q", c)
	}
	f.On([]string{"gh", "pr", "view", "7"}, sys.Result{Stdout: []byte(strings.Replace(ghViewFixture, `"isDraft": true`, `"isDraft": false`, 1))}, nil)
	if code, _, errs := runWith(t, f, "-C", ws, "pr", "ready", "7"); code != 1 || !strings.Contains(errs, "not a draft") {
		t.Errorf("ready on an open PR: exit %d %s", code, errs)
	}
}

// D152: comment, review and reply send the text on stdin, never in argv;
// review takes exactly one verdict; thread commands take a thread ID only.
func TestPrCommentActions(t *testing.T) {
	ws, _ := gitWS(t)
	f := fakeHost()
	f.Passthrough = []string{"git"}
	f.On([]string{"gh", "pr", "view", "7"}, sys.Result{Stdout: []byte(ghViewFixture)}, nil)
	f.On([]string{"gh", "api", "graphql"}, sys.Result{Stdout: []byte(ghMergeFixture)}, nil)
	last := func(prefix ...string) ([]string, string) {
		for i := len(f.Calls) - 1; i >= 0; i-- {
			if c := f.Calls[i]; len(c) >= len(prefix) && slices.Equal(c[:len(prefix)], prefix) {
				return c, string(f.Stdins[i])
			}
		}
		return nil, ""
	}
	text := "--flag-looking text\nsecond line"
	if code, _, errs := runStdin(t, f, text, "-C", ws, "pr", "comment", "7"); code != 0 {
		t.Fatalf("comment: exit %d %s", code, errs)
	}
	if c, in := last("gh", "pr", "comment"); !slices.Equal(c, []string{"gh", "pr", "comment", "7", "--repo", "[ORG]/[REPO]", "--body-file", "-"}) || in != text {
		t.Errorf("comment argv %q stdin %q", c, in)
	}
	if code, _, _ := runStdin(t, f, "  \n", "-C", ws, "pr", "comment", "7"); code != 1 {
		t.Errorf("a blank comment: exit %d; want 1", code)
	}
	if code, _, errs := runStdin(t, f, "", "-C", ws, "pr", "review", "7", "--approve"); code != 0 {
		t.Errorf("approve without text: exit %d %s", code, errs)
	}
	if c, _ := last("gh", "pr", "review"); !slices.Contains(c, "--approve") {
		t.Errorf("review argv %q", c)
	}
	for _, flags := range [][]string{{}, {"--approve", "--comment"}} {
		if code, _, _ := runStdin(t, f, "x", append([]string{"-C", ws, "pr", "review", "7"}, flags...)...); code != 2 {
			t.Errorf("review %q: exit %d; want 2", flags, code)
		}
	}
	if code, _, errs := runStdin(t, f, "done", "-C", ws, "pr", "reply", "PRRT_abc-1"); code != 0 {
		t.Fatalf("reply: exit %d %s", code, errs)
	}
	if c, in := last("gh", "api", "graphql"); !slices.Contains(c, "id=PRRT_abc-1") || !slices.Contains(c, "body=@-") || in != "done" {
		t.Errorf("reply argv %q stdin %q", c, in)
	}
	if code, _, _ := runWith(t, f, "-C", ws, "pr", "resolve", "PRRT_abc-1"); code != 0 {
		t.Errorf("resolve: exit %d", code)
	}
	if c, _ := last("gh", "api", "graphql"); !strings.Contains(strings.Join(c, " "), "resolveReviewThread(input:{threadId:$id})") {
		t.Errorf("resolve argv %q", c)
	}
	// pr agent types into the worktree's agent: another PR's review is refused
	f.On([]string{"gh", "pr", "list"}, sys.Result{Stdout: []byte(ghFixture)}, nil)
	if code, _, errs := runWith(t, f, "-C", ws, "-w", "feat", "pr", "agent", "4"); code != 1 || !strings.Contains(errs, "#4 is not worktree feat's PR (#7)") {
		t.Errorf("agent on another PR: exit %d %s", code, errs)
	}
	for _, id := range []string{"-PRRT_x", "PRRT_a b", "7"} {
		if code, _, _ := runWith(t, f, "-C", ws, "pr", "unresolve", id); code != 2 {
			t.Errorf("unresolve %q: exit %d; want 2", id, code)
		}
	}
}
