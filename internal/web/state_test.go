package web

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jbigler/railswitch/internal/jobs"
)

// drain returns the events queued for sub.
func drain(sub *subscriber) []event {
	var out []event
	for {
		select {
		case ev := <-sub.ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func ids(evs []event) []string {
	var out []string
	for _, ev := range evs {
		if ev.Type == "html" || ev.Type == "job" {
			out = append(out, ev.ID)
		}
	}
	slices.Sort(out)
	return out
}

// byID is the last event for fragment id.
func byID(evs []event, id string) (event, bool) {
	for _, ev := range slices.Backward(evs) {
		if ev.ID == id {
			return ev, true
		}
	}
	return event{}, false
}

func notifies(evs []event) []event {
	var out []event
	for _, ev := range evs {
		if ev.Type == "notify" {
			out = append(out, ev)
		}
	}
	return out
}

// TestRowChangeEvents: a change in one worktree is one row event (and the
// detail when it is selected); an unchanged poll sends nothing.
func TestRowChangeEvents(t *testing.T) {
	for _, sel := range []string{"b", "main"} {
		f := newFixture(t)
		ctx := context.Background()
		sub := f.s.subscribe("ftest", sel, parseSort("name"), "")
		defer f.s.unsubscribe(sub)
		f.s.pollOnce(ctx)
		first := drain(sub)
		for _, id := range []string{"header", "projects", "strip", "banner", "list", "detail", "jobs"} {
			if _, ok := byID(first, id); !ok {
				t.Errorf("sel %s: first events lack %s: %q", sel, id, ids(first))
			}
		}

		f.cli.set("--json -C /ws/ftest overview", ftestOverview("jb/b2", "up", agentJSON("idle", "", false)))
		f.clock.Add(2 * time.Second)
		f.s.pollOnce(ctx)
		got := ids(drain(sub))
		want := []string{"row-b"}
		if sel == "b" {
			want = []string{"detail", "row-b"}
		}
		if !slices.Equal(got, want) {
			t.Errorf("sel %s: events %q, want %q", sel, got, want)
		}

		f.clock.Add(2 * time.Second)
		f.s.pollOnce(ctx)
		if evs := drain(sub); len(evs) != 0 {
			t.Errorf("sel %s: unchanged poll sent %q", sel, ids(evs))
		}
		if n := f.cli.count("--json -C /ws/ftest overview"); n != 3 {
			t.Errorf("overview polled %d times, want 3", n)
		}
	}
}

// TestOrderChangeSendsList: a change that moves a row resends the list.
func TestOrderChangeSendsList(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sub := f.s.subscribe("ftest", "b", parseSort("running"), "")
	defer f.s.unsubscribe(sub)
	f.s.pollOnce(ctx)
	drain(sub)
	f.cli.set("--json -C /ws/ftest overview", ftestOverview("jb/b", "failed", agentJSON("idle", "", false)))
	f.clock.Add(2 * time.Second)
	f.s.pollOnce(ctx)
	evs := drain(sub)
	ev, ok := byID(evs, "list")
	if !ok {
		t.Fatalf("no list event: %q", ids(evs))
	}
	if i, j := strings.Index(ev.HTML, `id="row-b"`), strings.Index(ev.HTML, `id="row-main"`); i < 0 || j < 0 || i > j {
		t.Errorf("failed b not first: %s", ev.HTML)
	}
	if _, ok := byID(evs, "row-b"); ok {
		t.Errorf("row event besides the list: %q", ids(evs))
	}
}

// TestNotifications: transitions into a waiting state notify once each.
func TestNotifications(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sub := f.s.subscribe("hello", "master", parseSort("name"), "")
	defer f.s.unsubscribe(sub)
	long := strings.Repeat("é", 150) + strings.Repeat("x", 150)
	steps := []struct {
		state, message string
		reply          bool
		title          string // "" = no notification
	}{
		{"permission", "first", false, ""}, // the first poll is the baseline
		{"working", "w", false, ""},
		{"permission", long, false, "ftest / b needs permission"},
		{"permission", long, false, ""},
		{"permission", "other text", false, ""},
		{"working", "w", false, ""},
		{"permission", "again", false, "ftest / b needs permission"},
		{"question", "which?", false, "ftest / b has a question"},
		{"done", "Ship it?", true, "ftest / b is waiting for your reply"},
		{"done", "Done.", false, "ftest / b your turn"},
		{"idle", "", false, ""},
		{"off", "", false, ""},
		{"done", "Done.", false, "ftest / b your turn"},
	}
	for i, st := range steps {
		f.cli.set("--json -C /ws/ftest overview", ftestOverview("jb/b", "up", agentJSON(st.state, st.message, st.reply)))
		if i > 0 {
			f.clock.Add(10 * time.Second)
		}
		f.s.pollOnce(ctx)
		ns := notifies(drain(sub))
		if st.title == "" {
			if len(ns) != 0 {
				t.Errorf("step %d (%s): notified %+v", i, st.state, ns)
			}
			continue
		}
		if len(ns) != 1 {
			t.Errorf("step %d (%s): %d notifications, want 1", i, st.state, len(ns))
			continue
		}
		n := ns[0]
		if n.Title != st.title || n.URL != "/p/ftest/b" || n.Tag == "" {
			t.Errorf("step %d: notify %+v, want title %q url /p/ftest/b", i, n, st.title)
		}
		wantBody := st.message
		if st.message == long {
			wantBody = string([]rune(long)[:200])
		}
		if n.Body != wantBody || utf8.RuneCountInString(n.Body) > 200 {
			t.Errorf("step %d: body %q (%d runes), want %q", i, n.Body, utf8.RuneCountInString(n.Body), wantBody)
		}
	}
}

// TestNeedCount: waiting agents of every project count in the header and
// the title; the strip shows the other projects' ones.
func TestNeedCount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.cli.set("--json -C /ws/ftest overview", ftestOverview("jb/b", "up", agentJSON("permission", "rm?", false)))
	f.cli.set("--json -C /ws/hello overview", helloOverview(agentJSON("question", "which db?", false)))
	sub := f.s.subscribe("ftest", "b", parseSort("name"), "")
	defer f.s.unsubscribe(sub)
	f.s.pollOnce(ctx)
	evs := drain(sub)
	hd, _ := byID(evs, "header")
	if !strings.Contains(hd.HTML, `data-need="2"`) || !strings.Contains(hd.HTML, "◆ 2 need input") {
		t.Errorf("header: %s", hd.HTML)
	}
	st, _ := byID(evs, "strip")
	if !strings.Contains(st.HTML, "hello / master") || !strings.Contains(st.HTML, "has a question") || strings.Contains(st.HTML, "ftest / b") {
		t.Errorf("strip: %s", st.HTML)
	}
	w := f.do(req("GET", "/p/ftest/b", nil))
	if !strings.Contains(w.Body.String(), "<title>(2) RailSwitch</title>") {
		t.Errorf("page title: %s", w.Body)
	}

	f.cli.set("--json -C /ws/ftest overview", ftestOverview("jb/b", "up", agentJSON("done", "ok", false)))
	f.cli.set("--json -C /ws/hello overview", helloOverview("null"))
	f.clock.Add(10 * time.Second)
	f.s.pollOnce(ctx)
	hd, _ = byID(drain(sub), "header")
	if !strings.Contains(hd.HTML, `data-need="0"`) || strings.Contains(hd.HTML, "need input") {
		t.Errorf("header at 0: %s", hd.HTML)
	}
	w = f.do(req("GET", "/p/ftest/b", nil))
	if !strings.Contains(w.Body.String(), "<title>RailSwitch</title>") {
		t.Errorf("page title at 0: %s", w.Body)
	}
}

// TestPRError: a failing pr ls is one banner; polling goes on.
func TestPRError(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.cli.fail("--json -C /ws/ftest pr ls", errors.New("pr ls: gh: not logged in"))
	sub := f.s.subscribe("ftest", "b", parseSort("name"), "")
	defer f.s.unsubscribe(sub)
	banners := 0
	for i := range 3 {
		if i > 0 {
			f.clock.Add(60 * time.Second)
		}
		f.s.pollOnce(ctx)
		for _, ev := range drain(sub) {
			if ev.ID == "banner" && strings.Contains(ev.HTML, "PRs unavailable: pr ls: gh: not logged in") {
				banners++
			}
		}
	}
	if banners != 1 {
		t.Errorf("%d banner events, want 1", banners)
	}
	if n := f.cli.count("--json -C /ws/ftest pr ls"); n != 3 {
		t.Errorf("pr ls polled %d times, want 3", n)
	}
	if n := f.cli.count("--json -C /ws/ftest overview"); n != 3 {
		t.Errorf("overview polled %d times, want 3", n)
	}

	f.cli.set("--json -C /ws/ftest pr ls", ftestPRs)
	f.clock.Add(60 * time.Second)
	f.s.pollOnce(ctx)
	evs := drain(sub)
	if ev, ok := byID(evs, "banner"); !ok || strings.Contains(ev.HTML, "PRs unavailable") {
		t.Errorf("banner not cleared: %q", ids(evs))
	}
	if ev, ok := byID(evs, "row-b"); !ok || !strings.Contains(ev.HTML, "#2141") {
		t.Errorf("row-b without its PR: %q", ids(evs))
	}
}

// TestCadence: 2 s for a project with a page open, 10 s otherwise; PRs
// every 60 s, only with a page open, and at once on Refresh PRs.
func TestCadence(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sub := f.s.subscribe("ftest", "", parseSort("name"), "")
	defer f.s.unsubscribe(sub)
	count := func() (int, int, int, int) {
		return f.cli.count("--json -C /ws/ftest overview"), f.cli.count("--json -C /ws/hello overview"),
			f.cli.count("--json -C /ws/ftest pr ls"), f.cli.count("--json -C /ws/hello pr ls")
	}
	check := func(at string, ft, he, fpr, hpr int) {
		t.Helper()
		a, b, c, d := count()
		if a != ft || b != he || c != fpr || d != hpr {
			t.Errorf("%s: overview ftest %d hello %d, pr ftest %d hello %d; want %d %d %d %d", at, a, b, c, d, ft, he, fpr, hpr)
		}
	}
	f.s.pollOnce(ctx)
	check("0s", 1, 1, 1, 0)
	f.clock.Add(1 * time.Second)
	f.s.pollOnce(ctx)
	check("1s", 1, 1, 1, 0)
	f.clock.Add(1 * time.Second)
	f.s.pollOnce(ctx)
	check("2s", 2, 1, 1, 0)
	for range 4 {
		f.clock.Add(2 * time.Second)
		f.s.pollOnce(ctx)
	}
	check("10s", 6, 2, 1, 0)
	f.clock.Add(50 * time.Second)
	f.s.pollOnce(ctx)
	check("60s", 7, 3, 2, 0)

	w := f.do(req("POST", "/p/ftest/prs/refresh", nil))
	if w.Code != 202 {
		t.Errorf("refresh: %d", w.Code)
	}
	f.s.pollOnce(ctx)
	check("refresh", 7, 3, 3, 0)

	f.s.unsubscribe(sub)
	f.clock.Add(2 * time.Second)
	f.s.pollOnce(ctx)
	check("closed 62s", 7, 3, 3, 0)
	f.clock.Add(8 * time.Second)
	f.s.pollOnce(ctx)
	check("closed 70s", 8, 4, 3, 0)
	f.clock.Add(60 * time.Second)
	f.s.pollOnce(ctx)
	check("closed 130s", 9, 5, 3, 0)
}

// TestJobEndDuringRead: a job that ends while its project's overview is
// being read still makes the next poll read it again.
func TestJobEndDuringRead(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sub := f.s.subscribe("ftest", "b", parseSort("name"), "")
	defer f.s.unsubscribe(sub)
	f.s.pollOnce(ctx)
	f.clock.Add(2 * time.Second)
	ended := false
	f.cli.mu.Lock()
	f.cli.during = func(key string) {
		if key == "--json -C /ws/ftest overview" && !ended {
			ended = true
			f.s.jobEvent(jobs.Event{Job: jobs.Snapshot{ID: "j1", Spec: jobs.Spec{Project: "ftest", Slug: "b", Label: "up"}, State: jobs.StateOK}})
		}
	}
	f.cli.mu.Unlock()
	f.s.pollOnce(ctx)
	if !ended {
		t.Fatal("overview not read at 2 s")
	}
	before := f.cli.count("--json -C /ws/ftest overview")
	f.s.pollOnce(ctx) // same instant: due only through the job's end
	if n := f.cli.count("--json -C /ws/ftest overview"); n != before+1 {
		t.Errorf("overview reads %d after the job ended mid-read, want %d", n, before+1)
	}
	f.s.pollOnce(ctx)
	if n := f.cli.count("--json -C /ws/ftest overview"); n != before+1 {
		t.Errorf("overview read again without a cause: %d", n)
	}
}

// TestGhStack: a worktree whose branch is in a gh stack shows its place in
// the row and the stack in the detail: open branches top first, HEAD
// marked, a restack warning, merged ones folded, PR numbers linked through
// the worktree's own PR URL.
func TestGhStack(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.cli.set("--json -C /ws/ftest stack ls", `{"available":true,"worktrees":[
 {"slug":"main","stack":null,"error":""},
 {"slug":"b","stack":{"trunk":"main","current":"jb/b","branches":[
  {"name":"jb/a","current":false,"merged":true,"queued":false,"needs_rebase":false,"pr":{"number":2140,"state":"merged"}},
  {"name":"jb/b","current":true,"merged":false,"queued":false,"needs_rebase":true,"pr":{"number":2141,"state":"open"}},
  {"name":"jb/c","current":false,"merged":false,"queued":false,"needs_rebase":false,"pr":null}]},"error":""}]}`)
	sub := f.s.subscribe("ftest", "b", parseSort("name"), "")
	defer f.s.unsubscribe(sub)
	f.s.pollOnce(ctx)
	evs := drain(sub)
	if ev, ok := byID(evs, "row-b"); !ok || !strings.Contains(ev.HTML, "≡ 1/2") {
		t.Errorf("row-b without its stack place: %q", ids(evs))
	}
	ev, ok := byID(evs, "detail")
	if !ok {
		t.Fatalf("no detail: %q", ids(evs))
	}
	for _, want := range []string{
		"1/2 open · 1 merged · on main", "⚠ restack needed", "◀ HEAD",
		`href="https://github.com/o/r/pull/2141"`, `href="https://github.com/o/r/pull/2140"`,
		"✓ 1 merged", "no PR",
	} {
		if !strings.Contains(ev.HTML, want) {
			t.Errorf("detail lacks %q", want)
		}
	}
	if c, b := strings.Index(ev.HTML, "jb/c"), strings.Index(ev.HTML, "jb/b</strong> <strong"); c < 0 || b < 0 || c > b {
		t.Errorf("open branches not top first (jb/c at %d, jb/b at %d)", c, b)
	}

	// A click on jb/c runs the switch; the merged jb/a and HEAD's jb/b
	// are refused. When the switch ends ok, HEAD moves to jb/c at once.
	for _, b := range []string{"jb/a", "jb/b", "jb/zz"} {
		if w := f.do(req("POST", "/p/ftest/b/stack/switch", form("branch", b))); w.Code != 422 {
			t.Errorf("switch %s: %d", b, w.Code)
		}
	}
	if w := f.do(req("POST", "/p/ftest/b/stack/switch", form("branch", "jb/c"))); w.Code != 202 {
		t.Fatalf("switch jb/c: %d %s", w.Code, w.Body)
	}
	want := []string{testExe, "-C", "/ws/ftest", "-w", "b", "stack", "switch", "jb/c"}
	if got := f.run.wait(t, 1)[0]; !slices.Equal(got, want) {
		t.Errorf("argv %q", got)
	}
	drain(sub)
	f.s.jobEvent(jobs.Event{Job: jobs.Snapshot{ID: "j9", Spec: jobs.Spec{Project: "ftest", Slug: "b", Label: switchLabel + "jb/c"}, State: jobs.StateOK}})
	ev, ok = byID(drain(sub), "detail")
	if !ok || !strings.Contains(ev.HTML, "jb/c</strong> <strong class=\"accent\">◀ HEAD") || !strings.Contains(ev.HTML, "2/2 open") {
		t.Errorf("detail after the switch: %v\n%s", ok, ev.HTML)
	}
}
