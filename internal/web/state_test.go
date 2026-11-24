package web

import (
	"context"
	"errors"
	"slices"
	"strconv"
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
		sub := f.s.subscribe("ftest", sel, parseSort("name"), usageDefault, "")
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

// TestOrderChangeSendsList: a change that moves a row, here to another
// group (b's stack fails: Needs you), resends the list.
func TestOrderChangeSendsList(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sub := f.s.subscribe("ftest", "b", parseSort("name"), usageDefault, "")
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
	sub := f.s.subscribe("hello", "master", parseSort("name"), usageDefault, "")
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
	sub := f.s.subscribe("ftest", "b", parseSort("name"), usageDefault, "")
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
	sub := f.s.subscribe("ftest", "b", parseSort("name"), usageDefault, "")
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
	sub := f.s.subscribe("ftest", "", parseSort("name"), usageDefault, "")
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
	sub := f.s.subscribe("ftest", "b", parseSort("name"), usageDefault, "")
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
 {"slug":"b","stack":{"trunk":"main","current":"jb/b","number":10350,"branches":[
  {"name":"jb/a","current":false,"merged":true,"queued":false,"needs_rebase":false,"pr":{"number":2140,"state":"merged"}},
  {"name":"jb/b","current":true,"merged":false,"queued":false,"needs_rebase":true,"pr":{"number":2141,"state":"open","checks":"pass","review":"approved"}},
  {"name":"jb/c","current":false,"merged":false,"queued":false,"needs_rebase":false,"pr":null}]},"error":""}]}`)
	sub := f.s.subscribe("ftest", "b", parseSort("name"), usageDefault, "")
	defer f.s.unsubscribe(sub)
	f.s.pollOnce(ctx)
	evs := drain(sub)
	if ev, ok := byID(evs, "row-b"); !ok || !strings.Contains(ev.HTML, "≡ 2/3") {
		t.Errorf("row-b without its stack place: %q", ids(evs))
	}
	ev, ok := byID(evs, "detail")
	if !ok {
		t.Fatalf("no detail: %q", ids(evs))
	}
	for _, want := range []string{
		`◆ <a href="https://github.com/o/r/stack/10350" target="_blank" rel="noopener">Stack #10350</a> · Base: <span class="mono">main</span> · 3 branches (1 merged)`,
		"⚠ restack needed", "◀ HEAD", `#2141</a></td><td class="chk"><span class="green" title="checks">✓</span></td><td class="st"><span class="green">ready</span></td><td class="rv"><span class="blue">approved</span></td>`,
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
	if !ok || !strings.Contains(ev.HTML, "jb/c</strong> <strong class=\"accent\">◀ HEAD") {
		t.Errorf("detail after the switch: %v\n%s", ok, ev.HTML)
	}
}

// stackB is stack ls's document for ftest's worktree b, on branch cur of
// the stack jb/b, jb/c: with GitHub's details, or as --no-detail leaves
// them; all is the full read (every worktree), else -w b's.
func stackB(cur string, detail, all bool) string {
	chk := func(c, r string) string {
		if !detail {
			return `"checks":"","review":""`
		}
		return `"checks":"` + c + `","review":"` + r + `"`
	}
	main := ""
	if all {
		main = `{"slug":"main","branch":"main","stack":null,"error":""},`
	}
	return `{"available":true,"worktrees":[` + main + `
 {"slug":"b","branch":"` + cur + `","stack":{"trunk":"main","current":"` + cur + `","number":10350,"branches":[
  {"name":"jb/b","current":` + strconv.FormatBool(cur == "jb/b") + `,"merged":false,"queued":false,"needs_rebase":false,"pr":{"number":2141,"state":"open",` + chk("pass", "approved") + `}},
  {"name":"jb/c","current":` + strconv.FormatBool(cur == "jb/c") + `,"merged":false,"queued":false,"needs_rebase":false,"pr":{"number":2142,"state":"open",` + chk("fail", "changes_requested") + `}}]},"error":""}]}`
}

const (
	stackAll   = "--json -C /ws/ftest stack ls"
	stackQuick = "--json -C /ws/ftest -w b stack ls --no-detail"
)

// shownHead is the branch worktree b's stack shows as HEAD.
func shownHead(f *fixture) string {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	r := f.s.state["ftest"].stacks["b"]
	if r == nil || r.Stack == nil {
		return ""
	}
	for _, b := range r.Stack.Branches {
		if b.Current {
			return b.Name
		}
	}
	return ""
}

