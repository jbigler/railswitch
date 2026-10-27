package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func form(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return v
}

// TestActions: each POST runs the job argv it names, or refuses with the
// right status and runs nothing.
func TestActions(t *testing.T) {
	ws := []string{testExe, "-C", "/ws/ftest"}
	wsb := append(slices.Clone(ws), "-w", "b")
	for _, c := range []struct {
		name   string
		path   string
		form   url.Values
		code   int
		argv   []string // nil = no job
		bodyIn string
	}{
		{"add", "/p/ftest/worktrees", form("branch", "jb/x", "name", "x", "up", "on"), 202, append(slices.Clone(ws), "wt", "add", "--name", "x", "--up", "jb/x"), ""},
		{"add plain", "/p/ftest/worktrees", form("branch", "jb/x"), 202, append(slices.Clone(ws), "wt", "add", "jb/x"), ""},
		{"add no branch", "/p/ftest/worktrees", form("name", "x"), 422, nil, "branch"},
		{"add flag branch", "/p/ftest/worktrees", form("branch", "--force"), 422, nil, "branch"},
		{"add unknown project", "/p/nope/worktrees", form("branch", "jb/x"), 404, nil, ""},
		{"remove", "/p/ftest/b/remove", form(), 202, append(slices.Clone(ws), "wt", "rm", "b"), ""},
		{"remove force wrong", "/p/ftest/b/remove", form("force", "1", "confirm", "wrong"), 422, nil, "type"},
		{"remove force", "/p/ftest/b/remove", form("force", "1", "confirm", "b"), 202, append(slices.Clone(ws), "wt", "rm", "--force", "b"), ""},
		{"remove base", "/p/ftest/main/remove", form(), 422, nil, "base"},
		{"remove unknown", "/p/ftest/nope/remove", form(), 404, nil, ""},
		{"act unknown slug", "/p/ftest/nope/act/up", form(), 404, nil, ""},
		{"act unknown project", "/p/nope/b/act/up", form(), 404, nil, ""},
		{"act unknown action", "/p/ftest/b/act/down", form(), 404, nil, ""},
		{"stop", "/p/ftest/b/act/stop", form(), 202, append(slices.Clone(wsb), "stop"), ""},
		{"restart", "/p/ftest/main/act/restart", form(), 202, append(slices.Clone(ws), "-w", "main", "restart"), ""},
		{"task", "/p/ftest/b/task", form("task", "test", "args", `a "b c"`), 202, append(slices.Clone(wsb), "task", "test", "a", "b c"), ""},
		{"task term", "/p/ftest/b/task", form("task", "console", "term", "1"), 202, append(slices.Clone(wsb), "task", "--new-term", "console"), ""},
		{"task unknown", "/p/ftest/b/task", form("task", "nope"), 404, nil, ""},
		{"task bad args", "/p/ftest/b/task", form("task", "test", "args", `"x`), 422, nil, "unterminated"},
	} {
		f := newFixture(t)
		w := f.do(req("POST", c.path, c.form))
		if w.Code != c.code {
			t.Errorf("%s: %d %q, want %d", c.name, w.Code, w.Body, c.code)
			continue
		}
		if !strings.Contains(w.Body.String(), c.bodyIn) {
			t.Errorf("%s: body %q lacks %q", c.name, w.Body, c.bodyIn)
		}
		if c.argv == nil {
			time.Sleep(10 * time.Millisecond)
			if got := f.run.Argvs(); len(got) != 0 {
				t.Errorf("%s: ran %q", c.name, got)
			}
			continue
		}
		if got := f.run.wait(t, 1)[0]; !slices.Equal(got, c.argv) {
			t.Errorf("%s: argv %q, want %q", c.name, got, c.argv)
		}
	}
}

// TestAcceptedJSON: a fetch gets the job id as JSON.
func TestAcceptedJSON(t *testing.T) {
	f := newFixture(t)
	r := req("POST", "/p/ftest/b/act/up", form())
	r.Header.Set("Accept", "application/json")
	w := f.do(r)
	var got struct{ ID, Label string }
	if err := json.Unmarshal(w.Body.Bytes(), &got); w.Code != 202 || err != nil || got.ID == "" || got.Label != "up" {
		t.Errorf("%d %q (%v)", w.Code, w.Body, err)
	}
	r = req("POST", "/jobs/"+got.ID+"/cancel", form())
	if w := f.do(r); w.Code != 202 {
		t.Errorf("cancel: %d %q", w.Code, w.Body)
	}
	if w := f.do(req("POST", "/jobs/nope/cancel", form())); w.Code != 404 {
		t.Errorf("cancel unknown: %d", w.Code)
	}
}

// TestBusy: a second job for a busy worktree is 409.
func TestBusy(t *testing.T) {
	f := newFixture(t)
	f.run.block = make(chan struct{})
	if w := f.do(req("POST", "/p/ftest/b/act/up", form())); w.Code != 202 {
		t.Fatalf("first: %d", w.Code)
	}
	w := f.do(req("POST", "/p/ftest/b/act/stop", form()))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "busy: up") {
		t.Errorf("second: %d %q", w.Code, w.Body)
	}
	if w := f.do(req("POST", "/p/ftest/worktrees", form("branch", "jb/y"))); w.Code != 409 {
		t.Errorf("project job while b busy: %d", w.Code)
	}
	if w := f.do(req("POST", "/p/ftest/main/act/up", form())); w.Code != 202 {
		t.Errorf("other worktree: %d %q", w.Code, w.Body)
	}
}

