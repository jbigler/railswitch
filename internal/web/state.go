package web

import (
	"context"
	"fmt"
	"log"
	"maps"
	"slices"
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
	usageEvery    = 30 * time.Second // agent usage, only with a page open
	projectsEvery = 10 * time.Second // project ls
	noteMax       = 200              // characters of a notification's body
	subBuffer     = 256              // events queued per page before it is dropped
	// headGrace is how long after a switch made here an overview that
	// still shows another branch may be one read before it; after that
	// the overview is right (a checkout elsewhere undid the switch).
	headGrace = 10 * time.Second
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
	// heads are the worktrees' branches as last known (by slug): from the
	// overview when a branch changes there, and from a stack switch that
	// ended ok, which the overview may not show yet. A stack read made at
	// another branch is stale and never replaces the stack shown (#256).
	heads  map[string]string
	headAt map[string]time.Time // when heads last moved (zero: from the overview)
	// stackNow are the worktrees whose stack to read at once, alone and
	// without GitHub's details, after their branch changed (#254).
	stackNow map[string]bool
	// usage is the agents' usage history (D162), read every usageEvery
	// on its own (D225); a failed read leaves the last one.
	usage    *usageDoc
	usageAt  time.Time
	usageNow bool              // Refresh PRs: due at once
	kinds    map[string]string // agentKind per slug at the last overview
	known    bool              // kinds holds a first overview (the baseline)
	// queueTried is the agent status ts (by slug) a queue delivery last
	// started at: one try per status (D159).
	queueTried map[string]int64
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
	days        int // the usage views' range (D187)
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

func (s *Server) subscribe(prefix, sel string, k sortKey, days int, view string) *subscriber {
	sub := &subscriber{prefix: prefix, sel: sel, sort: k, days: days, view: view, ch: make(chan event, subBuffer), gone: make(chan struct{}), sent: map[string]string{}}
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
	s.pollStacks(ctx)
	s.pollUsage(ctx)
	s.pollPRs(ctx)
}

// loop runs poll now, on every tick and on every wake until ctx ends; a
// nil wakeup means ticks only.
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
	s.ttyMu.Lock()
	s.ttyBase = ctx
	s.ttyMu.Unlock()
	evs, stop := s.c.Jobs.Subscribe()
	var wg sync.WaitGroup
	wg.Go(func() {
		for ev := range evs {
			s.jobEvent(ev)
		}
	})
	wg.Go(func() { s.loop(ctx, s.wakeOV, s.pollOverviews) })
	wg.Go(func() { s.loop(ctx, s.wakePR, s.pollPRs) })
	wg.Go(func() { s.loop(ctx, s.wakeST, s.pollStacks) })
	wg.Go(func() { s.loop(ctx, nil, s.pollUsage) })
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
				was := p.stacks
				p.stacks = map[string]*ghStackRow{}
				for i := range stacks.Worktrees {
					r := &stacks.Worktrees[i]
					if !p.current(r) {
						// Read before a checkout this read didn't see: keep
						// what is shown; the checkout asked for a new read.
						if old := was[r.Slug]; old != nil {
							p.stacks[r.Slug] = old
						}
						continue
					}
					if stacks.Available && (r.Stack != nil || r.Error != "") {
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
	for _, spec := range s.storeOverview(prefix, ov, err) {
		if startHook != nil {
			startHook(spec)
		}
		if _, err := s.c.Jobs.Start(context.Background(), spec); err != nil {
			s.mu.Lock()
			if p := s.state[prefix]; p != nil {
				delete(p.queueTried, spec.Slug) // busy: try again at the next overview
			}
			s.mu.Unlock()
		}
	}
}

// queueSettle is lifecycle.QueueSettle: how long an agent stays idle
// before the dashboard asks for its queue's next item (D159).
const queueSettle = 5 * time.Second

// queueLabel is the label of a job delivering a queued prompt.
const queueLabel = "agent queue next"

// queueDue is the worktrees of ov whose agent may take its queue's next
// item: running, idle or its turn over (not asking anything), QueueSettle
// so, with a status not tried since. It marks them tried. s.mu is held.
func (s *Server) queueDue(p *projectState, ov overview) []jobs.Spec {
	var out []jobs.Spec
	for _, wt := range ov.Worktrees {
		a := wt.Agent
		if len(wt.Queue) == 0 || a == nil || !a.Running {
			continue
		}
		if k := agentKind(a); k != "idle" && k != "turn" {
			continue
		}
		if s.c.Now().Sub(time.Unix(a.TS, 0)) < queueSettle || p.queueTried[wt.Slug] == a.TS {
			continue
		}
		if p.queueTried == nil {
			p.queueTried = map[string]int64{}
		}
		p.queueTried[wt.Slug] = a.TS
		out = append(out, jobs.Spec{Project: p.row.Prefix, Slug: wt.Slug, Label: queueLabel, Dir: p.row.Workspace,
			Argv: []string{s.c.Exe, "-C", p.row.Workspace, "-w", wt.Slug, "agent", "queue", "next"}})
	}
	return out
}

// storeOverview is applyOverview under s.mu; it returns the queue
// deliveries to start.
func (s *Server) storeOverview(prefix string, ov overview, err error) []jobs.Spec {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.state[prefix]
	if p == nil {
		return nil
	}
	if err != nil {
		p.ovErr = err.Error()
		s.publish()
		return nil
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
	// A checkout made outside the dashboard (git switch in a terminal, #191):
	// the PR and stack shown are the branch before's until they are read
	// again. headMoved shows the stack at the new branch and reads it
	// again at once, as a switch made here does. An overview that still
	// shows the branch before a switch made here is one read before it,
	// for headGrace.
	was := map[string]string{}
	if p.ov != nil {
		for _, wt := range p.ov.Worktrees {
			was[wt.Slug] = wt.Branch
		}
	}
	if p.heads == nil {
		p.heads = map[string]string{}
	}
	now := s.c.Now()
	for _, wt := range ov.Worktrees {
		b, seen := was[wt.Slug]
		h, known := p.heads[wt.Slug]
		switch {
		case !known:
			p.heads[wt.Slug] = wt.Branch
		case wt.Branch == h:
		case seen && b != wt.Branch, now.Sub(p.headAt[wt.Slug]) >= headGrace:
			// A checkout this page didn't make, or one that undid a
			// switch made here before the overview ever showed it.
			s.headMoved(p, wt.Slug, wt.Branch)
		}
	}
	p.kinds, p.known, p.ov = kinds, true, &ov
	for sub := range s.subs {
		for _, n := range notes {
			sub.send(n)
		}
	}
	s.publish()
	return s.queueDue(p, ov)
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
		if sub.view == "office" {
			s.pushCyber(sub)
			continue
		}
		html, err := render("jobs", s.subJobs(sub, s.pageJobs(sub.prefix, sub.view == "library")))
		if err != nil {
			log.Printf("dashboard: %v", err)
			continue
		}
		sub.diff("job", "jobs", html, info)
	}
	if strings.HasPrefix(j.Spec.Label, switchLabel) && j.State == jobs.StateRunning {
		s.publish() // the stack shows the branch switching, the others not to click
	}
	if p := s.state[j.Spec.Project]; p != nil && j.State != jobs.StateRunning {
		p.ovAt = time.Time{}
		wake(s.wakeOV)
		if branch, ok := strings.CutPrefix(j.Spec.Label, switchLabel); ok {
			if j.State == jobs.StateOK {
				s.headMoved(p, j.Spec.Slug, branch)
			} else {
				p.prNow = true
				wake(s.wakePR)
			}
			s.publish() // ended either way: the switching mark goes
		}
	}
}

// pollUsage reads the agents' usage history of each project with a page
// open, every usageEvery or at once after Refresh PRs. It is a local
// file the overview poll adds to, so it is read on its own, not behind
// the PR poll's GitHub queries (D225).
func (s *Server) pollUsage(ctx context.Context) {
	now := s.c.Now()
	type job struct{ prefix, ws string }
	var todo []job
	s.mu.Lock()
	for _, row := range s.projects {
		p := s.state[row.Prefix]
		if p.usageNow || s.watched(row.Prefix) && (p.usageAt.IsZero() || now.Sub(p.usageAt) >= usageEvery) {
			p.usageNow, p.usageAt = false, now
			todo = append(todo, job{row.Prefix, row.Workspace})
		}
	}
	s.mu.Unlock()
	for _, j := range todo {
		usage, err := readUsage(ctx, s.c.CLI, j.ws)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			continue
		}
		s.mu.Lock()
		if p := s.state[j.prefix]; p != nil {
			p.usage = &usage
			s.publish()
		}
		s.mu.Unlock()
	}
}

// headMoved records that worktree slug is now on branch: its stack marks
// branch HEAD at once, a quick read of that stack alone follows
// (pollStacks), and the PRs and every stack with GitHub's details are
// read again behind it, which takes longer. s.mu is held.
func (s *Server) headMoved(p *projectState, slug, branch string) {
	if p.heads == nil {
		p.heads = map[string]string{}
	}
	if p.headAt == nil {
		p.headAt = map[string]time.Time{}
	}
	if p.stackNow == nil {
		p.stackNow = map[string]bool{}
	}
	p.heads[slug], p.headAt[slug] = branch, s.c.Now()
	p.switched(slug, branch)
	p.stackNow[slug] = true
	p.prNow = true
	wake(s.wakeST)
	wake(s.wakePR)
}

// current reports whether stack read r was made at its worktree's branch
// as last known; one made before a checkout is stale (#256). A read that
// doesn't say, or a worktree whose branch isn't known yet, counts as
// current.
func (p *projectState) current(r *ghStackRow) bool {
	want, read := p.heads[r.Slug], r.readBranch()
	return want == "" || read == "" || want == read
}

// pollStacks reads, alone and without GitHub's details, the stack of each
// worktree whose branch just changed (#254): a full stack ls reads every
// worktree and asks GitHub once per open branch, minutes on a big stack.
// The details already known of the same PRs carry over until the PR poll
// reads them again.
func (s *Server) pollStacks(ctx context.Context) {
	type job struct{ prefix, ws, slug string }
	var todo []job
	s.mu.Lock()
	for _, row := range s.projects {
		p := s.state[row.Prefix]
		for _, slug := range slices.Sorted(maps.Keys(p.stackNow)) {
			todo = append(todo, job{row.Prefix, row.Workspace, slug})
		}
		p.stackNow = nil
	}
	s.mu.Unlock()
	for _, j := range todo {
		doc, err := readStackQuick(ctx, s.c.CLI, j.ws, j.slug)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			continue // the PR poll reads it again anyway
		}
		s.mu.Lock()
		if p := s.state[j.prefix]; p != nil {
			for i := range doc.Worktrees {
				if r := &doc.Worktrees[i]; r.Slug == j.slug {
					p.quickStack(r, doc.Available)
				}
			}
			s.publish()
		}
		s.mu.Unlock()
	}
}

