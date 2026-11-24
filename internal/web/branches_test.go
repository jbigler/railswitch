package web

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/jobs"
)

const branchesJSON = `{"base":"main","fetched":"2026-01-01T00:00:00Z","warnings":["no PR states: gh: not logged in"],"branches":[
 {"name":"main","upstream":"origin/main","gone":false,"ahead":0,"behind":0,"commit":"a","date":"2026-01-01T00:00:00Z","merged":true,"worktree":"main","checked_out":true,"base":true,"pr":null,"stack":null,"reasons":[],"could_go":false},
 {"name":"jb/b","upstream":"origin/jb/b","gone":false,"ahead":0,"behind":2,"commit":"b","date":"2025-12-31T00:00:00Z","merged":false,"worktree":"b","checked_out":true,"base":false,"pr":{"number":7,"state":"open","url":"https://github.com/o/n/pull/7"},"stack":{"number":12},"reasons":[],"could_go":false},
 {"name":"done","upstream":"origin/done","gone":true,"ahead":0,"behind":0,"commit":"c","date":"2025-12-01T00:00:00Z","merged":true,"worktree":"","checked_out":false,"base":false,"pr":{"number":5,"state":"merged","url":"https://github.com/o/n/pull/5"},"stack":null,"reasons":["upstream-gone","pr-merged","merged"],"could_go":true},
 {"name":"wip","upstream":"","gone":false,"ahead":0,"behind":0,"commit":"d","date":"2025-11-01T00:00:00Z","merged":false,"worktree":"","checked_out":false,"base":false,"pr":null,"stack":null,"reasons":[],"could_go":false},
 {"name":"old","upstream":"origin/old","gone":false,"ahead":0,"behind":3,"commit":"e","date":"2025-10-01T00:00:00Z","merged":true,"worktree":"","checked_out":false,"base":false,"pr":null,"stack":null,"reasons":["merged"],"could_go":true}]}`

