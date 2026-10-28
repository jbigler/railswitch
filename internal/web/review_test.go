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

const reviewKey = "--json -C /ws/ftest lock --review"

var (
	shaOld = strings.Repeat("1", 64)
	shaNew = strings.Repeat("2", 64)
)

// reviewJSON: rails@1 changed (a host added to deps.allow at line 3, a
// manifest port flag, an unflagged README), kv@1 first (no stored copy).
var reviewJSON = `{"hash":"` + testHash + `","flags":3,"packages":[
 {"ref":"rails@1","kind":"pack","status":"changed","from":"` + shaOld + `","to":"` + shaNew + `","also_locked_in":["maybe"],"files":[
  {"path":"deps.allow","status":"changed","diff":"--- deps.allow\n+++ deps.allow\n@@ -1,3 +1,4 @@\n rubygems.org\n github.com\n+objects.githubusercontent.com\n # end\n","flags":[{"check":"firewall","line":3,"text":"objects.githubusercontent.com"}]},
  {"path":"pack.toml","status":"changed","diff":"--- pack.toml\n+++ pack.toml\n@@ -7 +7 @@\n-base = 33000\n+base = 34000\n","flags":[{"check":"port","line":0,"text":"port debug: base 33000 → 34000"}]},
  {"path":"README.md","status":"changed","diff":"--- README.md\n+++ README.md\n@@ -1 +1 @@\n-Rails\n+Rails, now with more\n","flags":[]}]},
 {"ref":"kv@1","kind":"option","status":"first","from":"","to":"` + shaNew + `","also_locked_in":[],"files":[
  {"path":"logo.png","status":"added","diff":"","flags":[{"check":"image","line":9,"text":"pull docker.io/kv:7"}]}]}],
 "restart":["main","b"]}`

const noReviewJSON = `{"hash":"sha256:` + "0000000000000000000000000000000000000000000000000000000000000000" + `","flags":0,"packages":[],"restart":[]}`

var noFlagsJSON = `{"hash":"` + testHash + `","flags":0,"packages":[
 {"ref":"rails@1","kind":"pack","status":"changed","from":"` + shaOld + `","to":"` + shaNew + `","also_locked_in":[],"files":[
  {"path":"README.md","status":"changed","diff":"@@ -1 +1 @@\n-a\n+b\n","flags":[]}]}],"restart":[]}`

func reviewFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.cli.set("--json -C /ws/ftest overview", pending(ftestOverview("jb/b", "up", agentJSON("idle", "", false))))
	f.cli.set(reviewKey, reviewJSON)
	return f
}

// pending is an overview document with review_pending set.
func pending(ov string) string {
	return strings.Replace(ov, `"workspace":"/ws/ftest",`, `"workspace":"/ws/ftest","review_pending":true,`, 1)
}

func TestReviewBanner(t *testing.T) {
	f := reviewFixture(t)
	f.do(req("GET", "/p/hello", nil)) // hello's overview read, so the switcher knows both
	body := f.do(req("GET", "/p/ftest", nil)).Body.String()
	contains(t, "project page", body,
		"Package update waiting for review.", `<a href="/p/ftest/review">Review update</a>`,
		`<b>ftest</b> <span class="muted">2 worktrees · 2 running</span> <span class="yellow" title="Package update waiting for review">update</span>`,
	)
	if strings.Contains(body, `<b>hello</b> <span class="muted">1 worktrees · 0 running</span> <span class="yellow"`) {
		t.Error("hello marked, it has no update waiting")
	}
	contains(t, "images page", f.do(req("GET", "/p/ftest/images", nil)).Body.String(), `<a href="/p/ftest/review">Review update</a>`)

	body = f.do(req("GET", "/p/hello", nil)).Body.String()
	if strings.Contains(body, "Package update waiting for review.") {
		t.Error("hello's page shows ftest's banner")
	}
	contains(t, "hello's switcher", body, `<b>ftest</b> <span class="muted">2 worktrees · 2 running</span> <span class="yellow" title="Package update waiting for review">update</span>`)

	g := newFixture(t)
	if body := g.do(req("GET", "/p/ftest", nil)).Body.String(); strings.Contains(body, "waiting for review") || strings.Contains(body, "/review") {
		t.Errorf("banner without review_pending: %s", body)
	}
}

