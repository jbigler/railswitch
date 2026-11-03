// Package agentusage keeps the agents' token (and cost) history (D162):
// per project one JSON file, <data>/usage/<prefix>.json, that sums the
// growth of each agent's session counter (agentstatus.Usage) per local day,
// worktree and agent. The counters come from status files a container
// writes, so they are hostile: agentstatus bounds each one, and every sum
// here saturates.
package agentusage

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/jbigler/railswitch/internal/agentstatus"
	"github.com/jbigler/railswitch/internal/tomlfile"
)

// Schema is the file's schema; a higher one is refused (D39).
const Schema = 1

// Keep is how many days, today included, the file keeps.
const Keep = 90

// maxCount caps each kept sum: a container resetting its counter on every
// poll can't overflow a total.
const maxCount = 1e15

// Runaway: a worktree whose total today is over runawayFactor times the
// median of its days with usage among the runawayWindow days before, with
// at least runawayMinDays such days, and at least runawayFloor tokens.
const (
	runawayWindow  = 14
	runawayMinDays = 3
	runawayFactor  = 3
	runawayFloor   = 1_000_000
)

// Tokens are counts of tokens, and the cost in US dollars when the agent's
// package reports one (nil: none did).
type Tokens struct {
	Input      int64    `json:"input"`
	Output     int64    `json:"output"`
	CacheRead  int64    `json:"cache_read"`
	CacheWrite int64    `json:"cache_write"`
	Cost       *float64 `json:"cost"`
}

// Total is the four counts summed.
func (t Tokens) Total() int64 {
	return sat(sat(t.Input, t.Output), sat(t.CacheRead, t.CacheWrite))
}

func sat(a, b int64) int64 { return min(a+b, maxCount) } // both are in [0, maxCount]

func (t *Tokens) add(u Tokens) {
	t.Input, t.Output = sat(t.Input, u.Input), sat(t.Output, u.Output)
	t.CacheRead, t.CacheWrite = sat(t.CacheRead, u.CacheRead), sat(t.CacheWrite, u.CacheWrite)
	if u.Cost != nil {
		c := min(*u.Cost, maxCount)
		if t.Cost != nil {
			c = min(*t.Cost+c, maxCount)
		}
		t.Cost = &c
	}
}

func (t Tokens) zero() bool { return t.Total() == 0 && (t.Cost == nil || *t.Cost == 0) }

func fromStatus(u agentstatus.Usage) Tokens {
	return Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Cost: u.Cost}
}

// Row is one day's usage of one agent in one worktree.
type Row struct {
	Day      string `json:"day"` // YYYY-MM-DD, local time
	Worktree string `json:"worktree"`
	Agent    string `json:"agent"`
	Tokens
}

// last is an agent's session counter as last seen, its status's ts, and
// when it was recorded (Unix seconds).
type last struct {
	agentstatus.Usage
	TS   int64 `json:"ts"`
	Seen int64 `json:"seen"`
}

// file is the JSON document on disk.
type file struct {
	Schema int             `json:"schema"`
	Last   map[string]last `json:"last"` // by "<worktree>/<agent>"
	Days   []Row           `json:"days"`
}

// Sample is an agent's status as read now: its session's counter and the
// status's ts.
type Sample struct {
	Worktree, Agent string
	Usage           agentstatus.Usage
	TS              int64
}

// Path is the usage file of project prefix under the data dir.
func Path(data, prefix string) string {
	return filepath.Join(data, "usage", prefix+".json")
}

