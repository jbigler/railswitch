package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/jbigler/railswitch/internal/jobs"
	"github.com/jbigler/railswitch/internal/naming"
)

// The new project page (spec "Page"): step 1 reads the repository with
// `project detect`, step 2 shows `project new --dry-run`, and Create runs
// `project new` as a job keyed by the new prefix. Every value is its own
// argv entry; one that could pass for a flag is refused before anything
// runs.

// newForm is step 1 as posted: the project new flags.
type newForm struct {
	Repo, Branch, Name, Prefix, Dir, Pack string
	Options                               []string // key=ref: the slots (from opt-<key>), then the extra services
	Nvim                                  bool
	Agents                                []string // name=ref; none ticked is --agent none
}

// badValue: a value the CLI could read as a flag, or one holding a line
// break or another control character.
func badValue(s string) bool {
	return strings.HasPrefix(s, "-") || strings.ContainsFunc(s, unicode.IsControl)
}

// pair checks a key=ref form value.
func pair(what, v string) error {
	k, ref, ok := strings.Cut(v, "=")
	if !ok || badName(k) || badName(ref) {
		return fmt.Errorf("bad %s %q: want name=ref", what, v)
	}
	return nil
}

// parseNewForm reads step 1; full (review, create) also needs the prefix,
// folder and pack, which reading the repository does not.
func parseNewForm(r *http.Request, home string, full bool) (newForm, error) {
	v := func(n string) string { return strings.TrimSpace(r.PostFormValue(n)) }
	f := newForm{Repo: v("repo"), Branch: v("branch"), Name: v("name"), Prefix: v("prefix"), Dir: v("dir"), Pack: v("pack")}
	if f.Repo == "" {
		return f, errors.New("a repository is required")
	}
	for _, c := range []struct{ label, val string }{
		{"repository", f.Repo}, {"base branch", f.Branch}, {"project name", f.Name},
		{"prefix", f.Prefix}, {"workspace folder", f.Dir}, {"pack", f.Pack},
	} {
		if badValue(c.val) {
			return f, fmt.Errorf("%s: can't start with - or hold a line break", c.label)
		}
	}
	if f.Branch != "" && badName(f.Branch) {
		return f, errors.New("base branch: no spaces")
	}
	if f.Pack != "" && badName(f.Pack) {
		return f, errors.New("bad pack ref")
	}
	if !full {
		return f, nil
	}
	if err := naming.ValidPrefix(f.Prefix); err != nil {
		return f, err
	}
	if rest, ok := strings.CutPrefix(f.Dir, "~/"); ok {
		f.Dir = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(f.Dir) {
		return f, errors.New("workspace folder: an absolute path, or one under ~/")
	}
	f.Dir = filepath.Clean(f.Dir)
	if f.Pack == "" {
		return f, errors.New("read the repository or pick a pack first")
	}
	var slots []string
	for k := range r.PostForm {
		if key, ok := strings.CutPrefix(k, "opt-"); ok {
			slots = append(slots, key)
		}
	}
	slices.Sort(slots)
	for _, key := range slots {
		f.Options = append(f.Options, key+"="+r.PostFormValue("opt-"+key))
	}
	f.Options = append(f.Options, r.PostForm["option"]...)
	for _, o := range f.Options {
		if err := pair("option", o); err != nil {
			return f, err
		}
	}
	for _, t := range r.PostForm["tool"] {
		if t != "nvim" {
			return f, fmt.Errorf("unknown tool %q", t)
		}
		f.Nvim = true
	}
	f.Agents = r.PostForm["agent"]
	for _, a := range f.Agents {
		if err := pair("agent", a); err != nil {
			return f, err
		}
	}
	return f, nil
}

// args is the project new command line, without the program.
func (f newForm) args() []string {
	a := []string{"project", "new", f.Dir, "--repo", f.Repo, "--prefix", f.Prefix}
	if f.Name != "" {
		a = append(a, "--name", f.Name)
	}
	if f.Branch != "" {
		a = append(a, "--branch", f.Branch)
	}
	a = append(a, "--pack", f.Pack)
	for _, o := range f.Options {
		a = append(a, "--option", o)
	}
	if f.Nvim {
		a = append(a, "--tool", "nvim")
	}
	if len(f.Agents) == 0 {
		return append(a, "--agent", "none")
	}
	for _, ag := range f.Agents {
		a = append(a, "--agent", ag)
	}
	return a
}

// View models.

type newView struct {
	Title, Prefix string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView
	Jobs          jobsView

	Home        string
	Agents      []agentChoice
	AgentsError string
	Stack       newStackView
	Review      newReviewView
}

type agentChoice struct {
	Name, Ref string
	Default   bool
}

// newStackView is the Stack card: the pack and one row per slot, as the
// repository suggests them. Read is false before a read.
type newStackView struct {
	Read             bool
	Branch           string
	Pack             string // "": no pack matched
	Evidence, Others []string
	Packs            []choiceView
	Slots            []slotView
	AddChoices       []string
}

type slotView struct {
	Key, Title, Ref, Source, Label string
	Others                         []string
	Choices                        []choiceView
}

type newReviewView struct {
	Empty     bool
	Error     string // the dry run's refusal: Create is off
	Doc       dryRunDoc
	CreateURL string
}

// worksWith mirrors pack.Resolve: an option fits a pack when works_with
// has "*" or the pack's name.
func worksWith(o catalogOption, packName string) bool {
	return slices.Contains(o.WorksWith, "*") || slices.Contains(o.WorksWith, packName)
}

// packName is ref's pack name: the catalog's, else the ref's own (a local
// pack is named by its ref).
func packName(cat catalogDoc, ref string) string {
	for _, p := range cat.Packs {
		if p.Ref == ref {
			return p.Name
		}
	}
	name, _, _ := strings.Cut(strings.TrimPrefix(ref, "local:"), "@")
	return name
}

func slotTitle(key string) string {
	if key == "" {
		return key
	}
	return strings.ToUpper(key[:1]) + key[1:]
}

func newStack(det detectDoc, cat catalogDoc) newStackView {
	v := newStackView{Read: true, Branch: det.Branch}
	if det.Pack != nil {
		v.Pack, v.Evidence, v.Others = det.Pack.Ref, det.Pack.Evidence, det.Pack.Others
	}
	for _, p := range cat.Packs {
		v.Packs = append(v.Packs, choiceView{Ref: p.Ref, Current: p.Ref == v.Pack})
	}
	if v.Pack != "" && !slices.ContainsFunc(v.Packs, func(c choiceView) bool { return c.Ref == v.Pack }) {
		v.Packs = append(v.Packs, choiceView{Ref: v.Pack, Current: true})
	}
	if v.Pack == "" {
		return v
	}
	name := packName(cat, v.Pack)
	for _, o := range cat.Options {
		if worksWith(o, name) {
			v.AddChoices = append(v.AddChoices, o.Ref)
		}
	}
	for _, m := range det.Options {
		sv := slotView{Key: m.Key, Title: slotTitle(m.Key), Ref: m.Ref, Source: m.Source, Others: m.Others, Label: "Pack default"}
		if m.Source == "repo" {
			sv.Label = "Read from the repository · " + m.Evidence
		}
		refs := []string{}
		for _, o := range cat.Options {
			if o.Slot == m.Key && worksWith(o, name) {
				refs = append(refs, o.Ref)
			}
		}
		refs = append(refs, "none")
		if !slices.Contains(refs, m.Ref) {
			refs = append([]string{m.Ref}, refs...)
		}
		for _, r := range refs {
			sv.Choices = append(sv.Choices, choiceView{Ref: r, Current: r == m.Ref})
		}
		v.Slots = append(v.Slots, sv)
	}
	return v
}

// Handlers.

func (s *Server) newPage(w http.ResponseWriter, r *http.Request) {
	if _, _, err := s.project(r.Context(), ""); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	pv := s.pageData("", "", sortKey{By: "name"})
	s.mu.Unlock()
	v := newView{
		Title: "New project · RailSwitch", Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner,
		Home: s.c.Home, Review: newReviewView{Empty: true},
	}
	cat, err := readCatalog(r.Context(), s.c.CLI)
	if err != nil {
		v.AgentsError = err.Error()
	}
	for _, a := range cat.Agents {
		v.Agents = append(v.Agents, agentChoice{Name: a.Name, Ref: a.Ref, Default: a.Default})
	}
	s.html(w, http.StatusOK, "new", v)
}

// newDetect reads the repository: the Stack card for its pack and
// options, or 422 with git's error.
func (s *Server) newDetect(w http.ResponseWriter, r *http.Request) {
	f, err := parseNewForm(r, s.c.Home, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	args := []string{"project", "detect", f.Repo}
	if f.Branch != "" {
		args = append(args, "--branch", f.Branch)
	}
	if f.Pack != "" {
		args = append(args, "--pack", f.Pack)
	}
	det, err := readJSON[detectDoc](r.Context(), s.c.CLI, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	cat, err := readCatalog(r.Context(), s.c.CLI)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.html(w, http.StatusOK, "newstack", newStack(det, cat))
}

// newReview is step 2: the dry run of the posted form, its refusal at the
// top when it has one.
func (s *Server) newReview(w http.ResponseWriter, r *http.Request) {
	f, err := parseNewForm(r, s.c.Home, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	v := newReviewView{CreateURL: "/new/create"}
	v.Doc, err = readJSON[dryRunDoc](r.Context(), s.c.CLI, append(f.args(), "--dry-run")...)
	if err != nil {
		v.Error = err.Error()
	}
	s.html(w, http.StatusOK, "newreview", v)
}

// newCreate runs project new [--up], a project-level job of the new
// prefix: a second create of it is refused busy while the first runs.
func (s *Server) newCreate(w http.ResponseWriter, r *http.Request) {
	f, err := parseNewForm(r, s.c.Home, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	argv := append([]string{s.c.Exe}, f.args()...)
	if r.PostFormValue("up") != "" {
		argv = append(argv, "--up")
	}
	s.start(w, r, jobs.Spec{Project: f.Prefix, Label: "project new " + f.Prefix, Dir: s.c.Home, Argv: argv}, nil, "/new")
}

// newRegistered answers whether prefix is registered now, from a fresh
// `project ls`: after a failed create job the page opens the project when
// project new got that far (its --up failed) and keeps the form when not.
func (s *Server) newRegistered(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	if err := naming.ValidPrefix(prefix); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.loadProjects(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	_, ok := s.state[prefix]
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Registered bool `json:"registered"`
	}{ok})
}