func TestReviewPage(t *testing.T) {
	f := reviewFixture(t)
	w := f.do(req("GET", "/p/ftest/review", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	contains(t, "review", body,
		`data-view="review"`,
		`<h2><span class="mono">rails@1</span> <span class="chip">pack</span> <span class="chip">changed</span></h2>`,
		`<span class="mono">111111111111</span> → <span class="mono">222222222222</span>`,
		"Also locked in: maybe",
		`<span class="mono">kv@1</span> <span class="chip">option</span> <span class="chip">first</span>`,
		"No stored copy of what was accepted before: every file is shown as added.",
		// flagged files open, the rest collapsed
		`<details class="rv-file" open><summary><span class="mono">deps.allow</span> <span class="chip">changed</span> <span class="red">1 flagged</span></summary>`,
		`<details class="rv-file" open><summary><span class="mono">pack.toml</span>`,
		`<details class="rv-file"><summary><span class="mono">README.md</span> <span class="chip">changed</span></summary>`,
		// a flagged diff line, highlighted; manifest flags above the diff
		`<span class="dl add flagged" title="flagged: firewall">&#43;objects.githubusercontent.com</span>`,
		`<span class="dl ctx"> github.com</span>`,
		`<li><span class="chip">port</span> port debug: base 33000 → 34000</li>`,
		// a flag on a file with no diff (binary) is listed, not lost
		`<li><span class="chip">image</span> line 9: pull docker.io/kv:7</li>`, "No diff: a binary file.",
		`action="/p/ftest/review/accept"`, `name="hash" value="`+testHash+`"`,
		`data-ack-required`, `<input type="checkbox" name="ack" value="1"> I&#39;ve read the 3 flagged items`,
		`name="restart" value="1"`, `<span class="mono">main, b</span>`,
		`data-next="/p/ftest"`,
	)
	if strings.Contains(body, `class="dl add flagged" title="flagged: port"`) {
		t.Error("a manifest flag (line 0) highlights a diff line")
	}
	noCSPViolation(t, "review", body)

	f.cli.set(reviewKey, noReviewJSON)
	body = f.do(req("GET", "/p/ftest/review", nil)).Body.String()
	contains(t, "empty review", body, "Nothing is waiting for review.")
	if strings.Contains(body, "/review/accept") {
		t.Error("an empty review offers Accept")
	}

	f.cli.fail(reviewKey, fmt.Errorf("stack.lock: written by a newer railswitch"))
	w = f.do(req("GET", "/p/ftest/review", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "written by a newer railswitch") || strings.Contains(w.Body.String(), "/review/accept") {
		t.Errorf("failed review: %d %s", w.Code, w.Body)
	}
	if w := f.do(req("GET", "/p/nope/review", nil)); w.Code != 404 {
		t.Errorf("unknown project: %d", w.Code)
	}
}

func TestReviewHostileText(t *testing.T) {
	f := reviewFixture(t)
	hostile := `</pre><script>alert(1)</script><img src=x onerror=alert(2)>`
	f.cli.set(reviewKey, fmt.Sprintf(`{"hash":%q,"flags":1,"packages":[{"ref":%q,"kind":"pack","status":"changed","from":%q,"to":%q,"also_locked_in":[%q],"files":[
	 {"path":%q,"status":"changed","diff":%q,"flags":[{"check":%q,"line":1,"text":%q},{"check":"runs-here","line":0,"text":%q}]}]}],"restart":[%q]}`,
		testHash, hostile, hostile, hostile, hostile, hostile, "@@ -0,0 +1 @@\n+"+hostile+"\n", hostile, hostile, hostile, hostile))
	body := f.do(req("GET", "/p/ftest/review", nil)).Body.String()
	if strings.Contains(body, "<script>alert") || strings.Contains(body, "<img src=x") || strings.Contains(body, "</pre><script") {
		t.Errorf("hostile text not escaped: %s", body)
	}
	contains(t, "escaped", body, "&lt;/pre&gt;&lt;script&gt;alert(1)&lt;/script&gt;")
	noCSPViolation(t, "hostile", body)
}

func TestReviewAccept(t *testing.T) {
	f := reviewFixture(t)
	f.run.block = make(chan struct{})
	w := f.do(req("POST", "/p/ftest/review/accept", form("hash", testHash, "ack", "1", "restart", "1")))
	if w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	got := f.run.wait(t, 1)[0]
	if want := []string{testExe, "-C", "/ws/ftest", "lock", "--accept", testHash, "--restart"}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
	if in := f.run.Stdins()[0]; in != nil {
		t.Errorf("stdin %q, want none", in)
	}
	js := f.s.c.Jobs.Jobs("ftest", "")
	if len(js) != 1 || js[0].Spec.Slug != "" || js[0].Spec.Dir != "/ws/ftest" {
		t.Fatalf("not one project-level job: %+v", js)
	}
	// One project job at a time: a second Accept is refused busy.
	if w := f.do(req("POST", "/p/ftest/review/accept", form("hash", testHash, "ack", "1"))); w.Code != 409 {
		t.Errorf("second accept while one runs: %d", w.Code)
	}
	close(f.run.block)
	f.run.block = nil
	f.ended(t)

	f.do(req("POST", "/p/ftest/review/accept", form("hash", testHash, "ack", "1")))
	if got, want := f.run.wait(t, 2)[1], []string{testExe, "-C", "/ws/ftest", "lock", "--accept", testHash}; !slices.Equal(got, want) {
		t.Errorf("unticked restart: argv %q, want %q", got, want)
	}
	f.ended(t)
	if n := f.cli.count(reviewKey); n != 3 {
		t.Errorf("review read %d times for 3 accepts, want each re-read", n)
	}
}

// noJob fails when the runner saw any job.
func noJob(t *testing.T, f *fixture, what string) {
	t.Helper()
	time.Sleep(10 * time.Millisecond)
	if got := f.run.Argvs(); len(got) != 0 {
		t.Errorf("%s: jobs %q", what, got)
	}
}

func TestReviewAcceptNeedsAck(t *testing.T) {
	f := reviewFixture(t)
	for _, ack := range []string{"", "0", "yes"} {
		w := f.do(req("POST", "/p/ftest/review/accept", form("hash", testHash, "ack", ack)))
		if w.Code != 400 || !strings.Contains(w.Body.String(), "3 flagged items") {
			t.Errorf("ack %q: %d %s", ack, w.Code, w.Body)
		}
	}
	if w := f.do(req("POST", "/p/ftest/review/accept", form("hash", testHash))); w.Code != 400 {
		t.Errorf("no ack: %d", w.Code)
	}
	noJob(t, f, "no acknowledgement")
}

func TestReviewAcceptStaleHash(t *testing.T) {
	f := reviewFixture(t)
	before := f.cli.count(reviewKey)
	w := f.do(req("POST", "/p/ftest/review/accept", form("hash", newHash, "ack", "1")))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "changed since") {
		t.Errorf("stale hash: %d %s", w.Code, w.Body)
	}
	if f.cli.count(reviewKey) != before+1 {
		t.Error("the stale hash was judged without reading the review again")
	}
	for _, hash := range []string{"", "--restart", "sha256:x"} {
		if w := f.do(req("POST", "/p/ftest/review/accept", form("hash", hash, "ack", "1"))); w.Code != 400 {
			t.Errorf("hash %q: %d", hash, w.Code)
		}
	}
	f.cli.set(reviewKey, noReviewJSON)
	if w := f.do(req("POST", "/p/ftest/review/accept", form("hash", "sha256:"+strings.Repeat("0", 64), "ack", "1"))); w.Code != 400 {
		t.Errorf("nothing to review: %d", w.Code)
	}
	f.cli.fail(reviewKey, fmt.Errorf("boom"))
	if w := f.do(req("POST", "/p/ftest/review/accept", form("hash", testHash, "ack", "1"))); w.Code != 500 {
		t.Errorf("review failing: %d", w.Code)
	}
	noJob(t, f, "refused accepts")
}

func TestReviewAcceptNoFlags(t *testing.T) {
	f := reviewFixture(t)
	f.cli.set(reviewKey, noFlagsJSON)
	body := f.do(req("GET", "/p/ftest/review", nil)).Body.String()
	if strings.Contains(body, `name="ack"`) || strings.Contains(body, "data-ack-required") {
		t.Errorf("acknowledgement asked with no flags: %s", body)
	}
	w := f.do(req("POST", "/p/ftest/review/accept", form("hash", testHash)))
	if w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got, want := f.run.wait(t, 1)[0], []string{testExe, "-C", "/ws/ftest", "lock", "--accept", testHash}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
	f.ended(t)
}

func TestReviewGuard(t *testing.T) {
	f := reviewFixture(t)
	r := req("POST", "/p/ftest/review/accept", form("hash", testHash, "ack", "1"))
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
			t.Errorf("accept %s: %d, want %d", c.name, w.Code, c.code)
		}
	}
	g := req("GET", "/p/ftest/review", nil)
	g.Header.Del("Cookie")
	if w := f.do(g); w.Code != 401 {
		t.Errorf("GET without cookie: %d", w.Code)
	}
	if calls := f.cli.Calls(); len(calls) != 0 {
		t.Errorf("CLI calls on refusal: %q", calls)
	}
	noJob(t, f, "guard")
	if w := f.do(req("GET", "/p/ftest/review/accept", nil)); w.Code != 405 {
		t.Errorf("GET accept: %d", w.Code)
	}
}
