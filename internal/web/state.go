package web

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jbigler/railswitch/internal/jobs"
)

// Poll cadence (spec "Live updates").
const (
	overviewOpen  = 2 * time.Second  // a project with a page open
	overviewIdle  = 10 * time.Second // every other project
	prEvery       = 60 * time.Second // only with a page open
	projectsEvery = 10 * time.Second // project ls
	noteMax       = 200              // characters of a notification's body
	subBuffer     = 256              // events queued per page before it is dropped
)

// projectState is what the pollers know of one project.
type projectState struct {
	row   projectRow
	ov    *overview // nil until the first overview
	ovAt  time.Time // last overview attempt; zero = due now
	ovErr string
	prs   map[string]*pr // by slug
	prAt  time.Time
	prErr string
	prNow bool // Refresh PRs: due at once
	// stacks are the worktrees' gh-stack stacks (by slug), read with the
	// PRs; a failed read leaves the last ones (gh stack is a nicety).
	stacks map[string]*ghStackRow
	kinds  map[string]string // agentKind per slug at the last overview
	known  bool              // kinds holds a first overview (the baseline)
}

// event is one Server-Sent Event, as JSON.
type event struct {
	Type  string   `json:"type"` // html, job or notify
	ID    string   `json:"id,omitempty"`
	HTML  string   `json:"html,omitempty"`
	Title string   `json:"title,omitempty"`
	Body  string   `json:"body,omitempty"`
	URL   string   `json:"url,omitempty"`
	Tag   string   `json:"tag,omitempty"`
	Job   *jobInfo `json:"job,omitempty"`
}

type jobInfo struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Label string `json:"label"`
	State string `json:"state"`
}

// subscriber is one open page's event stream. Its fields past ch are
// guarded by Server.mu.
type subscriber struct {
	prefix, sel string
	sort        sortKey
	// view is "" for a project's page; "settings" (a settings page) or
	// "new" (the new page, prefix the project it creates) get only the
	// project-level jobs; "library" (the Library page) gets them and the
	// machine-level ones.
	view    string
	ch      chan event
	gone    chan struct{} // closed when the page fell behind
	dropped bool
	sent    map[string]string // fragment id → the HTML last sent
	listKey string            // selection and row order of the list last sent
}

// send queues ev; a page that doesn't keep up is dropped (it reconnects
// and gets the whole state again). Server.mu is held.
func (sub *subscriber) send(ev event) {
	if sub.dropped {
		return
	}
	select {
	case sub.ch <- ev:
	default:
		sub.dropped = true
		close(sub.gone)
	}
}

// diff queues fragment id when it differs from what the page has.
func (sub *subscriber) diff(typ, id, html string, job *jobInfo) {
	if sub.sent[id] == html {
		return
	}
	sub.sent[id] = html
	sub.send(event{Type: typ, ID: id, HTML: html, Job: job})
}

func (s *Server) subscribe(prefix, sel string, k sortKey, view string) *subscriber {
	sub := &subscriber{prefix: prefix, sel: sel, sort: k, view: view, ch: make(chan event, subBuffer), gone: make(chan struct{}), sent: map[string]string{}}
	s.mu.Lock()
	s.subs[sub] = struct{}{}
	s.push(sub)
	s.mu.Unlock()
	wake(s.wakeOV)
	wake(s.wakePR)
	return sub
}

func (s *Server) unsubscribe(sub *subscriber) {
	s.mu.Lock()
	delete(s.subs, sub)
	s.mu.Unlock()
}

func wake(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// watched reports whether a page of prefix is open. s.mu is held.
func (s *Server) watched(prefix string) bool {
	for sub := range s.subs {
		if sub.prefix == prefix {
			return true
		}
	}
	return false
}

// pollOnce runs both pollers once (tests).
func (s *Server) pollOnce(ctx context.Context) {
	s.pollOverviews(ctx)
	s.pollPRs(ctx)
}

// loop runs poll now, on every tick and on every wake until ctx ends.
func (s *Server) loop(ctx context.Context, wakeup <-chan struct{}, poll func(context.Context)) {
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-wakeup:
		}
	}
}

