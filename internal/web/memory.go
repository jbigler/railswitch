package web

import (
	"net/http"
	"strconv"
)

// memoryDoc is `railswitch memory --json` (docs/json.md, D136), the part
// the memory page shows.
type memoryDoc struct {
	Server struct {
		Container string `json:"container"`
		State     string `json:"state"`
	} `json:"server"`
	Schema int `json:"schema"`
	Issues *struct {
		Total      int `json:"total"`
		Open       int `json:"open"`
		InProgress int `json:"in_progress"`
		Blocked    int `json:"blocked"`
		Ready      int `json:"ready"`
		Closed     int `json:"closed"`
	} `json:"issues"`
	Memories  int `json:"memories"`
	Worktrees []struct {
		Slug    string        `json:"slug"`
		Ready   []memoryIssue `json:"ready"`
		Claimed []memoryIssue `json:"claimed"`
		Blocked []memoryIssue `json:"blocked"`
	} `json:"worktrees"`
	Activity []struct {
		Seq     int64  `json:"seq"`
		TS      string `json:"ts"`
		Op      string `json:"op"`
		IssueID string `json:"issue_id"`
		Actor   string `json:"actor"`
	} `json:"activity"`
	Cursor int64  `json:"cursor"`
	Error  string `json:"error"`
}

type memoryIssue struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Assignee string `json:"assignee"`
	Priority int    `json:"priority"`
}

// memoryView is the memory page: read-only, read once per load.
type memoryView struct {
	Title, Prefix string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView
	Jobs          jobsView

	Doc      memoryDoc
	Error    string
	Activity []memoryEvent // newest first, at most memoryActivity
}

type memoryEvent struct {
	Seq                  string
	TS, Op, Issue, Actor string
}

// memoryActivity is how much activity the page lists.
const memoryActivity = 50

// memoryPage shows the project's shared memory (D136): the server, bd's
// counts, each worktree's ready, claimed and blocked work, and recent
// activity, all from one `railswitch memory --json`.
func (s *Server) memoryPage(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	pv, jv := s.chrome(p.Prefix)
	s.mu.Unlock()
	v := memoryView{
		Title: "Memory · " + p.Prefix, Prefix: p.Prefix,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
	}
	doc, err := readJSON[memoryDoc](r.Context(), s.c.CLI, "-C", p.Workspace, "memory")
	if err != nil {
		v.Error = err.Error()
	}
	v.Doc = doc
	for i := len(doc.Activity) - 1; i >= 0 && len(v.Activity) < memoryActivity; i-- {
		a := doc.Activity[i]
		v.Activity = append(v.Activity, memoryEvent{Seq: strconv.FormatInt(a.Seq, 10), TS: a.TS, Op: a.Op, Issue: a.IssueID, Actor: a.Actor})
	}
	s.html(w, http.StatusOK, "memory", v)
}
