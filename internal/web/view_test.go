package web

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/jobs"
)

func slugs(rows []row) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.WT.Slug)
	}
	return out
}

func wtAgent(state string, reply bool) *agent {
	return &agent{Name: "bot", State: state, Reply: reply, Running: true}
}

// TestSort: each key, its ties by name, and reverse.
func TestSort(t *testing.T) {
	fixture := func() []row {
		return []row{
			{WT: worktree{Slug: "zeta", Stack: "stopped", Agent: wtAgent("idle", false)}, PR: &pr{Number: 7}},
			{WT: worktree{Slug: "main", Base: true, Stack: "up", Agent: wtAgent("working", false)}},
			{WT: worktree{Slug: "beta", Stack: "partial", Agent: wtAgent("done", false)}, PR: &pr{Number: 2150}},
			{WT: worktree{Slug: "alpha", Stack: "failed", Agent: wtAgent("permission", false)}, PR: &pr{Number: 2141}},
			{WT: worktree{Slug: "gamma", Stack: "up", Agent: wtAgent("done", true)}},
			{WT: worktree{Slug: "delta", Stack: "stopped", Agent: nil}},
			{WT: worktree{Slug: "eps", Stack: "up", Agent: wtAgent("question", false)}, PR: &pr{Number: 9000}},
		}
	}
	for _, c := range []struct {
		key  string
		want []string
	}{
		{"name", []string{"main", "alpha", "beta", "delta", "eps", "gamma", "zeta"}},
		{"", []string{"main", "alpha", "beta", "delta", "eps", "gamma", "zeta"}},
		{"bogus", []string{"main", "alpha", "beta", "delta", "eps", "gamma", "zeta"}},
		{"pr", []string{"eps", "beta", "alpha", "zeta", "main", "delta", "gamma"}},
		{"agent", []string{"alpha", "eps", "gamma", "beta", "main", "zeta", "delta"}},
	} {
		rows := fixture()
		sortRows(rows, parseSort(c.key))
		if got := slugs(rows); !slices.Equal(got, c.want) {
			t.Errorf("sort %q: %q, want %q", c.key, got, c.want)
		}
		if c.key == "" || c.key == "bogus" {
			continue
		}
		rows = fixture()
		sortRows(rows, parseSort("-"+c.key))
		want := slices.Clone(c.want)
		slices.Reverse(want)
		if got := slugs(rows); !slices.Equal(got, want) {
			t.Errorf("sort -%s: %q, want %q", c.key, got, want)
		}
	}
	if k := parseSort("-pr"); k.By != "pr" || !k.Rev || k.String() != "-pr" {
		t.Errorf("parseSort(-pr) = %+v %q", k, k.String())
	}
	if k := parseSort("nope"); k.By != "name" || k.Rev || k.String() != "name" {
		t.Errorf("parseSort(nope) = %+v", k)
	}
}