// Run polls the projects and forwards job events until ctx ends.
func (s *Server) Run(ctx context.Context) error {
	evs, stop := s.c.Jobs.Subscribe()
	var wg sync.WaitGroup
	wg.Go(func() {
		for ev := range evs {
			s.jobEvent(ev)
		}
	})
	wg.Go(func() { s.loop(ctx, s.wakeOV, s.pollOverviews) })
	wg.Go(func() { s.loop(ctx, s.wakePR, s.pollPRs) })
	<-ctx.Done()
	stop()
	wg.Wait()
	return nil
}

// pollOverviews refreshes the project list when due, then each due
// project's overview.
func (s *Server) pollOverviews(ctx context.Context) {
	now := s.c.Now()
	s.mu.Lock()
	due := !s.projectsLoaded || now.Sub(s.projectsAt) >= projectsEvery
	s.mu.Unlock()
	if due {
		if err := s.loadProjects(ctx); err != nil {
			log.Printf("dashboard: %v", err)
		}
	}
	type job struct{ prefix, ws string }
	var todo []job
	s.mu.Lock()
	for _, row := range s.projects {
		p := s.state[row.Prefix]
		every := overviewIdle
		if s.watched(row.Prefix) {
			every = overviewOpen
		}
		if p.ovAt.IsZero() || now.Sub(p.ovAt) >= every {
			p.ovAt = now
			todo = append(todo, job{row.Prefix, row.Workspace})
		}
	}
	s.mu.Unlock()
	for _, j := range todo {
		ov, err := readOverview(ctx, s.c.CLI, j.ws)
		if ctx.Err() != nil {
			return
		}
		s.applyOverview(j.prefix, ov, err)
	}
}

// pollPRs refreshes the PRs of each project with a page open, every
// prEvery or at once after Refresh PRs.
func (s *Server) pollPRs(ctx context.Context) {
	now := s.c.Now()
	type job struct{ prefix, ws string }
	var todo []job
	s.mu.Lock()
	for _, row := range s.projects {
		p := s.state[row.Prefix]
		if p.prNow || s.watched(row.Prefix) && (p.prAt.IsZero() || now.Sub(p.prAt) >= prEvery) {
			p.prNow, p.prAt = false, now
			todo = append(todo, job{row.Prefix, row.Workspace})
		}
	}
	s.mu.Unlock()
	for _, j := range todo {
		rows, err := readPRs(ctx, s.c.CLI, j.ws)
		if ctx.Err() != nil {
			return
		}
		stacks, serr := readStacks(ctx, s.c.CLI, j.ws)
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		if p := s.state[j.prefix]; p != nil {
			if err != nil {
				p.prErr = err.Error()
			} else {
				p.prErr = ""
				p.prs = map[string]*pr{}
				for _, r := range rows {
					p.prs[r.Slug] = r.PR
				}
			}
			if serr == nil {
				p.stacks = map[string]*ghStackRow{}
				for i := range stacks.Worktrees {
					if r := &stacks.Worktrees[i]; stacks.Available && (r.Stack != nil || r.Error != "") {
						p.stacks[r.Slug] = r
					}
				}
			}
			s.publish()
		}
		s.mu.Unlock()
	}
}

// loadProjects reads project ls; projects whose workspace is missing are
// left out.
func (s *Server) loadProjects(ctx context.Context) error {
	rows, err := readProjects(ctx, s.c.CLI)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projectsAt = s.c.Now()
	if err != nil {
		return err
	}
	s.projectsLoaded = true
	s.projects = s.projects[:0]
	seen := map[string]bool{}
	for _, r := range rows {
		if r.WorkspaceMissing || seen[r.Prefix] {
			continue
		}
		seen[r.Prefix] = true
		s.projects = append(s.projects, r)
		if p := s.state[r.Prefix]; p != nil {
			p.row = r
		} else {
			s.state[r.Prefix] = &projectState{row: r}
		}
	}
	for prefix := range s.state {
		if !seen[prefix] {
			delete(s.state, prefix)
		}
	}
	s.publish()
	return nil
}

