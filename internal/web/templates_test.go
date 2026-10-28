package web

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

const tplSHA = "1111111111111111111111111111111111111111111111111111111111111111"
const tplNewSHA = "2222222222222222222222222222222222222222222222222222222222222222"

const templateLsJSON = `{"templates":[
 {"ref":"rails-pg@2","name":"rails-pg","version":2,"description":"Rails with PostgreSQL","source":"machine","packages":["web@1","pg@1"]},
 {"ref":"acme:shop@1","name":"shop","version":1,"description":"","source":"library acme","packages":["acme:pgx@1"]},
 {"ref":"--evil@1","name":"evil","version":1,"description":"","source":"machine","packages":[]}]}`

const railsPgShow = `{"ref":"rails-pg@2","sha256":"` + tplSHA + `","stack_toml":"schema = 1\nname = \"rails-pg\"\n",
 "ask":[{"key":"options.cache","prompt":"Cache","default":"kv@1"},{"key":"env.TZ","prompt":"Time zone","default":"UTC"}],
 "secrets":["rails_master_key"],"containerfiles":["Containerfile.pg-client"],"readme":"Start here."}`

func templateFixture(t *testing.T) *fixture {
	f := newProjectFixture(t)
	f.cli.set("--json template ls", templateLsJSON)
	f.cli.set("--json template show rails-pg@2", railsPgShow)
	return f
}

// tplStep is step 1 with the template picked and both questions
// answered; pack and options are the template's.
func tplStep(extra ...string) []string {
	return append([]string{
		"repo", hostileRepo, "branch", "trunk", "prefix", "masto", "dir", "~/workspace/masto",
		"template", "rails-pg@2", "answer-options.cache", "none", "answer-env.TZ", "Europe/Paris; $(id)",
	}, extra...)
}

var tplArgs = []string{"project", "new", "/home/u/workspace/masto", "--repo", hostileRepo, "--prefix", "masto",
	"--branch", "trunk", "--template", "rails-pg@2", "--answer", "options.cache=none", "--answer", "env.TZ=Europe/Paris; $(id)"}

const tplDryRun = `{"project_toml":"schema = 1\n[template]\nref = \"rails-pg@2\"\n",
 "worktree":{"slug":"trunk","routes":[],"containers":[],"env":[]},"warnings":[],"steps":["Clone"],"install":null,
 "template":{"ref":"rails-pg@2","sha256":"` + tplSHA + `","ask":[
  {"key":"options.cache","prompt":"Cache","default":"kv@1","answer":"none"},
  {"key":"env.TZ","prompt":"Time zone","default":"UTC","answer":"UTC"}]}}`

