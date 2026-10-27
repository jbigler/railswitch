// Package web is the dashboard (`railswitch dashboard serve`): one page per
// project with its worktrees, PRs, agents and links, live over
// Server-Sent Events. It reads only through `railswitch … --json` and acts
// only through `railswitch` jobs.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jbigler/railswitch/internal/jobs"
)

//go:embed static
var staticFiles embed.FS

// Config is what the dashboard runs with.
type Config struct {
	Token   string
	CLI     CLI
	Jobs    *jobs.Hub
	Exe     string // for job argv[0]
	Version string
	Now     func() time.Time
}

// Server is the dashboard: its handlers and its pollers.
type Server struct {
	c   Config
	mux *http.ServeMux

	tick time.Duration // how often the pollers look whether a poll is due
	ping time.Duration // SSE keep-alive

	wakeOV, wakePR chan struct{}

	mu             sync.Mutex
	projects       []projectRow // project ls, in registry order
	projectsAt     time.Time
	projectsLoaded bool
	state          map[string]*projectState // by prefix
	subs           map[*subscriber]struct{}
}

// New returns a Server; Run starts its pollers.
func New(c Config) *Server {
	if c.Now == nil {
		c.Now = time.Now
	}
	s := &Server{
		c: c, tick: time.Second, ping: 15 * time.Second,
		wakeOV: make(chan struct{}, 1), wakePR: make(chan struct{}, 1),
		state: map[string]*projectState{}, subs: map[*subscriber]struct{}{},
	}
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", s.healthz)
	m.HandleFunc("GET /{$}", s.root)
	m.HandleFunc("GET /p/{prefix}", s.page)
	m.HandleFunc("GET /p/{prefix}/{slug}", s.page)
	m.HandleFunc("GET /events", s.events)
	m.HandleFunc("GET /p/{prefix}/{slug}/remove", s.removeDialog)
	m.HandleFunc("GET /p/{prefix}/{slug}/tasks", s.tasksDialog)
	m.HandleFunc("GET /colors", s.colorsDialog)
	m.HandleFunc("GET /theme.css", s.theme)
	m.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	m.HandleFunc("POST /p/{prefix}/{slug}/act/{action}", s.act)
	m.HandleFunc("POST /p/{prefix}/{slug}/remove", s.remove)
	m.HandleFunc("POST /p/{prefix}/worktrees", s.addWorktree)
	m.HandleFunc("POST /p/{prefix}/{slug}/task", s.runTask)
	m.HandleFunc("POST /p/{prefix}/prs/refresh", s.refreshPRs)
	m.HandleFunc("POST /jobs/{id}/cancel", s.cancelJob)
	m.HandleFunc("POST /colors", s.setColors)
	m.HandleFunc("POST /colors/reset", s.resetColors)
	s.mux = m
	return s
}

// Handler is the dashboard's HTTP handler, every route behind the guard.
func (s *Server) Handler() http.Handler { return s.guard(s.mux) }

// Lookups: a prefix and a slug from a request are used only when the
// latest project ls and overview have them.