// applyOverview stores a project's overview, notifies of agents that
// started waiting, and updates the pages. The poll time was set when the
// read was scheduled, so a job ending during the read keeps it due.
func (s *Server) applyOverview(prefix string, ov overview, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.state[prefix]
	if p == nil {
		return
	}
	if err != nil {
		p.ovErr = err.Error()
		s.publish()
		return
	}
	p.ovErr = ""
	kinds := map[string]string{}
	var notes []event
	for _, wt := range ov.Worktrees {
		k := agentKind(wt.Agent)
		if phrase, ok := notePhrases[k]; ok && p.known && p.kinds[wt.Slug] != k {
			notes = append(notes, event{
				Type:  "notify",
				Title: fmt.Sprintf("%s / %s %s", prefix, wt.Slug, phrase),
				Body:  cutRunes(wt.Agent.Message, noteMax),
				URL:   "/p/" + prefix + "/" + wt.Slug,
				Tag:   prefix + "/" + wt.Slug,
			})
		}
		kinds[wt.Slug] = k
	}
	p.kinds, p.known, p.ov = kinds, true, &ov
	for sub := range s.subs {
		for _, n := range notes {
			sub.send(n)
		}
	}
	s.publish()
}

// cutRunes is s cut to n characters.
func cutRunes(s string, n int) string {
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}

// jobEvent updates the jobs of the pages of the job's project; a job that
// ended makes its project's overview due.
func (s *Server) jobEvent(ev jobs.Event) {
	j := ev.Job
	s.mu.Lock()
	defer s.mu.Unlock()
	info := &jobInfo{ID: j.ID, Slug: j.Spec.Slug, Label: j.Spec.Label, State: string(j.State)}
	for sub := range s.subs {
		if sub.prefix != j.Spec.Project && (sub.view != "library" || j.Spec.Project != machineKey) {
			continue
		}
		html, err := render("jobs", s.subJobs(sub, s.pageJobs(sub.prefix, sub.view == "library")))
		if err != nil {
			log.Printf("dashboard: %v", err)
			continue
		}
		sub.diff("job", "jobs", html, info)
	}
	if p := s.state[j.Spec.Project]; p != nil && j.State != jobs.StateRunning {
		p.ovAt = time.Time{}
		wake(s.wakeOV)
	}
}

// publish brings every page up to date. s.mu is held.
func (s *Server) publish() {
	for sub := range s.subs {
		s.push(sub)
	}
}

// push sends sub the fragments that changed since it last got them: a
// row on its own while the list's order and selection stay, else the
// whole list. s.mu is held.
func (s *Server) push(sub *subscriber) {
	shown := sub.prefix
	if sub.view == "new" {
		shown = "" // the header and switcher of no project
	}
	pv := s.pageData(shown, sub.sel, sub.sort)
	for _, f := range []struct {
		id   string
		data any
	}{{"header", pv.Header}, {"projects", pv.Projects}, {"strip", pv.Strip}, {"banner", pv.Banner}} {
		html, err := render(f.id, f.data)
		if err != nil {
			log.Printf("dashboard: %v", err)
			return
		}
		sub.diff("html", f.id, html, nil)
	}
	if sub.view != "" {
		html, err := render("jobs", s.subJobs(sub, s.pageJobs(sub.prefix, sub.view == "library")))
		if err != nil {
			log.Printf("dashboard: %v", err)
			return
		}
		sub.diff("html", "jobs", html, nil)
		return
	}
	rows := make([]string, len(pv.List.Rows))
	keys := []string{pv.Sel}
	for i, r := range pv.List.Rows {
		html, err := render("row", r)
		if err != nil {
			log.Printf("dashboard: %v", err)
			return
		}
		rows[i] = html
		keys = append(keys, r.Slug)
	}
	if key := strings.Join(keys, "\x00"); key != sub.listKey {
		html, err := render("list", pv.List)
		if err != nil {
			log.Printf("dashboard: %v", err)
			return
		}
		sub.listKey = key
		for i, r := range pv.List.Rows {
			sub.sent[r.ID] = rows[i]
		}
		sub.diff("html", "list", html, nil)
	} else {
		for i, r := range pv.List.Rows {
			sub.diff("html", r.ID, rows[i], nil)
		}
	}
	for _, f := range []struct {
		id   string
		data any
	}{{"detail", pv.Detail}, {"jobs", pv.Jobs}} {
		html, err := render(f.id, f.data)
		if err != nil {
			log.Printf("dashboard: %v", err)
			return
		}
		sub.diff("html", f.id, html, nil)
	}
}

