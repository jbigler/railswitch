package web

import (
	"slices"
	"testing"
)

const tidyJSON = `{"days":14,"bytes":3000,"warnings":["ftest: no PR states: gh: not logged in"],"items":[
 {"id":"wt:ftest/b","kind":"worktree","project":"ftest","name":"b","branch":"jb/b","reasons":["pr-merged"],"pr":7,"stack":"up","idle_since":"","idle_from":"","dirty":false,"bytes":1000,"partial":false},
 {"id":"wt:ftest/c","kind":"worktree","project":"ftest","name":"c","branch":"c","reasons":["stopped"],"pr":0,"stack":"stopped","idle_since":"2026-01-06T09:00:00Z","idle_from":"commit","dirty":true,"bytes":0,"partial":true},
 {"id":"wt:ftest/d","kind":"worktree","project":"ftest","name":"d","branch":"d","reasons":["stopped"],"pr":0,"stack":"stopped","idle_since":"2026-01-06T09:00:00Z","idle_from":"commit","dirty":false,"bytes":0,"partial":false},
 {"id":"image:localhost/ftest/base:3.3-deadbeef","kind":"image","project":"ftest","name":"localhost/ftest/base:3.3-deadbeef","reasons":["unused"],"pr":0,"stack":"","idle_since":"","idle_from":"","dirty":false,"bytes":2000,"partial":false}]}`

// D160: the page lists each candidate with why and its size, the clean
// ones whose PR is done or images ticked, the idle-only and dirty ones not; Remove selected is one tidy --rm job,
// --force only with the discard box, never for another project's item.
func TestTidyPage(t *testing.T) {
	f := newFixture(t)
	f.cli.set("--json -C /ws/ftest tidy", tidyJSON)
	w := f.do(req("GET", "/p/ftest/tidy", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	// The page shows before the slow read: it names where its candidates are.
	contains(t, "tidy", body, `data-view="tidy"`, `id="tidy-body" data-src="/p/ftest/tidy/items"`)
	noCSPViolation(t, "tidy", body)
	if n := f.cli.count("--json -C /ws/ftest tidy"); n != 0 {
		t.Errorf("the page read tidy %d times before it showed", n)
	}
	w = f.do(req("GET", "/p/ftest/tidy/items", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body = w.Body.String()
	contains(t, "tidy items", body, `<div id="tidy-body">`, `action="/p/ftest/tidy"`, "gh: not logged in",
		`name="id" value="wt:ftest/b" checked`, "PR #7 merged", "1.0 kB",
		`name="dirty" value="wt:ftest/c" aria-label`, `name="id" value="wt:ftest/d" aria-label`, "stopped over 14 days", "idle since 2026-01-06 (last commit)", "≥ 0 B", ">dirty<",
		`name="id" value="image:localhost/ftest/base:3.3-deadbeef" checked`, "no stack uses it", "3.0 kB")
	noCSPViolation(t, "tidy", body)
	contains(t, "header", f.do(req("GET", "/p/ftest", nil)).Body.String(), `href="/p/ftest/tidy"`)

	for _, c := range []struct {
		name string
		form []string
	}{
		{"nothing", nil},
		{"dirty unconfirmed", []string{"dirty", "wt:ftest/c"}},
		{"another project", []string{"id", "wt:hello/x"}},
		{"another project's image", []string{"id", "image:localhost/hello/base:3.3-deadbeef"}},
		{"a flag", []string{"id", "--all"}},
	} {
		if w := f.do(req("POST", "/p/ftest/tidy", form(c.form...))); w.Code != 400 {
			t.Errorf("%s: %d", c.name, w.Code)
		}
	}
	if len(f.run.Argvs()) != 0 {
		t.Fatalf("refused posts ran %q", f.run.Argvs())
	}

	if w := f.do(req("POST", "/p/ftest/tidy", form("id", "wt:ftest/b", "id", "image:localhost/ftest/base:3.3-deadbeef"))); w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got, want := f.run.wait(t, 1)[0], []string{testExe, "-C", "/ws/ftest", "tidy", "--rm", "wt:ftest/b", "image:localhost/ftest/base:3.3-deadbeef"}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
	f.ended(t)
	if w := f.do(req("POST", "/p/ftest/tidy", form("dirty", "wt:ftest/c", "confirm", "1"))); w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got, want := f.run.wait(t, 2)[1], []string{testExe, "-C", "/ws/ftest", "tidy", "--rm", "--force", "wt:ftest/c"}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
}
