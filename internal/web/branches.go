package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/jbigler/railswitch/internal/jobs"
)

// The Branches page (D227): `branch ls --json`, the project's local
// branches with their worktree, PR, stack, upstream and age; Fetch,
// Delete selected (with Force and Remote), and a fast-forward Pull per
// branch and for every branch behind. Each action is one job; the CLI
// judges every branch again before it changes anything.

type branchDoc struct {
	Base     string      `json:"base"`
	Fetched  string      `json:"fetched"`
	Branches []branchRow `json:"branches"`
	Warnings []string    `json:"warnings"`
}

type branchRow struct {
	Name       string    `json:"name"`
	Upstream   string    `json:"upstream"`
	Gone       bool      `json:"gone"`
	Ahead      int       `json:"ahead"`
	Behind     int       `json:"behind"`
	Date       string    `json:"date"`
	Merged     bool      `json:"merged"`
	Worktree   string    `json:"worktree"`
	CheckedOut bool      `json:"checked_out"`
	Base       bool      `json:"base"`
	PR         *branchPR `json:"pr"`
	Stack      *struct {
		Number int `json:"number"`
	} `json:"stack"`
	Reasons []string `json:"reasons"`
	CouldGo bool     `json:"could_go"`
}

type branchPR struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	URL    string `json:"url"`
}

type branchesView struct {
	Title, Prefix string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView
	Jobs          jobsView

	URL, Error, Base, Fetched string
	Warnings                  []string
	Rows                      []branchView
	Behind                    []string // the branches Pull all behind pulls
	Gone, CouldGo             int
}

type branchView struct {
	branchRow
	Track, Age, Why, StackText string
	Deletable                  bool   // not the base, not checked out (the CLI judges again)
	Pullable                   bool   // behind its upstream and not ahead
	AgentRunning               bool   // its worktree's agent runs: a pull changes files under it
	PRClass                    string // the PR state's colour
}

func branchesURL(prefix string) string { return "/p/" + prefix + "/branches" }

func (s *Server) branchesPage(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	pv, jv := s.chrome(p.Prefix)
	s.mu.Unlock()
	v := branchesView{
		Title: "Branches · " + p.Prefix, Prefix: p.Prefix,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
		URL: branchesURL(p.Prefix),
	}
	s.html(w, http.StatusOK, "branches", v)
}

// branchesItems is the page's table, fetched after the page shows: the
// read asks GitHub for the PRs.
func (s *Server) branchesItems(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	v := branchesView{URL: branchesURL(p.Prefix), Prefix: p.Prefix}
	doc, err := readBranches(r.Context(), s.c.CLI, p.Workspace)
	if err != nil {
		v.Error = err.Error()
	}
	v.Base, v.Warnings = doc.Base, doc.Warnings
	now := s.c.Now()
	if t, err := time.Parse(time.RFC3339, doc.Fetched); err == nil {
		v.Fetched = "fetched " + ago(now.Sub(t))
	} else {
		v.Fetched = "never fetched"
	}
	running := map[string]bool{}
	if ov, err := s.overviewOf(r.Context(), p.Prefix, p.Workspace); err == nil {
		for _, wt := range ov.Worktrees {
			running[wt.Slug] = wt.Agent != nil && wt.Agent.Running
			for _, sv := range wt.Services {
				if _, _, word := serviceLook(sv.State); sv.Role == "agent" && word == "running" {
					running[wt.Slug] = true
				}
			}
		}
	}
	for _, b := range doc.Branches {
		bv := branchView{branchRow: b, Deletable: !b.Base && !b.CheckedOut, AgentRunning: running[b.Worktree]}
		bv.Pullable = b.Upstream != "" && !b.Gone && b.Behind > 0 && b.Ahead == 0 && (!b.CheckedOut || b.Worktree != "")
		switch {
		case b.Gone:
			bv.Track = "gone"
			v.Gone++
		case b.Upstream == "":
			bv.Track = "—"
		case b.Ahead > 0 || b.Behind > 0:
			bv.Track = fmt.Sprintf("↑%d ↓%d", b.Ahead, b.Behind)
		default:
			bv.Track = "up to date"
		}
		if t, err := time.Parse(time.RFC3339, b.Date); err == nil {
			bv.Age = ago(now.Sub(t))
		}
		why := make([]string, 0, len(b.Reasons))
		for _, rs := range b.Reasons {
			switch rs {
			case "upstream-gone":
				why = append(why, "upstream gone")
			case "pr-merged", "pr-closed":
				why = append(why, "PR "+strings.TrimPrefix(rs, "pr-"))
			case "merged":
				why = append(why, "merged into "+doc.Base)
			default:
				why = append(why, rs)
			}
		}
		bv.Why = strings.Join(why, " · ")
		if b.Stack != nil {
			bv.StackText = "in a stack"
			if b.Stack.Number > 0 {
				bv.StackText = fmt.Sprintf("stack #%d", b.Stack.Number)
			}
		}
		if b.PR != nil {
			bv.PRClass = prStateClass(b.PR.State)
		}
		if b.CouldGo {
			v.CouldGo++
		}
		if bv.Pullable {
			v.Behind = append(v.Behind, b.Name)
		}
		v.Rows = append(v.Rows, bv)
	}
	s.html(w, http.StatusOK, "branches-body", v)
}