func load(path string) (file, error) {
	f := file{Schema: Schema, Last: map[string]last{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("%s: %w", path, err)
	}
	if f.Schema > Schema {
		return f, fmt.Errorf("%s: %w", path, &tomlfile.NewerSchemaError{Schema: f.Schema, Max: Schema})
	}
	if f.Last == nil {
		f.Last = map[string]last{}
	}
	return f, nil
}

// Record adds samples to the file at path, under its lock: each counter's
// growth since it was last seen goes to the local day of its status's ts.
func Record(path string, samples []Sample, now time.Time) error {
	if len(samples) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lk, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer lk.Close()
	for {
		if err = syscall.Flock(int(lk.Fd()), syscall.LOCK_EX); err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return err
	}
	f, err := load(path)
	if err != nil {
		return err
	}
	if !f.apply(samples, now) {
		return nil
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return tomlfile.WriteAtomic(path, data, 0o600)
}

// apply records samples and drops what is older than Keep days; it reports
// whether f changed.
func (f *file) apply(samples []Sample, now time.Time) bool {
	changed := false
	for _, s := range samples {
		key := s.Worktree + "/" + s.Agent
		prev, seen := f.Last[key]
		cur := fromStatus(s.Usage)
		delta := cur
		if seen && !lower(s.Usage, prev.Usage) {
			delta = Tokens{Input: cur.Input - prev.Input, Output: cur.Output - prev.Output,
				CacheRead: cur.CacheRead - prev.CacheRead, CacheWrite: cur.CacheWrite - prev.CacheWrite}
			if cur.Cost != nil {
				c := *cur.Cost
				if prev.Cost != nil {
					c -= *prev.Cost
				}
				delta.Cost = &c
			}
		}
		// else: first seen, or a count dropped: a new session, all of it new.
		// shortcut: a new session that already passed the old one's counts
		// reads as growth and loses the old one's unseen tail; a session id
		// in the status file would fix it.
		if !seen || !same(prev.Usage, s.Usage) || prev.TS != s.TS {
			f.Last[key] = last{Usage: s.Usage, TS: s.TS, Seen: now.Unix()}
			changed = true
		}
		if delta.zero() {
			continue
		}
		f.add(Row{Day: day(s.TS, now), Worktree: s.Worktree, Agent: s.Agent, Tokens: delta})
		changed = true
	}
	oldest := now.AddDate(0, 0, 1-Keep).Format(time.DateOnly)
	if n := len(f.Days); n > 0 {
		f.Days = slices.DeleteFunc(f.Days, func(r Row) bool { return r.Day < oldest })
		changed = changed || len(f.Days) != n
	}
	for k, l := range f.Last {
		if l.Seen < now.AddDate(0, 0, -Keep).Unix() {
			delete(f.Last, k)
			changed = true
		}
	}
	return changed
}

// lower reports a count (or the cost) of cur below prev's: a new session.
func lower(cur, prev agentstatus.Usage) bool {
	if cur.Input < prev.Input || cur.Output < prev.Output || cur.CacheRead < prev.CacheRead || cur.CacheWrite < prev.CacheWrite {
		return true
	}
	return prev.Cost != nil && (cur.Cost == nil || *cur.Cost < *prev.Cost)
}

func same(a, b agentstatus.Usage) bool {
	ca, cb := a.Cost, b.Cost
	a.Cost, b.Cost = nil, nil
	return a == b && (ca == nil) == (cb == nil) && (ca == nil || *ca == *cb)
}

// day is ts's local day; a ts not in the kept window (0, a clock in the
// future, a hostile value) is now's.
func day(ts int64, now time.Time) string {
	if ts <= 0 || ts > now.Unix()+3600 || ts < now.AddDate(0, 0, -Keep).Unix() {
		return now.Format(time.DateOnly)
	}
	return time.Unix(ts, 0).In(now.Location()).Format(time.DateOnly)
}

func (f *file) add(r Row) {
	for i := range f.Days {
		if d := &f.Days[i]; d.Day == r.Day && d.Worktree == r.Worktree && d.Agent == r.Agent {
			d.add(r.Tokens)
			return
		}
	}
	var t Tokens
	t.add(r.Tokens)
	r.Tokens = t
	f.Days = append(f.Days, r)
}

// Report is `agent usage --json` (docs/json.md).
type Report struct {
	Today string `json:"today"`
	Days  int    `json:"days"`
	// Daily is every day of the window, oldest first, summed over its
	// worktrees and agents.
	Daily []DayTotal `json:"daily"`
	// Rows are the window's days with usage, per worktree and agent:
	// oldest first, then by worktree and agent.
	Rows []Row `json:"rows"`
	// Worktrees are the totals of the window per worktree with usage in
	// it, by worktree.
	Worktrees []WorktreeTotal `json:"worktrees"`
}

// DayTotal is one day's usage.
type DayTotal struct {
	Day   string `json:"day"`
	Total int64  `json:"total"`
	Tokens
}

// WorktreeTotal is one worktree's usage in the window. Today is its total
// today, Median the median total of its days with usage among the 14
// before; Runaway flags today far above it.
type WorktreeTotal struct {
	Worktree string `json:"worktree"`
	Total    int64  `json:"total"`
	Tokens
	Today   int64 `json:"today"`
	Median  int64 `json:"median"`
	Runaway bool  `json:"runaway"`
}

// Read reports the file at path: the last days days, today included, of
// worktree ("": every worktree).
func Read(path string, days int, worktree string, now time.Time) (Report, error) {
	f, err := load(path)
	if err != nil {
		return Report{}, err
	}
	return f.report(days, worktree, now), nil
}

func (f *file) report(days int, worktree string, now time.Time) Report {
	today := now.Format(time.DateOnly)
	rep := Report{Today: today, Days: days, Daily: []DayTotal{}, Rows: []Row{}, Worktrees: []WorktreeTotal{}}
	at := map[string]int{}
	for i := days - 1; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format(time.DateOnly)
		at[d] = len(rep.Daily)
		rep.Daily = append(rep.Daily, DayTotal{Day: d})
	}
	daily := map[string]map[string]int64{} // worktree → day → total
	byWT := map[string]*WorktreeTotal{}
	for _, r := range f.Days {
		if worktree != "" && r.Worktree != worktree {
			continue
		}
		if daily[r.Worktree] == nil {
			daily[r.Worktree] = map[string]int64{}
		}
		daily[r.Worktree][r.Day] = sat(daily[r.Worktree][r.Day], r.Total())
		i, ok := at[r.Day]
		if !ok {
			continue
		}
		rep.Rows = append(rep.Rows, r)
		rep.Daily[i].add(r.Tokens)
		w := byWT[r.Worktree]
		if w == nil {
			w = &WorktreeTotal{Worktree: r.Worktree}
			byWT[r.Worktree] = w
		}
		w.add(r.Tokens)
	}
	for i := range rep.Daily {
		rep.Daily[i].Total = rep.Daily[i].Tokens.Total()
	}
	slices.SortFunc(rep.Rows, func(a, b Row) int {
		return cmp.Or(cmp.Compare(a.Day, b.Day), cmp.Compare(a.Worktree, b.Worktree), cmp.Compare(a.Agent, b.Agent))
	})
	for name, w := range byWT {
		w.Total = w.Tokens.Total()
		w.Today = daily[name][today]
		var prior []int64
		for i := 1; i <= runawayWindow; i++ {
			if n := daily[name][now.AddDate(0, 0, -i).Format(time.DateOnly)]; n > 0 {
				prior = append(prior, n)
			}
		}
		w.Median = median(prior)
		w.Runaway = len(prior) >= runawayMinDays && w.Today >= runawayFloor && w.Today > runawayFactor*w.Median
		rep.Worktrees = append(rep.Worktrees, *w)
	}
	slices.SortFunc(rep.Worktrees, func(a, b WorktreeTotal) int { return cmp.Compare(a.Worktree, b.Worktree) })
	return rep
}

func median(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	slices.Sort(v)
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return v[n/2-1]/2 + v[n/2]/2
}