// TestLabels: agent and PR wording, and the CSS class of each state per
// the spec's colour table.
func TestLabels(t *testing.T) {
	for _, c := range []struct {
		a           *agent
		kind, label string
		class       string
	}{
		{wtAgent("permission", false), "permission", "permission", "need"},
		{wtAgent("question", false), "question", "question", "need"},
		{wtAgent("done", true), "reply", "waiting for reply", "need"},
		{wtAgent("done", false), "turn", "your turn", "yellow"},
		{wtAgent("working", false), "working", "working", "blue"},
		{wtAgent("idle", false), "idle", "idle", "muted"},
		{wtAgent("off", false), "off", "off", "muted"},
		{nil, "off", "off", "muted"},
	} {
		k := agentKind(c.a)
		if k != c.kind || agentLabel(k) != c.label || agentClass(k) != c.class {
			t.Errorf("agent %+v: kind %q label %q class %q; want %q %q %q", c.a, k, agentLabel(k), agentClass(k), c.kind, c.label, c.class)
		}
	}
	for _, c := range []struct {
		p    *pr
		want string
	}{
		{&pr{Number: 2141, State: "open", Checks: "pass", Review: "approved"}, "#2141 ✓ ready · approved"},
		{&pr{Number: 2150, State: "draft", Checks: "fail", Review: "none"}, "#2150 ✗ draft"},
		{&pr{Number: 2133, State: "open", Checks: "pending", Review: "pending"}, "#2133 ◌ ready · pending"},
		{&pr{Number: 5, State: "open", Checks: "none", Review: "changes_requested"}, "#5 ready · changes requested"},
		{&pr{Number: 2098, State: "merged", Checks: "pass", Review: "approved"}, "#2098 ✓ merged"},
		{&pr{Number: 7, State: "closed", Checks: "none", Review: "none"}, "#7 closed"},
		{nil, "—"},
	} {
		if got := prLabel(c.p); got != c.want {
			t.Errorf("prLabel(%+v) = %q, want %q", c.p, got, c.want)
		}
	}
	for state, want := range map[string]string{"up": "green", "partial": "yellow", "stopped": "muted", "failed": "red"} {
		if got := stackClass(state); got != want {
			t.Errorf("stackClass(%s) = %q, want %q", state, got, want)
		}
	}
	for state, want := range map[string]string{"draft": "muted", "open": "green", "merged": "purple", "closed": "red"} {
		if got := prStateClass(state); got != want {
			t.Errorf("prStateClass(%s) = %q, want %q", state, got, want)
		}
	}
	for review, want := range map[string]string{"pending": "yellow", "approved": "blue", "changes_requested": "red"} {
		if got := reviewClass(review); got != want {
			t.Errorf("reviewClass(%s) = %q, want %q", review, got, want)
		}
	}
	for checks, want := range map[string]string{"pass": "green", "pending": "yellow", "fail": "red"} {
		if got := checksClass(checks); got != want {
			t.Errorf("checksClass(%s) = %q, want %q", checks, got, want)
		}
	}
}

func fixtureRow(stack string, base bool, p *pr) row {
	return row{
		WT: worktree{
			Slug: "feat-login", Branch: "jb/feat-login", Base: base, Stack: stack,
			Services: []service{{Service: "web", Role: "app", State: "active/running"}},
			Agent:    &agent{Name: "bot", State: "permission", Message: "Bash: <script>rm</script>", Running: true},
			Links: []link{
				{Label: "App", URL: "http://feat-login.ftest.localhost/", Group: "app"},
				{Label: "Flipper", URL: "http://feat-login.ftest.localhost/flipper", Group: "project"},
				{Label: "Evil", URL: "javascript:alert(1)", Group: "project"},
				{Label: "Browser (VNC)", URL: "http://vnc.feat-login.ftest.localhost", Group: "stack"},
			},
		},
		PR: p,
	}
}

var anchorRe = regexp.MustCompile(`<a [^>]*class="([^"]*)"[^>]*>([^<]*)</a>`)

