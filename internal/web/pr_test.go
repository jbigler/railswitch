package web

import (
	"net/url"
	"slices"
	"strings"
	"testing"
)

const prShowJSON = `{"number": 2141, "title": "Login <b>", "url": "https://github.com/o/r/pull/2141", "state": "open", "author": "ann",
 "body": "## Why\n<script>alert(1)</script>", "base": "main", "head": "jb/b", "labels": ["ui"], "mergeable": "mergeable", "merge_state": "clean",
 "review": "approved", "checks": "fail", "additions": 3, "deletions": 1, "created": "", "updated": "", "auto_merge": "", "queue": false, "auto_merge_allowed": true,
 "methods": ["squash", "merge"],
 "check_runs": [{"name": "test", "workflow": "ci", "state": "fail", "url": "https://github.com/o/r/actions/runs/41/job/5", "run": 41}],
 "reviews": [{"author": "bo", "state": "approved", "body": "nice", "submitted": "2026-11-01T10:00:00Z"}],
 "comments": [], "files": [{"path": "a.go", "additions": 3, "deletions": 1, "change": "modified"}],
 "threads": [{"id": "PRRT_done", "path": "b.go", "line": 2, "resolved": true, "outdated": false, "hunk": "@@ -1 +1 @@\n+x", "comments": [{"author": "cy", "body": "fine", "created": "", "url": ""}]},
  {"id": "PRRT_open", "path": "a.go", "line": 7, "resolved": false, "outdated": false, "hunk": "@@ -1,9 +1,9 @@\n l1\n l2\n l3\n l4\n l5\n l6\n+l7 <i>", "comments": [{"author": "bo", "body": "rename <this>", "created": "", "url": ""}]}]}`

// D152: the PR page shows pr show --json escaped, with the actions its
// state allows, and breaks no CSP rule.
func TestPRPage(t *testing.T) {
	f := newFixture(t)
	f.cli.set("--json -C /ws/ftest -w b pr show", prShowJSON)
	w := f.do(req("GET", "/p/ftest/b/pr", nil))
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, body)
	}
	contains(t, "pr page", body, `data-view="pr"`, "Login &lt;b&gt;", "&lt;script&gt;alert(1)&lt;/script&gt;", "data-md",
		`action="/p/ftest/b/pr/merge"`, `<option value="squash">`, "Merge when ready", "Merge now", "Rerun failed checks",
		`href="https://github.com/o/r/actions/runs/41/job/5"`)
	if strings.Contains(body, "Ready for review") {
		t.Error("an open PR offers Ready for review")
	}
	noCSPViolation(t, "pr page", body)
	// threads: unresolved first, the hunk's end escaped, each with its actions
	contains(t, "threads", body, "1 unresolved of 2", "Send all unresolved to the agent", `value="PRRT_open"`,
		"l7 &lt;i&gt;", "rename &lt;this&gt;", `action="/p/ftest/b/pr/resolve"`, `action="/p/ftest/b/pr/unresolve"`,
		`action="/p/ftest/b/pr/reply"`, `action="/p/ftest/b/pr/comment"`, `value="request-changes"`)
	if strings.Index(body, "PRRT_open") > strings.Index(body, "PRRT_done") || strings.Contains(body, " l1\n") {
		t.Error("the unresolved thread comes first, with the hunk's last 6 lines only")
	}

	// a repo without auto-merge: no Merge when ready
	f.cli.set("--json -C /ws/ftest -w b pr show", strings.Replace(prShowJSON, `"auto_merge_allowed": true`, `"auto_merge_allowed": false`, 1))
	if body := f.do(req("GET", "/p/ftest/b/pr", nil)).Body.String(); strings.Contains(body, "Merge when ready") || !strings.Contains(body, "Merge now") {
		t.Errorf("no auto-merge: %s", body)
	}
	// a queue on the base: one button joins it
	f.cli.set("--json -C /ws/ftest -w b pr show", strings.Replace(prShowJSON, `"queue": false`, `"queue": true`, 1))
	if body := f.do(req("GET", "/p/ftest/b/pr", nil)).Body.String(); !strings.Contains(body, "Add to merge queue") || strings.Contains(body, "Merge now") {
		t.Errorf("queue: %s", body)
	}
	if w := f.do(req("GET", "/p/ftest/nope/pr", nil)); w.Code != 404 {
		t.Errorf("unknown worktree: %d", w.Code)
	}
}