// TestStaleStackRead (#256): a full stack read that began before a switch
// ended, so it still sees the old HEAD, doesn't move HEAD back; the next
// read, made at the new branch, is shown.
func TestStaleStackRead(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.cli.set(stackAll, stackB("jb/b", true, true))
	sub := f.s.subscribe("ftest", "b", parseSort("name"), usageDefault, "") // the PR poll reads for a page open
	defer f.s.unsubscribe(sub)
	f.s.pollOnce(ctx)
	f.s.jobEvent(jobs.Event{Job: jobs.Snapshot{ID: "j1", Spec: jobs.Spec{Project: "ftest", Slug: "b", Label: switchLabel + "jb/c"}, State: jobs.StateOK}})
	if got := shownHead(f); got != "jb/c" {
		t.Fatalf("HEAD after the switch = %q, want jb/c", got)
	}
	f.s.pollPRs(ctx) // the stale read: stack ls still answers jb/b
	if got := shownHead(f); got != "jb/c" {
		t.Errorf("HEAD after a read from before the switch = %q, want jb/c", got)
	}
	f.cli.set(stackAll, stackB("jb/c", true, true))
	f.cli.set("--json -C /ws/ftest overview", ftestOverview("jb/c", "up", agentJSON("idle", "", false)))
	f.s.mu.Lock()
	f.s.state["ftest"].prNow = true
	f.s.mu.Unlock()
	f.s.pollOnce(ctx)
	if got := shownHead(f); got != "jb/c" {
		t.Errorf("HEAD after a current read = %q, want jb/c", got)
	}
}

// TestQuickStackAfterSwitch (#254): a switch that ended ok reads that
// worktree's stack alone, without GitHub's details, at once; the details
// already read for the same PRs carry over.
func TestQuickStackAfterSwitch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.cli.set(stackAll, stackB("jb/b", true, true))
	sub := f.s.subscribe("ftest", "b", parseSort("name"), usageDefault, "") // the PR poll reads for a page open
	defer f.s.unsubscribe(sub)
	f.s.pollOnce(ctx)
	f.cli.set(stackQuick, stackB("jb/c", false, false))
	f.s.jobEvent(jobs.Event{Job: jobs.Snapshot{ID: "j1", Spec: jobs.Spec{Project: "ftest", Slug: "b", Label: switchLabel + "jb/c"}, State: jobs.StateOK}})
	full := f.cli.count(stackAll)
	f.s.pollStacks(ctx)
	if n := f.cli.count(stackQuick); n != 1 {
		t.Errorf("quick reads = %d, want 1", n)
	}
	if n := f.cli.count(stackAll); n != full {
		t.Errorf("pollStacks ran the full stack ls")
	}
	if got := shownHead(f); got != "jb/c" {
		t.Errorf("HEAD = %q, want jb/c", got)
	}
	f.s.mu.Lock()
	r := f.s.state["ftest"].stacks["b"]
	f.s.mu.Unlock()
	for i, want := range [][2]string{{"pass", "approved"}, {"fail", "changes_requested"}} {
		if pr := r.Stack.Branches[i].PR; pr.Checks != want[0] || pr.Review != want[1] {
			t.Errorf("PR %d checks, review = %q, %q; want %q", pr.Number, pr.Checks, pr.Review, want)
		}
	}
	f.s.pollStacks(ctx)
	if n := f.cli.count(stackQuick); n != 1 {
		t.Errorf("quick reads after nothing changed = %d, want 1", n)
	}
}

// TestCheckoutUndoneElsewhere: a switch made here that a checkout in a
// terminal undid before the overview showed it. The overview that still
// shows the old branch is taken for a lagging read for headGrace, then
// for the truth.
func TestCheckoutUndoneElsewhere(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.cli.set(stackAll, stackB("jb/b", true, true))
	sub := f.s.subscribe("ftest", "b", parseSort("name"), usageDefault, "") // the PR poll reads for a page open
	defer f.s.unsubscribe(sub)
	f.s.pollOnce(ctx)
	f.s.jobEvent(jobs.Event{Job: jobs.Snapshot{ID: "j1", Spec: jobs.Spec{Project: "ftest", Slug: "b", Label: switchLabel + "jb/c"}, State: jobs.StateOK}})
	f.s.pollOverviews(ctx) // still jb/b, within headGrace
	if got := shownHead(f); got != "jb/c" {
		t.Errorf("HEAD within headGrace = %q, want jb/c", got)
	}
	f.clock.Add(headGrace + time.Second)
	f.s.pollOverviews(ctx)
	if got := shownHead(f); got != "jb/b" {
		t.Errorf("HEAD after headGrace = %q, want jb/b", got)
	}
	f.s.mu.Lock()
	again := f.s.state["ftest"].stackNow["b"]
	f.s.mu.Unlock()
	if !again {
		t.Error("no quick read asked for after the overview moved HEAD")
	}
}

