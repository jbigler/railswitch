package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/jobs"
)

var testHash = "sha256:" + strings.Repeat("ab", 32)

var projectShowJSON = `{"prefix":"ftest","name":"Ftest App","repo":"git@github.com:o/r.git","base_branch":"main","hash":"` + testHash + `",
 "options":[
  {"key":"cache","slot":true,"ref":"kv@1","choices":["kv@1","local:kv2@1","none"]},
  {"key":"analytics","slot":false,"ref":"stats@1","choices":["stats@1","none"]}],
 "add_choices":["kv@1","stats@1","local:kv2@1"],
 "env":[{"name":"BIND","value":"0.0.0.0"}],
 "links":[
  {"label":"Flipper","url":"{worktree.host}/flipper","from":"project","hidden":false},
  {"label":"Mailer previews","url":"{worktree.host}/rails/mailers","from":"pack web@1","hidden":true}],
 "file":"\n# first line blank\nschema = 1\n"}`

const secretsJSON = `[
 {"name":"api_key","scope":"project","podman_secret":"ftest_api_key","delivery":"env API_KEY","driver":"file","set":true,"created":"2026-10-25T09:08:07Z","services":["web"],"driver_differs":false,"git_signing":false},
 {"name":"token","scope":"project","podman_secret":"ftest_token","delivery":"file","driver":"file","set":false,"created":null,"services":["web","db"],"driver_differs":false,"git_signing":false}]`

var previewJSON = `{"hash":"` + testHash + `","diff":"--- project.toml\n+++ project.toml\n@@ -1 +1 @@\n-cache = \"kv@1\"\n+cache = \"none\"\n",
 "edits":[
  {"index":0,"impact":"data","reasons":["volume cache-data is left behind"],"error":""},
  {"index":1,"impact":"check","reasons":["BIND changes for web"],"error":""},
  {"index":2,"impact":"blocked","reasons":[],"error":"worker needs cache: edit project.toml directly"}],
 "restart":["main","b"]}`

const previewKey = "--json -C /ws/ftest settings preview"

// settingsFixture is the fixture with the settings reads scripted.
func settingsFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.cli.set("--json -C /ws/ftest project show", projectShowJSON)
	f.cli.set("--json -C /ws/ftest secret ls", secretsJSON)
	f.cli.set(previewKey, previewJSON)
	return f
}

// stdinOf is the stdin of the last CLI call with args key.
func (f *fakeCLI) stdinOf(key string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if strings.Join(f.calls[i], " ") == key {
			return f.stdins[i], true
		}
	}
	return nil, false
}