// quickStack stores quick read r of one worktree's stack unless it is
// stale, with the draft, checks and review last read for the same PRs.
// s.mu is held.
func (p *projectState) quickStack(r *ghStackRow, available bool) {
	if !p.current(r) {
		return
	}
	if !available || r.Stack == nil && r.Error == "" {
		delete(p.stacks, r.Slug)
		return
	}
	if r.Stack != nil {
		known := map[int]*ghStackPR{}
		for _, old := range p.stacks {
			if old.Stack == nil {
				continue
			}
			for _, b := range old.Stack.Branches {
				if b.PR != nil {
					known[b.PR.Number] = b.PR
				}
			}
		}
		for i := range r.Stack.Branches {
			b := &r.Stack.Branches[i]
			if b.PR == nil || b.PR.State != "open" || known[b.PR.Number] == nil {
				continue
			}
			k, pr := known[b.PR.Number], *b.PR
			if k.State == "draft" {
				pr.State = "draft"
			}
			pr.Checks, pr.Review = k.Checks, k.Review
			b.PR = &pr
		}
	}
	if p.stacks == nil {
		p.stacks = map[string]*ghStackRow{}
	}
	p.stacks[r.Slug] = r
}

// switchLabel starts the label of a job checking out a stack branch.
const switchLabel = "stack switch "

