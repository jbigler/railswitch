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
	return &agent{Name: "bot", State: state, Reply: reply}
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
		{"running", []string{"alpha", "main", "eps", "gamma", "beta", "delta", "zeta"}},
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
			Agent:    &agent{Name: "bot", State: "permission", Message: "Bash: <script>rm</script>"},
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

// TestDetailFragment: Links come before Services, App first and main; a
// stopped stack greys its links; remove only for non-base worktrees; a
// merged PR offers "Remove worktree"; hostile text is escaped.
func TestDetailFragment(t *testing.T) {
	open := &pr{Number: 2141, Title: "x", URL: "https://github.com/o/r/pull/2141", State: "open", Checks: "pass", Review: "approved"}
	html, err := render("detail", newDetail("ftest", fixtureRow("up", false, open), ""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `<a class="pr-line green" href="https://github.com/o/r/pull/2141"`) {
		t.Errorf("the header's PR line is no link:\n%s", html)
	}
	li, si := strings.Index(html, ">Links<"), strings.Index(html, ">Services<")
	if li < 0 || si < 0 || li > si {
		t.Errorf("Links at %d, Services at %d:\n%s", li, si, html)
	}
	links := anchorRe.FindAllStringSubmatch(html[li:si], -1)
	if len(links) == 0 || links[0][2] != "App" || !strings.Contains(" "+links[0][1]+" ", " main ") {
		t.Errorf("first link %q, want App with class main:\n%s", links, html)
	}
	var labels []string
	for _, l := range links {
		labels = append(labels, l[2])
		if strings.Contains(" "+l[1]+" ", " dim ") {
			t.Errorf("running stack: link %s has class dim", l[2])
		}
	}
	if want := []string{"App", "Flipper", "Evil", "Browser (VNC)", "PR", "Logs"}; !slices.Equal(labels, want) {
		t.Errorf("links %q, want %q", labels, want)
	}
	for _, want := range []string{"PR #2141 ready · checks ✓ · approved", `target="_blank"`, `rel="noopener"`, "remove…", "&lt;script&gt;"} {
		if !strings.Contains(html, want) {
			t.Errorf("detail lacks %q:\n%s", want, html)
		}
	}
	for _, bad := range []string{"<script>", "javascript:", "Remove worktree"} {
		if strings.Contains(html, bad) {
			t.Errorf("detail has %q", bad)
		}
	}

	html, _ = render("detail", newDetail("ftest", fixtureRow("stopped", false, nil), ""))
	li, si = strings.Index(html, ">Links<"), strings.Index(html, ">Services<")
	for _, l := range anchorRe.FindAllStringSubmatch(html[li:si], -1) {
		if l[2] == "Logs" {
			continue
		}
		if !strings.Contains(" "+l[1]+" ", " dim ") {
			t.Errorf("stopped stack: link %s class %q lacks dim", l[2], l[1])
		}
	}

	html, _ = render("detail", newDetail("ftest", fixtureRow("up", true, nil), ""))
	if strings.Contains(html, "remove…") || strings.Contains(html, "Remove worktree") || strings.Contains(html, "/remove") {
		t.Errorf("base worktree offers remove:\n%s", html)
	}

	merged := &pr{Number: 2098, State: "merged", Checks: "pass", Review: "approved", URL: "https://github.com/o/r/pull/2098"}
	html, _ = render("detail", newDetail("ftest", fixtureRow("up", false, merged), ""))
	if !strings.Contains(html, "Remove worktree") {
		t.Errorf("merged PR without Remove worktree:\n%s", html)
	}
}

// TestRowFragment: a row carries its id, the PR at a glance and the
// state classes.
func TestRowFragment(t *testing.T) {
	open := &pr{Number: 2141, Title: "Login <b>", URL: "https://github.com/o/r/pull/2141", State: "open", Checks: "pass", Review: "approved"}
	rv := newRow("ftest", fixtureRow("up", false, open), "feat-login", parseSort("running"))
	html, err := render("row", rv)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="row-feat-login"`, `class="sel"`, "#2141", "✓", "ready", "approved", `class="green"`, `class="need"`, "permission", `href="/p/ftest/feat-login?sort=running"`, "Login &lt;b&gt;"} {
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
		"stopped": {"start", "terminal"},
		"up":      {"restart", "stop", "terminal"},
		"partial": {"start", "restart", "stop", "terminal"},
		"failed":  {"start", "restart", "stop", "terminal"},
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
	}
	d := newDetail("ftest", r, "")
	web, db, bot := d.Services[0], d.Services[1], d.Services[2]
	if web.LogsURL != "http://logs.localhost/show?name=ftest-feat-login-web" || web.RestartURL != "/p/ftest/feat-login/restart/web" {
		t.Errorf("web: %+v", web)
	}
	if db.LogsURL != "" || db.RestartURL != "" {
		t.Errorf("stopped db: %+v", db)
	}
	if bot.LogsURL == "" || bot.RestartURL != "" {
		t.Errorf("agent: %+v", bot)
	}
	html, err := render("detail", d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<a href="http://logs.localhost/show?name=ftest-feat-login-web" target="_blank" rel="noopener"`, `<form method="post" action="/p/ftest/feat-login/restart/web" data-post>`} {
		if !strings.Contains(html, want) {
			t.Errorf("detail lacks %q", want)
		}
	}
}
