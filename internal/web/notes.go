package web

import "net/http"

// The notes page (D154): /p/{prefix}/notes lists the agents' shared notes
// and edits one (?n=<name>) or a new one (?new=<name>). It reads through
// notes ls and notes show; saves and removals post to cyberNoteWrite at
// /p/{prefix}/notes/write/{name}, the office kiosk's handler.

type notesView struct {
	Title, Prefix string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView

	Error   string
	Notes   []noteRow
	Open    *noteShow // nil: no note open
	New     bool      // Open is a note not yet written
	URL     string    // the page
	SaveURL string    // Open's write URL
}

type noteRow struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
	Current  bool   `json:"-"`
}

type noteShow struct {
	Name string `json:"name"`
	Text string `json:"text"`
	Hash string `json:"hash"`
}

func (s *Server) notesPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cyberProject(w, r)
	if !ok {
		return
	}
	base := "/p/" + p.Prefix + "/notes"
	s.mu.Lock()
	pv, _ := s.chrome(p.Prefix)
	s.mu.Unlock()
	v := notesView{Title: "Notes · " + p.Prefix, Prefix: p.Prefix,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, URL: base}
	q := r.URL.Query()
	if n := q.Get("new"); n != "" {
		if !noteName.MatchString(n) {
			http.Error(w, "bad note name: letters, digits, . _ - and .md at the end", http.StatusBadRequest)
			return
		}
		v.Open, v.New = &noteShow{Name: n}, true
	} else if n := q.Get("n"); n != "" {
		if !noteName.MatchString(n) {
			http.Error(w, "bad note name", http.StatusBadRequest)
			return
		}
		show, err := readJSON[noteShow](r.Context(), s.c.CLI, "-C", p.Workspace, "notes", "show", n)
		if err != nil {
			v.Error = err.Error()
		} else {
			v.Open = &show
		}
	}
	if v.Open != nil {
		v.SaveURL = base + "/write/" + v.Open.Name
	}
	ls, err := readJSON[struct {
		Notes []noteRow `json:"notes"`
	}](r.Context(), s.c.CLI, "-C", p.Workspace, "notes", "ls")
	if err != nil && v.Error == "" {
		v.Error = err.Error()
	}
	for _, n := range ls.Notes {
		n.Current = v.Open != nil && n.Name == v.Open.Name
		v.Notes = append(v.Notes, n)
	}
	s.html(w, http.StatusOK, "notes", v)
}