// ended waits for the project's newest job to end.
func (f *fixture) ended(t *testing.T) jobs.Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		js := f.s.c.Jobs.Jobs("ftest", "")
		if len(js) > 0 && js[0].State != jobs.StateRunning {
			return js[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no job of ftest ended: %+v", js)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// jsonPost is a POST of body as the page's script sends a preview.
func jsonPost(target, body string) *http.Request {
	r := httptest.NewRequest("POST", target, strings.NewReader(body))
	r.Host = testHost
	r.AddCookie(&http.Cookie{Name: "railswitch_dashboard", Value: testToken})
	r.Header.Set("Origin", "http://"+testHost)
	r.Header.Set("Content-Type", "application/json")
	return r
}

func noCSPViolation(t *testing.T, what, body string) {
	t.Helper()
	if strings.Contains(body, " style=") || strings.Contains(body, "<style") || strings.Contains(body, "onclick") ||
		strings.Count(body, "<script") != strings.Count(body, `<script src="/static/app.js" defer></script>`) {
		t.Errorf("%s: inline style or script (CSP)", what)
	}
}

func TestSettingsPageRenders(t *testing.T) {
	f := settingsFixture(t)
	w := f.do(req("GET", "/p/ftest/settings", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, want := range []string{
		"Ftest App", "git@github.com:o/r.git", "fixed: it names every container, volume, network and hostname",
		`class="opt" data-key="cache" data-ref="kv@1"`,
		`<input type="radio" name="opt-cache" value="kv@1" checked> <span class="mono">kv@1</span> <span class="tag">current</span>`,
		`value="local:kv2@1">`, `data-key="analytics"`, `<span class="muted">extra</span>`,
		`data-name="BIND" data-value="0.0.0.0"`,
		`data-label="Flipper" data-hidden="false"`, `<span class="chip">project</span>`,
		`data-label="Mailer previews" data-hidden="true"`, `<span class="chip">pack web@1</span>`,
		"{worktree.host}/flipper", "Use <code>{worktree.host}</code>",
		`<span class="green">set 2026-10-25</span>`, `<span class="yellow">not set</span>`, "env API_KEY", "web, db",
		`name="services" value="web"`, `name="services" value="db"`,
		`data-preview="/p/ftest/settings/preview"`, `action="/p/ftest/settings/secret"`, `href="/p/ftest/settings/file"`,
		"Nothing staged. Change a setting on the left to see what it does.",
		`data-view="settings"`,
		`<form class="add-row" data-stage="service">`, `<option value="local:kv2@1">local:kv2@1</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// Remove only on the project's own link; a stack link gets Show.
	if n := strings.Count(body, "data-link-remove"); n != 1 {
		t.Errorf("%d Remove buttons on links, want 1", n)
	}
	if !strings.Contains(body, `class="link-show" aria-label="Show Mailer previews"`) {
		t.Error("the hidden stack link's Show box is not unticked")
	}
	noCSPViolation(t, "settings", body)

	main := f.do(req("GET", "/p/ftest", nil)).Body.String()
	if !strings.Contains(main, `<a class="hb" href="/p/ftest/settings">settings</a>`) {
		t.Error("the header has no Settings link")
	}

	// project show failing still answers the page, with the error and the
	// raw editor's link.
	f.cli.fail("--json -C /ws/ftest project show", fmt.Errorf("project.toml: line 3: bad"))
	w = f.do(req("GET", "/p/ftest/settings", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "line 3: bad") || !strings.Contains(w.Body.String(), "Edit project.toml") {
		t.Errorf("broken project: %d %s", w.Code, w.Body)
	}
}

func TestSettingsPreviewPost(t *testing.T) {
	f := settingsFixture(t)
	list := `[{"op":"option","key":"cache","ref":"none"},{"op":"env.set","name":"BIND","value":"127.0.0.1"},{"op":"link.add","label":"Admin","url":"{worktree.host}/admin"}]`
	w := f.do(jsonPost("/p/ftest/settings/preview", list))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	in, ok := f.cli.stdinOf(previewKey)
	if !ok {
		t.Fatalf("no preview call: %q", f.cli.Calls())
	}
	if string(in) != list {
		t.Errorf("preview stdin %s, want %s", in, list)
	}
	if got := w.Header().Get("X-Settings-Hash"); got != testHash {
		t.Errorf("X-Settings-Hash %q", got)
	}
	body := w.Body.String()
	for _, want := range []string{
		`id="staged"`, "Staged changes · 3", "data-blocked",
		"cache: kv@1 → none", `Env BIND: &#34;0.0.0.0&#34; → &#34;127.0.0.1&#34;`, "Link Admin: added, {worktree.host}/admin",
		`<span class="chip red">Data not carried over</span>`, "volume cache-data is left behind",
		`<span class="chip yellow">Check before applying</span>`, "BIND changes for web",
		`<span class="chip red">Blocked</span>`, "worker needs cache: edit project.toml directly",
		`data-undo="2"`, "Restart the 2 affected running worktrees now", "main, b",
		`<button class="ln main" disabled>Apply 3 changes</button>`,
		`value="` + testHash + `"`, "See the project.toml diff", "&#43;cache = &#34;none&#34;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("panel lacks %q", want)
		}
	}
	noCSPViolation(t, "staged", body)

	// No blocked edit: Apply is on; no restart: no checkbox.
	f.cli.set(previewKey, `{"hash":"`+testHash+`","diff":"","edits":[{"index":0,"impact":"none","reasons":[],"error":""}],"restart":[]}`)
	body = f.do(jsonPost("/p/ftest/settings/preview", `[{"op":"link.hide","label":"Mailer previews"}]`)).Body.String()
	for _, want := range []string{`<span class="chip green">No data affected</span>`, `<button class="ln main">Apply 1 change</button>`, "Link Mailer previews: hidden"} {
		if !strings.Contains(body, want) {
			t.Errorf("unblocked panel lacks %q: %s", want, body)
		}
	}
	if strings.Contains(body, `name="restart"`) || strings.Contains(body, "data-blocked") {
		t.Errorf("unblocked panel: %s", body)
	}

	// The list is checked before anything runs.
	before := f.cli.count(previewKey)
	for _, bad := range []string{`{"op":"option"}`, `[{"op":"drop","key":"x"}]`, `[{"op":"option","key":"x","ref":"y","extra":1}]`, `[] []`, `not json`, `null`} {
		if w := f.do(jsonPost("/p/ftest/settings/preview", bad)); w.Code != 422 {
			t.Errorf("%s: %d %s", bad, w.Code, w.Body)
		}
	}
	// Nothing staged: the empty panel, without a preview.
	w = f.do(jsonPost("/p/ftest/settings/preview", `[]`))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Nothing staged.") {
		t.Errorf("empty: %d %s", w.Code, w.Body)
	}
	if n := f.cli.count(previewKey); n != before {
		t.Errorf("%d previews ran for bad or empty lists", n-before)
	}

	// A preview that fails as a whole keeps the cards (to undo) and blocks
	// Apply.
	f.cli.fail(previewKey, fmt.Errorf("edits: edit 0: option needs ref"))
	body = f.do(jsonPost("/p/ftest/settings/preview", `[{"op":"option","key":"cache","ref":"x"}]`)).Body.String()
	for _, want := range []string{"Preview failed: edits: edit 0: option needs ref", `data-undo="0"`, `disabled>Apply 1 change`} {
		if !strings.Contains(body, want) {
			t.Errorf("failed preview lacks %q: %s", want, body)
		}
	}
}

func TestSettingsApplyJob(t *testing.T) {
	f := settingsFixture(t)
	list := `[{"op":"option","key":"cache","ref":"none"}]`
	f.run.block = make(chan struct{})
	w := f.do(req("POST", "/p/ftest/settings/apply", form("edits", list, "hash", testHash, "restart", "1")))
	if w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	got := f.run.wait(t, 1)[0]
	if want := []string{testExe, "-C", "/ws/ftest", "settings", "apply", "--if-hash", testHash, "--restart"}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
	if in := f.run.Stdins()[0]; string(in) != list {
		t.Errorf("stdin %q, want %q", in, list)
	}
	js := f.s.c.Jobs.Jobs("ftest", "")
	if len(js) != 1 || js[0].Spec.Slug != "" {
		t.Fatalf("not one project-level job: %+v", js)
	}
	// A project-level job blocks every worktree of the project.
	if w := f.do(req("POST", "/p/ftest/b/act/up", form())); w.Code != 409 {
		t.Errorf("worktree job during apply: %d", w.Code)
	}
	close(f.run.block)
	f.run.block = nil
	f.ended(t)

	f.do(req("POST", "/p/ftest/settings/apply", form("edits", list, "hash", testHash)))
	if got := f.run.wait(t, 2)[1]; slices.Contains(got, "--restart") {
		t.Errorf("--restart unticked: %q", got)
	}
	f.ended(t)

	for _, c := range []struct{ name, edits, hash string }{
		{"no hash", list, ""},
		{"flag as hash", list, "--restart"},
		{"empty list", `[]`, testHash},
		{"bad list", `[{"op":"nope"}]`, testHash},
	} {
		if w := f.do(req("POST", "/p/ftest/settings/apply", form("edits", c.edits, "hash", c.hash))); w.Code != 422 {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
		}
	}
	time.Sleep(10 * time.Millisecond)
	if n := len(f.run.Argvs()); n != 2 {
		t.Errorf("%d jobs, want 2", n)
	}
}

// TestSettingsSecretNoLeak: a secret value reaches the job's stdin byte
// for byte and nothing else: not argv, the job log, log/slog output, the
// response, an event or the page.
func TestSettingsSecretNoLeak(t *testing.T) {
	const value = "s3cr\n'$(touch /tmp/x)\" `id` ; rm -rf ~ \\ é"
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)
	slog.Info("baseline") // slog's default handler goes through log

	f := settingsFixture(t)
	f.s.tick = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.s.Run(ctx) }()
	defer func() { cancel(); <-done }()
	sub := f.s.subscribe("ftest", "", parseSort("name"), true)
	defer f.s.unsubscribe(sub)

	var bodies []string
	for i, c := range []struct {
		form kv
		argv []string
	}{
		{kv{"name", "token", "value", value}, []string{testExe, "-C", "/ws/ftest", "secret", "set", "token"}},
		{kv{"add", "1", "name", "new_key", "services", "web", "services", "db", "env", "NEW_KEY", "value", value},
			[]string{testExe, "-C", "/ws/ftest", "secret", "add", "new_key", "--service", "web", "--service", "db", "--env", "NEW_KEY"}},
	} {
		r := req("POST", "/p/ftest/settings/secret", form(c.form...))
		r.Header.Set("Accept", "application/json")
		w := f.do(r)
		if w.Code != 202 {
			t.Fatalf("%v: %d %s", c.argv, w.Code, w.Body)
		}
		bodies = append(bodies, w.Body.String())
		if got := f.run.wait(t, i+1)[i]; !slices.Equal(got, c.argv) {
			t.Errorf("argv %q, want %q", got, c.argv)
		}
		if in := f.run.Stdins()[i]; string(in) != value {
			t.Errorf("stdin %q, want %q", in, value)
		}
		snap := f.ended(t)
		bodies = append(bodies, fmt.Sprintf("%+v", snap))
		b, _ := json.Marshal(snap)
		bodies = append(bodies, string(b))
	}
	time.Sleep(50 * time.Millisecond) // let the job events reach the page
	for drained := false; !drained; {
		select {
		case ev := <-sub.ch:
			b, _ := json.Marshal(ev)
			bodies = append(bodies, string(b))
		default:
			drained = true
		}
	}
	bodies = append(bodies, f.do(req("GET", "/p/ftest/settings", nil)).Body.String())
	logs, _ := filepath.Glob(filepath.Join(f.logs, "*.log"))
	if len(logs) != 2 {
		t.Errorf("%d job logs, want 2", len(logs))
	}
	for _, p := range logs {
		b, _ := os.ReadFile(p)
		bodies = append(bodies, string(b))
	}
	bodies = append(bodies, logBuf.String())
	for _, a := range f.run.Argvs() {
		bodies = append(bodies, strings.Join(a, " "))
	}
	for _, c := range f.cli.Calls() {
		bodies = append(bodies, strings.Join(c, " "))
	}
	for _, b := range bodies {
		for _, part := range []string{value, "s3cr", "touch /tmp/x"} {
			if strings.Contains(b, part) {
				t.Errorf("%q leaked into %q", part, b)
			}
		}
	}

	for _, c := range []struct {
		name string
		form kv
	}{
		{"no value", kv{"name", "token"}},
		{"flag name", kv{"name", "--machine", "value", "v"}},
		{"add without services", kv{"add", "1", "name", "k", "value", "v"}},
		{"flag service", kv{"add", "1", "name", "k", "services", "--machine", "value", "v"}},
		{"bad env", kv{"add", "1", "name", "k", "services", "web", "env", "A B", "value", "v"}},
	} {
		if w := f.do(req("POST", "/p/ftest/settings/secret", form(c.form...))); w.Code != 422 {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
		}
	}
	time.Sleep(10 * time.Millisecond)
	if n := len(f.run.Argvs()); n != 2 {
		t.Errorf("%d jobs, want 2", n)
	}
}

