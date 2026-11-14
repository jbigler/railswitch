package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
)

// The worktree's Whiteboard tab and editor page (D218): the board the
// Virtual Office's glass wall shows (D151), read through `whiteboard show`.
// The editor page saves and clears through the office's routes.

// boardDoc is `whiteboard show --json`.
type boardDoc struct {
	Hash  string          `json:"hash"` // "" without a board
	Scene json.RawMessage `json:"scene"`
	PNG   string          `json:"png"`       // the dark picture, base64; "" for none
	Light string          `json:"png_light"` // the picture on white; "" for none
}

func readBoard(ctx context.Context, c CLI, ws, slug string) (boardDoc, error) {
	return readJSON[boardDoc](ctx, c, "-C", ws, "-w", slug, "whiteboard", "show")
}

// boardPanel is the tab's content: the picture for the theme shown, and
// what can be done with the board.
type boardPanel struct {
	Slug, Hash        string
	Dark, Light       bool   // the board has that picture
	EditURL, ClearURL string // ClearURL: the office's write route, rm=1
	FileURL           string // + a boardFiles name
}

// boardPanelOf is worktree slug's panel of project prefix.
func boardPanelOf(prefix, slug string, d boardDoc) boardPanel {
	base := "/p/" + prefix + "/" + slug
	return boardPanel{Slug: slug, Hash: d.Hash, Dark: d.PNG != "", Light: d.Light != "",
		EditURL: base + "/whiteboard", FileURL: base + "/whiteboard-file?f=",
		ClearURL: "/p/" + prefix + "/virtual-office/whiteboard/" + slug + "?rm=1&if-hash=" + d.Hash}
}

// boardPanelPage is GET /p/{prefix}/{slug}/whiteboard-panel: the tab's
// content, which the page loads each time the tab shows.
func (s *Server) boardPanelPage(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	d, err := readBoard(r.Context(), s.c.CLI, p.Workspace, wt.Slug)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.html(w, http.StatusOK, "board-panel", boardPanelOf(p.Prefix, wt.Slug, d))
}

// boardFiles are the files GET …/whiteboard-file?f=<name> serves, with what
// a download of each is called (after the worktree's slug).
var boardFiles = map[string]struct{ typ, download string }{
	"board.png":        {"image/png", "-whiteboard.png"},
	"board-light.png":  {"image/png", "-whiteboard-light.png"},
	"board.excalidraw": {"application/json", "-whiteboard.excalidraw"},
}

// boardFile is GET /p/{prefix}/{slug}/whiteboard-file?f=<name>: a picture
// the tab shows, or (?dl=1) a file it downloads.
func (s *Server) boardFile(w http.ResponseWriter, r *http.Request) {
	f, known := boardFiles[r.URL.Query().Get("f")]
	if !known {
		http.Error(w, "unknown whiteboard file", http.StatusNotFound)
		return
	}
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	d, err := readBoard(r.Context(), s.c.CLI, p.Workspace, wt.Slug)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	var data []byte
	switch r.URL.Query().Get("f") {
	case "board.excalidraw":
		if d.Hash != "" {
			data = d.Scene
		}
	case "board.png":
		data, err = base64.StdEncoding.DecodeString(d.PNG)
	default:
		data, err = base64.StdEncoding.DecodeString(d.Light)
	}
	if err != nil || len(data) == 0 {
		http.Error(w, "the whiteboard has no such file", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", f.typ)
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("dl") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+wt.Slug+f.download+`"`) // a slug is [a-z0-9-]
	}
	_, _ = w.Write(data)
}

// boardEditView is the editor page.
type boardEditView struct {
	Title, Prefix, Slug string
	BoardURL, BackURL   string // BoardURL: the office's read and write route
}

// boardEdit is GET /p/{prefix}/{slug}/whiteboard: Excalidraw on a page of
// its own, outside the worktree page the event stream redraws. It alone
// (with the office) allows data: images, which the editor draws its icons
// and pasted pictures from (D151).
func (s *Server) boardEdit(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Security-Policy", officeCSP)
	s.html(w, http.StatusOK, "board-edit", boardEditView{
		Title: wt.Slug + " · Whiteboard · RailSwitch", Prefix: p.Prefix, Slug: wt.Slug,
		BoardURL: "/p/" + p.Prefix + "/virtual-office/whiteboard/" + wt.Slug, BackURL: "/p/" + p.Prefix + "/" + wt.Slug,
	})
}