// switching is the branch a running stack switch of worktree slug checks
// out, "" for none. s.mu is held.
func (s *Server) switching(prefix, slug string) string {
	for _, j := range s.c.Jobs.Jobs(prefix, slug) {
		if b, ok := strings.CutPrefix(j.Spec.Label, switchLabel); ok && j.State == jobs.StateRunning {
			return b
		}
	}
	return ""
}

// stackBranch reports whether branch is an open branch of worktree
// slug's stack, other than the current one, as last read. s.mu is not held.
func (s *Server) stackBranch(prefix, slug, branch string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.state[prefix]
	if p == nil || p.stacks[slug] == nil || p.stacks[slug].Stack == nil {
		return false
	}
	return slices.ContainsFunc(p.stacks[slug].Stack.Branches, func(b ghStackBranch) bool { return b.Name == branch && !b.Merged && !b.Current })
}

// switched marks branch current in worktree slug's stack, as a finished
// switch left it, until the stacks are read again. s.mu is held.
func (p *projectState) switched(slug, branch string) {
	r := p.stacks[slug]
	if r == nil || r.Stack == nil {
		return
	}
	st := *r.Stack
	st.Current = branch
	st.Branches = slices.Clone(st.Branches)
	for i := range st.Branches {
		st.Branches[i].Current = st.Branches[i].Name == branch
	}
	p.stacks[slug] = &ghStackRow{Slug: r.Slug, Stack: &st}
}

