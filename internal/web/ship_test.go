package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// TestShipPage: the starship page loads its module for the project.
func TestShipPage(t *testing.T) {
	f := newFixture(t)
	w := f.do(req("GET", "/p/ftest/ship", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `src="/static/ship/ship.js"`) || !strings.Contains(w.Body.String(), `data-prefix="ftest"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := f.do(req("GET", "/p/nope/ship", nil)); w.Code != 404 {
		t.Errorf("unknown project: %d", w.Code)
	}
	for _, p := range []string{"/static/ship/three.js", "/static/ship/models/computer.glb", "/static/ship/models/Textures/colormap.png"} {
		if w := f.do(req("GET", p, nil)); w.Code != 200 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

// TestShipReads: the ship's reads and writes run the CLI with exactly
// these arguments, text on stdin; bad names never reach it.
func TestShipReads(t *testing.T) {
	for _, c := range []struct {
		name, method, path, body string
		code                     int
		key                      string // the CLI call, "" for none
	}{
		{"issues", "GET", "/p/ftest/ship/issues", "", 200, "--json -C /ws/ftest issue ls"},
		{"notes", "GET", "/p/ftest/ship/notes", "", 200, "--json -C /ws/ftest notes ls"},
		{"note", "GET", "/p/ftest/ship/notes/setup_steps.md", "", 200, "--json -C /ws/ftest notes show setup_steps.md"},
		{"note bad name", "GET", "/p/ftest/ship/notes/..%2Fsecret.md", "", 400, ""},
		{"note not md", "GET", "/p/ftest/ship/notes/x.txt", "", 400, ""},
		{"note write", "POST", "/p/ftest/ship/notes/a.md?if-hash=abc", "hello", 200, "--json -C /ws/ftest notes write a.md --if-hash abc"},
		{"note create", "POST", "/p/ftest/ship/notes/a.md?if-hash=", "hi", 200, "--json -C /ws/ftest notes write a.md --if-hash "},
		{"note rm", "POST", "/p/ftest/ship/notes/a.md?rm=1&if-hash=abc", "", 200, "-C /ws/ftest notes rm a.md --if-hash abc"},
		{"note rm no hash", "POST", "/p/ftest/ship/notes/a.md?rm=1", "", 400, ""},
		{"screen", "GET", "/p/ftest/ship/agent/b/screen", "", 200, "--json -C /ws/ftest -w b agent screen"},
		{"screen named", "GET", "/p/ftest/ship/agent/b/screen?agent=claude", "", 200, "--json -C /ws/ftest -w b agent screen claude"},
		{"screen flag name", "GET", "/p/ftest/ship/agent/b/screen?agent=--all", "", 400, ""},
		{"screen unknown worktree", "GET", "/p/ftest/ship/agent/nope/screen", "", 404, ""},
		{"send", "POST", "/p/ftest/ship/agent/b/send?agent=claude", "run the tests", 200, "-C /ws/ftest -w b agent send claude"},
		{"send empty", "POST", "/p/ftest/ship/agent/b/send", "", 400, ""},
		{"send too long", "POST", "/p/ftest/ship/agent/b/send", strings.Repeat("x", sendMax+1), 400, ""},
	} {
		f := newFixture(t)
		if c.key != "" {
			f.cli.set(c.key, `{}`)
		}
		before := len(f.cli.calls)
		r := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		r.Host = testHost
		r.AddCookie(req("GET", "/", nil).Cookies()[0])
		if c.method == "POST" {
			r.Header.Set("Origin", "http://"+testHost)
		}
		w := f.do(r)
		if w.Code != c.code {
			t.Errorf("%s: %d %q, want %d", c.name, w.Code, w.Body, c.code)
			continue
		}
		f.cli.mu.Lock()
		var ran []string
		for _, a := range f.cli.calls[before:] {
			ran = append(ran, strings.Join(a, " "))
		}
		stdins := slices.Clone(f.cli.stdins[before:])
		f.cli.mu.Unlock()
		if c.key == "" {
			for _, k := range ran {
				if !strings.Contains(k, " overview") && k != "--json project ls" {
					t.Errorf("%s: ran %q", c.name, k)
				}
			}
			continue
		}
		i := slices.Index(ran, c.key)
		if i < 0 {
			t.Errorf("%s: ran %q, want %q", c.name, ran, c.key)
			continue
		}
		if c.method == "POST" && !strings.Contains(c.key, " rm ") && string(stdins[i]) != c.body {
			t.Errorf("%s: stdin %q, want %q", c.name, stdins[i], c.body)
		}
	}
}

// TestShipState: a ship page's state is the overview's worktrees with
// their PRs, arrays never null.
func TestShipState(t *testing.T) {
	f := newFixture(t)
	f.s.pollOnce(context.Background())
	f.s.mu.Lock()
	st := f.s.shipData("ftest")
	f.s.mu.Unlock()
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	wts, _ := got["worktrees"].([]any)
	if len(wts) == 0 || got["jobs"] == nil || got["banner"] == nil {
		t.Fatalf("%s", b)
	}
	for _, w := range wts {
		m := w.(map[string]any)
		if m["services"] == nil || m["links"] == nil || m["slug"] == "" {
			t.Errorf("worktree %v", m)
		}
	}
	if st := f.s.shipData("nope"); st.Error == "" {
		t.Error("unknown project has no error")
	}
}
