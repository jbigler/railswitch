package web

import (
	"errors"
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
	f.cli.set("--json -C /ws/ftest notes search -- db prep", `{"notes":[{"name":"b.md","modified":"2026-10-31T19:00:32Z","line":"<run> db prepare"}]}`)
	f.cli.set("--json -C /ws/ftest notes inbox ls", `{"suggestions":[{"worktree":"feat","name":"a.md","size":3,"modified":"2026-10-31T19:00:32Z","replaces":true}]}`)
	f.cli.set("--json -C /ws/ftest notes inbox show feat a.md", `{"worktree":"feat","name":"a.md","text":"<i>new</i>","hash":"sha256:ef","note":{"name":"a.md","text":"old","hash":"sha256:ab"}}`)
	for _, c := range []struct {
		path string
		code int
		want []string
	}{
		{"/p/ftest/notes", 200, []string{`href="/p/ftest/notes?n=a.md"`, "Open a note", `href="/p/ftest/notes?w=feat&amp;s=a.md"`, "replaces a note"}},
		{"/p/ftest/notes?w=feat&s=a.md", 200, []string{`action="/p/ftest/notes/inbox/feat/a.md"`, `value="sha256:ef"`, "&lt;i&gt;new&lt;/i&gt;", "It replaces the current note"}},
		{"/p/ftest/notes?q=+db+prep+&n=a.md", 200, []string{`href="/p/ftest/notes?n=b.md&amp;q=db%20prep"`, "&lt;run&gt; db prepare", `value="db prep"`, `name="n" value="a.md"`, `href="/p/ftest/notes?q=db%20prep">Close`}},
		{"/p/ftest/notes?w=..&s=a.md", 400, nil},
		{"/p/ftest/notes?n=a.md", 200, []string{`action="/p/ftest/notes/write/a.md"`, `data-hash="sha256:ab"`, "&lt;b&gt;hi&lt;/b&gt;", "data-note-rm"}},
		{"/p/ftest/notes?new=b.md", 200, []string{`action="/p/ftest/notes/write/b.md"`, `data-hash=""`, "new note"}},
		{"/p/ftest/notes?n=..%2Fx.md", 400, nil},
		{"/p/ftest/notes?new=Agent+queue+design", 200, []string{`action="/p/ftest/notes/write/agent_queue_design.md"`, "Agent queue design"}},
		{"/p/ftest/notes?new=---", 400, nil},
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
	// A changed title writes the text under its file name, then removes the
	// old note; the same title, or the note's own name, is a plain save.
	f.cli.set("--json -C /ws/ftest notes write a_better_name.md --if-hash ", `{"hash":"sha256:ef"}`)
	f.cli.set("-C /ws/ftest notes rm a.md --if-hash sha256:ab", `{}`)
	r = req("POST", "/p/ftest/notes/write/a.md?if-hash=sha256:ab&as=A+better+name", nil)
	r.Body = io.NopCloser(strings.NewReader("moved"))
	if w := f.do(r); w.Code != 200 || w.Header().Get("X-Note-Name") != "a_better_name.md" || f.cli.count("-C /ws/ftest notes rm a.md --if-hash sha256:ab") != 1 {
		t.Fatalf("rename: %d %s, name %q", w.Code, w.Body, w.Header().Get("X-Note-Name"))
	}
	// the write, then the rm
	if got := string(f.cli.stdins[len(f.cli.stdins)-2]); got != "moved" {
		t.Errorf("rename stdin %q", got)
	}
	r = req("POST", "/p/ftest/notes/write/a.md?if-hash=sha256:ab&as=A", nil)
	r.Body = io.NopCloser(strings.NewReader("new text"))
	if w := f.do(r); w.Code != 200 || w.Header().Get("X-Note-Name") != "a.md" || f.cli.count("-C /ws/ftest notes rm a.md --if-hash sha256:ab") != 1 {
		t.Errorf("an unchanged title: %d %s", w.Code, w.Body)
	}
	// The name is taken: nothing is removed.
	f.cli.fail("--json -C /ws/ftest notes write taken.md --if-hash ", errors.New("taken.md exists"))
	r = req("POST", "/p/ftest/notes/write/a.md?if-hash=sha256:ab&as=Taken", nil)
	r.Body = io.NopCloser(strings.NewReader("x"))
	if w := f.do(r); w.Code != 422 || f.cli.count("-C /ws/ftest notes rm a.md --if-hash sha256:ab") != 1 {
		t.Errorf("rename onto a note: %d %s", w.Code, w.Body)
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
		"playwright_clicks_on_a_flash_over_an_open_modal_need_force_true.md": "Playwright clicks on a flash over an open modal need force true",
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
	for title, want := range map[string]string{
		"Agent queue design":             "agent_queue_design.md",
		"HTTP server notes":              "http_server_notes.md",
		"  what's next? ":                "what_s_next.md",
		"Plan2 rollout":                  "plan2_rollout.md",
		"!!":                             "",
		strings.Repeat("a", 150):         strings.Repeat("a", 100) + ".md",
		strings.Repeat("a", 99) + " b c": strings.Repeat("a", 99) + ".md",
	} {
		got := noteFileName(title)
		if got != want {
			t.Errorf("noteFileName(%q) = %q, want %q", title, got, want)
		}
		if got != "" && !noteName.MatchString(got) {
			t.Errorf("noteFileName(%q) = %q is no note name", title, got)
		}
	}
	if got := noteTitle(noteFileName("Agent queue design")); got != "Agent queue design" {
		t.Errorf("a title does not come back: %q", got)
	}
	if got := shortTime("soon"); got != "soon" {
		t.Errorf("shortTime of no time = %q", got)
	}
}
