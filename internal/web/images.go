package web

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/jobs"
)

// The Images page and the file editor (spec "Pages"): the build chain
// from `containerfile list`, and one editor for a Containerfile or a
// local package file. Check runs the write's --dry-run; Save, Reset and
// New run the write as a project-level job. The text travels only on
// stdin, never argv.

// cfNameRe is a Containerfile.<name> the CLI accepts (internal/cli).
var cfNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// cfNewRe is a name `containerfile new` accepts.
var cfNewRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

const runAck = "I've read this file. Every RUN line runs on this machine during the build, outside the firewall."

// editorView is the editor page, for a Containerfile and a package file.
type editorView struct {
	Title, Prefix string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView
	Jobs          jobsView

	URL, Back, BackText string // URL: this page, for Reload
	File, Kind          string
	Error               string // the file could not be read: no editor
	Text, Hash          string
	ReadOnly            bool   // a built-in package's file
	New                 bool   // Add file: the file does not exist yet
	DefaultRef          string // "": no default panel
	Diff, Notice        string

	CheckURL, SaveURL, ResetURL, DeleteURL string
	AckText                                string // "": no acknowledgement needed
	CheckRequired                          bool
	Check                                  checkView
}

// checkView is the Check panel: what the dry run of the text said.
type checkView struct {
	Done     bool // a check ran
	OK       bool
	Error    string // the dry run refused as a whole
	Conflict bool   // ... because the file changed on disk
	Reload   string
	Errors   []string
	Steps    []tagChange
	Restart  string // "main, b"; "" for none
	Unused   bool   // a package the project doesn't use: no lock, no render
}

func newCheck(doc checkDoc, err error, reload string, pkg bool) checkView {
	v := checkView{Done: true, Reload: reload}
	if err != nil {
		v.Error = err.Error()
		v.Conflict = conflictRe.MatchString(v.Error)
		return v
	}
	v.Errors, v.Steps = doc.Errors, doc.Steps
	v.OK = len(doc.Errors) == 0
	v.Unused = pkg && !doc.Used
	if v.OK {
		v.Restart = strings.Join(doc.Restart, ", ")
	}
	return v
}

type imagesView struct {
	Title, Prefix string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView
	Jobs          jobsView

	Worktrees []wtPick
	Error     string
	Warnings  []string
	Steps     []stepView
	NewURL    string
}

type wtPick struct {
	Slug, URL string
	Current   bool
}

type stepView struct {
	Name, Kind, File, Tag, From, EditURL string
}

// stepKind is a build step's kind, from its name (docs/json.md).
func stepKind(name string) string {
	switch {
	case name == "base":
		return "base"
	case name == "agent-base":
		return "agent-base"
	case strings.HasPrefix(name, "addon "):
		return "addon"
	case strings.HasPrefix(name, "agent "):
		return "agent"
	}
	return "image"
}

func imagesURL(prefix string) string { return "/p/" + prefix + "/images" }

func (s *Server) imagesPage(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	ov, err := s.overviewOf(r.Context(), p.Prefix, p.Workspace)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	slug := r.URL.Query().Get("wt")
	if slug == "" {
		for _, wt := range ov.Worktrees {
			if wt.Base {
				slug = wt.Slug
			}
		}
	}
	if !slices.ContainsFunc(ov.Worktrees, func(wt worktree) bool { return wt.Slug == slug }) {
		http.Error(w, "unknown worktree", http.StatusNotFound)
		return
	}
	base := imagesURL(p.Prefix)
	s.mu.Lock()
	pv, jv := s.chrome(p.Prefix)
	s.mu.Unlock()
	v := imagesView{
		Title: "Images · " + p.Prefix, Prefix: p.Prefix,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
		NewURL: base + "/new",
	}
	for _, wt := range ov.Worktrees {
		v.Worktrees = append(v.Worktrees, wtPick{Slug: wt.Slug, URL: base + "?wt=" + wt.Slug, Current: wt.Slug == slug})
	}
	chain, err := readChain(r.Context(), s.c.CLI, p.Workspace, slug)
	if err != nil {
		v.Error = err.Error()
	}
	v.Warnings = chain.Warnings
	for _, st := range chain.Steps {
		name, _ := strings.CutPrefix(st.File, "containerfiles/Containerfile.")
		sv := stepView{Name: st.Name, Kind: stepKind(st.Name), File: path.Base(st.File), Tag: st.Tag, From: strings.Join(st.From, ", ")}
		if cfNameRe.MatchString(name) {
			sv.EditURL = base + "/edit/" + name
		}
		v.Steps = append(v.Steps, sv)
	}
	s.html(w, http.StatusOK, "images", v)
}

// cfName is the request's {name}, answering 404 itself when it isn't a
// Containerfile name.
func cfName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")
	if !cfNameRe.MatchString(name) {
		http.Error(w, "not a Containerfile name", http.StatusNotFound)
		return "", false
	}
	return name, true
}

