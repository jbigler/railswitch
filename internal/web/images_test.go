package web

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

var newHash = "sha256:" + strings.Repeat("cd", 32)

const chainMain = `{"steps":[
 {"name":"base","file":"containerfiles/Containerfile.base","tag":"localhost/ftest/base:3.3-11d1777c","from":["docker.io/library/ruby:3.3-slim"]},
 {"name":"addon pg-client","file":"containerfiles/Containerfile.pg-client","tag":"localhost/ftest/base:3.3-ba118cc2","from":[]},
 {"name":"agent bot","file":"containerfiles/Containerfile.bot","tag":"localhost/ftest/agent-bot:3.3-7d41e0b2","from":[]}],
 "warnings":["option kv@1 suggests addon kv-client, which [image] addons lacks"]}`

const chainB = `{"steps":[{"name":"base","file":"containerfiles/Containerfile.base","tag":"localhost/ftest/base:3.3-99999999","from":[]}],"warnings":[]}`

var cfShowJSON = `{"name":"pg-client","file":"containerfiles/Containerfile.pg-client","kind":"addon",
 "text":"# Ejected from pg@1 Containerfile.pg-client (sha256 aa) …\nARG BASE_IMAGE\nFROM ${BASE_IMAGE}\nRUN apt-get install -y postgresql-client\nUSER appuser\n",
 "hash":"` + testHash + `","default":{"ref":"pg@1","text":"…"},"ejected":{"ref":"pg@1","sha256":"aa"},
 "default_changed":false,"diff":"--- default\n+++ containerfiles/Containerfile.pg-client\n@@ -3 +3 @@\n-RUN apt-get install -y libpq\n+RUN apt-get install -y postgresql-client\n"}`

var cfCheckKey = "--json -C /ws/ftest containerfile write pg-client --if-hash " + testHash + " --dry-run"

var cfCheckJSON = `{"hash":"` + newHash + `","steps":[{"worktree":"main","name":"addon pg-client","old_tag":"localhost/ftest/base:3.3-ba118cc2","new_tag":"localhost/ftest/base:3.3-0f0f0f0f"}],"restart":["main"],"errors":[]}`

func imagesFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.cli.set("--json -C /ws/ftest -w main containerfile list", chainMain)
	f.cli.set("--json -C /ws/ftest -w b containerfile list", chainB)
	f.cli.set("--json -C /ws/ftest containerfile show pg-client", cfShowJSON)
	f.cli.set(cfCheckKey, cfCheckJSON)
	return f
}

func contains(t *testing.T, what, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("%s lacks %q", what, want)
		}
	}
}

