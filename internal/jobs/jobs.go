// Package jobs runs the dashboard's actions as jobs: one child process
// each, in its own systemd scope, its output in a log file and kept
// (capped) for the page, with at most one job per worktree.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

// Output and retention caps.
const (
	KeepLines = 5000    // lines kept per job
	KeepBytes = 4 << 20 // bytes kept per job
	MaxLine   = 64 << 10
	LogLimit  = 8 << 20 // a log past this is truncated
	KeepJobs  = 20
	Grace     = 3 * time.Second // SIGTERM to SIGKILL on cancel
)

// scope wraps every job, so a dashboard restart doesn't kill it.
var scope = []string{"systemd-run", "--user", "--scope", "--collect", "--quiet", "--"}

// publishEvery is the shortest gap between two output events of a job.
const publishEvery = 100 * time.Millisecond

// pollEvery is how often an idle tail looks at the log again.
const pollEvery = 25 * time.Millisecond

var (
	// ErrBusy is wrapped, with the running job's label, when a job of the
	// same worktree or project is running.
	ErrBusy = errors.New("busy")
	// ErrNotFound is returned for an unknown job id.
	ErrNotFound = errors.New("no such job")
)

// Spec says what to run.
type Spec struct {
	Project string   // prefix
	Slug    string   // "" = project-level job (blocks the whole project)
	Label   string   // shown in the page, e.g. "up", "task test"
	Argv    []string // full argv; argv[0] is the program
	Dir     string
}

// State is a job's state.
type State string

// Job states.
const (
	StateRunning   State = "running"
	StateOK        State = "ok"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

// Snapshot is a job at one moment. Lines is the caller's own copy.
type Snapshot struct {
	ID      string
	Spec    Spec
	State   State
	Exit    int
	Started time.Time
	Ended   time.Time
	Lines   []string // kept output, oldest first
	Dropped bool     // output was dropped by a cap
}

// Event is published on start, output (at most every 100 ms) and end.
type Event struct{ Job Snapshot }

type job struct {
	snap      Snapshot // guarded by Hub.mu, as are the fields below
	kept      int      // bytes in snap.Lines
	log       string
	cancel    context.CancelFunc
	cancelled bool
}

// Hub runs jobs and keeps the last KeepJobs of them.
type Hub struct {
	r   sys.Runner
	dir string
	env func(context.Context) []string

	mu   sync.Mutex
	jobs []*job // oldest first
	seq  int
	subs map[*subscriber]struct{}
}

// New returns a hub writing job logs to logDir, which it creates 0700 and
// clears of logs from a previous run. env gives the child's full
// environment at each start; NO_COLOR=1 is added.
func New(r sys.Runner, logDir string, env func(context.Context) []string) (*Hub, error) {
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, fmt.Errorf("jobs: %w", err)
	}
	if err := os.Chmod(logDir, 0o700); err != nil {
		return nil, fmt.Errorf("jobs: %w", err)
	}
	old, err := filepath.Glob(filepath.Join(logDir, "*.log"))
	if err != nil {
		return nil, fmt.Errorf("jobs: %w", err)
	}
	for _, p := range old {
		if err := os.Remove(p); err != nil {
			return nil, fmt.Errorf("jobs: %w", err)
		}
	}
	return &Hub{r: r, dir: logDir, env: env, subs: map[*subscriber]struct{}{}}, nil
}