// maxBranchArgs bounds one job's branches.
const maxBranchArgs = 500

// branchNames is the form's branch values, each one git could name and
// none an option or a <transport>:: (the CLI checks each again with
// sys.GitArg, and judges it against the branches it reads).
func branchNames(r *http.Request) ([]string, error) {
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	names := r.PostForm["branch"]
	switch {
	case len(names) == 0:
		return nil, fmt.Errorf("tick a branch first")
	case len(names) > maxBranchArgs:
		return nil, fmt.Errorf("too many branches")
	}
	for _, n := range names {
		if !branchNameOK(n) {
			return nil, fmt.Errorf("%q is not a branch name", n)
		}
	}
	return names, nil
}

func branchNameOK(n string) bool {
	if n == "" || len(n) > 255 || n[0] == '-' || strings.Contains(n, "::") || strings.Contains(n, "..") {
		return false
	}
	for _, c := range n {
		if unicode.IsControl(c) || unicode.IsSpace(c) {
			return false
		}
	}
	return true
}

// branchesFetch runs `branch fetch` as a project-level job.
func (s *Server) branchesFetch(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	s.start(w, r, jobs.Spec{Project: p.Prefix, Label: "fetch origin", Dir: p.Workspace,
		Argv: []string{s.c.Exe, "-C", p.Workspace, "branch", "fetch"}}, nil, branchesURL(p.Prefix))
}

// branchesRemove runs `branch rm [--force] [--remote] <branch>...`.
func (s *Server) branchesRemove(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	names, err := branchNames(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	argv := []string{s.c.Exe, "-C", p.Workspace, "branch", "rm"}
	if r.PostFormValue("force") == "1" {
		argv = append(argv, "--force")
	}
	if r.PostFormValue("remote") == "1" {
		argv = append(argv, "--remote")
	}
	label := "delete branch " + names[0]
	if len(names) > 1 {
		label = fmt.Sprintf("delete %d branches", len(names))
	}
	s.start(w, r, jobs.Spec{Project: p.Prefix, Label: label, Dir: p.Workspace, Argv: append(argv, names...)}, nil, branchesURL(p.Prefix))
}

// branchesPull runs `branch pull <branch>...`: fetch, then fast-forward.
func (s *Server) branchesPull(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	names, err := branchNames(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	label := "pull " + names[0]
	if len(names) > 1 {
		label = fmt.Sprintf("pull %d branches", len(names))
	}
	s.start(w, r, jobs.Spec{Project: p.Prefix, Label: label, Dir: p.Workspace,
		Argv: append([]string{s.c.Exe, "-C", p.Workspace, "branch", "pull"}, names...)}, nil, branchesURL(p.Prefix))
}