// publish brings every page up to date. s.mu is held.
func (s *Server) publish() {
	for sub := range s.subs {
		s.push(sub)
	}
}

// push sends sub the fragments that changed since it last got them: a
// row on its own while the list's order, groups and selection stay, else
// the whole list. s.mu is held.
func (s *Server) push(sub *subscriber) {
	if sub.view == "office" {
		s.pushCyber(sub)
		return
	}
	shown := sub.prefix
	if sub.view == "new" {
		shown = "" // the header and switcher of no project
	}
	pv := s.pageData(shown, sub.sel, sub.sort, sub.days)
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
	}
	// A row that moves to another group (its state changed) moves too.
	for _, g := range pv.List.Groups {
		keys = append(keys, g.Name)
		for _, r := range g.Rows {
			keys = append(keys, r.Slug)
		}
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
	}{{"usage", pv.Usage}, {"detail", pv.Detail}, {"jobs", pv.Jobs}} {
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
	v := newJobs(snaps, rows, sel)
	v.Fold = sub.view == ""
	return v
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
		rows = append(rows, row{WT: wt, PR: p.prs[wt.Slug], Stacked: p.stacks[wt.Slug], Now: s.c.Now(), DefaultAgent: p.ov.DefaultAgent})
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
func (s *Server) pageData(prefix, sel string, k sortKey, days int) pageView {
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
		if p.ov != nil {
			if p.ov.ReviewPending {
				banner.Review = reviewURL(prefix)
			}
			for _, sw := range p.ov.SignerWarnings {
				banner.Signers = append(banner.Signers, signerWarningText(sw))
			}
			for _, name := range p.ov.DefaultsChanged {
				banner.Defaults = append(banner.Defaults, bannerDefault{Name: name, URL: "/p/" + prefix + "/images/edit/" + name})
			}
			if p.ov.TemplateUpdate && p.ov.Template != nil {
				banner.Template = &bannerTemplate{Ref: p.ov.Template.Ref, URL: settingsURL(prefix) + "#template"}
			}
		}
	}
	pv := pageView{
		Title: title(need), Prefix: prefix, Sel: sel, Sort: k.String(), Days: days,
		Header: headerView{Prefix: prefix, Need: need, Terms: s.ttyOpen(), TermsMax: ttyMax}, Projects: projects, Strip: strip, Banner: banner,
		List:   newList(prefix, rows, sel, k),
		Detail: detailView{Empty: true},
		Jobs:   newJobs(s.c.Jobs.Jobs(prefix, ""), rows, sel),
	}
	pv.Jobs.Fold = true
	var usage *usageDoc
	if p := s.state[prefix]; p != nil {
		usage = p.usage
	}
	wtURL := func(slug string) string { return "/p/" + prefix + "/" + slug + pageQuery(k, days) }
	pv.Usage = newUsage(usage, days, usageLinks("/p/"+prefix+"/"+sel, k, days), wtURL)
	for _, r := range rows {
		if r.WT.Slug == sel {
			pv.Detail = newDetail(prefix, r, projectPRRepo(rows))
			pv.Detail.UsageCard = newUsageCard(usage, sel, days, usageLinks("/p/"+prefix+"/"+sel, k, days))
			pv.Detail.Switching = s.switching(prefix, sel)
		}
	}
	return pv
}
