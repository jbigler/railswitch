package web

import (
	"encoding/base64"
	"strings"
	"testing"
)

const boardCmd = "--json -C /ws/ftest -w b whiteboard show"

// D218: the Whiteboard tab's panel shows the picture for the theme (the
// dark one alone when there is no light one), with Edit, Clear and
// downloads; without a board, Draw.
func TestBoardPanel(t *testing.T) {
	f := newFixture(t)
	dark, light := base64.StdEncoding.EncodeToString([]byte("DARK")), base64.StdEncoding.EncodeToString([]byte("LIGHT"))
	f.cli.set(boardCmd, `{"slug":"b","hash":"","scene":null,"png":"","png_light":""}`)
	body := f.do(req("GET", "/p/ftest/b/whiteboard-panel", nil)).Body.String()
	contains(t, "empty", body, "No drawing yet", `<a class="ln main" href="/p/ftest/b/whiteboard">Draw</a>`)
	if strings.Contains(body, "data-board-clear") {
		t.Error("Clear offered without a board")
	}

	f.cli.set(boardCmd, `{"slug":"b","hash":"sha256:ab","scene":{"type":"excalidraw","elements":[]},"png":"`+dark+`","png_light":"`+light+`"}`)
	w := f.do(req("GET", "/p/ftest/b/whiteboard-panel", nil))
	body = w.Body.String()
	contains(t, "both", body,
		`<source media="(prefers-color-scheme: light)" srcset="/p/ftest/b/whiteboard-file?f=board-light.png">`,
		`<img class="board-pic" src="/p/ftest/b/whiteboard-file?f=board.png"`,
		`data-board-clear="/p/ftest/virtual-office/whiteboard/b?rm=1&amp;if-hash=sha256:ab"`,
		`class="ln dl-light" href="/p/ftest/b/whiteboard-file?f=board-light.png&amp;dl=1"`,
		`href="/p/ftest/b/whiteboard-file?f=board.excalidraw&amp;dl=1"`)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control %q", w.Header().Get("Cache-Control"))
	}
	noCSPViolation(t, "panel", body)

	f.cli.set(boardCmd, `{"slug":"b","hash":"sha256:ab","scene":{"type":"excalidraw","elements":[]},"png":"`+dark+`","png_light":""}`)
	body = f.do(req("GET", "/p/ftest/b/whiteboard-panel", nil)).Body.String()
	if strings.Contains(body, "<source") || strings.Contains(body, "dl-light") {
		t.Errorf("an old board, dark only, offers a light picture: %s", body)
	}
}

// D218: the file route serves a picture or the drawing (with ?dl=1 as a
// download named after the worktree), and nothing else.
func TestBoardFile(t *testing.T) {
	f := newFixture(t)
	dark := base64.StdEncoding.EncodeToString([]byte("DARK"))
	f.cli.set(boardCmd, `{"slug":"b","hash":"sha256:ab","scene":{"type":"excalidraw","elements":[]},"png":"`+dark+`","png_light":""}`)
	w := f.do(req("GET", "/p/ftest/b/whiteboard-file?f=board.png", nil))
	if w.Code != 200 || w.Body.String() != "DARK" || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Content-Disposition") != "" {
		t.Errorf("picture: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	w = f.do(req("GET", "/p/ftest/b/whiteboard-file?f=board.excalidraw&dl=1", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"excalidraw"`) || w.Header().Get("Content-Disposition") != `attachment; filename="b-whiteboard.excalidraw"` {
		t.Errorf("drawing: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	for _, q := range []string{"board-light.png", "../token", ""} {
		if w := f.do(req("GET", "/p/ftest/b/whiteboard-file?f="+q, nil)); w.Code != 404 {
			t.Errorf("%q: %d; want 404", q, w.Code)
		}
	}
	f.cli.set(boardCmd, `{"slug":"b","hash":"","scene":null,"png":"","png_light":""}`)
	if w := f.do(req("GET", "/p/ftest/b/whiteboard-file?f=board.excalidraw", nil)); w.Code != 404 {
		t.Errorf("no board's drawing: %d; want 404", w.Code)
	}
}

// D218: the editor page alone (with the office) allows data: images, and
// runs board.js with the office's board route; the Remove dialog names the
// drawing when there is one.
func TestBoardEditAndRemove(t *testing.T) {
	f := newFixture(t)
	w := f.do(req("GET", "/p/ftest/b/whiteboard", nil))
	if got := w.Header().Get("Content-Security-Policy"); got != wantCSP+"; img-src 'self' data:" {
		t.Errorf("editor CSP %q", got)
	}
	body := w.Body.String()
	contains(t, "editor", body, `<script src="/static/board.js" type="module">`, `data-board="/p/ftest/virtual-office/whiteboard/b" data-back="/p/ftest/b"`)
	noCSPViolation(t, "editor", body)
	if got := f.do(req("GET", "/p/ftest/b/whiteboard-panel", nil)).Header().Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("the panel loosens the CSP: %q", got)
	}

	f.cli.set(boardCmd, `{"slug":"b","hash":"","scene":null,"png":"","png_light":""}`)
	if body := f.do(req("GET", "/p/ftest/b/remove", nil)).Body.String(); strings.Contains(body, "whiteboard") {
		t.Error("the Remove dialog names a whiteboard there isn't")
	}
	f.cli.set(boardCmd, `{"slug":"b","hash":"sha256:ab","scene":{},"png":"","png_light":""}`)
	contains(t, "remove", f.do(req("GET", "/p/ftest/b/remove", nil)).Body.String(), "its whiteboard drawing")
	contains(t, "tabs", f.do(req("GET", "/p/ftest/b", nil)).Body.String(), `data-tab="whiteboard"`, `<section id="board" class="board-tab"`)
}
