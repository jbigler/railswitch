package web

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

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
	// Threads are PR's review threads, unresolved first, each with the end
	// of its diff hunk; Unresolved counts them for Send all to the agent.
	Threads    []threadView
	Unresolved int
	ActURL     string // + "/<action>"
}

type threadView struct {
	prs.Thread
	Tail string // the hunk's last lines: the commented one and a few before
}

// hunkTail is the last n lines of a diff hunk.
func hunkTail(h string, n int) string {
	lines := strings.Split(strings.TrimRight(h, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
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
		MergeURL: base + "/merge", ReadyURL: base + "/ready", RerunURL: base + "/rerun", ActURL: base,
	}
	pr, err := readJSON[prs.View](r.Context(), s.c.CLI, "-C", p.Workspace, "-w", wt.Slug, "pr", "show")
	if err != nil {
		v.Error = err.Error()
	} else {
		v.PR = &pr
		v.Failed = slices.ContainsFunc(pr.CheckRuns, func(c prs.Check) bool { return c.State == "fail" && c.Run != 0 })
		for _, t := range pr.Threads {
			v.Threads = append(v.Threads, threadView{Thread: t, Tail: hunkTail(t.Hunk, 6)})
			if !t.Resolved {
				v.Unresolved++
			}
		}
		slices.SortStableFunc(v.Threads, func(a, b threadView) int { return boolCmp(a.Resolved, b.Resolved) })
	}
	s.html(w, http.StatusOK, "pr", v)
}

// prAction runs `pr merge|ready|rerun|comment|review|agent <number>` or
// `pr reply|resolve|unresolve <thread>` for the worktree's PR as a job;
// typed text is the job's stdin. The form names the PR the page showed, so a branch that has a new
// PR since is never merged unseen.
func (s *Server) prAction(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	argv := []string{s.c.Exe, "-C", p.Workspace, "-w", wt.Slug, "pr"}
	label := ""
	var stdin []byte
	action := r.PathValue("action")
	switch action {
	case "reply", "resolve", "unresolve": // a thread, not a number
		id := r.FormValue("thread")
		if !prs.ThreadID.MatchString(id) {
			http.Error(w, "not a review thread", http.StatusBadRequest)
			return
		}
		argv = append(argv, action, id)
		label = action + " a review thread"
		if action == "reply" {
			if stdin = prText(w, r, true); stdin == nil {
				return
			}
		}
		s.start(w, r, jobs.Spec{Project: p.Prefix, Label: label, Dir: p.Workspace, Argv: argv}, stdin, prPageURL(p.Prefix, wt.Slug))
		return
	}
	n, err := strconv.Atoi(r.FormValue("number"))
	if err != nil || n <= 0 || n > 1e9 {
		http.Error(w, "not a PR number", http.StatusBadRequest)
		return
	}
	num := strconv.Itoa(n)
	switch action {
	case "comment":
		if stdin = prText(w, r, true); stdin == nil {
			return
		}
		argv = append(argv, "comment", num)
		label = "comment on #" + num
	case "review":
		v := r.FormValue("verdict")
		if v != "approve" && v != "request-changes" && v != "comment" {
			http.Error(w, "verdict: approve, request-changes or comment", http.StatusBadRequest)
			return
		}
		if stdin = prText(w, r, v != "approve"); stdin == nil {
			return
		}
		argv = append(argv, "review", num, "--"+v)
		label = "review #" + num + ": " + v
	case "agent":
		argv = append(argv, "agent", num)
		label = "send #" + num + "'s review to the agent"
		if id := r.FormValue("thread"); id != "" {
			if !prs.ThreadID.MatchString(id) {
				http.Error(w, "not a review thread", http.StatusBadRequest)
				return
			}
			argv = append(argv, "--thread", id)
			label = "send a review thread to the agent"
		}
		if r.FormValue("queue") != "" { // D159
			argv = append(argv, "--queue")
			label = strings.Replace(label, "send", "queue", 1)
		}
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
	s.start(w, r, jobs.Spec{Project: p.Prefix, Label: label, Dir: p.Workspace, Argv: argv}, stdin, prPageURL(p.Prefix, wt.Slug))
}

// prText is the form's text for a comment, review or reply, as the job's
// stdin (never argv): at most GitHub's 65536 bytes, not blank when needed.
// nil: refused, answered. An allowed empty text is a zero-length slice.
func prText(w http.ResponseWriter, r *http.Request, needed bool) []byte {
	t := r.FormValue("body")
	switch {
	case len(t) > 65536:
		http.Error(w, "the text is over 65536 bytes", http.StatusRequestEntityTooLarge)
		return nil
	case needed && strings.TrimSpace(t) == "":
		http.Error(w, "write something first", http.StatusBadRequest)
		return nil
	}
	return append([]byte{}, t...)
}

// boolCmp orders false before true.
func boolCmp(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}