// subJobs is the jobs fragment of sub's page: the selected worktree's
// with the project's, or on a settings or new page the project's alone.
// s.mu is held.
func (s *Server) subJobs(sub *subscriber, snaps []jobs.Snapshot) jobsView {
	rows, sel := s.rows(sub.prefix, sub.sort, sub.sel)
	if sub.view != "" {
		sel = ""
	}
	return newJobs(snaps, rows, sel)
}

// rows is prefix's worktrees with their PRs sorted by k, and the slug
// selected: sel when it exists, else the first row's. s.mu is held.
func (s *Server) rows(prefix string, k sortKey, sel string) ([]row, string) {
	p := s.state[prefix]
	if p == nil || p.ov == nil {
		return nil, ""
	}
	rows := make([]row, 0, len(p.ov.Worktrees))
	found := false
	for _, wt := range p.ov.Worktrees {
		rows = append(rows, row{WT: wt, PR: p.prs[wt.Slug], Stacked: p.stacks[wt.Slug]})
		found = found || wt.Slug == sel
	}
	sortRows(rows, k)
	if !found {
		sel = ""
		if len(rows) > 0 {
			sel = rows[0].WT.Slug
		}
	}
	return rows, sel
}

// pageData is everything a page of prefix shows. s.mu is held.
func (s *Server) pageData(prefix, sel string, k sortKey) pageView {
	rows, sel := s.rows(prefix, k, sel)
	need := 0
	var strip stripView
	var projects projectsView
	for i, pj := range s.projects {
		p := s.state[pj.Prefix]
		item := switchItem{Prefix: pj.Prefix, URL: "/p/" + pj.Prefix, Worktrees: pj.Worktrees, Current: pj.Prefix == prefix}
		if i < 9 {
			item.N = i + 1
		}
		if p.ov != nil {
			item.Worktrees = len(p.ov.Worktrees)
			item.Review = p.ov.ReviewPending
			for _, wt := range p.ov.Worktrees {
				kind := agentKind(wt.Agent)
				wv := switchWT{Slug: wt.Slug, URL: "/p/" + pj.Prefix + "/" + wt.Slug, Need: needsInput(kind)}
				item.WTs = append(item.WTs, wv)
				if wt.Stack == "up" || wt.Stack == "partial" {
					item.Running++
				}
				if !wv.Need {
					continue
				}
				item.Need++
				if pj.Prefix != prefix {
					strip.Items = append(strip.Items, stripItem{Text: fmt.Sprintf("◆ %s / %s · %s", pj.Prefix, wt.Slug, notePhrases[kind]), URL: wv.URL})
				}
			}
		}
		need += item.Need
		projects.Items = append(projects.Items, item)
	}
	var banner bannerView
	if p := s.state[prefix]; p != nil {
		if p.ovErr != "" {
			banner.Lines = append(banner.Lines, "Status unavailable: "+p.ovErr)
		}
		if p.prErr != "" {
			banner.Lines = append(banner.Lines, "PRs unavailable: "+p.prErr)
		}
		if p.ov != nil && p.ov.ReviewPending {
			banner.Review = reviewURL(prefix)
		}
		if p.ov != nil {
			for _, sw := range p.ov.SignerWarnings {
				banner.Signers = append(banner.Signers, signerWarningText(sw))
			}
			if p.ov.TemplateUpdate && p.ov.Template != nil {
				banner.Template = &bannerTemplate{Ref: p.ov.Template.Ref, URL: settingsURL(prefix) + "#template"}
			}
		}
	}
	pv := pageView{
		Title: title(need), Prefix: prefix, Sel: sel, Sort: k.String(),
		Header: headerView{Prefix: prefix, Need: need}, Projects: projects, Strip: strip, Banner: banner,
		List:   newList(prefix, rows, sel, k),
		Detail: detailView{Empty: true},
		Jobs:   newJobs(s.c.Jobs.Jobs(prefix, ""), rows, sel),
	}
	for _, r := range rows {
		if r.WT.Slug == sel {
			pv.Detail = newDetail(prefix, r)
		}
	}
	return pv
}
