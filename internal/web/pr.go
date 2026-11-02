package web

import (
	"net/http"
	"slices"
	"strconv"

	"github.com/jbigler/railswitch/internal/jobs"
	"github.com/jbigler/railswitch/internal/prs"
)

// The PR page (D152): a worktree's pull request as `pr show --json` gives
// it, with its merge, ready and rerun actions run as jobs. GitHub's text
// is written by anyone: the template escapes it, and the page's script
// formats a body's Markdown into text nodes only.

type prPageView struct {
	Title, Prefix string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView
	Jobs          jobsView

	Slug, Back string
	Error      string
	PR         *prs.View
	// Failed: a failed check has an Actions run to rerun.
	Failed                       bool
	MergeURL, ReadyURL, RerunURL string
}

func prPageURL(prefix, slug string) string { return "/p/" + prefix + "/" + slug + "/pr" }

func (s *Server) prPage(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	base := prPageURL(p.Prefix, wt.Slug)
	s.mu.Lock()
	pv, jv := s.chrome(p.Prefix)
	s.mu.Unlock()
	v := prPageView{
		Title: "PR · " + wt.Slug + " · " + p.Prefix, Prefix: p.Prefix,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
		Slug: wt.Slug, Back: "/p/" + p.Prefix + "/" + wt.Slug,
		MergeURL: base + "/merge", ReadyURL: base + "/ready", RerunURL: base + "/rerun",
	}
	pr, err := readJSON[prs.View](r.Context(), s.c.CLI, "-C", p.Workspace, "-w", wt.Slug, "pr", "show")
	if err != nil {
		v.Error = err.Error()
	} else {
		v.PR = &pr
		v.Failed = slices.ContainsFunc(pr.CheckRuns, func(c prs.Check) bool { return c.State == "fail" && c.Run != 0 })
	}
	s.html(w, http.StatusOK, "pr", v)
}

// prAction runs `pr merge|ready|rerun <number>` on the worktree's PR as a
// job. The form names the PR the page showed, so a branch that has a new
// PR since is never merged unseen.
func (s *Server) prAction(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	n, err := strconv.Atoi(r.FormValue("number"))
	if err != nil || n <= 0 || n > 1e9 {
		http.Error(w, "not a PR number", http.StatusBadRequest)
		return
	}
	argv := []string{s.c.Exe, "-C", p.Workspace, "-w", wt.Slug, "pr"}
	label := ""
	switch r.PathValue("action") {
	case "merge":
		m := r.FormValue("method")
		if m != "merge" && m != "squash" && m != "rebase" {
			http.Error(w, "method: merge, squash or rebase", http.StatusBadRequest)
			return
		}
		argv = append(argv, "merge", strconv.Itoa(n), "--method", m)
		label = "merge #" + strconv.Itoa(n)
		if r.FormValue("auto") == "1" {
			argv = append(argv, "--auto")
			label = "queue #" + strconv.Itoa(n)
		}
	case "ready":
		argv = append(argv, "ready", strconv.Itoa(n))
		label = "mark #" + strconv.Itoa(n) + " ready"
	case "rerun":
		argv = append(argv, "rerun", strconv.Itoa(n))
		label = "rerun #" + strconv.Itoa(n) + "'s failed checks"
	default:
		http.NotFound(w, r)
		return
	}
	s.start(w, r, jobs.Spec{Project: p.Prefix, Label: label, Dir: p.Workspace, Argv: argv}, nil, prPageURL(p.Prefix, wt.Slug))
}