// project is prefix's project ls row, reading project ls when it never
// has been.
func (s *Server) project(ctx context.Context, prefix string) (projectRow, bool, error) {
	s.mu.Lock()
	loaded := s.projectsLoaded
	s.mu.Unlock()
	if !loaded {
		if err := s.loadProjects(ctx); err != nil {
			return projectRow{}, false, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.state[prefix]; p != nil {
		return p.row, true, nil
	}
	return projectRow{}, false, nil
}

// overviewOf is prefix's latest overview, read now when there is none.
func (s *Server) overviewOf(ctx context.Context, prefix, ws string) (overview, error) {
	s.mu.Lock()
	if p := s.state[prefix]; p != nil && p.ov != nil {
		ov := *p.ov
		s.mu.Unlock()
		return ov, nil
	}
	s.mu.Unlock()
	ov, err := readOverview(ctx, s.c.CLI, ws)
	s.applyOverview(prefix, ov, err, s.c.Now())
	return ov, err
}

// lookup resolves the request's {prefix} and {slug} (when the route has
// one), answering 404 or 500 itself when it can't.
func (s *Server) lookup(w http.ResponseWriter, r *http.Request) (projectRow, worktree, bool) {
	p, ok, err := s.project(r.Context(), r.PathValue("prefix"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return p, worktree{}, false
	}
	if !ok {
		http.Error(w, "unknown project", http.StatusNotFound)
		return p, worktree{}, false
	}
	ov, err := s.overviewOf(r.Context(), p.Prefix, p.Workspace)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return p, worktree{}, false
	}
	slug := r.PathValue("slug")
	if slug == "" {
		return p, worktree{}, true
	}
	for _, wt := range ov.Worktrees {
		if wt.Slug == slug {
			return p, wt, true
		}
	}
	http.Error(w, "unknown worktree", http.StatusNotFound)
	return p, worktree{}, false
}

// Pages and fragments.

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "ok %s\n", s.c.Version)
}

// root redirects to the first project; the page's script swaps it for
// the one last open.
func (s *Server) root(w http.ResponseWriter, r *http.Request) {
	if _, _, err := s.project(r.Context(), ""); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	first := ""
	if len(s.projects) > 0 {
		first = s.projects[0].Prefix
	}
	s.mu.Unlock()
	if first == "" {
		s.simple(w, http.StatusOK, "", "No project is registered on this machine yet: create one with railswitch project new.")
		return
	}
	http.Redirect(w, r, "/p/"+first, http.StatusSeeOther)
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	pv := s.pageData(p.Prefix, wt.Slug, parseSort(r.URL.Query().Get("sort")))
	s.mu.Unlock()
	s.html(w, http.StatusOK, "page", pv)
}

// events is the page's Server-Sent Events stream: the current fragments
// at once, then the changes, notifications and job updates.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, ok, err := s.project(r.Context(), q.Get("p"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown project", http.StatusNotFound)
		return
	}
	_, _ = s.overviewOf(r.Context(), p.Prefix, p.Workspace) // its error is the banner
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	write := func(b []byte) bool {
		_, err := w.Write(b)
		return err == nil && rc.Flush() == nil
	}
	if !write([]byte("retry: 2000\n\n")) {
		return
	}
	sub := s.subscribe(p.Prefix, q.Get("w"), parseSort(q.Get("sort")))
	defer s.unsubscribe(sub)
	ping := time.NewTicker(s.ping)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.gone:
			return
		case <-ping.C:
			if !write([]byte(": ping\n\n")) {
				return
			}
		case ev := <-sub.ch:
			b, err := json.Marshal(ev)
			if err != nil || !write(append(append([]byte("data: "), b...), "\n\n"...)) {
				return
			}
		}
	}
}

func (s *Server) removeDialog(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if wt.Base {
		http.Error(w, "the base worktree can't be removed", http.StatusUnprocessableEntity)
		return
	}
	rows, err := readWorktrees(r.Context(), s.c.CLI, p.Workspace)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	i := slices.IndexFunc(rows, func(x wtRow) bool { return x.Slug == wt.Slug })
	if i < 0 {
		http.Error(w, "unknown worktree", http.StatusNotFound)
		return
	}
	s.html(w, http.StatusOK, "remove", removeView{Slug: wt.Slug, Dir: rows[i].Dir, Dirty: rows[i].Dirty, URL: "/p/" + p.Prefix + "/" + wt.Slug + "/remove"})
}

func (s *Server) tasksDialog(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	tasks, err := readTasks(r.Context(), s.c.CLI, p.Workspace, wt.Slug)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.html(w, http.StatusOK, "tasks", tasksView{Slug: wt.Slug, URL: "/p/" + p.Prefix + "/" + wt.Slug + "/task", Tasks: tasks})
}

