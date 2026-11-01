package web

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
)

// The starship (D144): an optional 3D view of a project, /p/{prefix}/ship.
// The bridge is the project (its PRs, issues and the agents' shared
// notes), each worktree a lab (its services in the server room, the
// user's desk, the agent's desk). The page is static/ship/; it follows
// the project through the "ship" event stream (shipState as JSON) and
// acts through the same POSTs as the 2D pages, plus the JSON reads below.
// Nothing runs while no ship page is open.

// shipState is what the ship page draws, sent whole when it changes.
type shipState struct {
	Prefix    string    `json:"prefix"`
	Error     string    `json:"error,omitempty"` // the overview's error
	PRError   string    `json:"pr_error,omitempty"`
	Worktrees []shipLab `json:"worktrees"`
	Jobs      []shipJob `json:"jobs"`
	Banner    []string  `json:"banner"`
}

type shipLab struct {
	Slug     string    `json:"slug"`
	Branch   string    `json:"branch"`
	Base     bool      `json:"base"`
	Stack    string    `json:"stack"`
	Services []service `json:"services"`
	Agent    *agent    `json:"agent"`
	Links    []link    `json:"links"`
	PR       *pr       `json:"pr"`
}

type shipJob struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Label string `json:"label"`
	State string `json:"state"`
}

// shipData is prefix's shipState. s.mu is held.
func (s *Server) shipData(prefix string) shipState {
	st := shipState{Prefix: prefix, Worktrees: []shipLab{}, Jobs: []shipJob{}, Banner: []string{}}
	p := s.state[prefix]
	if p == nil {
		st.Error = "unknown project"
		return st
	}
	st.Error, st.PRError = p.ovErr, p.prErr
	if p.ov != nil {
		for _, wt := range p.ov.Worktrees {
			lab := shipLab{Slug: wt.Slug, Branch: wt.Branch, Base: wt.Base, Stack: wt.Stack,
				Services: wt.Services, Agent: wt.Agent, Links: wt.Links, PR: p.prs[wt.Slug]}
			if lab.Services == nil {
				lab.Services = []service{}
			}
			if lab.Links == nil {
				lab.Links = []link{}
			}
			st.Worktrees = append(st.Worktrees, lab)
		}
		if p.ov.ReviewPending {
			st.Banner = append(st.Banner, "A package update waits for review.")
		}
		for _, f := range p.ov.DefaultsChanged {
			st.Banner = append(st.Banner, "The default of "+f+" changed since you ejected it.")
		}
	}
	for _, j := range s.c.Jobs.Jobs(prefix, "") {
		st.Jobs = append(st.Jobs, shipJob{ID: j.ID, Slug: j.Spec.Slug, Label: j.Spec.Label, State: string(j.State)})
	}
	return st
}

// pushShip sends a ship page its state when it changed. s.mu is held.
func (s *Server) pushShip(sub *subscriber) {
	b, err := json.Marshal(s.shipData(sub.prefix))
	if err != nil {
		log.Printf("dashboard: %v", err)
		return
	}
	sub.diff("ship", "state", string(b), nil)
}

type shipView struct {
	Title, Prefix string
}