// D152: each action is a job on the PR number the page showed; a bad
// number or method is refused before anything runs.
func TestPRActions(t *testing.T) {
	f := newFixture(t)
	post := func(action string, form url.Values) int {
		r := req("POST", "/p/ftest/b/pr/"+action, form)
		r.Header.Set("Accept", "application/json")
		return f.do(r).Code
	}
	for _, c := range []struct {
		action string
		form   url.Values
		code   int
	}{
		{"merge", url.Values{"number": {"0"}, "method": {"squash"}}, 400},
		{"merge", url.Values{"number": {"7"}, "method": {"octopus"}}, 400},
		{"rebase", url.Values{"number": {"7"}}, 404},
	} {
		if got := post(c.action, c.form); got != c.code {
			t.Errorf("%s %v: %d; want %d", c.action, c.form, got, c.code)
		}
	}
	if got := post("merge", url.Values{"number": {"2141"}, "method": {"squash"}, "auto": {"1"}}); got != 202 {
		t.Fatalf("merge: %d", got)
	}
	if got := f.run.wait(t, 1)[0]; !slices.Equal(got, []string{testExe, "-C", "/ws/ftest", "-w", "b", "pr", "merge", "2141", "--method", "squash", "--auto"}) {
		t.Errorf("merge argv %q", got)
	}
}

// D152: comment, review and reply send the text as the job's stdin, never
// argv; thread actions take a thread ID only.
func TestPRCommentRoutes(t *testing.T) {
	f := newFixture(t)
	post := func(action string, form url.Values) int {
		r := req("POST", "/p/ftest/b/pr/"+action, form)
		r.Header.Set("Accept", "application/json")
		return f.do(r).Code
	}
	for _, c := range []struct {
		action string
		form   url.Values
		code   int
	}{
		{"reply", url.Values{"thread": {"-PRRT_x"}, "body": {"hi"}}, 400},
		{"resolve", url.Values{"thread": {"7"}}, 400},
		{"comment", url.Values{"number": {"7"}, "body": {"  "}}, 400},
		{"review", url.Values{"number": {"7"}, "verdict": {"merge"}}, 400},
		{"review", url.Values{"number": {"7"}, "verdict": {"comment"}, "body": {""}}, 400},
		{"agent", url.Values{"number": {"7"}, "thread": {"x y"}}, 400},
		{"comment", url.Values{"number": {"7"}, "body": {strings.Repeat("x", 65537)}}, 413},
	} {
		if got := post(c.action, c.form); got != c.code {
			t.Errorf("%s %v: %d; want %d", c.action, c.form["thread"], got, c.code)
		}
	}
	steps := []struct {
		action string
		form   url.Values
		argv   []string
		stdin  string
	}{
		{"comment", url.Values{"number": {"7"}, "body": {"--not a flag"}}, []string{"comment", "7"}, "--not a flag"},
		{"review", url.Values{"number": {"7"}, "verdict": {"approve"}}, []string{"review", "7", "--approve"}, ""},
		{"reply", url.Values{"thread": {"PRRT_open"}, "body": {"done"}}, []string{"reply", "PRRT_open"}, "done"},
		{"unresolve", url.Values{"thread": {"PRRT_done"}}, []string{"unresolve", "PRRT_done"}, ""},
		{"agent", url.Values{"number": {"7"}, "thread": {"PRRT_open"}}, []string{"agent", "7", "--thread", "PRRT_open"}, ""},
	}
	for i, st := range steps {
		if got := post(st.action, st.form); got != 202 {
			t.Fatalf("%s: %d", st.action, got)
		}
		got := f.run.wait(t, i+1)[i]
		want := append([]string{testExe, "-C", "/ws/ftest", "-w", "b", "pr"}, st.argv...)
		if !slices.Equal(got, want) {
			t.Errorf("%s argv %q; want %q", st.action, got, want)
		}
		if in := string(f.run.Stdins()[i]); in != st.stdin {
			t.Errorf("%s stdin %q; want %q", st.action, in, st.stdin)
		}
		f.ended(t) // one job per project at a time
	}
}