func TestTemplatesTab(t *testing.T) {
	f := libraryFixture(t)
	f.cli.set("--json template ls", templateLsJSON)
	f.cli.set("--json template show rails-pg@2", railsPgShow)
	body := f.do(req("GET", "/p/ftest/library", nil)).Body.String()
	contains(t, "library", body, "<h2>Templates</h2>",
		`<li data-template="rails-pg@2"><a href="/p/ftest/library?template=rails-pg%402"><span class="mono">rails-pg@2</span> <span class="muted">Rails with PostgreSQL</span><span class="sp"></span><span class="chip">machine</span></a></li>`,
		`<span class="chip">library acme</span>`)
	if strings.Contains(body, "--evil") {
		t.Error("a malformed template ref is listed")
	}
	if strings.Contains(body, `id="template-detail"`) {
		t.Error("a template is shown with none selected")
	}

	w := f.do(req("GET", "/p/ftest/library?template=rails-pg%402", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body = w.Body.String()
	contains(t, "template", body, `<li class="cur" data-template="rails-pg@2">`, `id="template-detail"`,
		"rails-pg · version 2", "web@1, pg@1", tplSHA, "rails_master_key", "Containerfile.pg-client",
		`<tr class="ask"><td>Cache</td><td class="mono">options.cache</td><td class="mono">kv@1</td></tr>`,
		`<tr class="ask"><td>Time zone</td><td class="mono">env.TZ</td><td class="mono">UTC</td></tr>`,
		"schema = 1\nname = &#34;rails-pg&#34;\n</pre>", "Start here.")
	noCSPViolation(t, "template", body)

	if w := f.do(req("GET", "/p/ftest/library?template=nope%401", nil)); w.Code != 404 {
		t.Errorf("unknown template: %d", w.Code)
	}
	f.cli.fail("--json template show rails-pg@2", errors.New("stack.toml: unknown key"))
	if body := f.do(req("GET", "/p/ftest/library?template=rails-pg%402", nil)).Body.String(); !strings.Contains(body, `<p class="red set-error">stack.toml: unknown key</p>`) {
		t.Error("a template show failure is not shown")
	}
	f.cli.fail("--json template ls", errors.New("templates broke"))
	body = f.do(req("GET", "/p/ftest/library", nil)).Body.String()
	contains(t, "library", body, "Templates unavailable: templates broke", "<h2>Packs</h2>")
}

func TestSaveAsTemplateJob(t *testing.T) {
	f := settingsFixture(t)
	body := f.do(req("GET", "/p/ftest/settings", nil)).Body.String()
	contains(t, "settings", body, "<h2>Save as template</h2>",
		`action="/p/ftest/settings/template/export" class="add-row" data-job="template-export"`, `name="version" type="number" min="1"`)
	if strings.Contains(body, `id="template"`) {
		t.Error("a template notice without a template")
	}

	for _, v := range [][]string{
		{"name", "Rails", "version", "1"}, {"name", "-x", "version", "1"}, {"name", "rails", "version", "0"},
		{"name", "rails", "version", "-1"}, {"name", "rails", "version", "1.5"}, {"name", "rails", "version", ""},
	} {
		if w := f.do(req("POST", "/p/ftest/settings/template/export", form(v...))); w.Code != 400 {
			t.Errorf("%q: %d", v, w.Code)
		}
	}
	time.Sleep(10 * time.Millisecond)
	if n := len(f.run.Argvs()); n != 0 {
		t.Fatalf("%d jobs on refusal", n)
	}

	r := req("POST", "/p/ftest/settings/template/export", form("name", "rails-pg", "version", "3"))
	r.Header.Set("Accept", "application/json")
	if w := f.do(r); w.Code != 202 || !strings.Contains(w.Body.String(), `"label":"template export rails-pg@3"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	want := []string{testExe, "-C", "/ws/ftest", "template", "export", "--name", "rails-pg", "--version", "3"}
	if got := f.run.wait(t, 1)[0]; !slices.Equal(got, want) {
		t.Errorf("argv %q\nwant %q", got, want)
	}
	if js := f.ended(t); js.Spec.Project != "ftest" || js.Spec.Slug != "" || js.Spec.Dir != "/ws/ftest" {
		t.Errorf("job %+v", js.Spec)
	}
}

// TestNewFromTemplate: the picker lists the templates; picking one shows
// its questions; the review and Create pass --template and one --answer
// argv entry per question, and no --agent none (the template's agents
// stand); an Install review adds --accept-install.
func TestNewFromTemplate(t *testing.T) {
	f := templateFixture(t)
	body := f.do(req("GET", "/new", nil)).Body.String()
	contains(t, "new", body, `<select name="template" data-template>`, `<option value="">None: read the repository</option>`,
		`<option value="rails-pg@2">rails-pg@2 · Rails with PostgreSQL</option>`, `<option value="acme:shop@1">acme:shop@1</option>`)
	if strings.Contains(body, "--evil") {
		t.Error("a malformed template ref is offered")
	}

	w := f.do(req("POST", "/new/template", form("template", "rails-pg@2")))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	contains(t, "template card", w.Body.String(), `id="new-stack" class="card" data-pack="" data-template="rails-pg@2"`,
		`<input name="answer-options.cache" value="kv@1"`, `<input name="answer-env.TZ" value="UTC"`, "Time zone",
		"Containerfile.pg-client")
	noCSPViolation(t, "template card", w.Body.String())
	if body := f.do(req("POST", "/new/template", form("template", ""))).Body.String(); !strings.Contains(body, `data-pack=""`) || strings.Contains(body, "data-template") {
		t.Errorf("no template: %s", body)
	}
	if w := f.do(req("POST", "/new/template", form("template", "nope@1"))); w.Code != 400 {
		t.Errorf("unlisted template: %d", w.Code)
	}

	key := "--json " + strings.Join(tplArgs, " ") + " --dry-run"
	f.cli.set(key, tplDryRun)
	w = f.do(req("POST", "/new/review", form(tplStep()...)))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got, want := f.cli.Calls()[len(f.cli.Calls())-1], append(append([]string{"--json"}, tplArgs...), "--dry-run"); !slices.Equal(got, want) {
		t.Errorf("dry-run argv %q\nwant %q", got, want)
	}
	contains(t, "review", w.Body.String(), "From the template", `<span class="mono">rails-pg@2</span> <span class="muted mono">`+tplSHA+`</span>`,
		`<dt>Cache</dt><dd><span class="mono">none</span> <span class="muted">· <span class="mono">options.cache</span> · default <span class="mono">kv@1</span></span></dd>`,
		`<dt>Time zone</dt><dd><span class="mono">UTC</span> <span class="muted">· <span class="mono">env.TZ</span></span></dd>`,
		`<button class="ln main">Create project</button>`)

	if w := f.do(req("POST", "/new/create", form(tplStep("up", "1")...))); w.Code != 202 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	want := append(append([]string{testExe}, tplArgs...), "--up")
	if got := f.run.wait(t, 1)[0]; !slices.Equal(got, want) {
		t.Errorf("argv %q\nwant %q", got, want)
	}
	f.endedIn(t, "masto")

	// A library template installs through the review; a ticked agent goes
	// on top of the template's.
	f.cli.set(key+"", strings.Replace(tplDryRun, `"install":null`, `"install":`+installJSON, 1))
	if w := f.do(req("POST", "/new/create", form(tplStep("accept_install", newHash)...))); w.Code != 400 {
		t.Errorf("no tick: %d", w.Code)
	}
	if w := f.do(req("POST", "/new/create", form(tplStep("accept_install", newHash, "ack", "1")...))); w.Code != 202 {
		t.Fatalf("create with review: %d %s", w.Code, w.Body)
	}
	want = append(append([]string{testExe}, tplArgs...), "--accept-install", newHash)
	if got := f.run.wait(t, 2)[1]; !slices.Equal(got, want) {
		t.Errorf("argv %q\nwant %q", got, want)
	}
	f.endedIn(t, "masto")
	agentKey := "--json " + strings.Join(tplArgs, " ") + " --agent helper=helper@1 --dry-run"
	f.cli.set(agentKey, tplDryRun)
	if w := f.do(req("POST", "/new/review", form(tplStep("agent", "helper=helper@1")...))); w.Code != 200 || strings.Contains(w.Body.String(), "refusal") {
		t.Errorf("ticked agent: %d %s", w.Code, w.Body)
	}
}

// TestNewTemplateRefusesAnswerKey: an answer the template doesn't ask, or
// one without a template, is refused before the dry run or a job.
func TestNewTemplateRefusesAnswerKey(t *testing.T) {
	f := templateFixture(t)
	for _, v := range [][]string{
		tplStep("answer-env.EVIL", "x"),
		tplStep("answer---force", "x"),
		{"repo", hostileRepo, "prefix", "masto", "dir", "~/workspace/masto", "pack", "web@1", "answer-env.TZ", "UTC"},
		tplStep("answer-env.TZ", "second value"),
		append(tplStep()[:len(tplStep())-2], "answer-env.TZ", "UTC\n--x"),
		{"repo", hostileRepo, "prefix", "masto", "dir", "~/workspace/masto", "template", "--rails@1"},
		{"repo", hostileRepo, "prefix", "masto", "dir", "~/workspace/masto", "template", "local:rails@1"},
	} {
		for _, path := range []string{"/new/review", "/new/create"} {
			if w := f.do(req("POST", path, form(v...))); w.Code != 400 {
				t.Errorf("%s %q: %d %s", path, v, w.Code, w.Body)
			}
		}
	}
	for _, c := range f.cli.Calls() {
		if !slices.Equal(c, []string{"--json", "template", "show", "rails-pg@2"}) {
			t.Errorf("CLI call on refusal: %q", c)
		}
	}
	time.Sleep(10 * time.Millisecond)
	if n := len(f.run.Argvs()); n != 0 {
		t.Errorf("%d jobs on refusal", n)
	}
}

func tplOverview(update bool, ref string) string {
	stamp := `"template":{"ref":"` + ref + `","sha256":"` + tplSHA + `"},"template_update":` + map[bool]string{true: "true", false: "false"}[update] + `,`
	return strings.Replace(ftestOverview("jb/b", "up", agentJSON("idle", "", false)), `"workspace":"/ws/ftest",`, `"workspace":"/ws/ftest",`+stamp, 1)
}

func TestTemplateUpdateNotice(t *testing.T) {
	f := settingsFixture(t)
	f.cli.set("--json -C /ws/ftest overview", tplOverview(true, "acme:shop@1"))
	f.cli.set("--json template show acme:shop@1", `{"ref":"acme:shop@1","sha256":"`+tplNewSHA+`","stack_toml":"","ask":[],"secrets":[],"containerfiles":[],"readme":""}`)
	for _, page := range []string{"/p/ftest", "/p/ftest/settings"} {
		contains(t, page, f.do(req("GET", page, nil)).Body.String(), `<div id="banner" class="banner">`,
			`<div class="tpl-banner">The template <span class="mono">acme:shop@1</span> changed since this project was made from it. <a href="/p/ftest/settings#template">See it in Settings</a></div>`)
	}
	body := f.do(req("GET", "/p/ftest/settings", nil)).Body.String()
	contains(t, "settings", body, `id="template"`, "<h2>Template changed</h2>", "template check",
		`action="/p/ftest/settings/template/accept" class="btns start" data-job="template-accept"`,
		`<input type="hidden" name="sha256" value="`+tplNewSHA+`">`)
	noCSPViolation(t, "settings", body)

	for _, sha := range []string{tplSHA, "", "x"} {
		if w := f.do(req("POST", "/p/ftest/settings/template/accept", form("sha256", sha))); w.Code != 400 {
			t.Errorf("sha %q: %d %s", sha, w.Code, w.Body)
		}
	}
	time.Sleep(10 * time.Millisecond)
	if n := len(f.run.Argvs()); n != 0 {
		t.Fatalf("%d jobs on refusal", n)
	}
	if w := f.do(req("POST", "/p/ftest/settings/template/accept", form("sha256", tplNewSHA))); w.Code != 202 {
		t.Fatalf("accept: %d %s", w.Code, w.Body)
	}
	want := []string{testExe, "-C", "/ws/ftest", "template", "check", "--accept", tplNewSHA}
	if got := f.run.wait(t, 1)[0]; !slices.Equal(got, want) {
		t.Errorf("argv %q\nwant %q", got, want)
	}
	f.ended(t)

	g := settingsFixture(t)
	g.cli.set("--json -C /ws/ftest overview", tplOverview(false, "acme:shop@1"))
	for _, page := range []string{"/p/ftest", "/p/ftest/settings"} {
		if strings.Contains(g.do(req("GET", page, nil)).Body.String(), "tpl-banner") {
			t.Errorf("%s: a notice without a change", page)
		}
	}
	if w := g.do(req("POST", "/p/ftest/settings/template/accept", form("sha256", tplNewSHA))); w.Code != 400 {
		t.Errorf("accept with nothing waiting: %d", w.Code)
	}
}

func TestTemplatesGuard(t *testing.T) {
	f := templateFixture(t)
	posts := []struct {
		path string
		vals []string
	}{
		{"/new/template", []string{"template", "rails-pg@2"}},
		{"/new/review", tplStep()},
		{"/new/create", tplStep()},
		{"/p/ftest/settings/template/export", []string{"name", "rails", "version", "1"}},
		{"/p/ftest/settings/template/accept", []string{"sha256", tplNewSHA}},
	}
	for _, p := range posts {
		for _, c := range []struct {
			name   string
			mutate func(*http.Request)
			code   int
		}{
			{"no cookie", func(r *http.Request) { r.Header.Del("Cookie") }, 401},
			{"foreign Origin", func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, 403},
			{"no Origin", func(r *http.Request) { r.Header.Del("Origin") }, 403},
		} {
			r := req("POST", p.path, form(p.vals...))
			c.mutate(r)
			if w := f.do(r); w.Code != c.code {
				t.Errorf("%s %s: %d, want %d", p.path, c.name, w.Code, c.code)
			}
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

// TestTemplateHostile: a library template's text is escaped everywhere
// the page shows it; a question with a malformed key gets no field.
func TestTemplateHostile(t *testing.T) {
	f := libraryFixture(t)
	f.s.c.Home = testHome
	f.cli.set("--json template ls", `{"templates":[{"ref":"acme:x@1","name":"x","version":1,"description":"<script>alert(1)</script>","source":"library <b>acme</b>","packages":["<i>p</i>"]}]}`)
	f.cli.set("--json template show acme:x@1", `{"ref":"acme:x@1","sha256":"`+tplSHA+`","stack_toml":"</pre><script>alert(2)</script>",
 "ask":[{"key":"env.TZ","prompt":"<img src=x onerror=alert(3)>","default":"\" autofocus onfocus=\"alert(4)"},{"key":"x\" onfocus=\"alert(5)","prompt":"bad","default":""}],
 "secrets":["<u>s</u>"],"containerfiles":["<s>c</s>"],"readme":"<script>alert(6)</script>"}`)
	f.cli.set("--json pack ls", catalogJSON)
	pages := map[string]string{
		"library": f.do(req("GET", "/p/ftest/library?template=acme%3Ax%401", nil)).Body.String(),
		"new":     f.do(req("GET", "/new", nil)).Body.String(),
		"card":    f.do(req("POST", "/new/template", form("template", "acme:x@1"))).Body.String(),
	}
	for name, body := range pages {
		for _, bad := range []string{"<script>alert", "<img", "<b>acme", "<i>p", "<u>s", "<s>c", `" autofocus`, `x" onfocus`} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: unescaped %q", name, bad)
			}
		}
		noCSPViolation(t, name, body)
	}
	contains(t, "library", pages["library"], "&lt;script&gt;alert(1)&lt;/script&gt;", "&lt;/pre&gt;&lt;script&gt;alert(2)&lt;/script&gt;", "&lt;img src=x onerror=alert(3)&gt;")
	contains(t, "card", pages["card"], `name="answer-env.TZ" value="&#34; autofocus onfocus=&#34;alert(4)"`)
	if strings.Contains(pages["card"], "alert(5)") {
		t.Error("a question with a malformed key has a field")
	}
}
