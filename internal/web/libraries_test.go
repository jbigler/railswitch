package web

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/jobs"
)

const libLsKey = "--json library ls"

// libLsJSON: acme trusted; beta waits on an unknown signer (fpA); gamma
// on a rotation from the trusted fpB to fpA; delta on a new signer (fpB)
// no trusted key vouched for.
const libLsJSON = `{"libraries":[
 {"alias":"acme","url":"https://github.com/acme/lib","branch":"main","id":"github.com/acme/lib","status":"trusted","serial":4,
  "commit":"0123456789abcdef0123456789abcdef01234567","fetched":"2026-10-29T09:00:00Z","packages":2,"pending":null},
 {"alias":"beta","url":"file:///srv/beta","branch":"","id":"","status":"unknown-signer","serial":0,"commit":"","fetched":"2026-10-29T09:01:00Z","packages":0,
  "pending":{"fingerprint":"` + fpA + `","new_fingerprint":"","status":"unknown-signer","known_library":false}},
 {"alias":"gamma","url":"https://g.example/lib","branch":"","id":"g.example/lib","status":"rotation","serial":2,"commit":"aaaaaaaaaaaaaaaaaaaa","fetched":"2026-10-29T09:02:00Z","packages":1,
  "pending":{"fingerprint":"` + fpB + `","new_fingerprint":"` + fpA + `","status":"rotation","known_library":true}},
 {"alias":"delta","url":"https://d.example/lib","branch":"","id":"d.example/lib","status":"unknown-signer","serial":3,"commit":"bbbbbbbbbbbbbbbbbbbb","fetched":"2026-10-29T09:03:00Z","packages":1,
  "pending":{"fingerprint":"` + fpB + `","new_fingerprint":"","status":"unknown-signer","known_library":true}}]}`

// libCatalog is libraryCatalog with two acme packages: an option that
// names its library, and a pack that doesn't (the field is optional).
var libCatalog = strings.Replace(strings.Replace(libraryCatalog,
	`{"ref":"local:clj@1","name":"clj"}`, `{"ref":"local:clj@1","name":"clj"},{"ref":"acme:django@1","name":"django"}`, 1),
	`{"ref":"local:kv2@1"`, `{"ref":"acme:pgx@1","name":"pgx","slot":"database","works_with":["*"],"library":"acme"},{"ref":"local:kv2@1"`, 1)

func librariesFixture(t *testing.T) *fixture {
	f := libraryFixture(t)
	f.cli.set(libLsKey, libLsJSON)
	f.cli.set("--json -C /ws/ftest pack ls", libCatalog)
	f.cli.set("--json -C /ws/ftest pack show acme:pgx@1", `{"ref":"acme:pgx@1","kind":"option","source":"library acme","path":"","slot":"database","service":"db","runtimes":[],"ports":[],"pin":null,"gives":["DATABASE_URL"],"readme":"","files":[]}`)
	return f
}

