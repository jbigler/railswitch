package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// TestCyberpunkPage: the cyberpunk page loads its module, the office kit and its models.
func TestCyberpunkPage(t *testing.T) {
	f := newFixture(t)
	w := f.do(req("GET", "/p/ftest/virtual-office/cyberpunk", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `src="/static/cyberpunk/cyberpunk.js"`) || !strings.Contains(w.Body.String(), `data-prefix="ftest"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// D151: data: images (the whiteboard's icons, pasted pictures) on this
	// page only; scripts and styles stay 'self'.
	if got := w.Header().Get("Content-Security-Policy"); got != wantCSP+"; img-src 'self' data:" {
		t.Errorf("CSP %q", got)
	}
	if got := f.do(req("GET", "/p/ftest", nil)).Header().Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("dashboard CSP %q", got)
	}
	if w := f.do(req("GET", "/p/nope/virtual-office/cyberpunk", nil)); w.Code != 404 {
		t.Errorf("unknown project: %d", w.Code)
	}
	// the feature's own path, the set last (D150): the bare path goes to the
	// built-in set, and a worktree slug can no longer be the office's word
	if w := f.do(req("GET", "/p/ftest/virtual-office", nil)); w.Code != http.StatusFound || w.Header().Get("Location") != "/p/ftest/virtual-office/cyberpunk" {
		t.Errorf("virtual-office: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := f.do(req("GET", "/p/ftest/cyberpunk", nil)); strings.Contains(w.Body.String(), `src="/static/cyberpunk/cyberpunk.js"`) {
		t.Error("/p/ftest/cyberpunk still serves the office: a worktree named cyberpunk would be hidden")
	}
	for _, p := range []string{"/static/cyberpunk/three.js", "/static/office/kit.js", "/static/office/models/Furn_Workstation_Lived.gltf", "/static/office/models/Furn_Workstation_Lived.bin", "/static/office/models/RS_ScreenCode.png"} {
		if w := f.do(req("GET", p, nil)); w.Code != 200 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

// TestCyberpunkReads: the cyberpunk page's reads and writes run the CLI with exactly
// these arguments, text on stdin; bad names never reach it.
func TestCyberpunkReads(t *testing.T) {
	for _, c := range []struct {
		name, method, path, body string
		code                     int
		key                      string // the CLI call, "" for none
	}{
		{"issues", "GET", "/p/ftest/virtual-office/issues", "", 200, "--json -C /ws/ftest issue ls"},
		{"prs", "GET", "/p/ftest/virtual-office/prs", "", 200, "--json -C /ws/ftest pr ls --all"},
		{"issue start bad", "POST", "/p/ftest/virtual-office/issues/-1/start", "", 400, ""},
		{"issue start word", "POST", "/p/ftest/virtual-office/issues/abc/start", "", 400, ""},
		{"notes", "GET", "/p/ftest/virtual-office/notes", "", 200, "--json -C /ws/ftest notes ls"},
		{"note", "GET", "/p/ftest/virtual-office/notes/setup_steps.md", "", 200, "--json -C /ws/ftest notes show setup_steps.md"},
		{"note bad name", "GET", "/p/ftest/virtual-office/notes/..%2Fsecret.md", "", 400, ""},
		{"note not md", "GET", "/p/ftest/virtual-office/notes/x.txt", "", 400, ""},
		{"note write", "POST", "/p/ftest/virtual-office/notes/a.md?if-hash=abc", "hello", 200, "--json -C /ws/ftest notes write a.md --if-hash abc"},
		{"note create", "POST", "/p/ftest/virtual-office/notes/a.md?if-hash=", "hi", 200, "--json -C /ws/ftest notes write a.md --if-hash "},
		{"note rm", "POST", "/p/ftest/virtual-office/notes/a.md?rm=1&if-hash=abc", "", 200, "-C /ws/ftest notes rm a.md --if-hash abc"},
		{"note rm no hash", "POST", "/p/ftest/virtual-office/notes/a.md?rm=1", "", 400, ""},
		{"board", "GET", "/p/ftest/virtual-office/whiteboard/b", "", 200, "--json -C /ws/ftest -w b whiteboard show"},
		{"board unknown worktree", "GET", "/p/ftest/virtual-office/whiteboard/nope", "", 404, ""},
		{"board write", "POST", "/p/ftest/virtual-office/whiteboard/b?if-hash=abc", `{"scene":{}}`, 200, "--json -C /ws/ftest -w b whiteboard write --if-hash abc"},
		{"board rm", "POST", "/p/ftest/virtual-office/whiteboard/b?rm=1&if-hash=abc", "", 200, "--json -C /ws/ftest -w b whiteboard rm --if-hash abc"},
		{"board unknown worktree write", "POST", "/p/ftest/virtual-office/whiteboard/nope?if-hash=", "{}", 404, ""},
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

// TestCyberpunkState: a cyberpunk page's state is the overview's worktrees with
// their PRs, arrays never null.
func TestCyberpunkState(t *testing.T) {
	f := newFixture(t)
	f.s.pollOnce(context.Background())
	f.s.mu.Lock()
	st := f.s.cyberData("ftest")
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
	if st := f.s.cyberData("nope"); st.Error == "" {
		t.Error("unknown project has no error")
	}
}

// TestCyberpunkIssueStart: work on an issue is one project job: the
// worktree on the issue's branch, up, the agent on it.
func TestCyberpunkIssueStart(t *testing.T) {
	f := newFixture(t)
	r := httptest.NewRequest("POST", "/p/ftest/virtual-office/issues/118/start", nil)
	r.Host = testHost
	r.AddCookie(req("GET", "/", nil).Cookies()[0])
	r.Header.Set("Origin", "http://"+testHost)
	r.Header.Set("Accept", "application/json")
	if w := f.do(r); w.Code != 202 || !strings.Contains(w.Body.String(), `"label":"work on issue #118"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := f.run.wait(t, 1)[0]; !slices.Equal(got, []string{testExe, "-C", "/ws/ftest", "wt", "add", "--issue", "118", "--agent"}) {
		t.Errorf("job argv %q", got)
	}
}
