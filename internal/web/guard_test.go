package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/jobs"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys"
)

const (
	testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testExe   = "/usr/bin/railswitch"
	testHost  = "railswitch.localhost:7700"
	wantCSP   = "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
)

// fakeCLI answers `railswitch <args>` from scripted outputs, keyed by the
// args joined with spaces, and records every call. during, when set, runs
// inside each call (a read in flight).
type fakeCLI struct {
	mu     sync.Mutex
	out    map[string]string
	errs   map[string]error
	calls  [][]string
	stdins [][]byte // per call; nil for Run
	during func(key string)
}

func (f *fakeCLI) Run(ctx context.Context, args ...string) ([]byte, error) {
	return f.RunInput(ctx, nil, args...)
}

func (f *fakeCLI) RunInput(_ context.Context, stdin []byte, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	f.mu.Lock()
	during := f.during
	f.mu.Unlock()
	if during != nil {
		during(key)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, slices.Clone(args))
	f.stdins = append(f.stdins, slices.Clone(stdin))
	if err := f.errs[key]; err != nil {
		return nil, err
	}
	out, ok := f.out[key]
	if !ok {
		return nil, fmt.Errorf("fake: nothing scripted for %q", key)
	}
	return []byte(out), nil
}

func (f *fakeCLI) set(key, out string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.errs, key)
	f.out[key] = out
}

func (f *fakeCLI) fail(key string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[key] = err
}

func (f *fakeCLI) Calls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// count is how many calls had args joined equal to key.
func (f *fakeCLI) count(key string) int {
	n := 0
	for _, c := range f.Calls() {
		if strings.Join(c, " ") == key {
			n++
		}
	}
	return n
}

// recRunner is the job hub's runner: it records each argv (the
// systemd-run scope prefix cut off) and stdin (nil: none) and, with
// block set, keeps each job running until block is closed. A job writes
// out and, with exit set, fails with that code.
type recRunner struct {
	mu     sync.Mutex
	argvs  [][]string
	stdins [][]byte
	block  chan struct{}
	out    string
	exit   int
}

func (r *recRunner) Run(ctx context.Context, c sys.Cmd) (sys.Result, error) {
	argv := append([]string{c.Name}, c.Args...)
	if i := slices.Index(argv, "--"); i >= 0 && argv[0] == "systemd-run" {
		argv = argv[i+1:]
	}
	var in []byte
	if c.Stdin != nil {
		in, _ = io.ReadAll(c.Stdin)
	}
	r.mu.Lock()
	r.argvs = append(r.argvs, argv)
	r.stdins = append(r.stdins, in)
	block, out, exit := r.block, r.out, r.exit
	r.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
		}
	}
	if out != "" {
		_, _ = io.WriteString(c.Stdout, out)
	}
	if exit != 0 {
		return sys.Result{ExitCode: exit}, fmt.Errorf("job: %w (exit %d)", sys.ErrExit, exit)
	}
	return sys.Result{}, nil
}

func (r *recRunner) Stdins() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.stdins)
}

func (r *recRunner) Argvs() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.argvs)
}

