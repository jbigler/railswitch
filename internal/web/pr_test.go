package web

import (
	"net/url"
	"slices"
	"strings"
	"testing"
)

const prShowJSON = `{"number": 2141, "title": "Login <b>", "url": "https://github.com/o/r/pull/2141", "state": "open", "author": "ann",
 "body": "## Why\n<script>alert(1)</script>", "base": "main", "head": "jb/b", "labels": ["ui"], "mergeable": "mergeable", "merge_state": "clean",
 "review": "approved", "checks": "fail", "additions": 3, "deletions": 1, "created": "", "updated": "", "auto_merge": "", "queue": false,
 "methods": ["squash", "merge"],
 "check_runs": [{"name": "test", "workflow": "ci", "state": "fail", "url": "https://github.com/o/r/actions/runs/41/job/5", "run": 41}],
 "reviews": [{"author": "bo", "state": "approved", "body": "nice", "submitted": "2026-11-01T10:00:00Z"}],
 "comments": [], "files": [{"path": "a.go", "additions": 3, "deletions": 1, "change": "modified"}]}`

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