// TestSwitchingFromJobs: the detail's Switching is the branch of the
// worktree's running stack switch job, and clears when it ends.
func TestSwitchingFromJobs(t *testing.T) {
	f := newFixture(t)
	f.s.pollOnce(context.Background())
	f.run.block = make(chan struct{})
	if _, err := f.s.c.Jobs.Start(context.Background(), jobs.Spec{Project: "ftest", Slug: "b", Label: switchLabel + "jb/q", Argv: []string{"railswitch"}}); err != nil {
		t.Fatal(err)
	}
	f.run.wait(t, 1)
	detail := func() string {
		f.s.mu.Lock()
		defer f.s.mu.Unlock()
		return f.s.pageData("ftest", "b", sortKey{By: "name"}, usageDefault).Detail.Switching
	}
	if got := detail(); got != "jb/q" {
		t.Errorf("Switching while running = %q, want jb/q", got)
	}
	close(f.run.block)
	f.run.block = nil
	f.ended(t)
	if got := detail(); got != "" {
		t.Errorf("Switching after the job = %q, want none", got)
	}
}

// TestHeaderRepoLink (#253): the header links the project's repository
// on GitHub when overview has one, and shows no link otherwise.
func TestHeaderRepoLink(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ov := ftestOverview("jb/b", "up", agentJSON("idle", "", false))
	f.cli.set("--json -C /ws/ftest overview", strings.Replace(ov, `"prefix":"ftest",`, `"prefix":"ftest","repo_url":"https://github.com/o/r",`, 1))
	sub := f.s.subscribe("ftest", "b", parseSort("name"), usageDefault, "")
	defer f.s.unsubscribe(sub)
	f.s.pollOnce(ctx)
	link := `<a class="hb" href="https://github.com/o/r" target="_blank" rel="noopener" title="The project's repository on GitHub">GitHub ↗</a>`
	if hd, _ := byID(drain(sub), "header"); !strings.Contains(hd.HTML, link) {
		t.Errorf("no repo link in the header:\n%s", hd.HTML)
	}
	f.cli.set("--json -C /ws/ftest overview", ov)
	f.clock.Add(time.Minute)
	f.s.pollOnce(ctx)
	if hd, ok := byID(drain(sub), "header"); !ok || strings.Contains(hd.HTML, "GitHub ↗") {
		t.Errorf("header without repo_url (sent %v): %s", ok, hd.HTML)
	}
}

// TestStoppedButtons (#255): while a worktree's stack is stopped, Open
// app and Run tests are disabled, and so are the Database tab's take,
// restore and Use, which need its database running; start, the
// terminals and delete stay.
func TestStoppedButtons(t *testing.T) {
	ctx := context.Background()
	for _, stack := range []string{"up", "stopped"} {
		f := newFixture(t)
		ov := ftestOverview("jb/b", stack, agentJSON("idle", "", false))
		ov = strings.Replace(ov, `"slug":"b","id":2,`, `"slug":"b","id":2,"tests":{"state":"never","stale":false,"last":null},`, 1)
		f.cli.set("--json -C /ws/ftest overview", ov)
		sub := f.s.subscribe("ftest", "b", parseSort("name"), usageDefault, "")
		f.s.pollOnce(ctx)
		d, _ := byID(drain(sub), "detail")
		f.s.unsubscribe(sub)
		stopped := stack == "stopped"
		for want, on := range map[string]bool{
			` data-stopped>`: stopped,
			`<a class="ln" aria-disabled="true" title="Start the worktree first">Open app ↗</a>`:                           stopped,
			`<a class="ln" href="http://b.ftest.localhost/" target="_blank" rel="noopener">Open app ↗</a>`:                 !stopped,
			`<button class="ln" disabled title="Start the worktree first: the tests need its services">Run tests</button>`: stopped,
			`<button class="ln">Run tests</button>`:                                                                        !stopped,
		} {
			if strings.Contains(d.HTML, want) != on {
				t.Errorf("%s: detail has %q = %v, want %v", stack, want, !on, on)
			}
		}
		panel := f.do(req("GET", "/p/ftest/b/database-panel", nil)).Body.String()
		if n := strings.Count(panel, " disabled title=\"Start the worktree first: its database must be running\""); stopped && n != 3 || !stopped && n != 0 {
			t.Errorf("%s: %d disabled database buttons:\n%s", stack, n, panel)
		}
		if !strings.Contains(panel, `<button class="ln danger">Delete</button>`) {
			t.Errorf("%s: delete disabled:\n%s", stack, panel)
		}
	}
}
