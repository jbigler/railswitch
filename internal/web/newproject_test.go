package web

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/jobs"
)

const testHome = "/home/u"

// A repository URL that a shell would split and run: it must stay one
// argv entry.
const hostileRepo = "https://h.example/o/r p;$(id).git"

const catalogJSON = `{"packs":[{"ref":"lamp@1","name":"lamp"},{"ref":"web@1","name":"web"}],
 "options":[
  {"ref":"jobq@1","name":"jobq","slot":"jobs","works_with":["web"]},
  {"ref":"kv@1","name":"kv","slot":"cache","works_with":["*"]},
  {"ref":"my@1","name":"my","slot":"database","works_with":["lamp"]},
  {"ref":"pg@1","name":"pg","slot":"database","works_with":["*"]}],
 "agents":[{"ref":"bot@1","name":"bot","default":true},{"ref":"helper@1","name":"helper","default":false}]}`

const detectJSON = `{"branch":"trunk","pack":{"ref":"web@1","evidence":["Gemfile","config/app.rb"],"others":["lamp@1"]},
 "options":[
  {"key":"database","ref":"pg@1","source":"repo","evidence":"Gemfile: pg","others":[]},
  {"key":"cache","ref":"none","source":"default","evidence":"","others":[]},
  {"key":"jobs","ref":"jobq@1","source":"repo","evidence":"Gemfile: jobq","others":[]}]}`

const dryRunJSON = `{"project_toml":"schema = 1\n\n[project]\nprefix = \"masto\"\n",
 "worktree":{"slug":"trunk","routes":[{"host":"trunk.masto.localhost","service":"web"}],
  "containers":[{"name":"masto-trunk-web","role":"app"},{"name":"masto-trunk-db","role":"data"}],
  "env":["DATABASE_URL","REDIS_URL"]},
 "warnings":["web: a lint warning"],
 "steps":["Clone the repository (trunk) into /home/u/workspace/masto/trunk","Write project.toml and stack.lock, generate local passwords","Register, lock and render"]}`

// newProjectFixture is the fixture with the new page's reads scripted.
func newProjectFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.s.c.Home = testHome
	f.cli.set("--json pack ls", catalogJSON)
	f.cli.set("--json project detect "+hostileRepo, detectJSON)
	return f
}