func (s *Server) shipPage(w http.ResponseWriter, r *http.Request) {
	p, ok, err := s.project(r.Context(), r.PathValue("prefix"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown project", http.StatusNotFound)
		return
	}
	s.html(w, http.StatusOK, "ship", shipView{Title: p.Prefix + " · Starship · RailSwitch", Prefix: p.Prefix})
}

// shipJSON answers the output of a railswitch --json read as is.
func (s *Server) shipJSON(w http.ResponseWriter, r *http.Request, stdin []byte, args ...string) {
	var out []byte
	var err error
	if stdin != nil {
		out, err = s.c.CLI.RunInput(r.Context(), stdin, append([]string{"--json"}, args...)...)
	} else {
		out, err = s.c.CLI.Run(r.Context(), append([]string{"--json"}, args...)...)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

// shipProject is the {prefix} of a ship read; false: answered.
func (s *Server) shipProject(w http.ResponseWriter, r *http.Request) (projectRow, bool) {
	p, ok, err := s.project(r.Context(), r.PathValue("prefix"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return p, false
	}
	if !ok {
		http.Error(w, "unknown project", http.StatusNotFound)
	}
	return p, ok
}

func (s *Server) shipIssues(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.shipProject(w, r); ok {
		s.shipJSON(w, r, nil, "-C", p.Workspace, "issue", "ls")
	}
}

// agentName is an agent's name as the URL gives it ("" = the default).
var agentName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

func shipAgentArgs(r *http.Request) ([]string, bool) {
	n := r.URL.Query().Get("agent")
	if n == "" {
		return nil, true
	}
	return []string{n}, agentName.MatchString(n)
}

func (s *Server) shipAgentScreen(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	name, ok := shipAgentArgs(r)
	if !ok {
		http.Error(w, "bad agent name", http.StatusBadRequest)
		return
	}
	s.shipJSON(w, r, nil, append([]string{"-C", p.Workspace, "-w", wt.Slug, "agent", "screen"}, name...)...)
}

// sendMax is the most text one agent send carries (the CLI's limit).
const sendMax = 4096

func (s *Server) shipAgentSend(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	name, ok := shipAgentArgs(r)
	if !ok {
		http.Error(w, "bad agent name", http.StatusBadRequest)
		return
	}
	text, err := io.ReadAll(io.LimitReader(r.Body, sendMax+1))
	if err != nil || len(text) > sendMax {
		http.Error(w, "send at most 4096 bytes (none presses Enter)", http.StatusBadRequest)
		return
	}
	out, err := s.c.CLI.RunInput(r.Context(), text, append([]string{"-C", p.Workspace, "-w", wt.Slug, "agent", "send"}, name...)...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(out)
}

// noteName is a shared note's file name (the CLI checks it again).
var noteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}\.md$`)

func (s *Server) shipNotes(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.shipProject(w, r); ok {
		s.shipJSON(w, r, nil, "-C", p.Workspace, "notes", "ls")
	}
}

func (s *Server) shipNote(w http.ResponseWriter, r *http.Request) {
	p, ok := s.shipProject(w, r)
	if !ok {
		return
	}
	n := r.PathValue("name")
	if !noteName.MatchString(n) {
		http.Error(w, "bad note name", http.StatusBadRequest)
		return
	}
	s.shipJSON(w, r, nil, "-C", p.Workspace, "notes", "show", n)
}

// noteMaxBytes is the largest note the page saves (the CLI's limit).
const noteMaxBytes = 1 << 20

// shipNoteWrite saves (POST …/notes/{name}?if-hash=) or removes
// (…?rm=1) a note; if-hash "" creates one.
func (s *Server) shipNoteWrite(w http.ResponseWriter, r *http.Request) {
	p, ok := s.shipProject(w, r)
	if !ok {
		return
	}
	n := r.PathValue("name")
	if !noteName.MatchString(n) {
		http.Error(w, "bad note name", http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	hash := q.Get("if-hash")
	if q.Get("rm") == "1" {
		if hash == "" {
			http.Error(w, "rm needs if-hash", http.StatusBadRequest)
			return
		}
		out, err := s.c.CLI.Run(r.Context(), "-C", p.Workspace, "notes", "rm", n, "--if-hash", hash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		_, _ = w.Write(out)
		return
	}
	text, err := io.ReadAll(io.LimitReader(r.Body, noteMaxBytes+1))
	if err != nil || len(text) > noteMaxBytes {
		http.Error(w, "a note is at most 1 MiB", http.StatusBadRequest)
		return
	}
	if strings.ContainsRune(string(text), 0) {
		http.Error(w, "a note is text", http.StatusBadRequest)
		return
	}
	s.shipJSON(w, r, text, "-C", p.Workspace, "notes", "write", n, "--if-hash", hash)
}