// wait returns the runner's argvs once there are n of them.
func (r *recRunner) wait(t *testing.T, n int) [][]string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := r.Argvs()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("runner saw %d jobs, want %d: %q", len(got), n, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// fakeClock is Config.Now.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

const projectLS = `[
 {"prefix":"ftest","workspace":"/ws/ftest","subnet":"10.214.0.0/24","worktrees":2,"workspace_missing":false},
 {"prefix":"hello","workspace":"/ws/hello","subnet":"10.214.1.0/24","worktrees":1,"workspace_missing":false}
]`

// ftestOverview is ftest with base main and worktree b; agentB is b's
// agent object ("null" for none).
func ftestOverview(branchB, stackB, agentB string) string {
	return `{"prefix":"ftest","workspace":"/ws/ftest","worktrees":[
 {"slug":"main","id":1,"branch":"main","base":true,"stack":"up",
  "services":[{"service":"web","role":"app","state":"active/running"}],
  "agent":{"name":"bot","package":"bot@1","run":"on-demand","state":"working","message":"busy","ts":1,"reply":false,"running":true},
  "links":[{"label":"App","url":"http://main.ftest.localhost/","group":"app"}]},
 {"slug":"b","id":2,"branch":"` + branchB + `","base":false,"stack":"` + stackB + `",
  "services":[{"service":"web","role":"app","state":"active/running","container":"ftest-b-web","restartable":true},{"service":"db","role":"data","state":"inactive/dead","container":"ftest-b-db","restartable":true}],
  "agent":` + agentB + `,
  "links":[{"label":"App","url":"http://b.ftest.localhost/","group":"app"},{"label":"Flipper","url":"http://b.ftest.localhost/flipper","group":"project"},{"label":"Browser (VNC)","url":"http://vnc.b.ftest.localhost","group":"stack"}]}
]}`
}

// agentJSON is an agent object in state with message.
func agentJSON(state, message string, reply bool) string {
	return fmt.Sprintf(`{"name":"bot","package":"bot@1","run":"on-demand","state":%q,"message":%q,"ts":1,"reply":%t,"running":true}`, state, message, reply)
}

func helloOverview(agent string) string {
	return `{"prefix":"hello","workspace":"/ws/hello","worktrees":[
 {"slug":"master","id":3,"branch":"master","base":true,"stack":"stopped","services":[],"agent":` + agent + `,"links":[]}
]}`
}

const ftestPRs = `[
 {"slug":"main","branch":"main","pr":null},
 {"slug":"b","branch":"jb/b","pr":{"number":2141,"title":"Login <b>","url":"https://github.com/o/r/pull/2141","state":"open","checks":"pass","review":"approved"}}
]`

const ftestAgents = `[{"name":"claude","config":true,"default_pending":true,"running":true,"default":{"from":"main","promoted":"2026-10-30T12:00:00Z"}},{"name":"probe","config":false,"default":null}]`

const ftestTasks = `[{"name":"console","command":"bin/rails console","source":"pack"},{"name":"test","command":"bin/rails test","source":"project"}]`

const colorsJSON = `{"light":{"base":"flexoki","overrides":{},"colors":{"bg":"#FFFCF0","bg2":"#F2F0E5","text":"#100F0F","muted":"#6F6E69","accent":"#BC5215","blue":"#205EA6","green":"#536907","yellow":"#8E6B01","sel":"#E6E4D9","border":"#CECDC3","purple":"#5E409D","red":"#AF3029"}},
 "dark":{"base":"flexoki","overrides":{},"colors":{"bg":"#100F0F","bg2":"#1C1B1A","text":"#CECDC3","muted":"#878580","accent":"#DA702C","blue":"#4385BE","green":"#879A39","yellow":"#D0A215","sel":"#282726","border":"#403E3C","purple":"#8B7EC8","red":"#D14D41"}},
 "warnings":{"light":[],"dark":[]}}`

const wtLS = `[{"slug":"main","id":1,"branch":"main","dir":"/ws/ftest/main","dirty":false,"share":null},{"slug":"b","id":2,"branch":"jb/b","dir":"/ws/ftest/b","dirty":true,"share":null}]`

type fixture struct {
	s     *Server
	cli   *fakeCLI
	run   *recRunner
	clock *fakeClock
	logs  string // the job hub's log directory
}

// newFixture is a server over the scripted CLI and a real job hub.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	cli := &fakeCLI{out: map[string]string{
		"--json project ls":                   projectLS,
		"--json -C /ws/ftest overview":        ftestOverview("jb/b", "up", agentJSON("idle", "", false)),
		"--json -C /ws/hello overview":        helloOverview("null"),
		"--json -C /ws/ftest pr ls":           ftestPRs,
		"--json -C /ws/hello pr ls":           `[{"slug":"master","branch":"master","pr":null}]`,
		"--json -C /ws/ftest -w b task ls":    ftestTasks,
		"--json -C /ws/ftest -w b agent ls":   ftestAgents,
		"--json -C /ws/ftest -w main task ls": ftestTasks,
		"--json -C /ws/ftest wt ls":           wtLS,
		"--json dashboard colors":             colorsJSON,
	}, errs: map[string]error{}}
	run := &recRunner{}
	logs := t.TempDir()
	hub, err := jobs.New(run, logs, func(context.Context) []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	s := New(Config{Token: testToken, CLI: cli, Jobs: hub, Exe: testExe, Version: "v9", Now: clock.Now})
	t.Cleanup(func() {
		run.mu.Lock()
		if run.block != nil {
			close(run.block)
			run.block = nil
		}
		run.mu.Unlock()
		deadline := time.Now().Add(2 * time.Second)
		for hub.Running() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
	})
	return &fixture{s: s, cli: cli, run: run, clock: clock, logs: logs}
}