// kv is form's key, value list.
type kv []string

// TestSettingsGuard: the settings POSTs need the cookie and the page's
// Origin; refused, nothing runs.
func TestSettingsGuard(t *testing.T) {
	f := settingsFixture(t)
	posts := map[string]*http.Request{}
	for _, path := range []string{"/p/ftest/settings/preview", "/p/ftest/settings/apply", "/p/ftest/settings/secret", "/p/ftest/settings/file"} {
		posts[path] = req("POST", path, form("name", "token", "value", "v", "edits", "[]", "hash", testHash, "text", "x"))
	}
	for path, r := range posts {
		for _, c := range []struct {
			name   string
			mutate func(*http.Request)
			code   int
		}{
			{"no cookie", func(r *http.Request) { r.Header.Del("Cookie") }, 401},
			{"foreign Origin", func(r *http.Request) { r.Header.Set("Origin", "http://b.ftest.localhost") }, 403},
			{"no Origin", func(r *http.Request) { r.Header.Del("Origin") }, 403},
		} {
			rr := r.Clone(context.Background())
			c.mutate(rr)
			if w := f.do(rr); w.Code != c.code {
				t.Errorf("%s %s: %d, want %d", path, c.name, w.Code, c.code)
			}
		}
	}
	for _, path := range []string{"/p/ftest/settings", "/p/ftest/settings/file"} {
		r := req("GET", path, nil)
		r.Header.Del("Cookie")
		if w := f.do(r); w.Code != 401 {
			t.Errorf("GET %s without cookie: %d", path, w.Code)
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

// TestSettingsFileConflict: the raw editor shows the file and its hash;
// Save runs project write with the text on stdin; a refusal for an
// outside change marks the job, which the page turns into its reload
// offer while the text stays.
func TestSettingsFileConflict(t *testing.T) {
	f := settingsFixture(t)
	w := f.do(req("GET", "/p/ftest/settings/file", nil))
	body := w.Body.String()
	for _, want := range []string{
		`<input type="hidden" name="hash" value="` + testHash + `">`,
		"aria-label=\"project.toml\">\n\n# first line blank\nschema = 1\n</textarea>",
		`id="file-conflict" class="red" hidden`, `<a href="/p/ftest/settings/file">Reload the file</a>`,
		`data-view="settingsfile"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("file page lacks %q", want)
		}
	}
	noCSPViolation(t, "file", body)

	f.run.out = "railswitch: project.toml changed since it was read; reload it\n"
	f.run.exit = 1
	text := "schema = 1\r\nname = \"x\"\r\n"
	w = f.do(req("POST", "/p/ftest/settings/file", form("hash", testHash, "text", text)))
	if w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got, want := f.run.wait(t, 1)[0], []string{testExe, "-C", "/ws/ftest", "project", "write", "--if-hash", testHash}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
	if in := f.run.Stdins()[0]; string(in) != "schema = 1\nname = \"x\"\n" {
		t.Errorf("stdin %q", in)
	}
	snap := f.ended(t)
	if snap.Spec.Slug != "" || snap.State != jobs.StateFailed {
		t.Errorf("job %+v", snap)
	}
	// The settings pages' jobs fragment marks it.
	sub := f.s.subscribe("ftest", "", parseSort("name"), true)
	defer f.s.unsubscribe(sub)
	f.s.mu.Lock()
	html, err := render("jobs", f.s.subJobs(sub, f.s.c.Jobs.Jobs("ftest", "")))
	f.s.mu.Unlock()
	if err != nil || !strings.Contains(html, `data-state="failed" data-conflict`) || !strings.Contains(html, "changed since it was read") {
		t.Errorf("jobs fragment %s (%v)", html, err)
	}

	for _, c := range []struct{ name, hash, text string }{
		{"no hash", "", "schema = 1\n"},
		{"empty", testHash, " \n"},
	} {
		if w := f.do(req("POST", "/p/ftest/settings/file", form("hash", c.hash, "text", c.text))); w.Code != 422 {
			t.Errorf("%s: %d", c.name, w.Code)
		}
	}
}

// TestSettingsViewJobs: a settings page's stream carries the project's
// jobs only, not a worktree's.
func TestSettingsViewJobs(t *testing.T) {
	f := settingsFixture(t)
	f.do(req("POST", "/p/ftest/main/act/up", form()))
	f.ended(t)
	f.do(req("POST", "/p/ftest/settings/apply", form("edits", `[{"op":"link.hide","label":"Mailer previews"}]`, "hash", testHash)))
	f.ended(t)
	sub := f.s.subscribe("ftest", "", parseSort("name"), true)
	defer f.s.unsubscribe(sub)
	var ids []string
	for drained := false; !drained; {
		select {
		case ev := <-sub.ch:
			ids = append(ids, ev.ID)
			if ev.ID == "jobs" && (strings.Contains(ev.HTML, "<b>up</b>") || !strings.Contains(ev.HTML, "settings apply")) {
				t.Errorf("settings jobs fragment %s", ev.HTML)
			}
		default:
			drained = true
		}
	}
	if !slices.Contains(ids, "jobs") || slices.Contains(ids, "list") || slices.Contains(ids, "detail") {
		t.Errorf("settings stream sent %q", ids)
	}
}

// TestStaleMarker: a worktree running older units says "restart to
// apply" in its row and its detail.
func TestStaleMarker(t *testing.T) {
	f := newFixture(t)
	ov := strings.Replace(ftestOverview("jb/b", "up", "null"), `"base":false,"stack":"up"`, `"base":false,"stack":"up","stale":true`, 1)
	f.cli.set("--json -C /ws/ftest overview", ov)
	body := f.do(req("GET", "/p/ftest/b", nil)).Body.String()
	row := body[strings.Index(body, `id="row-b"`):]
	row = row[:strings.Index(row, "</tr>")]
	if !strings.Contains(row, `<span class="stale">restart to apply</span>`) {
		t.Errorf("row b: %s", row)
	}
	main := body[strings.Index(body, `id="row-main"`):]
	main = main[:strings.Index(main, "</tr>")]
	if strings.Contains(main, "restart to apply") {
		t.Errorf("row main: %s", main)
	}
	detail := body[strings.Index(body, `id="detail"`):]
	if !strings.Contains(detail[:strings.Index(detail, "</section>")], "restart to apply") {
		t.Error("detail of b has no marker")
	}
	if body := f.do(req("GET", "/p/ftest/main", nil)).Body.String(); strings.Contains(body[strings.Index(body, `id="detail"`):], "restart to apply") {
		t.Error("detail of main has the marker")
	}
}
