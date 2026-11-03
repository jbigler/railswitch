package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/jbigler/railswitch/internal/jobs"
	"github.com/jbigler/railswitch/internal/office"
	"github.com/jbigler/railswitch/internal/safefile"
)

// The Virtual Office's sets (D153): which one a page draws, from
// office set ls --json, and the installed sets' files.

type setEntry struct {
	Name        string          `json:"name"`
	Source      string          `json:"source"`
	Version     int             `json:"version"`
	Status      string          `json:"status"`
	Reason      string          `json:"reason"`
	Description string          `json:"description"`
	Look        json.RawMessage `json:"look"`
	Have        []string        `json:"have"`
}

type setLs struct {
	Active string     `json:"active"`
	Sets   []setEntry `json:"sets"`
}

// setDoc is /office/set.json: the set a page draws and where its models are.
type setDoc struct {
	Name     string          `json:"name"`
	Base     string          `json:"base"`
	Version  int             `json:"version"`
	Look     json.RawMessage `json:"look"`
	Have     []string        `json:"have"`
	Fallback string          `json:"fallback,omitempty"`
}

func (l *setLs) find(name string) *setEntry {
	for i := range l.Sets {
		if l.Sets[i].Name == name {
			return &l.Sets[i]
		}
	}
	return nil
}

// pick is the set to draw for name ("" the active one): that set when it
// is ok, else the built-in default with why.
func (l *setLs) pick(name string) (setDoc, bool) {
	if name == "" {
		name = l.Active
	}
	e, why := l.find(name), ""
	switch {
	case e == nil:
		why = fmt.Sprintf("there is no set %s", name)
	case e.Status != "ok":
		why = fmt.Sprintf("%s is %s: %s", name, e.Status, e.Reason)
	}
	if why != "" {
		if e = l.find(office.DefaultSet); e == nil {
			return setDoc{}, false
		}
	}
	base := "/static/office/sets/" + e.Name + "/"
	if e.Source == "installed" {
		base = "/office/sets/" + e.Name + "/"
	}
	return setDoc{Name: e.Name, Base: base, Version: e.Version, Look: e.Look, Have: e.Have, Fallback: why}, true
}

// officeSetJSON answers GET /office/set.json[?name=<set>].
func (s *Server) officeSetJSON(w http.ResponseWriter, r *http.Request) {
	ls, err := readJSON[setLs](r.Context(), s.c.CLI, "office", "set", "ls")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	doc, ok := ls.pick(r.URL.Query().Get("name"))
	if !ok {
		http.Error(w, "no built-in set", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(doc)
}

// setTypes are the content types of a set's files by extension.
var setTypes = map[string]string{".gltf": "model/gltf+json", ".bin": "application/octet-stream", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".toml": "text/plain; charset=utf-8"}

// officeSetFile answers GET /office/sets/{name}/{path...}: a file an
// installed set's index.json lists, read as bytes and never parsed. It
// reads <data>/office/ itself, not through railswitch --json (D153's
// exception): only office set add writes there, after its checks, no
// container mounts it, and one CLI run per file would be hundreds per page.
func (s *Server) officeSetFile(w http.ResponseWriter, r *http.Request) {
	name, rel := r.PathValue("name"), r.PathValue("path")
	if s.c.Data == "" || office.ValidName(name) != nil {
		http.NotFound(w, r)
		return
	}
	dir := filepath.Join(s.c.Data, "office", name)
	raw, err := safefile.Read(filepath.Join(dir, "index.json"), 1<<20)
	var idx office.Index
	if err != nil || json.Unmarshal(raw, &idx) != nil || idx.Files[rel] == "" || rel == "index.json" || path.Clean(rel) != rel || strings.HasPrefix(rel, "../") {
		http.NotFound(w, r)
		return
	}
	data, err := safefile.Read(filepath.Join(dir, filepath.FromSlash(rel)), office.MaxFileSize)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := setTypes[strings.ToLower(path.Ext(rel))]
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(data)
}

// officeUse runs office set use <name> from the Machine page.
func (s *Server) officeUse(w http.ResponseWriter, r *http.Request) {
	name := r.PostFormValue("name")
	if office.ValidName(name) != nil {
		http.Error(w, "not a set name", http.StatusBadRequest)
		return
	}
	s.start(w, r, jobs.Spec{
		Project: machineKey, Label: "office set use " + name, Dir: s.c.Home,
		Argv: []string{s.c.Exe, "office", "set", "use", name},
	}, nil, "/machine")
}