// req is a request as the logged-in page sends it: allowed Host, the
// cookie, and for a POST the matching Origin and a form body.
func req(method, target string, form url.Values) *http.Request {
	var r *http.Request
	if form != nil {
		r = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.Host = testHost
	r.AddCookie(&http.Cookie{Name: "railswitch_dashboard", Value: testToken})
	if method == http.MethodPost {
		r.Header.Set("Origin", "http://"+testHost)
	}
	return r
}

func (f *fixture) do(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, r)
	return w
}

func checkHeaders(t *testing.T, what string, w *httptest.ResponseRecorder) {
	t.Helper()
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("%s: X-Frame-Options %q", what, got)
	}
	if got := w.Header().Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("%s: CSP %q", what, got)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "same-origin" {
		t.Errorf("%s: Referrer-Policy %q", what, got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("%s: X-Content-Type-Options %q", what, got)
	}
}

// TestGuardRefuses: every refusal has the security headers and runs
// nothing, neither a read through the CLI nor a job.
func TestGuardRefuses(t *testing.T) {
	f := newFixture(t)
	noCookie := func(r *http.Request) *http.Request { r.Header.Del("Cookie"); return r }
	host := func(r *http.Request) *http.Request { r.Host = "evil.example:7700"; return r }
	for _, c := range []struct {
		name string
		r    *http.Request
		code int
	}{
		{"host GET", host(req("GET", "/p/ftest", nil)), 403},
		{"host POST", host(req("POST", "/p/ftest/b/act/up", url.Values{})), 403},
		{"host healthz", host(req("GET", "/healthz", nil)), 403},
		{"no cookie page", noCookie(req("GET", "/p/ftest", nil)), 401},
		{"no cookie events", noCookie(req("GET", "/events?p=ftest", nil)), 401},
		{"no cookie static", noCookie(req("GET", "/static/app.js", nil)), 401},
		{"wrong cookie", func() *http.Request {
			r := noCookie(req("GET", "/p/ftest", nil))
			r.AddCookie(&http.Cookie{Name: "railswitch_dashboard", Value: "nope"})
			return r
		}(), 401},
		{"bad login", req("GET", "/login?token=bad", nil), 403},
		{"POST without Origin", func() *http.Request {
			r := req("POST", "/p/ftest/b/act/up", url.Values{})
			r.Header.Del("Origin")
			return r
		}(), 403},
		{"POST foreign Origin", func() *http.Request {
			r := req("POST", "/p/ftest/b/act/up", url.Values{})
			r.Header.Set("Origin", "http://evil.example")
			return r
		}(), 403},
		{"POST other allowed host as Origin", func() *http.Request {
			r := req("POST", "/p/ftest/b/act/up", url.Values{})
			r.Header.Set("Origin", "http://localhost:7700")
			return r
		}(), 403},
	} {
		w := f.do(c.r)
		if w.Code != c.code {
			t.Errorf("%s: %d, want %d", c.name, w.Code, c.code)
		}
		checkHeaders(t, c.name, w)
		if sc := w.Header().Get("Set-Cookie"); sc != "" {
			t.Errorf("%s: Set-Cookie %q", c.name, sc)
		}
	}
	if calls := f.cli.Calls(); len(calls) != 0 {
		t.Errorf("CLI calls on refusal: %q", calls)
	}
	time.Sleep(20 * time.Millisecond)
	if argvs := f.run.Argvs(); len(argvs) != 0 {
		t.Errorf("jobs on refusal: %q", argvs)
	}
}