func (s *Server) colorsDialog(w http.ResponseWriter, r *http.Request) {
	d, err := readColors(r.Context(), s.c.CLI)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.html(w, http.StatusOK, "colors", newColors(d))
}

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// theme is the colours as CSS variables: light, and dark when the system
// prefers it (a route, not a <style> block: the CSP allows no inline
// style).
func (s *Server) theme(w http.ResponseWriter, r *http.Request) {
	d, err := readColors(r.Context(), s.c.CLI)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	vars := func(m colorsMode) string {
		var b strings.Builder
		for _, tok := range colorTokensAll {
			if v := m.Colors[tok]; hexColor.MatchString(v) {
				fmt.Fprintf(&b, "--%s:%s;", tok, v)
			}
		}
		return b.String()
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	fmt.Fprintf(w, ":root{%s}\n@media (prefers-color-scheme: dark){:root{%s}}\n", vars(d.Light), vars(d.Dark))
}

// Actions.

// start runs spec as a job: 202 with its id, 409 while busy.
func (s *Server) start(w http.ResponseWriter, r *http.Request, spec jobs.Spec, back string) {
	snap, err := s.c.Jobs.Start(r.Context(), spec)
	if errors.Is(err, jobs.ErrBusy) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.accepted(w, r, snap.ID, spec.Label, back)
}

// accepted answers 202: JSON for the page's script, a page with a way
// back for a form posted without it.
func (s *Server) accepted(w http.ResponseWriter, r *http.Request, id, label, back string) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		}{id, label})
		return
	}
	s.simple(w, http.StatusAccepted, back, "Started: "+label+".")
}

func worktreePath(p projectRow, slug string) string { return "/p/" + p.Prefix + "/" + slug }

func (s *Server) act(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action != "up" && action != "stop" && action != "restart" {
		http.Error(w, "unknown action", http.StatusNotFound)
		return
	}
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	s.start(w, r, jobs.Spec{
		Project: p.Prefix, Slug: wt.Slug, Label: action, Dir: p.Workspace,
		Argv: []string{s.c.Exe, "-C", p.Workspace, "-w", wt.Slug, action},
	}, worktreePath(p, wt.Slug))
}

// remove runs wt rm; --force only with the worktree's name typed.
func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	if wt.Base {
		http.Error(w, "the base worktree can't be removed", http.StatusUnprocessableEntity)
		return
	}
	argv := []string{s.c.Exe, "-C", p.Workspace, "wt", "rm"}
	if r.PostFormValue("force") != "" {
		if r.PostFormValue("confirm") != wt.Slug {
			http.Error(w, "type the worktree's name to confirm", http.StatusUnprocessableEntity)
			return
		}
		argv = append(argv, "--force")
	}
	s.start(w, r, jobs.Spec{Project: p.Prefix, Slug: wt.Slug, Label: "remove", Dir: p.Workspace, Argv: append(argv, wt.Slug)}, "/p/"+p.Prefix)
}

// badName: empty, a flag, or holding a blank or control character.
func badName(s string) bool {
	return s == "" || strings.HasPrefix(s, "-") || strings.ContainsFunc(s, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) })
}

// addWorktree runs wt add, a project-level job.
func (s *Server) addWorktree(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	branch, name := r.PostFormValue("branch"), r.PostFormValue("name")
	if branch == "" {
		http.Error(w, "a branch is required", http.StatusUnprocessableEntity)
		return
	}
	if badName(branch) {
		http.Error(w, "bad branch name", http.StatusUnprocessableEntity)
		return
	}
	argv := []string{s.c.Exe, "-C", p.Workspace, "wt", "add"}
	if name != "" {
		if badName(name) {
			http.Error(w, "bad worktree name", http.StatusUnprocessableEntity)
			return
		}
		argv = append(argv, "--name", name)
	}
	if r.PostFormValue("up") != "" {
		argv = append(argv, "--up")
	}
	s.start(w, r, jobs.Spec{Project: p.Prefix, Label: "wt add " + branch, Dir: p.Workspace, Argv: append(argv, branch)}, "/p/"+p.Prefix)
}

