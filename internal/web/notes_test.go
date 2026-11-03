package web

import (
	"io"
	"strings"
	"testing"
)

// TestNotesPage: the notes page lists notes, opens one with its hash for
// the save, and saves through the office's write handler.
func TestNotesPage(t *testing.T) {
	f := newFixture(t)
	f.cli.set("--json -C /ws/ftest notes ls", `{"notes":[{"name":"a.md","size":5,"modified":"2026-10-31T19:00:32Z"}]}`)
	f.cli.set("--json -C /ws/ftest notes show a.md", `{"name":"a.md","text":"<b>hi</b>","hash":"sha256:ab"}`)
	for _, c := range []struct {
		path string
		code int
		want []string
	}{
		{"/p/ftest/notes", 200, []string{`href="/p/ftest/notes?n=a.md"`, "Open a note"}},
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
}