// TestColors: the editor runs `dashboard colors set|reset` through the
// CLI, synchronously, and refuses bad input.
func TestColors(t *testing.T) {
	f := newFixture(t)
	f.cli.set("dashboard colors set --base paper accent=#7C3AED", "")
	f.cli.set("dashboard colors set --dark --base solar", "")
	f.cli.set("dashboard colors reset --dark", "")
	for _, c := range []struct {
		path string
		form url.Values
		code int
		call string
	}{
		{"/colors", form("mode", "light", "base", "paper", "accent", "#7C3AED", "sel", "#000000", "nope", "#000000"), 200, "dashboard colors set --base paper accent=#7C3AED"},
		{"/colors", form("mode", "dark", "base", "solar"), 200, "dashboard colors set --dark --base solar"},
		{"/colors", form("mode", "light", "base", "paper", "accent", "red"), 422, ""},
		{"/colors", form("mode", "light", "base", "nope"), 422, ""},
		{"/colors", form("mode", "sepia", "base", "paper"), 422, ""},
		{"/colors/reset", form("mode", "dark"), 200, "dashboard colors reset --dark"},
	} {
		before := len(f.cli.Calls())
		w := f.do(req("POST", c.path, c.form))
		if w.Code != c.code {
			t.Errorf("%s %v: %d %q, want %d", c.path, c.form, w.Code, w.Body, c.code)
			continue
		}
		calls := f.cli.Calls()[before:]
		if c.call == "" {
			if len(calls) != 0 {
				t.Errorf("%v: ran %q", c.form, calls)
			}
			continue
		}
		if len(calls) == 0 || strings.Join(calls[0], " ") != c.call {
			t.Errorf("%v: calls %q, want %q first", c.form, calls, c.call)
		}
	}
	time.Sleep(10 * time.Millisecond)
	if got := f.run.Argvs(); len(got) != 0 {
		t.Errorf("colours ran jobs: %q", got)
	}
}

// TestPages: the pages and fragments render from the CLI's JSON.
func TestPages(t *testing.T) {
	f := newFixture(t)
	w := f.do(req("GET", "/", nil))
	if w.Code != 303 || w.Header().Get("Location") != "/p/ftest" {
		t.Errorf("/: %d %q", w.Code, w.Header().Get("Location"))
	}
	for _, c := range []struct {
		path string
		code int
		want []string
	}{
		{"/p/ftest", 200, []string{`id="row-main"`, `id="row-b"`, `id="detail"`, `/static/app.js`, `/theme.css`, "<h1>main</h1>"}},
		{"/p/ftest/b?sort=-name", 200, []string{"<h1>b</h1>", `class="sel"`, "Flipper", `href="/p/ftest/b?sort=name"`}},
		{"/p/nope", 404, nil},
		{"/p/ftest/nope", 404, nil},
		{"/p/ftest/b/remove", 200, []string{"Discard changes", `name="confirm"`, "/ws/ftest/b"}},
		{"/p/ftest/main/remove", 422, nil},
		{"/p/ftest/b/tasks", 200, []string{"console", "bin/rails test", `name="args"`}},
		{"/colors", 200, []string{`value="#BC5215"`, "paper", "solar", "contrast"}},
		{"/theme.css", 200, []string{"--accent:#BC5215", "prefers-color-scheme: dark", "--accent:#DA702C"}},
		{"/static/app.js", 200, []string{"EventSource"}},
		{"/static/app.css", 200, []string{"--accent"}},
	} {
		w := f.do(req("GET", c.path, nil))
		if w.Code != c.code {
			t.Errorf("%s: %d %q", c.path, w.Code, w.Body)
			continue
		}
		for _, want := range c.want {
			if !strings.Contains(w.Body.String(), want) {
				t.Errorf("%s lacks %q", c.path, want)
			}
		}
		html := strings.HasPrefix(w.Header().Get("Content-Type"), "text/html")
		if body := w.Body.String(); html && (strings.Contains(body, " style=") || strings.Contains(body, "<style") || strings.Contains(body, "onclick") || (strings.Contains(body, "<script") && !strings.Contains(body, `<script src="/static/app.js" defer></script>`))) {
			t.Errorf("%s: inline style or script (CSP)", c.path)
		}
	}
}

// TestEventsStream: /events sends the current state, then pings.
func TestEventsStream(t *testing.T) {
	f := newFixture(t)
	f.s.tick = 10 * time.Millisecond
	f.s.ping = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.s.Run(ctx) }()
	ts := httptest.NewServer(f.s.Handler())
	defer ts.Close()

	r, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/events?p=ftest&w=b&sort=name", nil)
	r.Host = testHost
	r.AddCookie(&http.Cookie{Name: "railswitch_dashboard", Value: testToken})
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("headers %q %q", ct, resp.Header.Get("Cache-Control"))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(nil, 1<<20)
	seen := map[string]bool{}
	pinged := false
	deadline := time.AfterFunc(2*time.Second, cancel)
	defer deadline.Stop()
	for sc.Scan() && !(pinged && seen["detail"]) {
		line := sc.Text()
		if line == ": ping" {
			pinged = true
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			var ev event
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				t.Fatalf("event %q: %v", data, err)
			}
			seen[ev.ID] = true
		}
	}
	if !pinged || !seen["header"] || !seen["list"] || !seen["detail"] {
		t.Errorf("pinged %t, seen %v", pinged, seen)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run: %v", err)
	}
	if w := f.do(req("GET", "/events?p=nope", nil)); w.Code != 404 {
		t.Errorf("events for unknown project: %d", w.Code)
	}
}