// TestDetailFragment: the Links card lists the links, App first and main; a
// stopped stack greys its links; remove only for non-base worktrees; a
// merged PR says so on its one remove button; hostile text is escaped.
func TestDetailFragment(t *testing.T) {
	open := &pr{Number: 2141, Title: "x", URL: "https://github.com/o/r/pull/2141", State: "open", Checks: "pass", Review: "approved"}
	html, err := render("detail", newDetail("ftest", fixtureRow("up", false, open), ""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `<a class="pill pr-line green" href="https://github.com/o/r/pull/2141"`) {
		t.Errorf("the header's PR line is no link:\n%s", html)
	}
	// The links are the Links card's.
	li := strings.Index(html, ">Links<")
	if li < 0 {
		t.Fatalf("no Links card:\n%s", html)
	}
	si := li + strings.Index(html[li:], "</section>")
	links := anchorRe.FindAllStringSubmatch(html[li:si], -1)
	if len(links) == 0 || links[0][2] != "App" {
		t.Errorf("first link %q, want App:\n%s", links, html)
	}
	var labels []string
	for _, l := range links {
		labels = append(labels, l[2])
		if l[1] != "ln" { // D208: one style
			t.Errorf("link %s has class %q, want ln", l[2], l[1])
		}
	}
	if want := []string{"App", "Flipper", "Evil", "Browser (VNC)", "PR", "Logs"}; !slices.Equal(labels, want) {
		t.Errorf("links %q, want %q", labels, want)
	}
	for _, want := range []string{"PR #2141 ready · checks ✓ · approved", `target="_blank"`, `rel="noopener"`, "Remove…", "&lt;script&gt;"} {
		if !strings.Contains(html, want) {
			t.Errorf("detail lacks %q:\n%s", want, html)
		}
	}
	for _, bad := range []string{"<script>", "javascript:", "PR merged"} {
		if strings.Contains(html, bad) {
			t.Errorf("detail has %q", bad)
		}
	}

	html, _ = render("detail", newDetail("ftest", fixtureRow("stopped", false, nil), ""))
	// D208: a stopped stack has nothing to open, and its services fold.
	if strings.Contains(html, ">Links<") || !strings.Contains(html, `<details class="svc-fold" data-keep="services"><summary>`) {
		t.Errorf("stopped stack: a Links card, or the services not folded:\n%s", html)
	}

	html, _ = render("detail", newDetail("ftest", fixtureRow("up", true, nil), ""))
	if strings.Contains(html, "remove…") || strings.Contains(html, "/remove") {
		t.Errorf("base worktree offers remove:\n%s", html)
	}

	merged := &pr{Number: 2098, State: "merged", Checks: "pass", Review: "approved", URL: "https://github.com/o/r/pull/2098"}
	html, _ = render("detail", newDetail("ftest", fixtureRow("up", false, merged), ""))
	if !strings.Contains(html, ">PR merged: remove…</a>") || strings.Count(html, `data-dialog="remove"`) != 1 {
		t.Errorf("merged PR: want one remove button saying so:\n%s", html)
	}
}

// TestRowFragment: a row carries its id, the PR at a glance and the
// state classes.
func TestRowFragment(t *testing.T) {
	open := &pr{Number: 2141, Title: "Login <b>", URL: "https://github.com/o/r/pull/2141", State: "open", Checks: "pass", Review: "approved"}
	rv := newRow("ftest", fixtureRow("up", false, open), "feat-login", parseSort("pr"))
	html, err := render("row", rv)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="row-feat-login"`, `class="sel"`, "#2141", "✓", "ready", "approved", `class="green"`, `class="need"`, "permission", `href="/p/ftest/feat-login?sort=pr"`, "Login &lt;b&gt;"} {
		if !strings.Contains(html, want) {
			t.Errorf("row lacks %q:\n%s", want, html)
		}
	}
}

// TestJobsOfRemovedWorktree: a job of a worktree the overview no longer
// has (its `wt rm`) stays in the list; another live worktree's does not.
func TestJobsOfRemovedWorktree(t *testing.T) {
	rows := []row{{WT: worktree{Slug: "main", Base: true}}, {WT: worktree{Slug: "b"}}}
	snaps := []jobs.Snapshot{
		{ID: "1", Spec: jobs.Spec{Slug: "gone", Label: "remove"}, State: jobs.StateOK},
		{ID: "2", Spec: jobs.Spec{Slug: "b", Label: "up"}, State: jobs.StateOK},
		{ID: "3", Spec: jobs.Spec{Slug: "main", Label: "stop"}, State: jobs.StateOK},
		{ID: "4", Spec: jobs.Spec{Label: "wt add x"}, State: jobs.StateOK},
	}
	var got []string
	for _, j := range newJobs(snaps, rows, "main").Jobs {
		got = append(got, j.Label)
	}
	if want := []string{"remove", "stop", "wt add x"}; !slices.Equal(got, want) {
		t.Errorf("jobs %q, want %q", got, want)
	}
	html, err := render("jobs", newJobs(snaps, rows, "main"))
	if err != nil || !strings.Contains(html, `id="job-1"`) {
		t.Errorf("removed worktree's job not rendered (%v):\n%s", err, html)
	}
}

// TestServiceLook: each systemd state reads as one plain word, coloured
// like its glyph.
func TestServiceLook(t *testing.T) {
	for state, want := range map[string][3]string{
		"active/running":    {"●", "green", "running"},
		"active/exited":     {"●", "green", "running"},
		"activating/start":  {"◐", "yellow", "starting"},
		"deactivating/stop": {"◐", "yellow", "stopping"},
		"failed/failed":     {"✗", "red", "failed"},
		"inactive/dead":     {"○", "muted", "stopped"},
		"on-demand":         {"○", "muted", "on demand"},
		"reloading/reload":  {"◐", "yellow", "reloading"},
		"maintenance/clean": {"◐", "yellow", "busy"},
		"/":                 {"○", "muted", "unknown"},
	} {
		g, c, w := serviceLook(state)
		if got := [3]string{g, c, w}; got != want {
			t.Errorf("%s: %q; want %q", state, got, want)
		}
	}
}

// TestStackedView: PR numbers link through the project's PR URL (any
// row's), a queued PR is coloured, and a hostile branch name is text.
func TestStackedView(t *testing.T) {
	rows := []row{{WT: worktree{Slug: "main"}}, {WT: worktree{Slug: "b"}, PR: &pr{URL: "https://github.com/o/r/pull/7"}}}
	repo := projectPRRepo(rows)
	if repo != "https://github.com/o/r/pull/" {
		t.Fatalf("projectPRRepo = %q", repo)
	}
	st := &ghStackRow{Slug: "c", Stack: &ghStack{Trunk: "main", Current: "<b>x", Branches: []ghStackBranch{
		{Name: "jb/q", Queued: true},
		{Name: "<b>x", Current: true},
	}}}
	st.Stack.Branches[0].PR = &ghStackPR{Number: 9, State: "queued"}
	d := newDetail("ftest", row{WT: worktree{Slug: "c", Branch: "<b>x"}, Stacked: st}, repo)
	html, err := render("detail", d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`href="https://github.com/o/r/pull/9"`, `class="pr blue"`, "&lt;b&gt;x", ">queued</span>",
		// another branch is a switch form; the stack's actions follow
		`action="/p/ftest/c/stack/switch"`, `name="branch" value="jb/q"`,
		`action="/p/ftest/c/act/term"`} {
		if !strings.Contains(html, want) {
			t.Errorf("detail lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "<b>x") {
		t.Error("a branch name reached the page as HTML")
	}
	for _, act := range []string{"restack", "stack-push", "stack-up", "stack-down"} {
		if strings.Contains(html, "/act/"+act) {
			t.Errorf("a %s button", act)
		}
	}
	if strings.Contains(html, `value="&lt;b&gt;x"`) {
		t.Error("the current branch is offered as a switch")
	}
	// No stack, no stack actions.
	html, _ = render("detail", newDetail("ftest", row{WT: worktree{Slug: "c"}}, repo))
	if strings.Contains(html, "stack/switch") {
		t.Errorf("stack actions without a stack:\n%s", html)
	}
}

// TestStackSwitching: while a switch runs, its branch says so and no
// branch is a switch form, so a second click can't race the first.
func TestStackSwitching(t *testing.T) {
	st := &ghStackRow{Slug: "c", Stack: &ghStack{Trunk: "main", Current: "jb/a", Branches: []ghStackBranch{
		{Name: "jb/a", Current: true}, {Name: "jb/q"}, {Name: "jb/r"},
	}}}
	d := newDetail("ftest", row{WT: worktree{Slug: "c", Branch: "jb/a"}, Stacked: st}, "")
	d.Switching = "jb/q"
	html, err := render("detail", d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `<span class="mono">jb/q</span> <span class="blue switching">◐ switching…</span>`) {
		t.Errorf("jb/q not marked switching:\n%s", html)
	}
	if strings.Contains(html, "stack-switch") {
		t.Errorf("a switch form while switching:\n%s", html)
	}
}

// TestDetailAgentTerms: each configured agent (a service agent-<name>,
// on demand or not) gets a terminal, marked while it runs; the Terminal
// menu and the Agent card both offer them.
func TestDetailAgentTerms(t *testing.T) {
	r := fixtureRow("up", false, nil)
	r.WT.Services = append(r.WT.Services,
		service{Service: "agent-claude", Role: "agent", State: "active/running"},
		service{Service: "agent-omp", Role: "agent", State: "on-demand"})
	d := newDetail("ftest", r, "")
	if len(d.AgentTerms) != 2 || d.AgentTerms[0] != (agentTerm{Name: "claude", URL: "/p/ftest/tty/feat-login/agent?agent=claude", Running: true}) ||
		d.AgentTerms[1] != (agentTerm{Name: "omp", URL: "/p/ftest/tty/feat-login/agent?agent=omp"}) {
		t.Fatalf("agent terms %+v", d.AgentTerms)
	}
	html, err := render("detail", d)
	if err != nil {
		t.Fatal(err)
	}
	// D176: claude runs, so its terminal is in the menu and its row of the
	// card; omp's is in the menu, and its row switches to it.
	for url, want := range map[string]int{"agent?agent=claude": 2, "agent?agent=omp": 1} {
		if n := strings.Count(html, `data-term="/p/ftest/tty/feat-login/`+url+`"`); n != want {
			t.Errorf("%s: %d terminal buttons, want %d", url, n, want)
		}
	}
	for _, want := range []string{
		`<tr class="on"><td class="ag-name"><span class="agent-dot" aria-hidden="true"></span><b class="mono">claude</b></td><td class="ag-state">running`,
		`<tr><td class="ag-name"><span class="agent-dot" aria-hidden="true"></span><b class="mono">omp</b></td><td class="ag-state"><span class="muted">stopped</span>`,
		`>Open terminal</button>`,
		`<button type="button" class="ln" data-switch-url="/p/ftest/feat-login/agent-stop" data-switch-from="claude" data-switch-term="/p/ftest/tty/feat-login/agent?agent=omp" data-switch-to="omp" title="Stop claude and start omp">Switch</button>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("card lacks %q:\n%s", want, html)
		}
	}
	if !strings.Contains(html, "Agent: claude ● ▣") || !strings.Contains(html, "Agent: omp ▣") {
		t.Errorf("menu entries don't name the agents or mark the running one:\n%s", html)
	}
	// None running: no row is on, a Start per agent, the first the main one.
	r.WT.Services[len(r.WT.Services)-2].State = "inactive/dead"
	idle, err := render("detail", newDetail("ftest", r, ""))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(idle, `<tr class="on">`) || !strings.Contains(idle, `class="ln main" data-term="/p/ftest/tty/feat-login/agent?agent=claude"`) ||
		!strings.Contains(idle, `title="Start omp">Start</button>`) || strings.Contains(idle, "Stop claude") || strings.Contains(idle, ">Switch<") {
		t.Errorf("idle card wrong:\n%s", idle)
	}
	// D176: the running agent has a Stop; the stopped one doesn't.
	if !strings.Contains(html, `<form method="post" action="/p/ftest/feat-login/agent-stop" data-post data-confirm="Stop claude?`) ||
		!strings.Contains(html, `<input type="hidden" name="agent" value="claude"><button class="ln danger" title="Stop claude">Stop</button>`) || strings.Contains(html, "Stop omp") {
		t.Errorf("Stop buttons wrong:\n%s", html)
	}
	// D188: every agent can have its setup run again, after a confirm.
	for _, n := range []string{"claude", "omp"} {
		if !strings.Contains(html, `<form method="post" action="/p/ftest/feat-login/agent-setup" data-post data-confirm="Run `+n+`'s setup commands again?`) {
			t.Errorf("no setup form for %s:\n%s", n, html)
		}
	}
}

