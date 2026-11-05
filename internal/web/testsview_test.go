package web

import (
	"strings"
	"testing"
	"time"
)

// TestTestsColumn: the list's tests column and the detail's Tests line
// (D158): passed, failed, never, stale, and nothing without a test task;
// the detail's button runs the test task as a job.
func TestTestsColumn(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	ran := func(state string, code int, stale bool) *tests {
		return &tests{State: state, Stale: stale, Last: &testRun{ExitCode: code, StartedAt: now.Add(-6 * time.Minute), EndedAt: now.Add(-5 * time.Minute), Commit: strings.Repeat("ab", 20)}}
	}
	for _, c := range []struct {
		name  string
		t     *tests
		want  []string
		never []string
	}{
		{"passed", ran("passed", 0, false), []string{`<span class="green"`, "✓ passed 5m ago", "exit 0", "took 1m0s", "at abababababab"}, []string{"stale"}},
		{"failed stale", ran("failed", 2, true), []string{`<span class="red"`, "✗ failed 5m ago", "exit 2", `<span class="stale">stale</span>`}, nil},
		{"never", &tests{State: "never"}, []string{"— never"}, []string{"stale"}},
	} {
		r := fixtureRow("up", false, nil)
		r.WT.Tests, r.Now = c.t, now
		rowHTML, err := render("row", newRow("ftest", r, "", parseSort("name")))
		if err != nil {
			t.Fatal(err)
		}
		detail, err := render("detail", newDetail("ftest", r, ""))
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range c.want {
			if !strings.Contains(rowHTML, w) {
				t.Errorf("%s: row lacks %q:\n%s", c.name, w, rowHTML)
			}
		}
		for _, w := range c.never {
			if strings.Contains(rowHTML, w) {
				t.Errorf("%s: row has %q:\n%s", c.name, w, rowHTML)
			}
		}
		if !strings.Contains(detail, `>Tests<`) || !strings.Contains(detail, `<form method="post" action="/p/ftest/feat-login/task" data-post><input type="hidden" name="task" value="test"><button class="ln">Run tests</button></form>`) {
			t.Errorf("%s: detail lacks the Tests line and its button:\n%s", c.name, detail)
		}
	}
	r := fixtureRow("up", false, nil)
	if html, _ := render("detail", newDetail("ftest", r, "")); strings.Contains(html, ">Tests<") {
		t.Errorf("no test task, yet a Tests line:\n%s", html)
	}
	if html, _ := render("row", newRow("ftest", r, "", parseSort("name"))); !strings.Contains(html, `<div class="tests"><span class="muted">—</span></div>`) {
		t.Errorf("no test task: tests cell not —:\n%s", html)
	}
}