// runTask runs one of the worktree's tasks, with shell-split args; term
// opens it in a new terminal.
func (s *Server) runTask(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	args, err := SplitArgs(r.PostFormValue("args"))
	if err != nil {
		http.Error(w, "args: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	tasks, err := readTasks(r.Context(), s.c.CLI, p.Workspace, wt.Slug)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	name := r.PostFormValue("task")
	if !slices.ContainsFunc(tasks, func(t task) bool { return t.Name == name }) {
		http.Error(w, "unknown task", http.StatusNotFound)
		return
	}
	argv := []string{s.c.Exe, "-C", p.Workspace, "-w", wt.Slug, "task"}
	if r.PostFormValue("term") != "" {
		argv = append(argv, "--new-term")
	}
	argv = append(append(argv, name), args...)
	s.start(w, r, jobs.Spec{Project: p.Prefix, Slug: wt.Slug, Label: "task " + name, Dir: p.Workspace, Argv: argv}, worktreePath(p, wt.Slug))
}

func (s *Server) refreshPRs(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	if st := s.state[p.Prefix]; st != nil {
		st.prNow = true
	}
	s.mu.Unlock()
	wake(s.wakePR)
	s.accepted(w, r, "", "refresh PRs", "/p/"+p.Prefix)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.c.Jobs.Cancel(id); errors.Is(err, jobs.ErrNotFound) {
		http.Error(w, "unknown job", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.accepted(w, r, id, "cancel", "/")
}

// colorsMode is the form's mode as a flag: none for light, --dark.
func colorsModeArgs(mode string) ([]string, bool) {
	switch mode {
	case "light":
		return nil, true
	case "dark":
		return []string{"--dark"}, true
	}
	return nil, false
}

// setColors runs `dashboard colors set` with the form's preset and
// tokens, then answers the editor again.
func (s *Server) setColors(w http.ResponseWriter, r *http.Request) {
	mode, ok := colorsModeArgs(r.PostFormValue("mode"))
	if !ok {
		http.Error(w, "mode is light or dark", http.StatusUnprocessableEntity)
		return
	}
	args := append([]string{"dashboard", "colors", "set"}, mode...)
	if base := r.PostFormValue("base"); base != "" {
		if !slices.Contains(colorPresets, base) {
			http.Error(w, "unknown preset", http.StatusUnprocessableEntity)
			return
		}
		args = append(args, "--base", base)
	}
	for _, tok := range colorTokens {
		v := r.PostFormValue(tok)
		if v == "" {
			continue
		}
		if !hexColor.MatchString(v) {
			http.Error(w, tok+": not a #rrggbb colour", http.StatusUnprocessableEntity)
			return
		}
		args = append(args, tok+"="+v)
	}
	if len(args) == 3+len(mode) {
		http.Error(w, "nothing to change", http.StatusUnprocessableEntity)
		return
	}
	s.colorsCommand(w, r, args)
}

func (s *Server) resetColors(w http.ResponseWriter, r *http.Request) {
	mode, ok := colorsModeArgs(r.PostFormValue("mode"))
	if !ok {
		http.Error(w, "mode is light or dark", http.StatusUnprocessableEntity)
		return
	}
	s.colorsCommand(w, r, append([]string{"dashboard", "colors", "reset"}, mode...))
}

// colorsCommand runs a colours command synchronously (it only writes
// dashboard.toml) and answers the editor with the new state.
func (s *Server) colorsCommand(w http.ResponseWriter, r *http.Request, args []string) {
	if _, err := s.c.CLI.Run(r.Context(), args...); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	s.colorsDialog(w, r)
}

// Responses.

func (s *Server) html(w http.ResponseWriter, code int, name string, data any) {
	html, err := render(name, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(html))
}

// simple answers a small page without CSS: refusals, the no-project page,
// a form posted without the page's script.
func (s *Server) simple(w http.ResponseWriter, code int, back string, lines ...string) {
	s.html(w, code, "simple", struct {
		Lines []string
		Back  string
	}{lines, back})
}