func (s *Server) imagesEditor(w http.ResponseWriter, r *http.Request) {
	name, ok := cfName(w, r)
	if !ok {
		return
	}
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	base := imagesURL(p.Prefix)
	s.mu.Lock()
	pv, jv := s.chrome(p.Prefix)
	s.mu.Unlock()
	v := editorView{
		Title: "Containerfile." + name + " · " + p.Prefix, Prefix: p.Prefix,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
		URL: base + "/edit/" + name, Back: base, BackText: "Images", File: "containerfiles/Containerfile." + name,
		CheckURL: base + "/check/" + name, SaveURL: base + "/save/" + name,
		AckText: runAck, CheckRequired: true,
	}
	doc, err := readContainerfile(r.Context(), s.c.CLI, p.Workspace, name)
	if err != nil {
		v.Error = err.Error()
		s.html(w, http.StatusOK, "editor", v)
		return
	}
	v.File, v.Kind, v.Text, v.Hash, v.Diff = doc.File, doc.Kind, doc.Text, doc.Hash, doc.Diff
	if doc.Default != nil {
		v.DefaultRef, v.ResetURL = doc.Default.Ref, base+"/reset/"+name
		if doc.DefaultChanged {
			v.Notice = "The default (" + doc.Default.Ref + ") changed since it was ejected. Compare it below, or reset to it."
		}
	}
	s.html(w, http.StatusOK, "editor", v)
}

// editorForm reads a posted editor: the hash it was opened with and the
// text, line breaks LF again (a browser posts a textarea with CRLF).
// allowNew lets the hash be empty: the file must not exist yet.
func editorForm(w http.ResponseWriter, r *http.Request, allowNew bool) (hash string, text []byte, err error) {
	r.Body = http.MaxBytesReader(w, r.Body, 4*maxEdits)
	hash = r.PostFormValue("hash")
	if !hashRe.MatchString(hash) && (hash != "" || !allowNew) {
		return "", nil, errors.New("no file hash: reload the page")
	}
	t := strings.ReplaceAll(r.PostFormValue("text"), "\r\n", "\n")
	if len(t) > maxEdits {
		return "", nil, errors.New("the file is larger than 1 MiB")
	}
	return hash, []byte(t), nil
}

// imagesCheck answers the Check panel for the posted text, judged by
// `containerfile write --dry-run`.
func (s *Server) imagesCheck(w http.ResponseWriter, r *http.Request) {
	name, ok := cfName(w, r)
	if !ok {
		return
	}
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	hash, text, err := editorForm(w, r, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	doc, err := readCheck(r.Context(), s.c.CLI, text, "-C", p.Workspace, "containerfile", "write", name, "--if-hash", hash, "--dry-run")
	s.html(w, http.StatusOK, "check", newCheck(doc, err, imagesURL(p.Prefix)+"/edit/"+name, false))
}

// acked: the form carries the acknowledgement, answering 400 when not.
func acked(w http.ResponseWriter, r *http.Request) bool {
	if r.PostFormValue("ack") != "1" {
		http.Error(w, "tick \"I've read this file\" first", http.StatusBadRequest)
		return false
	}
	return true
}

// imagesSave runs `containerfile write <name> --if-hash <h> [--restart]`
// with the text on stdin.
func (s *Server) imagesSave(w http.ResponseWriter, r *http.Request) {
	name, ok := cfName(w, r)
	if !ok {
		return
	}
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	hash, text, err := editorForm(w, r, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if !acked(w, r) {
		return
	}
	argv := []string{s.c.Exe, "-C", p.Workspace, "containerfile", "write", name, "--if-hash", hash}
	label := "containerfile write " + name
	if r.PostFormValue("restart") != "" {
		argv = append(argv, "--restart")
		label += " --restart"
	}
	s.start(w, r, jobs.Spec{Project: p.Prefix, Label: label, Dir: p.Workspace, Argv: argv}, text, imagesURL(p.Prefix)+"/edit/"+name)
}

// imagesReset runs `containerfile eject <name> --force`: the default again.
func (s *Server) imagesReset(w http.ResponseWriter, r *http.Request) {
	name, ok := cfName(w, r)
	if !ok {
		return
	}
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	s.start(w, r, jobs.Spec{
		Project: p.Prefix, Label: "containerfile eject " + name, Dir: p.Workspace,
		Argv: []string{s.c.Exe, "-C", p.Workspace, "containerfile", "eject", name, "--force"},
	}, nil, imagesURL(p.Prefix)+"/edit/"+name)
}

// imagesNew runs `containerfile new <name>`: a user addon.
func (s *Server) imagesNew(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	name := r.PostFormValue("name")
	if !cfNewRe.MatchString(name) {
		http.Error(w, fmt.Sprintf("a name is lower-case letters, digits and -, starting with a letter (%s)", cfNewRe), http.StatusUnprocessableEntity)
		return
	}
	s.start(w, r, jobs.Spec{
		Project: p.Prefix, Label: "containerfile new " + name, Dir: p.Workspace,
		Argv: []string{s.c.Exe, "-C", p.Workspace, "containerfile", "new", name},
	}, nil, imagesURL(p.Prefix))
}