// TestDetailActions: the actions follow the stack's state; a running
// service links its logs in Dozzle by container name and, when it has a
// unit of its own, restarts alone.
func TestDetailActions(t *testing.T) {
	acts := func(stack string) []string {
		var out []string
		for _, a := range newDetail("ftest", fixtureRow(stack, false, nil), "").Actions {
			out = append(out, a.Label)
		}
		return out
	}
	for stack, want := range map[string][]string{
		"stopped": {"start"},
		"up":      {"restart", "stop"},
		"partial": {"start", "restart", "stop"},
		"failed":  {"start", "restart", "stop"},
	} {
		if got := acts(stack); !slices.Equal(got, want) {
			t.Errorf("%s: actions %v; want %v", stack, got, want)
		}
	}

	r := fixtureRow("partial", false, nil)
	r.WT.Services = []service{
		{Service: "web", Role: "app", State: "active/running", Container: "ftest-feat-login-web", Restartable: true},
		{Service: "db", Role: "data", State: "inactive/dead", Container: "ftest-feat-login-db", Restartable: true},
		{Service: "agent-bot", Role: "agent", State: "active/running", Container: "ftest-feat-login-agent-bot"},
		{Service: "agent-svc", Role: "agent", State: "active/running", Container: "ftest-feat-login-agent-svc", Restartable: true},
		{Service: "deps", Role: "deps", State: "inactive/dead", Container: "ftest-feat-login-deps"},
	}
	d := newDetail("ftest", r, "")
	web, db, bot, svc, deps := d.Services[0], d.Services[1], d.Services[2], d.Services[3], d.Services[4]
	// a oneshot's log is its journal, not Dozzle's: a job prints it, even once it has exited (D186)
	if deps.LogsURL != "" || deps.LogsPost != "/p/ftest/feat-login/logs/deps" || web.LogsPost != "" {
		t.Errorf("deps: %+v; web: %+v", deps, web)
	}
	if web.LogsURL != "http://logs.localhost/show?name=ftest-feat-login-web" || web.RestartURL != "/p/ftest/feat-login/restart/web" {
		t.Errorf("web: %+v", web)
	}
	if db.LogsURL != "" || db.RestartURL != "" {
		t.Errorf("stopped db: %+v", db)
	}
	if bot.LogsURL == "" || bot.RestartURL != "" {
		t.Errorf("on-demand agent: %+v", bot)
	}
	// a service agent restarts (D148), after a confirm: its session ends
	if svc.RestartURL != "/p/ftest/feat-login/restart/agent-svc" || !strings.Contains(svc.Confirm, "Its session ends") || web.Confirm != "" {
		t.Errorf("service agent: %+v", svc)
	}
	html, err := render("detail", d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<a href="http://logs.localhost/show?name=ftest-feat-login-web" target="_blank" rel="noopener"`, `<form method="post" action="/p/ftest/feat-login/restart/web" data-post>`, `action="/p/ftest/feat-login/restart/agent-svc" data-post data-confirm="Restart agent-svc? Its session ends`, `<form method="post" action="/p/ftest/feat-login/logs/deps" data-post><button title="Print the journal of deps in Jobs">Logs</button></form>`} {
		if !strings.Contains(html, want) {
			t.Errorf("detail lacks %q", want)
		}
	}
}