// D227: the page shows before its read; the table has each branch's
// worktree, PR, stack, upstream and why it could go; only a branch no
// worktree has checked out can be ticked; Pull shows for one behind and
// warns of b's running agent; the actions are one job each.
func TestBranchesPage(t *testing.T) {
	f := newFixture(t)
	f.cli.set("--json -C /ws/ftest branch ls", branchesJSON)
	f.cli.set("--json -C /ws/ftest overview", ftestOverview("jb/b", "up", agentJSON("working", "busy", false)))
	w := f.do(req("GET", "/p/ftest/branches", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	contains(t, "branches", body, `data-view="branches"`, `id="branches-body" data-src="/p/ftest/branches/items"`)
	noCSPViolation(t, "branches", body)
	if n := f.cli.count("--json -C /ws/ftest branch ls"); n != 0 {
		t.Errorf("the page read branch ls %d times before it showed", n)
	}
	contains(t, "header", f.do(req("GET", "/p/ftest", nil)).Body.String(), `href="/p/ftest/branches"`)

	w = f.do(req("GET", "/p/ftest/branches/items", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body = w.Body.String()
	contains(t, "items", body, "gh: not logged in", "Could go only <span class=\"muted\">(2)</span>",
		`>base</span>`, `≡ stack #12`, `<a href="/p/ftest/b">b</a>`, `#7 open`, `↑0 ↓2`, `>gone<`, "upstream gone · PR merged · merged into main",
		`name="branch" value="done" aria-label`, `name="branch" value="wip" aria-label="Select wip" data-unmerged`,
		"Pull all behind (2)", "b's agent is running, and its files change under it", "Delete (unmerged)")
	for _, no := range []string{`value="main" aria-label`, `value="jb/b" aria-label`} {
		if strings.Contains(body, no) {
			t.Errorf("items offer %s", no)
		}
	}
	noCSPViolation(t, "branches items", body)

	for _, c := range []struct {
		path string
		form []string
	}{
		{"/p/ftest/branches/rm", nil},
		{"/p/ftest/branches/rm", []string{"branch", "--force"}},
		{"/p/ftest/branches/rm", []string{"branch", "a b"}},
		{"/p/ftest/branches/pull", []string{"branch", "ext::sh"}},
	} {
		if w := f.do(req("POST", c.path, form(c.form...))); w.Code != 400 {
			t.Errorf("%s %v: %d", c.path, c.form, w.Code)
		}
	}
	if len(f.run.Argvs()) != 0 {
		t.Fatalf("refused posts ran %q", f.run.Argvs())
	}
	for i, c := range []struct {
		path string
		form []string
		want []string
	}{
		{"/p/ftest/branches/fetch", nil, []string{"branch", "fetch"}},
		{"/p/ftest/branches/rm", []string{"branch", "done", "branch", "wip", "force", "1", "remote", "1"}, []string{"branch", "rm", "--force", "--remote", "done", "wip"}},
		{"/p/ftest/branches/pull", []string{"branch", "old"}, []string{"branch", "pull", "old"}},
	} {
		if w := f.do(req("POST", c.path, form(c.form...))); w.Code != 202 {
			t.Fatalf("%s: %d %s", c.path, w.Code, w.Body)
		}
		if got, want := f.run.wait(t, i+1)[i], append([]string{testExe, "-C", "/ws/ftest"}, c.want...); !slices.Equal(got, want) {
			t.Errorf("argv %q, want %q", got, want)
		}
		f.ended(t)
	}
}

// D228: origin is fetched every fetchEvery while a page of the project is
// open, never without one; opening the Branches page fetches first when
// the last fetch is older; a failed fetch shows there, not in Jobs.
func TestFetchPoll(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const fetch = "-C /ws/ftest branch fetch"
	f.cli.set(fetch, "fetched origin\n")
	f.cli.set("--json -C /ws/ftest branch ls", branchesJSON)
	f.s.pollOnce(ctx) // loads the projects; no page is open
	if n := f.cli.count(fetch); n != 0 {
		t.Errorf("fetched with no page open: %d", n)
	}
	sub := f.s.subscribe("ftest", "b", parseSort("name"), usageDefault, "")
	defer f.s.unsubscribe(sub)
	f.s.pollFetch(ctx)
	f.s.pollFetch(ctx)
	if n := f.cli.count(fetch); n != 1 {
		t.Errorf("fetches before fetchEvery = %d, want 1", n)
	}
	// The page within fetchEvery reads without fetching.
	if w := f.do(req("GET", "/p/ftest/branches/items", nil)); w.Code != 200 || f.cli.count(fetch) != 1 {
		t.Errorf("%d, fetches %d", w.Code, f.cli.count(fetch))
	}
	f.clock.Add(fetchEvery)
	f.cli.fail(fetch, errors.New("git fetch --prune origin: exit status 128: Permission denied (publickey)"))
	body := f.do(req("GET", "/p/ftest/branches/items", nil)).Body.String()
	if n := f.cli.count(fetch); n != 2 {
		t.Errorf("fetches after fetchEvery, on opening the page = %d, want 2", n)
	}
	contains(t, "items", body, "The last fetch, at ", "failed: git fetch --prune origin: exit status 128: Permission denied (publickey)")
	if len(f.run.Argvs()) != 0 {
		t.Errorf("a fetch ran as a job: %q", f.run.Argvs())
	}
	f.s.pollFetch(ctx)
	if n := f.cli.count(fetch); n != 2 {
		t.Errorf("the poll fetched again right after the page did: %d", n)
	}
	f.clock.Add(fetchEvery)
	f.cli.set(fetch, "fetched origin\n")
	f.s.pollFetch(ctx)
	if body := f.do(req("GET", "/p/ftest/branches/items", nil)).Body.String(); strings.Contains(body, "The last fetch") {
		t.Error("a fetch that went well left the failure shown")
	}
}

// D230: a worktree whose open PR is in no stack read, with gh stack
// installed, offers Get its stack, which runs stack checkout with the PR
// number the dashboard read; in a stack, or without gh stack, it doesn't.
func TestGetStack(t *testing.T) {
	ctx := context.Background()
	const noStack = `{"available":true,"worktrees":[{"slug":"main","stack":null,"error":""},{"slug":"b","stack":null,"error":""}]}`
	f := newFixture(t)
	f.cli.set("--json -C /ws/ftest stack ls", noStack)
	sub := f.s.subscribe("ftest", "b", parseSort("name"), usageDefault, "")
	defer f.s.unsubscribe(sub)
	f.s.pollOnce(ctx)
	ev, ok := byID(drain(sub), "detail")
	if !ok || !strings.Contains(ev.HTML, `action="/p/ftest/b/stack/checkout"`) || !strings.Contains(ev.HTML, "Get its stack") {
		t.Fatalf("detail without Get its stack: %v", ok)
	}
	if w := f.do(req("POST", "/p/ftest/b/stack/checkout", form("pr", "1"))); w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got, want := f.run.wait(t, 1)[0], []string{testExe, "-C", "/ws/ftest", "-w", "b", "stack", "checkout", "2141"}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
	// Once it ends, the worktree's stack is read at once (its HEAD often
	// stays put): in a stack now, Get its stack goes.
	f.cli.set(stackQuick, stackB("jb/b", false, false))
	f.s.jobEvent(jobs.Event{Job: f.ended(t)})
	drain(sub)
	f.s.pollStacks(ctx)
	if ev, ok := byID(drain(sub), "detail"); !ok || strings.Contains(ev.HTML, "Get its stack") {
		t.Errorf("Get its stack after the checkout: %v", ok)
	}
	if w := f.do(req("POST", "/p/ftest/main/stack/checkout", form())); w.Code != 422 {
		t.Errorf("main, no PR: %d", w.Code)
	}

	g := newFixture(t)
	g.cli.set("--json -C /ws/ftest stack ls", `{"available":false,"worktrees":[{"slug":"main","stack":null,"error":""},{"slug":"b","stack":null,"error":""}]}`)
	sub2 := g.s.subscribe("ftest", "b", parseSort("name"), usageDefault, "")
	defer g.s.unsubscribe(sub2)
	g.s.pollOnce(ctx)
	if ev, ok := byID(drain(sub2), "detail"); ok && strings.Contains(ev.HTML, "Get its stack") {
		t.Error("Get its stack without gh stack")
	}
	if w := g.do(req("POST", "/p/ftest/b/stack/checkout", form())); w.Code != 422 {
		t.Errorf("without gh stack: %d", w.Code)
	}
}