// endedIn waits for project prefix's newest job to end.
func (f *fixture) endedIn(t *testing.T, prefix string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for js := f.s.c.Jobs.Jobs(prefix, ""); len(js) == 0 || js[0].State == jobs.StateRunning; js = f.s.c.Jobs.Jobs(prefix, "") {
		if time.Now().After(deadline) {
			t.Fatalf("no job of %s ended", prefix)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// stepOne is a filled step 1: the hostile repository, the stack as read
// with cache changed, one extra service, nvim and the helper agent.
func stepOne(extra ...string) url.Values {
	return form(append([]string{
		"repo", hostileRepo, "branch", "trunk", "name", "Masto Don", "prefix", "masto", "dir", "~/workspace/masto",
		"pack", "web@1", "opt-database", "pg@1", "opt-cache", "kv@1", "opt-jobs", "jobq@1",
		"option", "analytics=pg@1", "tool", "nvim", "agent", "helper=helper@1",
	}, extra...)...)
}

var stepOneArgs = []string{"project", "new", "/home/u/workspace/masto", "--repo", hostileRepo, "--prefix", "masto",
	"--name", "Masto Don", "--branch", "trunk", "--pack", "web@1",
	"--option", "cache=kv@1", "--option", "database=pg@1", "--option", "jobs=jobq@1", "--option", "analytics=pg@1",
	"--tool", "nvim", "--agent", "helper=helper@1"}

func TestNewPageRenders(t *testing.T) {
	f := newProjectFixture(t)
	w := f.do(req("GET", "/new", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, want := range []string{
		`data-view="new" data-home="/home/u"`, `<h2>1 · Stack and options</h2>`, `<h2>2 · Review and create</h2>`,
		`name="repo"`, `name="branch"`, `name="name"`, `name="prefix"`, `name="dir"`,
		"Must be unused on this machine.", "Read the repository", "Next: review",
		`<input type="checkbox" name="tool" value="nvim">`,
		`<input type="checkbox" name="agent" value="bot=bot@1" checked>`,
		`<input type="checkbox" name="agent" value="helper=helper@1">`,
		`id="new-stack" class="card" data-pack=""`, `id="jobs"`, `href="/new">New project…</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("new page lacks %q", want)
		}
	}
	if strings.Contains(body, "/p//") {
		t.Error("new page links to a project with no prefix")
	}
	noCSPViolation(t, "new", body)

	f.cli.fail("--json pack ls", errors.New("pack ls broke"))
	if body := f.do(req("GET", "/new", nil)).Body.String(); !strings.Contains(body, "Agents unavailable: pack ls broke") {
		t.Error("a pack ls failure is not shown")
	}
}

// TestNewDetectPost: Read runs project detect with the repository as one
// argv entry and answers the Stack card: the pack with its evidence, a
// row per slot labelled by its source, choices that work with the pack.
func TestNewDetectPost(t *testing.T) {
	f := newProjectFixture(t)
	w := f.do(req("POST", "/new/detect", form("repo", hostileRepo, "branch", "", "name", "ignored")))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := f.cli.Calls()[0]; !slices.Equal(got, []string{"--json", "project", "detect", hostileRepo}) {
		t.Errorf("detect argv %q", got)
	}
	body := w.Body.String()
	for _, want := range []string{
		`id="new-stack" class="card" data-pack="web@1" data-branch="trunk"`,
		`<span class="mono">web@1</span> <span class="muted">· read from the repository · Gemfile, config/app.rb</span>`,
		`Also matched: <span class="mono">lamp@1</span>`,
		`<option value="web@1" selected>web@1</option>`, `<option value="lamp@1">lamp@1</option>`,
		`data-key="database" data-ref="pg@1" data-source="repo" data-title="Database" data-label="Read from the repository · Gemfile: pg"`,
		`data-key="cache" data-ref="none" data-source="default" data-title="Cache" data-label="Pack default"`,
		`<input type="radio" name="opt-database" value="pg@1" checked>`, `<input type="radio" name="opt-database" value="none">`,
		`<input type="radio" name="opt-cache" value="kv@1">`, `<input type="radio" name="opt-cache" value="none" checked> <span class="mono">none</span> <span class="tag">pack default</span>`,
		`value="pg@1" checked> <span class="mono">pg@1</span> <span class="tag">suggested</span>`,
		`<input type="radio" name="opt-jobs" value="jobq@1" checked>`,
		`<option value="jobq@1">jobq@1</option><option value="kv@1">kv@1</option><option value="pg@1">pg@1</option></select>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("stack card lacks %q", want)
		}
	}
	if strings.Contains(body, "my@1") {
		t.Error("an option that works only with another pack is offered")
	}
	noCSPViolation(t, "stack", body)

	// The picked pack and a branch go along; a lamp stack offers my@1.
	f.cli.set("--json project detect "+hostileRepo+" --branch dev --pack lamp@1",
		`{"branch":"dev","pack":{"ref":"lamp@1","evidence":[],"others":[]},"options":[{"key":"database","ref":"my@1","source":"default","evidence":"","others":[]}]}`)
	w = f.do(req("POST", "/new/detect", form("repo", hostileRepo, "branch", "dev", "pack", "lamp@1")))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="opt-database" value="my@1" checked>`) || strings.Contains(w.Body.String(), "jobq@1") {
		t.Errorf("lamp read: %d %s", w.Code, w.Body)
	}

	// No pack matched.
	f.cli.set("--json project detect git@h:o/empty.git", `{"branch":"main","pack":null,"options":[]}`)
	body = f.do(req("POST", "/new/detect", form("repo", "git@h:o/empty.git"))).Body.String()
	if !strings.Contains(body, "No pack matched; pick one") || !strings.Contains(body, `<option value="">Pick a pack</option>`) || strings.Contains(body, "opt-") {
		t.Errorf("no-pack card %s", body)
	}

	// git's error is the answer, 422.
	f.cli.fail("--json project detect git@h:o/gone.git", errors.New("fatal: repository not found"))
	if w := f.do(req("POST", "/new/detect", form("repo", "git@h:o/gone.git"))); w.Code != 422 || !strings.Contains(w.Body.String(), "repository not found") {
		t.Errorf("git failure: %d %s", w.Code, w.Body)
	}
}

// TestNewRefusals: a value that could pass for a flag, a line break, a
// bad prefix or folder: 400, nothing run.
func TestNewRefusals(t *testing.T) {
	f := newProjectFixture(t)
	for _, c := range []struct {
		path string
		vals []string
	}{
		{"/new/detect", []string{"repo", "--upload-pack=touch /tmp/x"}},
		{"/new/detect", []string{"repo", "git@h:o/r.git\n--x"}},
		{"/new/detect", []string{"repo", ""}},
		{"/new/detect", []string{"repo", "git@h:o/r.git", "branch", "-b"}},
		{"/new/review", []string{"repo", "-x"}},
		{"/new/review", []string{"prefix", "Masto"}},
		{"/new/review", []string{"prefix", "masto-x"}},
		{"/new/review", []string{"dir", "workspace/masto"}},
		{"/new/review", []string{"dir", "-rf"}},
		{"/new/review", []string{"name", "x\ny"}},
		{"/new/review", []string{"pack", ""}},
		{"/new/create", []string{"branch", "--orphan"}},
		{"/new/create", []string{"option", "--x"}},
		{"/new/create", []string{"opt-cache", "-kv@1"}},
		{"/new/create", []string{"agent", "bot"}},
		{"/new/create", []string{"tool", "emacs"}},
	} {
		v := stepOne()
		for i := 0; i+1 < len(c.vals); i += 2 {
			v[c.vals[i]] = []string{c.vals[i+1]}
		}
		if w := f.do(req("POST", c.path, v)); w.Code != http.StatusBadRequest {
			t.Errorf("%s %q: %d %s", c.path, c.vals, w.Code, w.Body)
		}
	}
	if calls := f.cli.Calls(); len(calls) != 0 {
		t.Errorf("CLI calls on refusal: %q", calls)
	}
	time.Sleep(10 * time.Millisecond)
	if got := f.run.Argvs(); len(got) != 0 {
		t.Errorf("jobs on refusal: %q", got)
	}
}

// TestNewReviewPost: Next runs the dry run of the form and shows what it
// writes and gives; a refusal is at the top and Create is off.
func TestNewReviewPost(t *testing.T) {
	f := newProjectFixture(t)
	key := "--json " + strings.Join(stepOneArgs, " ") + " --dry-run"
	f.cli.set(key, dryRunJSON)
	w := f.do(req("POST", "/new/review", stepOne()))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got, want := f.cli.Calls()[0], append(append([]string{"--json"}, stepOneArgs...), "--dry-run"); !slices.Equal(got, want) {
		t.Errorf("dry-run argv %q\nwant %q", got, want)
	}
	body := w.Body.String()
	for _, want := range []string{
		`<span class="mono">trunk.masto.localhost</span> <span class="muted">→ web</span>`,
		`<span class="mono">masto-trunk-db</span> <span class="muted">data</span>`,
		"DATABASE_URL, REDIS_URL", `<p class="yellow">web: a lint warning</p>`,
		"schema = 1\n\n[project]\nprefix = &#34;masto&#34;\n</pre>",
		"<li>Register, lock and render</li>",
		`<input type="checkbox" name="up" value="1" checked> Start <span class="mono">trunk</span> when it's created`,
		`<button class="ln main">Create project</button>`, `data-stale hidden`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("review lacks %q", want)
		}
	}
	noCSPViolation(t, "review", body)

	f.cli.fail(key, errors.New("prefix masto is used by /ws/masto"))
	body = f.do(req("POST", "/new/review", stepOne())).Body.String()
	if !strings.Contains(body, `class="red set-error refusal">prefix masto is used by /ws/masto</p>`) ||
		!strings.Contains(body, `<button class="ln main" disabled>Create project</button>`) || strings.Contains(body, "Each worktree gets") {
		t.Errorf("refused review %s", body)
	}
}

// TestNewCreateJob: Create runs project new with the form's flags, each
// its own argv entry, --up when ticked, as a project-level job of the new
// prefix run from home; a second create of the prefix is busy.
func TestNewCreateJob(t *testing.T) {
	f := newProjectFixture(t)
	f.run.block = make(chan struct{})
	r := req("POST", "/new/create", stepOne("up", "1"))
	r.Header.Set("Accept", "application/json")
	w := f.do(r)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"label":"project new masto"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	want := append(append([]string{testExe}, stepOneArgs...), "--up")
	if got := f.run.wait(t, 1)[0]; !slices.Equal(got, want) {
		t.Errorf("argv %q\nwant %q", got, want)
	}
	js := f.s.c.Jobs.Jobs("masto", "")
	if len(js) != 1 || js[0].Spec.Slug != "" || js[0].Spec.Dir != testHome || js[0].State != jobs.StateRunning {
		t.Errorf("jobs %+v", js)
	}
	if w := f.do(req("POST", "/new/create", stepOne())); w.Code != http.StatusConflict {
		t.Errorf("second create while the first runs: %d", w.Code)
	}
	close(f.run.block)
	f.run.mu.Lock()
	f.run.block = nil
	f.run.mu.Unlock()
	f.endedIn(t, "masto")

	v := stepOne()
	delete(v, "agent")
	delete(v, "tool")
	delete(v, "name")
	f.do(req("POST", "/new/create", v))
	got := f.run.wait(t, 2)[1]
	if slices.Contains(got, "--up") || slices.Contains(got, "--name") || slices.Contains(got, "--tool") {
		t.Errorf("argv %q", got)
	}
	if !slices.Equal(got[len(got)-2:], []string{"--agent", "none"}) {
		t.Errorf("no agent ticked: argv %q, want --agent none", got)
	}
}

// TestNewGuard: the new page's POSTs need the cookie and the page's
// Origin; refused, nothing runs.
func TestNewGuard(t *testing.T) {
	f := newProjectFixture(t)
	for _, path := range []string{"/new/detect", "/new/review", "/new/create"} {
		for _, c := range []struct {
			name   string
			mutate func(*http.Request)
			code   int
		}{
			{"no cookie", func(r *http.Request) { r.Header.Del("Cookie") }, 401},
			{"foreign Origin", func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, 403},
			{"no Origin", func(r *http.Request) { r.Header.Del("Origin") }, 403},
		} {
			r := req("POST", path, stepOne())
			c.mutate(r)
			if w := f.do(r); w.Code != c.code {
				t.Errorf("%s %s: %d, want %d", path, c.name, w.Code, c.code)
			}
		}
	}
	r := req("GET", "/new", nil)
	r.Header.Del("Cookie")
	if w := f.do(r); w.Code != 401 {
		t.Errorf("GET /new without cookie: %d", w.Code)
	}
	if calls := f.cli.Calls(); len(calls) != 0 {
		t.Errorf("CLI calls on refusal: %q", calls)
	}
	time.Sleep(10 * time.Millisecond)
	if got := f.run.Argvs(); len(got) != 0 {
		t.Errorf("jobs on refusal: %q", got)
	}
}

// TestNewSwitcherLink: every switcher offers New project…; the root page
// links to it while no project is registered.
func TestNewSwitcherLink(t *testing.T) {
	f := newProjectFixture(t)
	if body := f.do(req("GET", "/p/ftest", nil)).Body.String(); !strings.Contains(body, `<li class="proj-new"><a href="/new">New project…</a></li>`) {
		t.Error("the project page's switcher lacks New project…")
	}
	g := newProjectFixture(t)
	g.cli.set("--json project ls", "[]")
	w := g.do(req("GET", "/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `<a href="/new">Create a project</a>`) {
		t.Errorf("root with no project: %d %s", w.Code, w.Body)
	}
}

// TestNewStreamFollowsCreate: the new page's stream, for a prefix not
// registered yet, carries that prefix's create job; once project new has
// registered it, its page opens.
func TestNewStreamFollowsCreate(t *testing.T) {
	f := newProjectFixture(t)
	if w := f.do(req("GET", "/events?view=new&p=-x", nil)); w.Code != http.StatusBadRequest {
		t.Errorf("bad prefix: %d", w.Code)
	}
	if w := f.do(req("GET", "/events?p=masto", nil)); w.Code != http.StatusNotFound {
		t.Errorf("unregistered prefix on a project page's stream: %d", w.Code)
	}
	f.run.out = "cloned\n"
	f.do(req("POST", "/new/create", stepOne()))
	f.endedIn(t, "masto")
	sub := f.s.subscribe("masto", "", parseSort("name"), "new")
	defer f.s.unsubscribe(sub)
	var ids []string
	for drained := false; !drained; {
		select {
		case ev := <-sub.ch:
			ids = append(ids, ev.ID)
			if ev.ID == "jobs" && (!strings.Contains(ev.HTML, "<b>project new masto</b>") || !strings.Contains(ev.HTML, `data-state="ok"`)) {
				t.Errorf("new page jobs fragment %s", ev.HTML)
			}
			if ev.ID == "header" && strings.Contains(ev.HTML, "masto") {
				t.Errorf("new page header names the project: %s", ev.HTML)
			}
		default:
			drained = true
		}
	}
	if !slices.Contains(ids, "jobs") || slices.Contains(ids, "list") {
		t.Errorf("new stream sent %q", ids)
	}

	f.cli.set("--json project ls", `[{"prefix":"masto","workspace":"/home/u/workspace/masto","worktrees":1,"workspace_missing":false}]`)
	f.cli.set("--json -C /home/u/workspace/masto overview", `{"prefix":"masto","workspace":"/home/u/workspace/masto","worktrees":[]}`)
	if w := f.do(req("GET", "/p/masto", nil)); w.Code != 200 {
		t.Errorf("the new project's page: %d %s", w.Code, w.Body)
	}
}

// TestNewCreateUpFailed: a create job that ends failed carries its exit
// code, and /new/registered says from a fresh project ls whether the
// project was created anyway (project new --up exits 3 when only up
// failed), so the page opens the project instead of keeping the form.
func TestNewCreateUpFailed(t *testing.T) {
	f := newProjectFixture(t)
	f.run.exit = 3
	v := stepOne()
	v.Set("up", "1")
	f.do(req("POST", "/new/create", v))
	f.endedIn(t, "masto")
	sub := f.s.subscribe("masto", "", parseSort("name"), "new")
	defer f.s.unsubscribe(sub)
	sawExit := false
	for drained := false; !drained; {
		select {
		case ev := <-sub.ch:
			sawExit = sawExit || ev.ID == "jobs" && strings.Contains(ev.HTML, `data-state="failed" data-exit="3"`)
		default:
			drained = true
		}
	}
	if !sawExit {
		t.Error(`jobs fragment lacks data-state="failed" data-exit="3"`)
	}

	registered := func() string {
		t.Helper()
		w := f.do(req("GET", "/new/registered?prefix=masto", nil))
		if w.Code != 200 {
			t.Fatalf("/new/registered: %d %s", w.Code, w.Body)
		}
		return strings.TrimSpace(w.Body.String())
	}
	if got := registered(); got != `{"registered":false}` {
		t.Errorf("before registration: %s", got)
	}
	f.cli.set("--json project ls", `[{"prefix":"masto","workspace":"/home/u/workspace/masto","worktrees":1,"workspace_missing":false}]`)
	if got := registered(); got != `{"registered":true}` {
		t.Errorf("after registration: %s", got)
	}
	if w := f.do(req("GET", "/new/registered?prefix=-x", nil)); w.Code != http.StatusBadRequest {
		t.Errorf("bad prefix: %d", w.Code)
	}
}