// Start runs s as a new job. It fails with ErrBusy while a job of the same
// worktree runs, or of the same project when either job is project-level.
// The job outlives ctx; only Cancel ends it early.
func (h *Hub) Start(ctx context.Context, s Spec) (Snapshot, error) {
	if len(s.Argv) == 0 {
		return Snapshot{}, errors.New("jobs: empty argv")
	}
	s.Argv = slices.Clone(s.Argv)
	env := append(slices.Clone(h.env(ctx)), "NO_COLOR=1")

	h.mu.Lock()
	defer h.mu.Unlock()
	for _, j := range h.jobs {
		o := j.snap.Spec
		if j.snap.State == StateRunning && o.Project == s.Project && (o.Slug == "" || s.Slug == "" || o.Slug == s.Slug) {
			return Snapshot{}, fmt.Errorf("%w: %s", ErrBusy, o.Label)
		}
	}
	h.seq++
	started := time.Now()
	id := started.UTC().Format("20060102-150405") + "-" + strconv.Itoa(h.seq)
	path := filepath.Join(h.dir, id+".log")
	t, err := openLog(path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("jobs: %w", err)
	}
	jctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	j := &job{
		snap:   Snapshot{ID: id, Spec: s, State: StateRunning, Started: started},
		log:    path,
		cancel: cancel,
	}
	h.jobs = append(h.jobs, j)
	h.prune()
	h.publish(j)
	go h.run(jctx, j, t, env)
	return j.snapshot(), nil
}

// Cancel ends a running job: SIGTERM to its process group, SIGKILL after
// Grace. A finished job is left as it is.
func (h *Hub) Cancel(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, j := range h.jobs {
		if j.snap.ID != id {
			continue
		}
		if j.snap.State == StateRunning {
			j.cancelled = true
			j.cancel()
		}
		return nil
	}
	return fmt.Errorf("jobs: %w: %s", ErrNotFound, id)
}

// Jobs returns the kept jobs of project, newest first; slug "" means all
// of the project's jobs.
func (h *Hub) Jobs(project, slug string) []Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Snapshot
	for _, j := range slices.Backward(h.jobs) {
		if j.snap.Spec.Project == project && (slug == "" || j.snap.Spec.Slug == slug) {
			out = append(out, j.snapshot())
		}
	}
	return out
}

// Running reports whether any job is running.
func (h *Hub) Running() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.ContainsFunc(h.jobs, func(j *job) bool { return j.snap.State == StateRunning })
}

// Subscribe returns a channel of job events and a function that ends the
// subscription and closes the channel. A slow reader never blocks a job:
// it gets the latest state of each job when it reads again.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	s := &subscriber{
		pending: map[string]Event{},
		wake:    make(chan struct{}, 1),
		out:     make(chan Event),
		done:    make(chan struct{}),
	}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	go s.loop()
	var once sync.Once
	return s.out, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, s)
			h.mu.Unlock()
			close(s.done)
		})
	}
}

// run runs the job's child and records how it ended.
func (h *Hub) run(ctx context.Context, j *job, t *logTail, env []string) {
	defer j.cancel()
	exited := make(chan struct{})
	followed := make(chan struct{})
	go func() {
		defer close(followed)
		h.follow(j, t, exited)
	}()
	s := j.snap.Spec // immutable after Start
	res, err := h.r.Run(ctx, sys.Cmd{
		Name:      scope[0],
		Args:      append(slices.Clone(scope[1:]), s.Argv...),
		Dir:       s.Dir,
		Env:       env,
		Stdin:     nil, // /dev/null
		Stdout:    t.w, // the same *os.File for both: written directly,
		Stderr:    t.w, // no pipe through the dashboard
		Timeout:   sys.NoTimeout,
		KillGrace: Grace,
	})
	close(exited)
	<-followed
	t.Close()

	h.mu.Lock()
	defer h.mu.Unlock()
	j.snap.Ended = time.Now()
	j.snap.Exit = res.ExitCode
	switch {
	case j.cancelled:
		j.snap.State = StateCancelled
	case err == nil:
		j.snap.State = StateOK
	case errors.Is(err, sys.ErrExit):
		j.snap.State = StateFailed
	default: // it never ran (or its log broke)
		j.snap.State = StateFailed
		if j.snap.Exit == 0 {
			j.snap.Exit = -1
		}
		h.addLines(j, []string{"railswitch: " + err.Error()})
	}
	h.prune()
	h.publish(j)
}