// TestGuardLoginAndAccept: a good login sets the cookie and redirects;
// a good POST runs its job; healthz needs no cookie.
func TestGuardLoginAndAccept(t *testing.T) {
	f := newFixture(t)
	r := req("GET", "/login?token="+testToken, nil)
	r.Header.Del("Cookie")
	w := f.do(r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("login: %d %q", w.Code, w.Header().Get("Location"))
	}
	checkHeaders(t, "login", w)
	sc := w.Header().Get("Set-Cookie")
	for _, want := range []string{"railswitch_dashboard=" + testToken, "HttpOnly", "SameSite=Strict", "Path=/", "Max-Age=31536000"} {
		if !strings.Contains(sc, want) {
			t.Errorf("Set-Cookie %q lacks %q", sc, want)
		}
	}

	r = httptest.NewRequest("GET", "/healthz", nil)
	r.Host = "127.0.0.1:7700"
	w = f.do(r)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "ok v9" {
		t.Errorf("healthz: %d %q", w.Code, w.Body.String())
	}
	nonce := strings.Repeat("ab", 16)
	r = httptest.NewRequest("GET", "/healthz?nonce="+nonce, nil)
	r.Host = "127.0.0.1:7700"
	if w := f.do(r); strings.TrimSpace(w.Body.String()) != "ok v9 "+machine.ProbeProof(testToken, nonce) {
		t.Errorf("healthz with a nonce: %q", w.Body.String())
	}
	r = httptest.NewRequest("GET", "/healthz?nonce=../x", nil)
	r.Host = "127.0.0.1:7700"
	if w := f.do(r); strings.TrimSpace(w.Body.String()) != "ok v9" {
		t.Errorf("healthz with a bad nonce: %q", w.Body.String())
	}
	checkHeaders(t, "healthz", w)

	for _, host := range []string{"localhost:7700", "127.0.0.1:7700"} {
		r := req("GET", "/p/ftest", nil)
		r.Host = host
		if w := f.do(r); w.Code != 200 {
			t.Errorf("GET via %s: %d", host, w.Code)
		}
	}

	w = f.do(req("POST", "/p/ftest/b/act/up", url.Values{}))
	if w.Code != http.StatusAccepted {
		t.Fatalf("act/up: %d %s", w.Code, w.Body)
	}
	checkHeaders(t, "act/up", w)
	got := f.run.wait(t, 1)
	if want := []string{testExe, "-C", "/ws/ftest", "-w", "b", "up"}; !slices.Equal(got[0], want) {
		t.Errorf("job argv %q, want %q", got[0], want)
	}
}

// TestGuardCacheControl: pages and events are never cached.
func TestGuardCacheControl(t *testing.T) {
	f := newFixture(t)
	w := f.do(req("GET", "/p/ftest", nil))
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("page Cache-Control %q", got)
	}
}

// TestGuardFormPost: a plain form POST (no script: no Accept JSON) with
// the page's own Origin is accepted and answered with a page.
func TestGuardFormPost(t *testing.T) {
	f := newFixture(t)
	r := httptest.NewRequest("POST", "/p/ftest/b/act/restart", strings.NewReader(""))
	r.Host = "localhost:7700"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	r.Header.Set("Origin", "http://localhost:7700")
	r.AddCookie(&http.Cookie{Name: "railswitch_dashboard", Value: testToken})
	w := f.do(r)
	if w.Code != http.StatusAccepted || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), `href="/p/ftest/b"`) {
		t.Fatalf("%d %q %s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	if got := f.run.wait(t, 1)[0]; !slices.Equal(got, []string{testExe, "-C", "/ws/ftest", "-w", "b", "restart"}) {
		t.Errorf("argv %q", got)
	}
}

// TestGuardHostHint: the refusal names the Host it got, quoted, and the
// addresses that work.
func TestGuardHostHint(t *testing.T) {
	f := newFixture(t)
	r := req("GET", "/login?token=x", nil)
	r.Host = "localhost:7701\x1b[2J"
	rec := f.do(r)
	body := rec.Body.String()
	if rec.Code != 403 || !strings.Contains(body, `forbidden host "localhost:7701\x1b[2J"`) || !strings.Contains(body, "http://railswitch.localhost:7700/") || strings.Contains(body, "\x1b") {
		t.Errorf("%d %q", rec.Code, body)
	}
}
