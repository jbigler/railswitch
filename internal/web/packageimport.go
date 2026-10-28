package web

import (
	"net/http"

	"github.com/jbigler/railswitch/internal/jobs"
)

// A package folder someone handed over (D114), from the Library page:
// the review is `pack import <dir> [--as <name>] --json` in the project's
// workspace, shown with the review component; Import runs `pack import
// <dir> [--as <name>] --accept <hash> [--restart]` as a project job once
// the review read again has that hash and, with flags, the tick. Export
// runs `pack export <ref> --to <dir>`.

// packImportArgs is `pack import` of the posted or queried folder and
// name for project p's workspace; ok false for a bad path or name.
func packImportArgs(ws, rawDir, as string) ([]string, string, bool) {
	dir, ok := importDir(rawDir)
	if !ok || as != "" && !packNameRe.MatchString(as) {
		return nil, "", false
	}
	args := []string{"-C", ws, "pack", "import", dir}
	if as != "" {
		args = append(args, "--as", as)
	}
	return args, dir, true
}

func (s *Server) packImportPage(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	pv, jv := s.chrome(p.Prefix)
	s.mu.Unlock()
	back := libraryURL(p.Prefix)
	v := importView{
		Title: "Import package · " + p.Prefix, Prefix: p.Prefix,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
		ImportURL: back + "/import", Back: back, BackText: "Library", Heading: "Import package",
		Intro: "A package from a folder someone gave you, as one of this workspace's own (local:). Read every file before you import it: its Containerfiles are built and its scripts run in your containers.",
	}
	q := r.URL.Query()
	args, dir, ok := packImportArgs(p.Workspace, q.Get("dir"), q.Get("as"))
	if !ok {
		v.Error = "give the package folder's full path, and a name of lower-case letters, digits and -"
		s.html(w, http.StatusOK, "templateimport", v)
		return
	}
	v.Dir, v.As = dir, q.Get("as")
	v.fill(readJSON[importDoc](r.Context(), s.c.CLI, args...))
	s.html(w, http.StatusOK, "templateimport", v)
}

func (s *Server) packImport(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	args, _, ok := packImportArgs(p.Workspace, r.PostFormValue("dir"), r.PostFormValue("as"))
	if !ok {
		http.Error(w, "not a package folder's full path, or a bad name", http.StatusBadRequest)
		return
	}
	hash := r.PostFormValue("hash")
	if !importHashRe.MatchString(hash) {
		http.Error(w, "no review hash: reload the page", http.StatusBadRequest)
		return
	}
	doc, err := readJSON[importDoc](r.Context(), s.c.CLI, args...)
	switch {
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case doc.Status != "new" && doc.Status != "update":
		http.Error(w, "nothing to import: "+doc.Ref+" is here already", http.StatusBadRequest)
		return
	case hash != doc.Hash:
		http.Error(w, "the folder changed since you reviewed it: reload the page and review again", http.StatusBadRequest)
		return
	case doc.Flags > 0 && r.PostFormValue("ack") != "1":
		http.Error(w, "tick \""+ackText(doc.Flags)+"\" first", http.StatusBadRequest)
		return
	}
	args = append(args, "--accept", hash)
	if r.PostFormValue("restart") != "" {
		args = append(args, "--restart")
	}
	s.start(w, r, jobs.Spec{
		Project: p.Prefix, Label: "pack import " + doc.Ref, Dir: p.Workspace,
		Argv: append([]string{s.c.Exe}, args...),
	}, nil, libraryURL(p.Prefix))
}

func (s *Server) packExport(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	ref := r.PostFormValue("ref")
	to, okDir := importDir(r.PostFormValue("to"))
	if !libRefRe.MatchString(ref) || !okDir {
		http.Error(w, "a package ref and the new folder's full path", http.StatusBadRequest)
		return
	}
	s.start(w, r, jobs.Spec{
		Project: p.Prefix, Label: "pack export " + ref, Dir: p.Workspace,
		Argv: []string{s.c.Exe, "-C", p.Workspace, "pack", "export", ref, "--to", to},
	}, nil, libraryURL(p.Prefix))
}
