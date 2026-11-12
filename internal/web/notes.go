package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/naming"
)

// The notes page (D155): /p/{prefix}/notes lists the agents' shared notes
// and edits one (?n=<name>) or a new one (?new=<name>). It reads through
// notes ls and notes show; saves and removals post to cyberNoteWrite at
// /p/{prefix}/notes/write/{name}, the office kiosk's handler. Agents only
// suggest notes (D199): the page lists every worktree's inbox, shows one
// (?w=<slug>&s=<name>) beside the note it would replace, and accepts or
// rejects it at /p/{prefix}/notes/inbox/{slug}/{name}.

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

	Inbox     []inboxRow
	Sugg      *inboxShow // nil: no suggestion open
	InboxPost string     // Sugg's accept/reject URL
}

type inboxRow struct {
	Worktree string `json:"worktree"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
	Replaces bool   `json:"replaces"`
	Current  bool   `json:"-"`
}

type inboxShow struct {
	Worktree string    `json:"worktree"`
	Name     string    `json:"name"`
	Text     string    `json:"text"`
	Hash     string    `json:"hash"`
	Note     *noteShow `json:"note"`
}

// validSlug reports whether s is a worktree slug (the CLI checks it again).
func validSlug(s string) bool {
	t, err := naming.Sanitize(s)
	return err == nil && t == s
}

type noteRow struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
	Current  bool   `json:"-"`
}

// Title and When are how the lists show a row.
func (n noteRow) Title() string  { return noteTitle(n.Name) }
func (n noteRow) When() string   { return noteWhen(n.Modified) }
func (n inboxRow) Title() string { return noteTitle(n.Name) }

var noteWord = regexp.MustCompile(`([a-z0-9])([A-Z])|([A-Z])([A-Z][a-z])|[-_]+`)

// noteTitle is a note's file name as words: agents name notes in snake
// case ("agent_queue_design.md" is "Agent queue design"; camel case
// splits too), which reads badly in a list. Only the first word gets a
// capital; a word all in capitals ("HTTP") keeps them. An open note shows
// the file name under it.
func noteTitle(name string) string {
	t := strings.TrimSuffix(name, ".md")
	// twice: a match takes the letter the next one would start at ("aBC").
	for range 2 {
		t = noteWord.ReplaceAllString(t, "$1$3 $2$4")
	}
	words := strings.Fields(t)
	if len(words) == 0 {
		return name
	}
	for i, w := range words {
		if i > 0 && (len(w) == 1 || w != strings.ToUpper(w)) {
			words[i] = strings.ToLower(w[:1]) + w[1:]
		}
	}
	t = strings.Join(words, " ")
	return strings.ToUpper(t[:1]) + t[1:]
}

var notWord = regexp.MustCompile(`[^A-Za-z0-9]+`)

// noteFileName is the file name of a new note typed as a title, in the
// agents' snake case: "Agent queue design" is "agent_queue_design.md",
// which noteTitle reads back. "" when the title has no letter or digit.
func noteFileName(title string) string {
	words := strings.Fields(notWord.ReplaceAllString(strings.ToLower(strings.TrimSuffix(title, ".md")), " "))
	name := strings.Join(words, "_")
	if name == "" {
		return ""
	}
	return strings.TrimRight(name[:min(len(name), 100)], "_") + ".md"
}

// noteWhen is an RFC 3339 time to the minute, or s as it is.
func noteWhen(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Local().Format("2006-01-02 15:04")
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
		// a title, unless it is a file name already
		if !noteName.MatchString(n) {
			n = noteFileName(n)
		}
		if !noteName.MatchString(n) {
			http.Error(w, "bad note title: it needs a letter or a digit", http.StatusBadRequest)
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
	if wt, n := q.Get("w"), q.Get("s"); wt != "" || n != "" {
		if !validSlug(wt) || !noteName.MatchString(n) {
			http.Error(w, "bad suggestion", http.StatusBadRequest)
			return
		}
		show, err := readJSON[inboxShow](r.Context(), s.c.CLI, "-C", p.Workspace, "notes", "inbox", "show", wt, n)
		if err != nil {
			v.Error = err.Error()
		} else {
			v.Sugg, v.InboxPost = &show, base+"/inbox/"+wt+"/"+n
		}
	}
	if v.Open != nil {
		v.SaveURL = base + "/write/" + v.Open.Name
	}
	in, err := readJSON[struct {
		Suggestions []inboxRow `json:"suggestions"`
	}](r.Context(), s.c.CLI, "-C", p.Workspace, "notes", "inbox", "ls")
	if err != nil && v.Error == "" {
		v.Error = err.Error()
	}
	for _, n := range in.Suggestions {
		n.Current = v.Sugg != nil && n.Worktree == v.Sugg.Worktree && n.Name == v.Sugg.Name
		v.Inbox = append(v.Inbox, n)
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

// notesInboxPost accepts (op=accept, hash=, as=) or rejects (op=reject)
// worktree {slug}'s suggestion {name}, then goes back to the notes page:
// to the accepted note, or the list.
func (s *Server) notesInboxPost(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cyberProject(w, r)
	if !ok {
		return
	}
	wt, n := r.PathValue("slug"), r.PathValue("name")
	if !validSlug(wt) || !noteName.MatchString(n) {
		http.Error(w, "bad suggestion", http.StatusBadRequest)
		return
	}
	base := "/p/" + p.Prefix + "/notes"
	var args []string
	back := base
	switch r.PostFormValue("op") {
	case "accept":
		as := r.PostFormValue("as")
		if as == "" {
			as = n
		}
		if !noteName.MatchString(as) {
			http.Error(w, "bad note name: letters, digits, . _ - and .md at the end", http.StatusBadRequest)
			return
		}
		args = []string{"accept", wt, n, "--hash", r.PostFormValue("hash"), "--as", as}
		back = base + "?n=" + url.QueryEscape(as)
	case "reject":
		args = []string{"reject", wt, n}
	default:
		http.Error(w, "op is accept or reject", http.StatusBadRequest)
		return
	}
	if _, err := s.c.CLI.Run(r.Context(), append([]string{"-C", p.Workspace, "notes", "inbox"}, args...)...); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
