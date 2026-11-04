package web

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/jobs"
)

// The Tidy page (D160): `tidy --json`'s candidates with their reasons and
// sizes, and Remove selected, one `tidy --rm` job that judges each item
// again before it goes. A dirty worktree is never ticked by default and
// needs the discard box.

type tidyDoc struct {
	Days     int        `json:"days"`
	Items    []tidyItem `json:"items"`
	Bytes    int64      `json:"bytes"`
	Warnings []string   `json:"warnings"`
}

type tidyItem struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Branch    string   `json:"branch"`
	Reasons   []string `json:"reasons"`
	PR        int      `json:"pr"`
	Stack     string   `json:"stack"`
	IdleSince string   `json:"idle_since"`
	IdleFrom  string   `json:"idle_from"`
	Dirty     bool     `json:"dirty"`
	Bytes     int64    `json:"bytes"`
	Partial   bool     `json:"partial"`
}

type tidyView struct {
	Title, Prefix string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView
	Jobs          jobsView

	URL, Error string
	Days       int
	Warnings   []string
	Items      []tidyRow
	Total      string
}

type tidyRow struct {
	tidyItem
	Why, Size, Idle string
	Ticked          bool // ticked for you: clean, and its PR is done or it's an unused image (idle alone is a guess)
}

// tidyIDRe is an id tidy prints: wt:<prefix>/<slug> or image:<a name
// railswitch builds> (podman.BuiltPrefix's shape).
var tidyIDRe = regexp.MustCompile(`^(wt:([a-z][a-z0-9]{1,15})/[a-z0-9][a-z0-9-]{0,39}|image:localhost/([a-z][a-z0-9]{1,15})/[a-z0-9][a-z0-9._-]*:[A-Za-z0-9._-]+-[0-9a-f]{8})$`)

func tidyURL(prefix string) string { return "/p/" + prefix + "/tidy" }

func (s *Server) tidyPage(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	pv, jv := s.chrome(p.Prefix)
	s.mu.Unlock()
	v := tidyView{
		Title: "Tidy · " + p.Prefix, Prefix: p.Prefix,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
		URL: tidyURL(p.Prefix),
	}
	doc, err := readJSON[tidyDoc](r.Context(), s.c.CLI, "-C", p.Workspace, "tidy")
	if err != nil {
		v.Error = err.Error()
	}
	v.Days, v.Warnings, v.Total = doc.Days, doc.Warnings, humanSize(doc.Bytes)
	for _, it := range doc.Items {
		why := make([]string, 0, len(it.Reasons))
		for _, rs := range it.Reasons {
			switch rs {
			case "pr-merged", "pr-closed":
				why = append(why, "PR #"+strconv.Itoa(it.PR)+" "+strings.TrimPrefix(rs, "pr-"))
			case "stopped":
				why = append(why, fmt.Sprintf("stopped over %d days", doc.Days))
			case "unused":
				why = append(why, "no stack uses it")
			default:
				why = append(why, rs)
			}
		}
		row := tidyRow{tidyItem: it, Why: strings.Join(why, " · "), Size: humanSize(it.Bytes)}
		row.Ticked = !it.Dirty && slices.ContainsFunc(it.Reasons, func(r string) bool { return r != "stopped" })
		if it.Partial {
			row.Size = "≥ " + row.Size
		}
		if len(it.IdleSince) >= 10 {
			row.Idle = "idle since " + it.IdleSince[:10]
			if it.IdleFrom == "commit" {
				row.Idle += " (last commit)"
			}
		}
		v.Items = append(v.Items, row)
	}
	s.html(w, http.StatusOK, "tidy", v)
}

// tidyRemove runs `tidy --rm [--force] <id>...` as a project-level job:
// the ids ticked (id), and the dirty worktrees ticked (dirty), which need
// the discard box and add --force.
func (s *Server) tidyRemove(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	clean, dirty := r.PostForm["id"], r.PostForm["dirty"]
	ids := append(append([]string{}, clean...), dirty...)
	switch {
	case len(ids) == 0:
		http.Error(w, "tick something to remove", http.StatusBadRequest)
		return
	case len(ids) > 500:
		http.Error(w, "too many items", http.StatusBadRequest)
		return
	case len(dirty) > 0 && r.PostFormValue("confirm") != "1":
		http.Error(w, "tick \"Discard their changes\" to remove a dirty worktree", http.StatusBadRequest)
		return
	}
	for _, id := range ids {
		m := tidyIDRe.FindStringSubmatch(id)
		if m == nil || m[2]+m[3] != p.Prefix {
			http.Error(w, "not one of this project's tidy items", http.StatusBadRequest)
			return
		}
	}
	argv := []string{s.c.Exe, "-C", p.Workspace, "tidy", "--rm"}
	if len(dirty) > 0 {
		argv = append(argv, "--force")
	}
	label := fmt.Sprintf("tidy: remove %d item(s)", len(ids))
	s.start(w, r, jobs.Spec{Project: p.Prefix, Label: label, Dir: p.Workspace, Argv: append(argv, ids...)}, nil, tidyURL(p.Prefix))
}

// humanSize is n bytes as "1.4 GB".
func humanSize(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}