// TestReviewAppLink: a PR with a review app links it next to PR, on a
// stopped stack too (the PR's CI deployed it, not this stack).
func TestReviewAppLink(t *testing.T) {
	p := &pr{Number: 7, URL: "https://github.com/o/r/pull/7", State: "open", ReviewApp: "https://app-pr-7.example.dev"}
	d := newDetail("ftest", fixtureRow("stopped", false, p), "")
	i := slices.IndexFunc(d.Links, func(l linkView) bool { return l.Label == "Review app" })
	if i < 0 || d.Links[i].URL != "https://app-pr-7.example.dev" || strings.Contains(d.Links[i].Class, "dim") || d.Links[i-1].Label != "PR" {
		t.Errorf("links %+v", d.Links)
	}
	if d := newDetail("ftest", fixtureRow("up", false, &pr{Number: 8, URL: "https://github.com/o/r/pull/8", State: "open"}), ""); slices.ContainsFunc(d.Links, func(l linkView) bool { return l.Label == "Review app" }) {
		t.Error("a Review app link without one")
	}
}

// TestRowNeedMarker: a worktree whose agent waits on you says so under its
// name, where the row never scrolls out of view; the branch is cut short
// with its full name as the tooltip.
func TestRowNeedMarker(t *testing.T) {
	r := fixtureRow("up", false, nil) // its agent asks for permission
	html, err := render("row", newRow("ftest", r, "", sortKey{By: "name"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<div class="need" title="bot: Bash: &lt;script&gt;rm&lt;/script&gt;">◆ permission</div>`, `<span class="trunc" title="jb/feat-login">jb/feat-login</span>`} {
		if !strings.Contains(html, want) {
			t.Errorf("row lacks %q:\n%s", want, html)
		}
	}
	r.WT.Agent.State = "working"
	if html, _ := render("row", newRow("ftest", r, "", sortKey{By: "name"})); strings.Contains(html, `class="need"`) {
		t.Errorf("a working agent marked as needing input:\n%s", html)
	}
}

// TestDetailTerminals: the Terminal menu opens the agent (named when the
// overview names one), nvim when the worktree runs it, and a shell; the
// dock for the panels sits outside the detail, which SSE replaces.
func TestDetailTerminals(t *testing.T) {
	r := fixtureRow("up", false, nil)
	html, err := render("detail", newDetail("ftest", r, ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`data-term="/p/ftest/tty/feat-login/agent?agent=bot"`, `data-term="/p/ftest/tty/feat-login/shell"`,
		// the desktop window sits in the Terminal menu, after the panels
		`shell ▣</button><form method="post" action="/p/ftest/feat-login/act/term"`,
		`<span class="mono br" title="branch">⎇ jb/feat-login</span>`} {
		if !strings.Contains(html, want) {
			t.Errorf("detail lacks %s", want)
		}
	}
	if strings.Contains(html, "/tty/feat-login/nvim") {
		t.Error("an nvim button without an nvim service")
	}
	// The Agent card opens the same terminal as the menu's agent item.
	if n := strings.Count(html, `data-term="/p/ftest/tty/feat-login/agent?agent=bot"`); n != 2 {
		t.Errorf("agent terminal buttons: %d, want 2 (Terminal menu, Agent card)", n)
	}
	r.WT.Services = append(r.WT.Services, service{Service: "nvim", Role: "tool", State: "active/running"})
	r.WT.Agent = nil
	html, _ = render("detail", newDetail("ftest", r, ""))
	for _, want := range []string{`data-term="/p/ftest/tty/feat-login/nvim"`, `data-term="/p/ftest/tty/feat-login/agent"`} {
		if !strings.Contains(html, want) {
			t.Errorf("detail lacks %s", want)
		}
	}
	if strings.Contains(html, `id="terms"`) {
		t.Error("the dock is inside the detail")
	}
	// a branch named as the slug isn't repeated under it
	r.WT.Branch = r.WT.Slug
	if html, _ = render("detail", newDetail("ftest", r, "")); strings.Contains(html, "⎇") {
		t.Errorf("the branch repeats the slug:\n%s", html)
	}
	// the dock comes after the jobs, which stay in view
	f := newFixture(t)
	page := f.do(req("GET", "/p/ftest/b", nil)).Body.String()
	if !strings.Contains(page, `<div id="terms" class="terms"></div>`) || strings.Index(page, `id="jobs"`) > strings.Index(page, `id="terms"`) {
		t.Error("the page has no terminal dock under the jobs")
	}
}

// TestListGroups (D170): Needs you holds an agent waiting on you (even in
// a stopped stack) and a failed stack, Running an up or partial one,
// Stopped the rest; each group keeps the rows' order, an empty one goes.
func TestListGroups(t *testing.T) {
	rows := []row{
		{WT: worktree{Slug: "a-up", Stack: "up"}},
		{WT: worktree{Slug: "b-stopped", Stack: "stopped"}},
		{WT: worktree{Slug: "c-partial", Stack: "partial"}},
		{WT: worktree{Slug: "d-failed", Stack: "failed"}},
		{WT: worktree{Slug: "e-waits", Stack: "stopped", Agent: wtAgent("done", true)}},
		{WT: worktree{Slug: "f-up-asks", Stack: "up", Agent: wtAgent("question", false)}},
	}
	var got []string
	for _, g := range newList("ftest", rows, "", sortKey{By: "name"}).Groups {
		got = append(got, g.Name+":")
		for _, r := range g.Rows {
			got = append(got, r.Slug)
		}
	}
	want := []string{"Needs you:", "d-failed", "e-waits", "f-up-asks", "Running:", "a-up", "c-partial", "Stopped:", "b-stopped"}
	if !slices.Equal(got, want) {
		t.Errorf("groups %q, want %q", got, want)
	}
	if g := newList("ftest", rows[:1], "", sortKey{By: "name"}).Groups; len(g) != 1 || g[0].Name != "Running" {
		t.Errorf("one up worktree: groups %+v, want Running alone", g)
	}
}

// TestStoppedAgentSaysNothing: an agent whose container doesn't run is
// off, whatever its status file last said: no "waiting for reply" in the
// row, the detail or the Needs you group.
func TestStoppedAgentSaysNothing(t *testing.T) {
	r := row{WT: worktree{Slug: "w", Stack: "stopped", Agent: &agent{Name: "bot", State: "done", Reply: true, Message: "Ship it?"}}}
	if k := agentKind(r.WT.Agent); k != "off" {
		t.Errorf("stopped agent's kind %q, want off", k)
	}
	html, _ := render("row", newRow("ftest", r, "", sortKey{By: "name"}))
	if strings.Contains(html, "waiting for reply") || strings.Contains(html, "Ship it?") {
		t.Errorf("row of a stopped agent:\n%s", html)
	}
	d := newDetail("ftest", r, "")
	if d.Agent == nil || d.Agent.Label != "off" || d.Agent.Message != "" {
		t.Errorf("detail agent %+v, want off without its message", d.Agent)
	}
	if g := newList("ftest", []row{r}, "", sortKey{By: "name"}).Groups; len(g) != 1 || g[0].Name != "Stopped" {
		t.Errorf("groups %+v, want Stopped", g)
	}
}
