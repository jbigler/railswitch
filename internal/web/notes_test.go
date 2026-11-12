package web

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNotesPage: the notes page lists notes, opens one with its hash for
// the save, and saves through the office's write handler.
func TestNotesPage(t *testing.T) {
	f := newFixture(t)
	f.cli.set("--json -C /ws/ftest notes ls", `{"notes":[{"name":"a.md","size":5,"modified":"2026-10-31T19:00:32Z"}]}`)
	f.cli.set("--json -C /ws/ftest notes show a.md", `{"name":"a.md","text":"<b>hi</b>","hash":"sha256:ab"}`)
	f.cli.set("--json -C /ws/ftest notes inbox ls", `{"suggestions":[{"worktree":"feat","name":"a.md","size":3,"modified":"2026-10-31T19:00:32Z","replaces":true}]}`)
	f.cli.set("--json -C /ws/ftest notes inbox show feat a.md", `{"worktree":"feat","name":"a.md","text":"<i>new</i>","hash":"sha256:ef","note":{"name":"a.md","text":"old","hash":"sha256:ab"}}`)
	for _, c := range []struct {
		path string
		code int
		want []string
	}{
		{"/p/ftest/notes", 200, []string{`href="/p/ftest/notes?n=a.md"`, "Open a note", `href="/p/ftest/notes?w=feat&amp;s=a.md"`, "replaces a note"}},
		{"/p/ftest/notes?w=feat&s=a.md", 200, []string{`action="/p/ftest/notes/inbox/feat/a.md"`, `value="sha256:ef"`, "&lt;i&gt;new&lt;/i&gt;", "It replaces the current note"}},
		{"/p/ftest/notes?w=..&s=a.md", 400, nil},
		{"/p/ftest/notes?n=a.md", 200, []string{`action="/p/ftest/notes/write/a.md"`, `data-hash="sha256:ab"`, "&lt;b&gt;hi&lt;/b&gt;", "data-note-rm"}},
		{"/p/ftest/notes?new=b.md", 200, []string{`action="/p/ftest/notes/write/b.md"`, `data-hash=""`, "new note"}},
		{"/p/ftest/notes?n=..%2Fx.md", 400, nil},
		{"/p/ftest/notes?new=x.txt", 400, nil},
		{"/p/nope/notes", 404, nil},
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
	}
	f.cli.set("--json -C /ws/ftest notes write a.md --if-hash sha256:ab", `{"hash":"sha256:cd"}`)
	r := req("POST", "/p/ftest/notes/write/a.md?if-hash=sha256:ab", nil)
	r.Body = io.NopCloser(strings.NewReader("new text"))
	if w := f.do(r); w.Code != 200 || !strings.Contains(w.Body.String(), "sha256:cd") {
		t.Fatalf("write: %d %s", w.Code, w.Body)
	}
	if got := string(f.cli.stdins[len(f.cli.stdins)-1]); got != "new text" {
		t.Errorf("stdin %q", got)
	}
	r = req("POST", "/p/ftest/notes/write/a.md?if-hash=sha256:ab", nil)
	r.Header.Del("Origin")
	if w := f.do(r); w.Code != 403 {
		t.Errorf("no Origin: %d", w.Code)
	}

	// Accept runs the CLI at the reviewed hash and opens the note; reject
	// goes back to the list.
	post := func(path, body string) *httptest.ResponseRecorder {
		r := req("POST", path, nil)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Body = io.NopCloser(strings.NewReader(body))
		return f.do(r)
	}
	f.cli.set("-C /ws/ftest notes inbox accept feat a.md --hash sha256:ef --as b.md", "")
	if w := post("/p/ftest/notes/inbox/feat/a.md", "op=accept&hash=sha256:ef&as=b.md"); w.Code != 303 || w.Header().Get("Location") != "/p/ftest/notes?n=b.md" {
		t.Errorf("accept: %d %s %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	f.cli.set("-C /ws/ftest notes inbox reject feat a.md", "")
	if w := post("/p/ftest/notes/inbox/feat/a.md", "op=reject"); w.Code != 303 || w.Header().Get("Location") != "/p/ftest/notes" {
		t.Errorf("reject: %d %s", w.Code, w.Body)
	}
	for _, c := range []struct{ path, body string }{
		{"/p/ftest/notes/inbox/feat/a.md", "op=accept&hash=x&as=../x.md"},
		{"/p/ftest/notes/inbox/feat/a.md", "op=delete"},
		{"/p/ftest/notes/inbox/Feat/a.md", "op=reject"},
	} {
		if w := post(c.path, c.body); w.Code != 400 {
			t.Errorf("%s %s: %d", c.path, c.body, w.Code)
		}
	}
}

func TestNoteTitle(t *testing.T) {
	for name, want := range map[string]string{
		"agentQueueDesign.md": "Agent queue design",
		"HTTPServerNotes.md":  "HTTP server notes",
		"aBC.md":              "A BC",
		"db-restore_steps.md": "Db restore steps",
		"plan2Rollout.md":     "Plan2 rollout",
		"a.md":                "A",
		".md":                 ".md",
	} {
		if got := noteTitle(name); got != want {
			t.Errorf("noteTitle(%q) = %q, want %q", name, got, want)
		}
	}
	if got := noteWhen("soon"); got != "soon" {
		t.Errorf("noteWhen of no time = %q", got)
	}
}
