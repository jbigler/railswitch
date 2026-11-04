package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/jobs"
)

// queueView is the agent queue's add dialog (D159).
type queueView struct{ Slug, AddURL string }

func queueURL(prefix, slug string) string { return "/p/" + prefix + "/" + slug + "/queue" }

func (s *Server) queueDialog(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	s.html(w, http.StatusOK, "agentqueue", queueView{Slug: wt.Slug, AddURL: queueURL(p.Prefix, wt.Slug) + "/add"})
}

// queueAction runs agent queue add|rm|clear as a job; add's text goes on
// its stdin, an issue by number.
func (s *Server) queueAction(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	argv := []string{s.c.Exe, "-C", p.Workspace, "-w", wt.Slug, "agent", "queue"}
	var stdin []byte
	label := ""
	switch verb := r.PathValue("verb"); verb {
	case "add":
		label = "queue a prompt"
		argv = append(argv, "add")
		if n := r.PostFormValue("issue"); n != "" {
			i, err := strconv.Atoi(strings.TrimPrefix(n, "#"))
			if err != nil || i <= 0 || i > 1e9 {
				http.Error(w, "not an issue number", http.StatusBadRequest)
				return
			}
			argv = append(argv, "--issue", strconv.Itoa(i))
			label = "queue issue #" + strconv.Itoa(i)
			break
		}
		t := r.PostFormValue("text")
		switch {
		case len(t) > 2*4096: // the CLI checks the 4096 bytes, after CRLFs
			http.Error(w, "the text is over 4096 bytes", http.StatusRequestEntityTooLarge)
			return
		case strings.TrimSpace(t) == "":
			http.Error(w, "write something, or give an issue number", http.StatusBadRequest)
			return
		}
		stdin = []byte(t)
	case "rm":
		id, err := strconv.Atoi(r.PostFormValue("id"))
		if err != nil || id <= 0 {
			http.Error(w, "not a queue item", http.StatusBadRequest)
			return
		}
		argv = append(argv, "rm", strconv.Itoa(id))
		label = "unqueue #" + strconv.Itoa(id)
	case "clear":
		argv = append(argv, "clear")
		label = "clear the agent queue"
	default:
		http.Error(w, "unknown action", http.StatusNotFound)
		return
	}
	s.start(w, r, jobs.Spec{Project: p.Prefix, Slug: wt.Slug, Label: label, Dir: p.Workspace, Argv: argv}, stdin, worktreePath(p, wt.Slug))
}

// queueList is the agent queue in a worktree's detail: its items (each
// text's first line, cut, the whole as its title), the add dialog's URL,
// the rm and clear forms'.
type queueList struct {
	Items                []queueRow
	URL, RmURL, ClearURL string
}

type queueRow struct {
	ID         int
	Line, Text string
}

func newQueue(prefix string, wt worktree) queueList {
	u := queueURL(prefix, wt.Slug)
	q := queueList{URL: u, RmURL: u + "/rm", ClearURL: u + "/clear"}
	for _, it := range wt.Queue {
		line, _, _ := strings.Cut(it.Text, "\n")
		q.Items = append(q.Items, queueRow{ID: it.ID, Line: cutRunes(line, 80), Text: cutRunes(it.Text, 600)})
	}
	return q
}
