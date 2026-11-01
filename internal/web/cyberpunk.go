package web

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/jobs"
)

// The cyberpunk office (D146): an optional 3D view of a project,
// /p/{prefix}/cyberpunk, the starship's logic (D144) on the office kit
// (D145). The lobby is the project (its PRs, issues and the agents' shared
// notes), each worktree a floor of the tower (its services in the server
// room, the user's desk, the agent's desk, one pet); the elevator goes
// between them. The page is static/cyberpunk/; it follows the project
// through the "cyberpunk" event stream (cyberState as JSON) and acts
// through the same POSTs as the 2D pages, plus the JSON reads below.
// Nothing runs while no cyberpunk page is open.

// cyberState is what the cyberpunk page draws, sent whole when it changes.
type cyberState struct {
	Prefix    string       `json:"prefix"`
	Error     string       `json:"error,omitempty"` // the overview's error
	PRError   string       `json:"pr_error,omitempty"`
	Worktrees []cyberFloor `json:"worktrees"`
	Jobs      []cyberJob   `json:"jobs"`
	Banner    []string     `json:"banner"`
}

type cyberFloor struct {
	Slug     string    `json:"slug"`
	Branch   string    `json:"branch"`
	Base     bool      `json:"base"`
	Stack    string    `json:"stack"`
	Services []service `json:"services"`
	Agent    *agent    `json:"agent"`
	Links    []link    `json:"links"`
	PR       *pr       `json:"pr"`
}

type cyberJob struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Label string `json:"label"`
	State string `json:"state"`
}

// cyberData is prefix's cyberState. s.mu is held.
func (s *Server) cyberData(prefix string) cyberState {
	st := cyberState{Prefix: prefix, Worktrees: []cyberFloor{}, Jobs: []cyberJob{}, Banner: []string{}}
	p := s.state[prefix]
	if p == nil {
		st.Error = "unknown project"
		return st
	}
	st.Error, st.PRError = p.ovErr, p.prErr
	if p.ov != nil {
		for _, wt := range p.ov.Worktrees {
			fl := cyberFloor{Slug: wt.Slug, Branch: wt.Branch, Base: wt.Base, Stack: wt.Stack,
				Services: wt.Services, Agent: wt.Agent, Links: wt.Links, PR: p.prs[wt.Slug]}
			if fl.Services == nil {
				fl.Services = []service{}
			}
			if fl.Links == nil {
				fl.Links = []link{}
			}
			st.Worktrees = append(st.Worktrees, fl)
		}
		if p.ov.ReviewPending {
			st.Banner = append(st.Banner, "A package update waits for review.")
		}
		for _, f := range p.ov.DefaultsChanged {
			st.Banner = append(st.Banner, "The default of "+f+" changed since you ejected it.")
		}
	}
	for _, j := range s.c.Jobs.Jobs(prefix, "") {
		st.Jobs = append(st.Jobs, cyberJob{ID: j.ID, Slug: j.Spec.Slug, Label: j.Spec.Label, State: string(j.State)})
	}
	return st
}

// pushCyber sends a cyberpunk page its state when it changed. s.mu is held.
func (s *Server) pushCyber(sub *subscriber) {
	b, err := json.Marshal(s.cyberData(sub.prefix))
	if err != nil {
		log.Printf("dashboard: %v", err)
		return
	}
	sub.diff("cyberpunk", "state", string(b), nil)
}

type cyberView struct {
	Title, Prefix string
}

func (s *Server) cyberPage(w http.ResponseWriter, r *http.Request) {
	p, ok, err := s.project(r.Context(), r.PathValue("prefix"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown project", http.StatusNotFound)
		return
	}
	s.html(w, http.StatusOK, "cyberpunk", cyberView{Title: p.Prefix + " · Cyberpunk office · RailSwitch", Prefix: p.Prefix})
}

// cyberJSON answers the output of a railswitch --json read as is.
func (s *Server) cyberJSON(w http.ResponseWriter, r *http.Request, stdin []byte, args ...string) {
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

// cyberProject is the {prefix} of a cyberpunk read; false: answered.
func (s *Server) cyberProject(w http.ResponseWriter, r *http.Request) (projectRow, bool) {
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

func (s *Server) cyberIssues(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.cyberProject(w, r); ok {
		s.cyberJSON(w, r, nil, "-C", p.Workspace, "issue", "ls")
	}
}

func (s *Server) cyberPRs(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.cyberProject(w, r); ok {
		s.cyberJSON(w, r, nil, "-C", p.Workspace, "pr", "ls", "--all")
	}
}

// cyberIssueStart starts work on issue {n}: a worktree on the issue's
// branch, up, and the default agent on the issue in the background
// (`wt add --issue <n> --agent`), as a project job.
func (s *Server) cyberIssueStart(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cyberProject(w, r)
	if !ok {
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n <= 0 || n > 1e9 {
		http.Error(w, "not an issue number", http.StatusBadRequest)
		return
	}
	s.start(w, r, jobs.Spec{Project: p.Prefix, Label: fmt.Sprintf("work on issue #%d", n), Dir: p.Workspace,
		Argv: []string{s.c.Exe, "-C", p.Workspace, "wt", "add", "--issue", strconv.Itoa(n), "--agent"}}, nil, "/p/"+p.Prefix+"/cyberpunk")
}

// agentName is an agent's name as the URL gives it ("" = the default).
var agentName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

func cyberAgentArgs(r *http.Request) ([]string, bool) {
	n := r.URL.Query().Get("agent")
	if n == "" {
		return nil, true
	}
	return []string{n}, agentName.MatchString(n)
}

func (s *Server) cyberAgentScreen(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	name, ok := cyberAgentArgs(r)
	if !ok {
		http.Error(w, "bad agent name", http.StatusBadRequest)
		return
	}
	s.cyberJSON(w, r, nil, append([]string{"-C", p.Workspace, "-w", wt.Slug, "agent", "screen"}, name...)...)
}

// sendMax is the most text one agent send carries (the CLI's limit).
const sendMax = 4096

func (s *Server) cyberAgentSend(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	name, ok := cyberAgentArgs(r)
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

func (s *Server) cyberNotes(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.cyberProject(w, r); ok {
		s.cyberJSON(w, r, nil, "-C", p.Workspace, "notes", "ls")
	}
}

func (s *Server) cyberNote(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cyberProject(w, r)
	if !ok {
		return
	}
	n := r.PathValue("name")
	if !noteName.MatchString(n) {
		http.Error(w, "bad note name", http.StatusBadRequest)
		return
	}
	s.cyberJSON(w, r, nil, "-C", p.Workspace, "notes", "show", n)
}

// noteMaxBytes is the largest note the page saves (the CLI's limit).
const noteMaxBytes = 1 << 20

// cyberNoteWrite saves (POST …/notes/{name}?if-hash=) or removes
// (…?rm=1) a note; if-hash "" creates one.
func (s *Server) cyberNoteWrite(w http.ResponseWriter, r *http.Request) {
	p, ok := s.cyberProject(w, r)
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
	s.cyberJSON(w, r, text, "-C", p.Workspace, "notes", "write", n, "--if-hash", hash)
}