func TestImagesPage(t *testing.T) {
	f := imagesFixture(t)
	w := f.do(req("GET", "/p/ftest/images", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	contains(t, "images", body,
		`data-view="images"`,
		"option kv@1 suggests addon kv-client, which [image] addons lacks",
		`<td class="mono">Containerfile.pg-client</td>`, "localhost/ftest/base:3.3-ba118cc2", "docker.io/library/ruby:3.3-slim",
		`<a class="ln" href="/p/ftest/images/edit/pg-client">Edit</a>`,
		`<a class="ln" href="/p/ftest/images/edit/bot">Edit</a>`,
		`<span class="chip">addon</span>`, `<span class="chip">agent</span>`,
		`<a class="wtpick cur" href="/p/ftest/images?wt=main">main</a>`, `<a class="wtpick" href="/p/ftest/images?wt=b">b</a>`,
		`action="/p/ftest/images/new"`,
	)
	noCSPViolation(t, "images", body)

	body = f.do(req("GET", "/p/ftest/images?wt=b", nil)).Body.String()
	contains(t, "images b", body, "localhost/ftest/base:3.3-99999999", `<a class="wtpick cur" href="/p/ftest/images?wt=b">b</a>`)
	if strings.Contains(body, "pg-client") {
		t.Error("worktree b's chain shows main's addon")
	}
	if w := f.do(req("GET", "/p/ftest/images?wt=nope", nil)); w.Code != 404 {
		t.Errorf("unknown worktree: %d", w.Code)
	}

	main := f.do(req("GET", "/p/ftest", nil)).Body.String()
	contains(t, "header", main, `<a class="hb" href="/p/ftest/images">images</a>`, `<a class="hb" href="/p/ftest/library">library</a>`)
}

func TestImagesEditor(t *testing.T) {
	f := imagesFixture(t)
	w := f.do(req("GET", "/p/ftest/images/edit/pg-client", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	contains(t, "editor", body,
		`data-view="editor"`, "containerfiles/Containerfile.pg-client",
		"\nARG BASE_IMAGE\nFROM ${BASE_IMAGE}\nRUN apt-get install -y postgresql-client\nUSER appuser\n</textarea>",
		`name="hash" value="`+testHash+`"`,
		"Changes from the default (pg@1)", "&#43;RUN apt-get install -y postgresql-client",
		`data-check="/p/ftest/images/check/pg-client"`, `action="/p/ftest/images/save/pg-client"`,
		`action="/p/ftest/images/reset/pg-client"`,
		"I&#39;ve read this file. Every RUN line runs on this machine during the build, outside the firewall.",
		`name="ack" value="1"`, `data-check-required`, "Load from file",
	)
	if strings.Contains(body, "changed since it was ejected") {
		t.Error("default_changed false, yet the notice shows")
	}
	noCSPViolation(t, "editor", body)

	f.cli.set("--json -C /ws/ftest containerfile show pg-client", strings.Replace(cfShowJSON, `"default_changed":false`, `"default_changed":true`, 1))
	body = f.do(req("GET", "/p/ftest/images/edit/pg-client", nil)).Body.String()
	contains(t, "changed default", body, "The default (pg@1) changed since it was ejected")

	// A user addon: no default, no diff, no Reset.
	f.cli.set("--json -C /ws/ftest containerfile show extra", `{"name":"extra","file":"containerfiles/Containerfile.extra","kind":"user","text":"FROM x\n","hash":"`+testHash+`","default":null,"ejected":null,"default_changed":false,"diff":""}`)
	body = f.do(req("GET", "/p/ftest/images/edit/extra", nil)).Body.String()
	if strings.Contains(body, "Changes from the default") || strings.Contains(body, "/reset/") {
		t.Errorf("user addon shows a default: %s", body)
	}
	// show failing answers the page with the error.
	f.cli.fail("--json -C /ws/ftest containerfile show gone", fmt.Errorf("containerfiles/Containerfile.gone: no such file; eject or new first"))
	w = f.do(req("GET", "/p/ftest/images/edit/gone", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "eject or new first") || strings.Contains(w.Body.String(), "<textarea") {
		t.Errorf("missing file: %d %s", w.Code, w.Body)
	}
}

func TestImagesCheck(t *testing.T) {
	f := imagesFixture(t)
	text := "ARG BASE_IMAGE\r\nFROM ${BASE_IMAGE}\r\nRUN echo hi\r\nUSER appuser\r\n"
	w := f.do(req("POST", "/p/ftest/images/check/pg-client", form("hash", testHash, "text", text)))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	in, ok := f.cli.stdinOf(cfCheckKey)
	if !ok {
		t.Fatalf("no dry run: %q", f.cli.Calls())
	}
	if want := strings.ReplaceAll(text, "\r\n", "\n"); string(in) != want {
		t.Errorf("stdin %q, want %q", in, want)
	}
	body := w.Body.String()
	contains(t, "check", body, `id="check"`, "data-ok", "addon pg-client", "localhost/ftest/base:3.3-0f0f0f0f",
		"Rebuild and restart:", `name="restart" value="1"`, "main")
	if strings.Contains(body, "checked") {
		t.Error("Rebuild and restart is ticked by default")
	}
	noCSPViolation(t, "check", body)

	f.cli.set(cfCheckKey, `{"hash":"`+newHash+`","steps":[],"restart":[],"errors":["Containerfile.pg-client: the last FROM must be FROM ${BASE_IMAGE}"]}`)
	body = f.do(req("POST", "/p/ftest/images/check/pg-client", form("hash", testHash, "text", "FROM x\n"))).Body.String()
	contains(t, "errors", body, "the last FROM must be FROM ${BASE_IMAGE}")
	if strings.Contains(body, "data-ok") || strings.Contains(body, `name="restart"`) {
		t.Errorf("a failed check passes: %s", body)
	}

	// The dry run refusing (exit 1): the error, and Reload on a mismatch.
	f.cli.fail(cfCheckKey, fmt.Errorf("containerfiles/Containerfile.pg-client changed on disk since you opened it"))
	body = f.do(req("POST", "/p/ftest/images/check/pg-client", form("hash", testHash, "text", "FROM x\n"))).Body.String()
	contains(t, "mismatch", body, "Changed on disk since you opened it", `href="/p/ftest/images/edit/pg-client">Reload`)
	if strings.Contains(body, "data-ok") {
		t.Error("a refused check passes")
	}

	before := len(f.cli.Calls())
	for _, c := range []struct{ name, hash, text string }{
		{"no hash", "", "x"},
		{"flag hash", "--force", "x"},
		{"too big", testHash, strings.Repeat("a", maxEdits+1)},
	} {
		if w := f.do(req("POST", "/p/ftest/images/check/pg-client", form("hash", c.hash, "text", c.text))); w.Code < 400 || w.Code >= 500 {
			t.Errorf("%s: %d", c.name, w.Code)
		}
	}
	if n := len(f.cli.Calls()) - before; n > 2 { // project ls and overview at most
		t.Errorf("refused checks ran %q", f.cli.Calls()[before:])
	}
}

func TestImagesSave(t *testing.T) {
	f := imagesFixture(t)
	text := "ARG BASE_IMAGE\nFROM ${BASE_IMAGE}\nRUN echo '$(id)' \"`x`\"\nUSER appuser\n"
	f.run.block = make(chan struct{})
	w := f.do(req("POST", "/p/ftest/images/save/pg-client", form("hash", testHash, "text", text, "ack", "1", "restart", "1")))
	if w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	got := f.run.wait(t, 1)[0]
	if want := []string{testExe, "-C", "/ws/ftest", "containerfile", "write", "pg-client", "--if-hash", testHash, "--restart"}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
	if in := f.run.Stdins()[0]; string(in) != text {
		t.Errorf("stdin %q, want %q", in, text)
	}
	js := f.s.c.Jobs.Jobs("ftest", "")
	if len(js) != 1 || js[0].Spec.Slug != "" {
		t.Fatalf("not one project-level job: %+v", js)
	}
	close(f.run.block)
	f.run.block = nil
	f.ended(t)

	f.do(req("POST", "/p/ftest/images/save/pg-client", form("hash", testHash, "text", text, "ack", "1")))
	if got := f.run.wait(t, 2)[1]; slices.Contains(got, "--restart") {
		t.Errorf("--restart unticked: %q", got)
	}
	f.ended(t)

	// Reset and New addon.
	f.do(req("POST", "/p/ftest/images/reset/pg-client", form()))
	if got, want := f.run.wait(t, 3)[2], []string{testExe, "-C", "/ws/ftest", "containerfile", "eject", "pg-client", "--force"}; !slices.Equal(got, want) {
		t.Errorf("reset argv %q, want %q", got, want)
	}
	f.ended(t)
	f.do(req("POST", "/p/ftest/images/new", form("name", "extra-tools")))
	if got, want := f.run.wait(t, 4)[3], []string{testExe, "-C", "/ws/ftest", "containerfile", "new", "extra-tools"}; !slices.Equal(got, want) {
		t.Errorf("new argv %q, want %q", got, want)
	}
	f.ended(t)
	for _, name := range []string{"", "-x", "Bad", "a b", "../x"} {
		if w := f.do(req("POST", "/p/ftest/images/new", form("name", name))); w.Code != 422 {
			t.Errorf("new %q: %d", name, w.Code)
		}
	}
	time.Sleep(10 * time.Millisecond)
	if n := len(f.run.Argvs()); n != 4 {
		t.Errorf("%d jobs, want 4", n)
	}

	// A stale hash: the job is marked, which the editor turns into Reload.
	f.run.out, f.run.exit = "railswitch: containerfiles/Containerfile.pg-client changed on disk since you opened it\n", 1
	f.do(req("POST", "/p/ftest/images/save/pg-client", form("hash", testHash, "text", text, "ack", "1")))
	f.run.wait(t, 5)
	f.ended(t)
	sub := f.s.subscribe("ftest", "", parseSort("name"), "settings")
	defer f.s.unsubscribe(sub)
	f.s.mu.Lock()
	html, err := render("jobs", f.s.subJobs(sub, f.s.c.Jobs.Jobs("ftest", "")))
	f.s.mu.Unlock()
	if err != nil || !strings.Contains(html, `data-state="failed" data-conflict`) {
		t.Errorf("jobs fragment %s (%v)", html, err)
	}
}

func TestImagesSaveNeedsAck(t *testing.T) {
	f := imagesFixture(t)
	for _, ack := range []string{"", "0", "yes"} {
		w := f.do(req("POST", "/p/ftest/images/save/pg-client", form("hash", testHash, "text", "FROM x\n", "ack", ack)))
		if w.Code != 400 {
			t.Errorf("ack %q: %d %s", ack, w.Code, w.Body)
		}
	}
	w := f.do(req("POST", "/p/ftest/images/save/pg-client", form("hash", testHash, "text", "FROM x\n")))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "read this file") {
		t.Errorf("no ack: %d %s", w.Code, w.Body)
	}
	time.Sleep(10 * time.Millisecond)
	if got := f.run.Argvs(); len(got) != 0 {
		t.Errorf("jobs without the acknowledgement: %q", got)
	}
}

func TestImagesHostileText(t *testing.T) {
	f := imagesFixture(t)
	hostile := `</textarea><script>alert(1)</script><img src=x onerror=alert(2)>`
	f.cli.set("--json -C /ws/ftest containerfile show pg-client", fmt.Sprintf(`{"name":"pg-client","file":"containerfiles/Containerfile.pg-client","kind":"addon","text":%q,"hash":%q,"default":{"ref":"pg@1"},"default_changed":false,"diff":%q}`, hostile, testHash, "+"+hostile))
	body := f.do(req("GET", "/p/ftest/images/edit/pg-client", nil)).Body.String()
	if strings.Contains(body, "<script>alert") || strings.Contains(body, "<img src=x") || strings.Count(body, "</textarea>") != 1 {
		t.Errorf("hostile text not escaped: %s", body)
	}
	contains(t, "escaped", body, "&lt;/textarea&gt;&lt;script&gt;alert(1)&lt;/script&gt;")
	noCSPViolation(t, "hostile", body)

	f.cli.set(cfCheckKey, fmt.Sprintf(`{"hash":%q,"steps":[],"restart":[],"errors":[%q]}`, newHash, hostile))
	body = f.do(req("POST", "/p/ftest/images/check/pg-client", form("hash", testHash, "text", "x"))).Body.String()
	if strings.Contains(body, "<script>alert") {
		t.Errorf("hostile error not escaped: %s", body)
	}
}

func TestImagesGuard(t *testing.T) {
	f := imagesFixture(t)
	for _, path := range []string{"/p/ftest/images/check/pg-client", "/p/ftest/images/save/pg-client", "/p/ftest/images/reset/pg-client", "/p/ftest/images/new"} {
		r := req("POST", path, form("hash", testHash, "text", "x", "ack", "1", "name", "extra"))
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
	for _, path := range []string{"/p/ftest/images", "/p/ftest/images/edit/pg-client"} {
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

func TestImagesNameRefused(t *testing.T) {
	f := imagesFixture(t)
	for _, name := range []string{"..%2Fx", "Bad", "-x", "a.b", "a%20b"} {
		for _, r := range []*http.Request{
			req("GET", "/p/ftest/images/edit/"+name, nil),
			req("POST", "/p/ftest/images/check/"+name, form("hash", testHash, "text", "x")),
			req("POST", "/p/ftest/images/save/"+name, form("hash", testHash, "text", "x", "ack", "1")),
			req("POST", "/p/ftest/images/reset/"+name, form()),
		} {
			if w := f.do(r); w.Code != 404 {
				t.Errorf("%s %s: %d", r.Method, r.URL, w.Code)
			}
		}
	}
	if calls := f.cli.Calls(); len(calls) != 0 {
		t.Errorf("CLI calls for a bad name: %q", calls)
	}
	time.Sleep(10 * time.Millisecond)
	if got := f.run.Argvs(); len(got) != 0 {
		t.Errorf("jobs for a bad name: %q", got)
	}
}