func TestLibrariesSection(t *testing.T) {
	f := librariesFixture(t)
	w := f.do(req("GET", "/p/ftest/library", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	contains(t, "library", body,
		`<section class="card" id="libraries">`, `<h2>Libraries</h2>`,
		`<h3><span class="mono">acme</span> <span class="chip green">trusted</span></h3>`,
		`<dd class="mono">https://github.com/acme/lib · main</dd>`, `<dd class="mono">github.com/acme/lib</dd>`,
		`2026-10-29T09:00:00Z · serial 4 · <span class="mono">0123456789ab</span> · 2 packages`,
		`action="/machine/libraries/fetch"`, `<input type="hidden" name="alias" value="acme">`,
		// beta: Trust this key, with the fingerprint to check
		`<h3><span class="mono">beta</span> <span class="chip yellow">unknown-signer</span></h3>`,
		`<b>Trust this key</b>`, `<span class="mono fp">`+fpA+`</span>`, "Check this fingerprint with the publisher",
		`data-confirm="Trust `+fpA+` to sign beta? Packages it signs can then be installed."`,
		// gamma: both fingerprints, the new one posted
		`<b>Confirm rotation</b>`, `<span class="muted">Trusted key</span> <span class="mono fp">`+fpB+`</span>`,
		`<span class="muted">New key</span> <span class="mono fp">`+fpA+`</span>`,
		`<input type="hidden" name="kind" value="rotation">`, `>Confirm rotation</button>`,
		// delta: the second confirmation
		`<div class="lib-trust danger">`, "none of them vouched for this one",
		`<input type="checkbox" name="new_signer" value="1" required>`,
		`action="/machine/libraries/trust"`, `action="/machine/libraries/add"`, `name="alias" placeholder="alias"`,
		// library packages, with their alias, in the lists
		`href="/p/ftest/library?ref=acme%3Apgx%401"`, `href="/p/ftest/library?ref=acme%3Adjango%401"`,
	)
	for _, ref := range []string{"acme:pgx@1", "acme:django@1"} {
		row := section(body, `data-ref="`+ref+`"`)
		if !strings.Contains(row[:strings.Index(row, "</li>")], `<span class="chip">library acme</span>`) {
			t.Errorf("%s not marked as acme's: %s", ref, row)
		}
	}
	if strings.Contains(body, `<option value="acme:pgx@1">`) || strings.Contains(body, `<option value="acme:django@1">`) {
		t.Error("a library package offered as a copy source")
	}
	if n := strings.Count(body, `name="new_signer"`); n != 1 {
		t.Errorf("%d second confirmations, want 1 (delta's; not gamma's rotation)", n)
	}
	noCSPViolation(t, "library", body)

	body = f.do(req("GET", "/p/ftest/library?ref=acme%3Apgx%401", nil)).Body.String()
	contains(t, "library package", body,
		`<span class="mono">acme:pgx@1</span> <span class="chip">library acme</span>`,
		`From the library <span class="mono">acme</span> (<span class="mono">github.com/acme/lib</span>) · last fetch <span class="chip green">trusted</span>`,
		`href="/p/ftest/library/acme:pgx@1/option.toml">View</a>`,
	)
	if w := f.do(req("POST", "/p/ftest/library/save/acme:pgx@1/option.toml", form("hash", testHash, "text", "x", "ack", "1"))); w.Code != 422 || !strings.Contains(w.Body.String(), "library package is read-only") {
		t.Errorf("write to a library package: %d %s", w.Code, w.Body)
	}

	f.cli.fail(libLsKey, errors.New("libraries.toml: written by a newer railswitch"))
	w = f.do(req("GET", "/p/ftest/library", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Libraries unavailable: libraries.toml: written by a newer railswitch") || !strings.Contains(w.Body.String(), "<h2>Packs</h2>") {
		t.Errorf("failed library ls: %d %s", w.Code, w.Body)
	}
}

func TestLibraryAddJob(t *testing.T) {
	f := librariesFixture(t)
	w := f.do(req("POST", "/machine/libraries/add", form("url", hostileRepo, "alias", "acme2", "branch", "")))
	if w.Code != http.StatusAccepted {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got, want := f.run.wait(t, 1)[0], []string{testExe, "library", "add", hostileRepo, "--alias", "acme2"}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
	f.endedIn(t, machineKey)
	f.do(req("POST", "/machine/libraries/add", form("url", "file:///srv/lib", "alias", "b", "branch", "release/1")))
	if got, want := f.run.wait(t, 2)[1], []string{testExe, "library", "add", "file:///srv/lib", "--alias", "b", "--branch", "release/1"}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
	f.endedIn(t, machineKey)
	if js := f.s.c.Jobs.Jobs(machineKey, ""); len(js) != 2 || js[0].Spec.Label != "library add b" || js[0].Spec.Dir != f.s.c.Home {
		t.Errorf("machine jobs %+v", js)
	}

	for _, c := range []struct{ name, url, alias, branch string }{
		{"no url", "", "a", ""},
		{"flag url", "--upload-pack=x", "a", ""},
		{"line break", "https://h/x\nb", "a", ""},
		{"long url", "https://h/" + strings.Repeat("x", 2048), "a", ""},
		{"local alias", "https://h/x", "local", ""},
		{"upper alias", "https://h/x", "Acme", ""},
		{"long alias", "https://h/x", "a" + strings.Repeat("b", 20), ""},
		{"digit alias", "https://h/x", "1a", ""},
		{"colon alias", "https://h/x", "a:b", ""},
		{"flag branch", "https://h/x", "a", "-b"},
		{"space branch", "https://h/x", "a", "a b"},
	} {
		if w := f.do(req("POST", "/machine/libraries/add", form("url", c.url, "alias", c.alias, "branch", c.branch))); w.Code != 400 {
			t.Errorf("%s: %d, want 400", c.name, w.Code)
		}
	}
	time.Sleep(20 * time.Millisecond)
	if n := len(f.run.Argvs()); n != 2 {
		t.Errorf("%d jobs, want 2", n)
	}
}

// TestLibraryFetchJob: Fetch is a machine-level job, which the Library
// page shows beside the project's and its stream carries; a settings
// page's stream doesn't.
func TestLibraryFetchJob(t *testing.T) {
	f := librariesFixture(t)
	lib := f.s.subscribe("ftest", "", parseSort("name"), "library")
	defer f.s.unsubscribe(lib)
	set := f.s.subscribe("ftest", "", parseSort("name"), "settings")
	defer f.s.unsubscribe(set)
	drain := func(sub *subscriber) string {
		var html string
		for {
			select {
			case ev := <-sub.ch:
				if ev.ID == "jobs" {
					html = ev.HTML
				}
			default:
				return html
			}
		}
	}
	drain(lib)
	drain(set)

	w := f.do(req("POST", "/machine/libraries/fetch", form("alias", "acme")))
	if w.Code != http.StatusAccepted {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got, want := f.run.wait(t, 1)[0], []string{testExe, "library", "fetch", "acme"}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
	f.endedIn(t, machineKey)
	f.s.jobEvent(jobs.Event{Job: f.s.c.Jobs.Jobs(machineKey, "")[0]}) // as Run passes the hub's events on
	if got := drain(lib); !strings.Contains(got, "<b>library fetch acme</b>") {
		t.Errorf("library stream lacks the fetch job: %q", got)
	}
	if got := drain(set); strings.Contains(got, "library fetch") {
		t.Errorf("settings stream shows a machine job: %q", got)
	}
	contains(t, "library page jobs", f.do(req("GET", "/p/ftest/library", nil)).Body.String(), "<b>library fetch acme</b>")
	if strings.Contains(f.do(req("GET", "/p/ftest/settings", nil)).Body.String(), "library fetch acme") {
		t.Error("settings page shows a machine job")
	}

	for _, bad := range []string{"", "-a", "Acme", "local", "a b"} {
		if w := f.do(req("POST", "/machine/libraries/fetch", form("alias", bad))); w.Code != 400 {
			t.Errorf("alias %q: %d, want 400", bad, w.Code)
		}
	}
	time.Sleep(20 * time.Millisecond)
	if n := len(f.run.Argvs()); n != 1 {
		t.Errorf("%d jobs, want 1", n)
	}
}

// TestLibraryTrustJob: the posted fingerprint and kind must be what
// `library ls`, read again, has waiting; a rotation adds --rotation, a
// new signer of a library with other keys needs the second confirmation
// and adds --new-signer.
func TestLibraryTrustJob(t *testing.T) {
	f := librariesFixture(t)
	n := 0
	ok := func(name string, v []string, want ...string) {
		t.Helper()
		before := f.cli.count(libLsKey)
		if w := f.do(req("POST", "/machine/libraries/trust", form(v...))); w.Code != http.StatusAccepted {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
		if f.cli.count(libLsKey) != before+1 {
			t.Errorf("%s: library ls not read again", name)
		}
		n++
		if got := f.run.wait(t, n)[n-1]; !slices.Equal(got, append([]string{testExe}, want...)) {
			t.Errorf("%s: argv %q, want %q", name, got, want)
		}
		f.endedIn(t, machineKey)
	}
	ok("unknown signer", []string{"alias", "beta", "fingerprint", fpA, "kind", "trust"},
		"library", "trust", "beta", "--fingerprint", fpA)
	ok("rotation", []string{"alias", "gamma", "fingerprint", fpA, "kind", "rotation"},
		"library", "trust", "gamma", "--fingerprint", fpA, "--rotation")
	ok("new signer", []string{"alias", "delta", "fingerprint", fpB, "kind", "trust", "new_signer", "1"},
		"library", "trust", "delta", "--fingerprint", fpB, "--new-signer")

	for _, c := range []struct {
		name string
		v    []string
	}{
		{"not the pending key", []string{"alias", "beta", "fingerprint", fpB, "kind", "trust"}},
		{"trust posted for a rotation", []string{"alias", "gamma", "fingerprint", fpA, "kind", "trust"}},
		{"rotation posted for a new key", []string{"alias", "beta", "fingerprint", fpA, "kind", "rotation"}},
		{"the rotation's old key", []string{"alias", "gamma", "fingerprint", fpB, "kind", "rotation"}},
		{"no second confirmation", []string{"alias", "delta", "fingerprint", fpB, "kind", "trust"}},
		{"nothing waits", []string{"alias", "acme", "fingerprint", fpA, "kind", "trust"}},
		{"unknown library", []string{"alias", "zeta", "fingerprint", fpA, "kind", "trust"}},
		{"bad kind", []string{"alias", "beta", "fingerprint", fpA, "kind", "new-signer"}},
		{"short fingerprint", []string{"alias", "beta", "fingerprint", "SHA256:abc", "kind", "trust"}},
		{"flag alias", []string{"alias", "--rotation", "fingerprint", fpA, "kind", "trust"}},
	} {
		if w := f.do(req("POST", "/machine/libraries/trust", form(c.v...))); w.Code != 400 {
			t.Errorf("%s: %d %s, want 400", c.name, w.Code, w.Body)
		}
	}
	f.cli.fail(libLsKey, errors.New("boom"))
	if w := f.do(req("POST", "/machine/libraries/trust", form("alias", "beta", "fingerprint", fpA, "kind", "trust"))); w.Code != 500 {
		t.Errorf("library ls failed: %d", w.Code)
	}
	time.Sleep(20 * time.Millisecond)
	if got := len(f.run.Argvs()); got != n {
		t.Errorf("%d jobs, want %d", got, n)
	}
}

func TestLibrariesGuard(t *testing.T) {
	f := librariesFixture(t)
	for _, c := range []struct {
		name, path string
		v          []string
	}{
		{"add", "/machine/libraries/add", []string{"url", "https://h/x", "alias", "a"}},
		{"fetch", "/machine/libraries/fetch", []string{"alias", "acme"}},
		{"trust", "/machine/libraries/trust", []string{"alias", "beta", "fingerprint", fpA, "kind", "trust"}},
	} {
		noCookie := req("POST", c.path, form(c.v...))
		noCookie.Header.Del("Cookie")
		foreign := req("POST", c.path, form(c.v...))
		foreign.Header.Set("Origin", "http://evil.example")
		noOrigin := req("POST", c.path, form(c.v...))
		noOrigin.Header.Del("Origin")
		for what, r := range map[string]*http.Request{"no cookie": noCookie, "foreign origin": foreign, "no origin": noOrigin} {
			want := 403
			if what == "no cookie" {
				want = 401
			}
			if w := f.do(r); w.Code != want {
				t.Errorf("%s %s: %d, want %d", c.name, what, w.Code, want)
			}
		}
	}
	if w := f.do(req("GET", "/machine/libraries/add", nil)); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET add: %d", w.Code)
	}
	time.Sleep(20 * time.Millisecond)
	if argvs := f.run.Argvs(); len(argvs) != 0 {
		t.Errorf("jobs on refusal: %q", argvs)
	}
	if n := f.cli.count(libLsKey); n != 0 {
		t.Errorf("library ls read %d times for refused requests", n)
	}
}

// TestLibrariesHostile: a library's text (alias, URL, id, status,
// fingerprints) is escaped wherever the page shows it.
func TestLibrariesHostile(t *testing.T) {
	f := librariesFixture(t)
	f.cli.set(libLsKey, `{"libraries":[{"alias":"<script>a</script>","url":"\"><img src=x>","branch":"<q>","id":"<i>id</i>",
 "status":"<script>s</script>","serial":1,"commit":"<u>c</u>","fetched":"<em>t</em>","packages":1,
 "pending":{"fingerprint":"SHA256:<script>f</script>","new_fingerprint":"","status":"unknown-signer","known_library":true}}]}`)
	body := f.do(req("GET", "/p/ftest/library", nil)).Body.String()
	for _, bad := range []string{"<script>a", "<script>s", "<script>f", "<img src=x>", "<q>", "<i>id", "<u>c", "<em>t"} {
		if strings.Contains(body, bad) {
			t.Errorf("page holds unescaped %q", bad)
		}
	}
	contains(t, "page", body, "&lt;script&gt;a&lt;/script&gt;", "&#34;&gt;&lt;img src=x&gt;", "SHA256:&lt;script&gt;f&lt;/script&gt;")
	noCSPViolation(t, "library", body)
}

// installJSON: installing acme:pgx@1, two flagged lines, one with
// markup in it.
var installJSON = `{"hash":"` + newHash + `","flags":2,"packages":[
 {"ref":"acme:pgx@1","kind":"option","status":"added","from":"","to":"` + shaNew + `","also_locked_in":[],"files":[
  {"path":"option.toml","status":"added","diff":"--- /dev/null\n+++ option.toml\n@@ -0,0 +1,2 @@\n+slot = \"database\"\n+image = \"docker.io/pg:16\"\n","flags":[{"check":"image","line":2,"text":"pull docker.io/pg:16"}]},
  {"path":"entrypoints/pg.sh","status":"added","diff":"--- /dev/null\n+++ entrypoints/pg.sh\n@@ -0,0 +1 @@\n+curl https://x.example/</pre><script>alert(1)</script>\n","flags":[{"check":"script","line":1,"text":"fetches x.example"}]}]}],
 "restart":[]}`

var installPreviewJSON = `{"hash":"` + testHash + `","diff":"","edits":[{"index":0,"impact":"none","reasons":[],"error":""}],"restart":[],"install":` + installJSON + `}`

func TestSettingsInstallReview(t *testing.T) {
	f := settingsFixture(t)
	list := `[{"op":"option","key":"database","ref":"acme:pgx@1"}]`
	f.cli.set(previewKey, installPreviewJSON)
	body := f.do(jsonPost("/p/ftest/settings/preview", list)).Body.String()
	contains(t, "panel", body,
		`<section id="install-review" class="install-review">`, "<h2>Install review</h2>",
		`<h2><span class="mono">acme:pgx@1</span> <span class="chip">option</span> <span class="chip">added</span></h2>`,
		`nothing → <span class="mono">222222222222</span>`,
		`<span class="dl add flagged" title="flagged: image">&#43;image = &#34;docker.io/pg:16&#34;</span>`,
		"&lt;/pre&gt;&lt;script&gt;alert(1)&lt;/script&gt;",
		"<li>The library packages above are locked as reviewed.</li>",
		`data-apply data-ack-required>`, `<input type="hidden" name="accept_install" value="`+newHash+`">`,
		`<input type="checkbox" name="ack" value="1"> I&#39;ve read the 2 flagged items`,
		`<button class="ln main" disabled>Apply 1 change</button>`,
	)
	if strings.Contains(body, "<script>alert") {
		t.Error("install diff unescaped")
	}
	noCSPViolation(t, "staged", body)

	apply := func(v ...string) int {
		return f.do(req("POST", "/p/ftest/settings/apply", form(append([]string{"edits", list, "hash", testHash}, v...)...))).Code
	}
	for _, c := range []struct {
		name string
		v    []string
	}{
		{"no tick", []string{"accept_install", newHash}},
		{"another review", []string{"accept_install", testHash, "ack", "1"}},
		{"not a hash", []string{"accept_install", "--force", "ack", "1"}},
	} {
		if code := apply(c.v...); code != 400 {
			t.Errorf("%s: %d, want 400", c.name, code)
		}
	}
	before := f.cli.count(previewKey)
	if code := apply("accept_install", newHash, "ack", "1"); code != 202 {
		t.Fatalf("apply: %d", code)
	}
	if f.cli.count(previewKey) != before+1 {
		t.Error("apply didn't read the preview again")
	}
	if in, _ := f.cli.stdinOf(previewKey); string(in) != list {
		t.Errorf("preview stdin %q", in)
	}
	if got, want := f.run.wait(t, 1)[0], []string{testExe, "-C", "/ws/ftest", "settings", "apply", "--if-hash", testHash, "--accept-install", newHash}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
	f.ended(t)

	// The review changed after the page read it: stale.
	f.cli.set(previewKey, strings.Replace(installPreviewJSON, newHash, "sha256:"+strings.Repeat("ef", 32), 1))
	if code := apply("accept_install", newHash, "ack", "1"); code != 400 {
		t.Errorf("stale review: %d, want 400", code)
	}
	// It no longer installs anything.
	f.cli.set(previewKey, strings.Replace(installPreviewJSON, `"install":`+installJSON, `"install":null`, 1))
	if code := apply("accept_install", newHash, "ack", "1"); code != 400 {
		t.Errorf("no install now: %d, want 400", code)
	}
	f.cli.fail(previewKey, errors.New("boom"))
	if code := apply("accept_install", newHash, "ack", "1"); code != 422 {
		t.Errorf("preview failed: %d, want 422", code)
	}

	// No flags: nothing to tick.
	f.cli.set(previewKey, strings.Replace(installPreviewJSON, `"flags":2`, `"flags":0`, 1))
	body = f.do(jsonPost("/p/ftest/settings/preview", list)).Body.String()
	contains(t, "unflagged panel", body, `<button class="ln main">Apply 1 change</button>`, `name="accept_install" value="`+newHash+`"`)
	if strings.Contains(body, `name="ack"`) || strings.Contains(body, "data-ack-required") {
		t.Error("an unflagged install asks for the tick")
	}
	if code := apply("accept_install", newHash); code != 202 {
		t.Errorf("unflagged apply: %d", code)
	}
	if got := f.run.wait(t, 2)[1]; !slices.Contains(got, "--accept-install") {
		t.Errorf("argv %q", got)
	}
	f.ended(t)

	// Without an install the panel and argv are 6b's.
	f.cli.set(previewKey, previewJSON)
	if body := f.do(jsonPost("/p/ftest/settings/preview", list)).Body.String(); strings.Contains(body, "Install review") || strings.Contains(body, "accept_install") {
		t.Error("install review without install")
	}
	before = f.cli.count(previewKey)
	if code := apply(); code != 202 {
		t.Errorf("plain apply: %d", code)
	}
	if f.cli.count(previewKey) != before {
		t.Error("a plain apply read the preview")
	}
	if got := f.run.wait(t, 3)[2]; slices.Contains(got, "--accept-install") {
		t.Errorf("plain argv %q", got)
	}
	time.Sleep(20 * time.Millisecond)
	if n := len(f.run.Argvs()); n != 3 {
		t.Errorf("%d jobs, want 3", n)
	}
}

func TestNewInstallReview(t *testing.T) {
	f := newProjectFixture(t)
	key := "--json " + strings.Join(stepOneArgs, " ") + " --dry-run"
	f.cli.set(key, strings.Replace(dryRunJSON, `"steps":`, `"install":`+installJSON+`,"steps":`, 1))
	body := f.do(req("POST", "/new/review", stepOne())).Body.String()
	contains(t, "review", body,
		"<h2>Install review</h2>", `<span class="mono">acme:pgx@1</span> <span class="chip">option</span> <span class="chip">added</span>`,
		`data-create data-ack-required>`, `<input type="hidden" name="accept_install" value="`+newHash+`">`,
		`<input type="checkbox" name="ack" value="1"> I&#39;ve read the 2 flagged items`,
		`<button class="ln main" disabled>Create project</button>`,
		"&lt;/pre&gt;&lt;script&gt;alert(1)&lt;/script&gt;",
	)
	if strings.Contains(body, "<script>alert") {
		t.Error("install diff unescaped")
	}
	noCSPViolation(t, "review", body)

	for _, c := range []struct {
		name string
		v    []string
	}{
		{"no tick", []string{"accept_install", newHash}},
		{"another review", []string{"accept_install", testHash, "ack", "1"}},
	} {
		if w := f.do(req("POST", "/new/create", stepOne(c.v...))); w.Code != 400 {
			t.Errorf("%s: %d %s, want 400", c.name, w.Code, w.Body)
		}
	}
	if w := f.do(req("POST", "/new/create", stepOne("accept_install", newHash, "ack", "1", "up", "1"))); w.Code != 202 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	want := append(append(append([]string{testExe}, stepOneArgs...), "--accept-install", newHash), "--up")
	if got := f.run.wait(t, 1)[0]; !slices.Equal(got, want) {
		t.Errorf("argv %q\nwant %q", got, want)
	}
	f.endedIn(t, "masto")

	f.cli.fail(key, errors.New("prefix masto is used"))
	if w := f.do(req("POST", "/new/create", stepOne("accept_install", newHash, "ack", "1"))); w.Code != 422 {
		t.Errorf("dry run failed: %d", w.Code)
	}
	time.Sleep(20 * time.Millisecond)
	if n := len(f.run.Argvs()); n != 1 {
		t.Errorf("%d jobs, want 1", n)
	}
}

func TestSignerWarningsHeader(t *testing.T) {
	f := settingsFixture(t)
	f.cli.set("--json -C /ws/ftest overview", strings.Replace(ftestOverview("jb/b", "up", agentJSON("idle", "", false)),
		`"workspace":"/ws/ftest",`, `"workspace":"/ws/ftest","signer_warnings":[
  {"ref":"acme:pgx@1","library":"github.com/acme/lib","fingerprint":"`+fpA+`"},
  {"ref":"acme:<script>x</script>@1","library":"<b>lib</b>","fingerprint":"`+fpB+`"}],`, 1))
	want := `<div class="signer-warning">The signer of acme:pgx@1 (github.com/acme/lib) is no longer trusted. The project keeps running what it has; replace it or re-check the library.</div>`
	for _, page := range []string{"/p/ftest", "/p/ftest/settings"} {
		body := f.do(req("GET", page, nil)).Body.String()
		contains(t, page, body, want, `<div id="banner" class="banner">`, "acme:&lt;script&gt;x&lt;/script&gt;@1 (&lt;b&gt;lib&lt;/b&gt;)")
		if strings.Contains(body, "<script>x") || strings.Contains(body, "<b>lib") {
			t.Errorf("%s: warning unescaped", page)
		}
	}
	if body := f.do(req("GET", "/p/hello", nil)).Body.String(); strings.Contains(body, "signer-warning") {
		t.Error("hello shows ftest's signer warning")
	}
	g := newFixture(t)
	if body := g.do(req("GET", "/p/ftest", nil)).Body.String(); strings.Contains(body, "signer-warning") || !strings.Contains(body, `<div id="banner" class="banner" hidden>`) {
		t.Error("signer warning without signer_warnings")
	}
}