// follow turns the job's log into kept lines until the child has exited
// and the log is read to its end.
func (h *Hub) follow(j *job, t *logTail, exited <-chan struct{}) {
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	var last time.Time
	dirty, finishing := false, false
	for {
		lines, dropped, read, err := t.poll()
		if finishing && !read && err == nil {
			lines = t.flush(lines)
		}
		if len(lines) > 0 || dropped {
			h.mu.Lock()
			h.addLines(j, lines)
			if dropped {
				j.snap.Dropped = true
			}
			dirty = true
			h.mu.Unlock()
		}
		if err != nil {
			// Unread, the log would grow without bound: end the job.
			h.mu.Lock()
			h.addLines(j, []string{"railswitch: job log: " + err.Error()})
			h.mu.Unlock()
			j.cancel()
			return
		}
		if dirty && time.Since(last) >= publishEvery {
			h.mu.Lock()
			h.publish(j)
			h.mu.Unlock()
			last, dirty = time.Now(), false
		}
		if read {
			continue
		}
		if finishing {
			return // run publishes the end
		}
		select {
		case <-exited:
			finishing = true
		case <-t.wake:
		case <-tick.C:
		}
	}
}

// addLines keeps lines within KeepLines and KeepBytes, dropping the
// oldest. h.mu is held.
func (h *Hub) addLines(j *job, lines []string) {
	for _, l := range lines {
		j.snap.Lines = append(j.snap.Lines, l)
		j.kept += len(l)
	}
	for len(j.snap.Lines) > KeepLines || j.kept > KeepBytes {
		j.kept -= len(j.snap.Lines[0])
		j.snap.Lines[0] = "" // release it before the array is reallocated
		j.snap.Lines = j.snap.Lines[1:]
		j.snap.Dropped = true
	}
}

// prune keeps at most KeepJobs jobs, dropping the oldest finished ones and
// their logs. h.mu is held.
func (h *Hub) prune() {
	for len(h.jobs) > KeepJobs {
		i := slices.IndexFunc(h.jobs, func(j *job) bool { return j.snap.State != StateRunning })
		if i < 0 {
			return
		}
		_ = os.Remove(h.jobs[i].log)
		h.jobs = slices.Delete(h.jobs, i, i+1)
	}
}

// publish offers j's state to every subscriber. h.mu is held, so events
// of one job are offered in order.
func (h *Hub) publish(j *job) {
	if len(h.subs) == 0 {
		return
	}
	ev := Event{Job: j.snapshot()}
	for s := range h.subs {
		s.offer(ev)
	}
}

// snapshot copies j's state. h.mu is held.
func (j *job) snapshot() Snapshot {
	s := j.snap
	s.Spec.Argv = slices.Clone(s.Spec.Argv)
	s.Lines = slices.Clone(s.Lines)
	return s
}

// subscriber coalesces events per job, so offering never blocks.
type subscriber struct {
	mu      sync.Mutex
	pending map[string]Event // latest undelivered event per job id
	order   []string         // ids in pending, first offered first
	wake    chan struct{}
	out     chan Event
	done    chan struct{}
}

func (s *subscriber) offer(ev Event) {
	s.mu.Lock()
	if _, ok := s.pending[ev.Job.ID]; !ok {
		s.order = append(s.order, ev.Job.ID)
	}
	s.pending[ev.Job.ID] = ev
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *subscriber) loop() {
	defer close(s.out)
	for {
		select {
		case <-s.done:
			return
		case <-s.wake:
		}
		for {
			s.mu.Lock()
			if len(s.order) == 0 {
				s.mu.Unlock()
				break
			}
			id := s.order[0]
			s.order = s.order[1:]
			ev := s.pending[id]
			delete(s.pending, id)
			s.mu.Unlock()
			// The event is shared by every subscriber; each reader gets
			// its own Lines.
			ev.Job.Lines = slices.Clone(ev.Job.Lines)
			ev.Job.Spec.Argv = slices.Clone(ev.Job.Spec.Argv)
			select {
			case s.out <- ev:
			case <-s.done:
				return
			}
		}
	}
}
