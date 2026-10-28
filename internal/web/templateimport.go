package web

import (
	"maps"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/jbigler/railswitch/internal/jobs"
)

// Importing a template folder someone handed over (D113), from the
// machine page: the folder's path is read with `template import <dir>
// --json`, the review shown with the review component, and Import runs
// `template import <dir> --accept <hash>` as a machine job once the
// review read again has that hash and, with flags, the tick.

const maxImportPath = 4096

// importHashRe is a template's content hash, as --accept takes it.
var importHashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// importDoc is `template import --json`: lock --review's document plus
// the template's status and warnings.
type importDoc struct {
	reviewDoc
	Ref          string   `json:"ref"`
	Status       string   `json:"status"` // new, update, same, older
	Warnings     []string `json:"warnings"`
	ComparedWith string   `json:"compared_with"` // pack import: the built-in a new package is shown against
	// template import: by bundle directory, the built-in it is shown against
	BundlesComparedWith map[string]string `json:"bundles_compared_with"`
	Replaces            *struct {
		Ref string `json:"ref"`
	} `json:"replaces"`
}

type importView struct {
	Title, Prefix string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView
	Jobs          jobsView

	Back, BackText, Heading, Intro    string
	Dir, Ref, Status, Replaces, Error string
	ComparedWith                      string
	Bundles                           []string // "packs/rails/ … rails@1" notes
	Hash, AckText, As, Restart        string
	Warnings                          []string
	Packages                          []reviewPkgView
	ImportURL                         string
}

// fill sets v from an import review read with err.
func (v *importView) fill(doc importDoc, err error) {
	if err != nil {
		v.Error = err.Error()
		return
	}
	v.Ref, v.Status, v.Warnings, v.ComparedWith = doc.Ref, doc.Status, doc.Warnings, doc.ComparedWith
	for _, d := range slices.Sorted(maps.Keys(doc.BundlesComparedWith)) {
		v.Bundles = append(v.Bundles, d+"/ is shown as its changes from the built-in "+doc.BundlesComparedWith[d])
	}
	if doc.Replaces != nil {
		v.Replaces = doc.Replaces.Ref
	}
	v.Hash, v.AckText, v.Packages = doc.Hash, ackText(doc.Flags), reviewPackages(doc.reviewDoc)
	v.Restart = strings.Join(doc.Restart, ", ")
}

// importDir is the posted folder: an absolute, clean path with no
// control characters.
func importDir(raw string) (string, bool) {
	if raw == "" || len(raw) > maxImportPath || !filepath.IsAbs(raw) || filepath.Clean(raw) != raw ||
		strings.ContainsFunc(raw, unicode.IsControl) {
		return "", false
	}
	return raw, true
}

// templateImportPage reviews the folder ?dir=.
func (s *Server) templateImportPage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	pv := s.pageData("", "", sortKey{By: "name"})
	jv := newJobs(s.c.Jobs.Jobs(machineKey, ""), nil, "")
	s.mu.Unlock()
	v := importView{
		Title: "Import template · RailSwitch", Prefix: machineKey,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
		ImportURL: "/machine/templates/import",
		Back:      "/machine", BackText: "Machine", Heading: "Import template",
		Intro: "A template from a folder someone gave you. Read every file before you import it: a new project started from it builds these Containerfiles and gets these firewall, route and secret lines.",
	}
	dir, ok := importDir(r.URL.Query().Get("dir"))
	if !ok {
		v.Error = "give the template folder's full path, e.g. /home/you/Downloads/rails-stack"
		s.html(w, http.StatusOK, "templateimport", v)
		return
	}
	v.Dir = dir
	v.fill(readJSON[importDoc](r.Context(), s.c.CLI, "template", "import", dir))
	s.html(w, http.StatusOK, "templateimport", v)
}

// templateImport runs `template import <dir> --accept <hash>`.
func (s *Server) templateImport(w http.ResponseWriter, r *http.Request) {
	dir, ok := importDir(r.PostFormValue("dir"))
	if !ok {
		http.Error(w, "not a template folder's full path", http.StatusBadRequest)
		return
	}
	hash := r.PostFormValue("hash")
	if !importHashRe.MatchString(hash) {
		http.Error(w, "no review hash: reload the page", http.StatusBadRequest)
		return
	}
	doc, err := readJSON[importDoc](r.Context(), s.c.CLI, "template", "import", dir)
	switch {
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case doc.Status != "new" && doc.Status != "update":
		http.Error(w, "nothing to import: this machine already has "+doc.Ref, http.StatusBadRequest)
		return
	case hash != doc.Hash:
		http.Error(w, "the folder changed since you reviewed it: reload the page and review again", http.StatusBadRequest)
		return
	case doc.Flags > 0 && r.PostFormValue("ack") != "1":
		http.Error(w, "tick \""+ackText(doc.Flags)+"\" first", http.StatusBadRequest)
		return
	}
	s.start(w, r, jobs.Spec{
		Project: machineKey, Label: "template import " + doc.Ref, Dir: s.c.Home,
		Argv: []string{s.c.Exe, "template", "import", dir, "--accept", hash},
	}, nil, "/machine")
}
