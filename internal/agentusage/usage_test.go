package agentusage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/agentstatus"
	"github.com/jbigler/railswitch/internal/tomlfile"
)

// zone is UTC-5: its days start at 05:00 UTC, so a day boundary in local
// time is not UTC's.
var zone = time.FixedZone("test", -5*3600)

func at(day string, hour int) time.Time {
	t, err := time.ParseInLocation(time.DateOnly, day, zone)
	if err != nil {
		panic(err)
	}
	return t.Add(time.Duration(hour) * time.Hour)
}

func sample(wt, agent string, in, out int64, ts time.Time) Sample {
	return Sample{Worktree: wt, Agent: agent, Usage: agentstatus.Usage{Input: in, Output: out}, TS: ts.Unix()}
}

func rowsOf(f *file) map[string]int64 {
	m := map[string]int64{}
	for _, r := range f.Days {
		m[r.Day+" "+r.Worktree+" "+r.Agent] = r.Total()
	}
	return m
}

func TestApplyAccumulates(t *testing.T) {
	f := file{Last: map[string]last{}}
	now := at("2026-11-04", 10)
	f.apply([]Sample{sample("a", "x", 100, 10, now), sample("a", "y", 5, 0, now), sample("b", "x", 7, 0, now)}, now)
	// Growth adds the difference; the same counter again adds nothing.
	f.apply([]Sample{sample("a", "x", 150, 20, now.Add(time.Minute)), sample("a", "y", 5, 0, now)}, now.Add(time.Minute))
	f.apply([]Sample{sample("a", "x", 150, 20, now.Add(time.Minute))}, now.Add(2*time.Minute))
	// A drop is a new session: all of its counter is new.
	f.apply([]Sample{sample("a", "x", 30, 0, now.Add(time.Hour))}, now.Add(time.Hour))
	want := map[string]int64{"2026-11-04 a x": 170 + 30, "2026-11-04 a y": 5, "2026-11-04 b x": 7}
	if got := rowsOf(&f); !equal(got, want) {
		t.Errorf("rows %v; want %v", got, want)
	}
}

func equal(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// The day is the status's ts in local time: 23:00 and 01:00 local are two
// days though both are the same UTC day.
func TestApplyLocalDays(t *testing.T) {
	f := file{Last: map[string]last{}}
	late := at("2026-11-03", 23)
	early := at("2026-11-04", 1)
	if late.UTC().Format(time.DateOnly) != early.UTC().Format(time.DateOnly) {
		t.Fatal("test zone: both should be one UTC day")
	}
	f.apply([]Sample{sample("a", "x", 100, 0, late)}, late)
	f.apply([]Sample{sample("a", "x", 160, 0, early)}, early)
	// A ts in the future or 0 is now's day.
	f.apply([]Sample{sample("a", "x", 170, 0, early.Add(48*time.Hour))}, early)
	f.apply([]Sample{{Worktree: "a", Agent: "x", Usage: agentstatus.Usage{Input: 175}}}, early)
	want := map[string]int64{"2026-11-03 a x": 100, "2026-11-04 a x": 60 + 10 + 5}
	if got := rowsOf(&f); !equal(got, want) {
		t.Errorf("rows %v; want %v", got, want)
	}
}

func TestApplyCost(t *testing.T) {
	c := func(v float64) *float64 { return &v }
	f := file{Last: map[string]last{}}
	now := at("2026-11-04", 10)
	f.apply([]Sample{{Worktree: "a", Agent: "x", Usage: agentstatus.Usage{Input: 1, Cost: c(0.5)}, TS: now.Unix()}}, now)
	f.apply([]Sample{{Worktree: "a", Agent: "x", Usage: agentstatus.Usage{Input: 2, Cost: c(1.25)}, TS: now.Unix()}}, now)
	f.apply([]Sample{{Worktree: "a", Agent: "x", Usage: agentstatus.Usage{Input: 3, Cost: c(0.25)}, TS: now.Unix()}}, now) // cost dropped: new session
	f.apply([]Sample{sample("b", "x", 9, 0, now)}, now)
	rep := f.report(1, "", now)
	if len(rep.Worktrees) != 2 || rep.Worktrees[0].Cost == nil || *rep.Worktrees[0].Cost != 1.5 || rep.Worktrees[0].Total != 5 {
		t.Errorf("a: %+v", rep.Worktrees)
	}
	if rep.Worktrees[1].Cost != nil {
		t.Errorf("b reported no cost: %+v", rep.Worktrees[1])
	}
	if d := rep.Daily[0]; d.Cost == nil || *d.Cost != 1.5 || d.Total != 14 {
		t.Errorf("daily %+v", d)
	}
}

// A hostile counter can't overflow: every sum saturates.
func TestApplySaturates(t *testing.T) {
	f := file{Last: map[string]last{}}
	now := at("2026-11-04", 10)
	for i := range 400 {
		f.apply([]Sample{sample("a", "x", int64(i%2)*1e13, 0, now)}, now)
	}
	if r := f.report(1, "", now); r.Daily[0].Total != maxCount {
		t.Errorf("total %d; want %d", r.Daily[0].Total, int64(maxCount))
	}
}

func TestApplyPrunes(t *testing.T) {
	f := file{Last: map[string]last{}}
	old := at("2026-07-01", 10)
	f.apply([]Sample{sample("gone", "x", 5, 0, old)}, old)
	now := at("2026-11-04", 10)
	if !f.apply(nil, now) || len(f.Days) != 0 || len(f.Last) != 0 {
		t.Errorf("kept %+v %+v", f.Days, f.Last)
	}
}

func TestReportRunaway(t *testing.T) {
	f := file{Last: map[string]last{}}
	now := at("2026-11-04", 12)
	add := func(wt string, daysAgo int, n int64) {
		f.add(Row{Day: now.AddDate(0, 0, -daysAgo).Format(time.DateOnly), Worktree: wt, Agent: "x", Tokens: Tokens{Input: n}})
	}
	for _, d := range []int{1, 2, 5, 9} {
		add("busy", d, 2_000_000)
		add("calm", d, 2_000_000)
	}
	add("busy", 0, 7_000_000)
	add("calm", 0, 5_000_000)
	add("new", 0, 50_000_000) // no history: never runaway
	add("small", 1, 10)
	add("small", 2, 10)
	add("small", 3, 10)
	add("small", 0, 900_000) // under the floor
	rep := f.report(7, "", now)
	got := map[string]bool{}
	for _, w := range rep.Worktrees {
		got[w.Worktree] = w.Runaway
	}
	if !got["busy"] || got["calm"] || got["new"] || got["small"] {
		t.Errorf("runaway %v", got)
	}
	if len(rep.Daily) != 7 || rep.Daily[6].Day != "2026-11-04" || rep.Daily[0].Day != "2026-10-29" {
		t.Errorf("daily %+v", rep.Daily)
	}
	// Day 9 is outside the window's totals but counts for the median.
	if rep.Worktrees[0].Worktree != "busy" || rep.Worktrees[0].Total != 3*2_000_000+7_000_000 || rep.Worktrees[0].Median != 2_000_000 {
		t.Errorf("busy %+v", rep.Worktrees[0])
	}
	f.add(Row{Day: now.AddDate(0, 0, -3).Format(time.DateOnly), Worktree: "reads", Agent: "x", Tokens: Tokens{Input: 10, CacheRead: 1000}})
	f.add(Row{Day: now.AddDate(0, 0, -2).Format(time.DateOnly), Worktree: "reads", Agent: "x", Tokens: Tokens{Output: 30, CacheWrite: 20, CacheRead: 1000}})
	for _, w := range f.report(7, "", now).Worktrees {
		switch w.Worktree {
		case "busy":
			if w.PriorDays != 4 || w.MedianNewOutput != 2_000_000 {
				t.Errorf("busy usual day %+v", w)
			}
		case "reads": // cache reads count for the total, not the new tokens and output
			if w.PriorDays != 2 || w.Median != 1030 || w.MedianNewOutput != 30 {
				t.Errorf("reads usual day %+v", w)
			}
		}
	}
	if one := f.report(7, "calm", now); len(one.Worktrees) != 1 || one.Worktrees[0].Worktree != "calm" {
		t.Errorf("one worktree %+v", one.Worktrees)
	}
}

func TestRecordFile(t *testing.T) {
	path := Path(t.TempDir(), "proj")
	now := at("2026-11-04", 10)
	if err := Record(path, []Sample{sample("a", "x", 10, 1, now)}, now); err != nil {
		t.Fatal(err)
	}
	if err := Record(path, []Sample{sample("a", "x", 20, 1, now)}, now); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("stat %v %v", st, err)
	}
	if d, _ := os.Stat(filepath.Dir(path)); d.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", d.Mode())
	}
	rep, err := Read(path, 3, "", now)
	if err != nil || len(rep.Rows) != 1 || rep.Rows[0].Total() != 21 || rep.Today != "2026-11-04" {
		t.Fatalf("report %+v %v", rep, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), `{"schema":1,`) {
		t.Errorf("file %s", data)
	}
	if err := os.WriteFile(path, []byte(`{"schema":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var newer *tomlfile.NewerSchemaError
	if _, err := Read(path, 3, "", now); !errors.As(err, &newer) {
		t.Errorf("schema 2: %v", err)
	}
	if err := Record(path, []Sample{sample("a", "x", 30, 1, now)}, now); !errors.As(err, &newer) {
		t.Errorf("record over schema 2: %v", err)
	}
	if rep, err := Read(filepath.Join(t.TempDir(), "none.json"), 2, "", now); err != nil || len(rep.Daily) != 2 || len(rep.Rows) != 0 {
		t.Errorf("missing file: %+v %v", rep, err)
	}
}

// An idle agent's status file reads the same for months: its counter is
// not forgotten and counted again once Keep days pass.
func TestApplyKeepsIdleAgent(t *testing.T) {
	f := file{Last: map[string]last{}}
	old := at("2026-07-01", 10)
	s := sample("idle", "x", 5, 0, old)
	f.apply([]Sample{s}, old)
	now := at("2026-11-04", 10)
	f.apply([]Sample{s}, now)
	f.apply([]Sample{s}, now.Add(time.Minute))
	if got := rowsOf(&f); len(got) != 0 {
		t.Errorf("counted again: %v", got)
	}
}

// D187: two sessions writing one status file in turn (two terminals on one
// worktree's agent) each count once: a session's counter is compared with
// its own last value, not the other session's.
func TestApplySessionsAlternate(t *testing.T) {
	f := file{Last: map[string]last{}}
	now := at("2026-11-04", 10)
	s := func(id string, in int64, min int) Sample {
		ts := now.Add(time.Duration(min) * time.Minute)
		return Sample{Worktree: "a", Agent: "x", Usage: agentstatus.Usage{Input: in, Session: id}, TS: ts.Unix()}
	}
	for i, smp := range []Sample{s("A", 100, 0), s("B", 50, 1), s("A", 120, 2), s("B", 60, 3), s("A", 130, 4)} {
		f.apply([]Sample{smp}, now.Add(time.Duration(i)*time.Minute))
	}
	if got, want := rowsOf(&f), map[string]int64{"2026-11-04 a x": 130 + 60}; !equal(got, want) {
		t.Errorf("rows %v; want %v", got, want)
	}
}

// D187: a removed worktree's rows keep their usage under a new name; a
// new worktree of the old name starts at zero, its counter new.
func TestRetire(t *testing.T) {
	path := Path(t.TempDir(), "proj")
	if err := Retire(path, "a", at("2026-11-04", 9)); err != nil {
		t.Fatalf("no file yet: %v", err)
	}
	day1, now := at("2026-11-03", 10), at("2026-11-04", 10)
	s := func(in int64, ts time.Time) Sample {
		return Sample{Worktree: "a", Agent: "x", Usage: agentstatus.Usage{Input: in, Session: "S"}, TS: ts.Unix()}
	}
	for _, r := range []struct {
		smp Sample
		at  time.Time
	}{{s(100, day1), day1}, {s(150, now), now}, {sample("b", "x", 7, 0, now), now}} {
		if err := Record(path, []Sample{r.smp}, r.at); err != nil {
			t.Fatal(err)
		}
	}
	if err := Retire(path, "a", now); err != nil {
		t.Fatal(err)
	}
	// The same session's status again (a new worktree named a reusing the
	// status file's last value) counts as new, under a.
	if err := Record(path, []Sample{s(150, now)}, now); err != nil {
		t.Fatal(err)
	}
	rep, err := Read(path, 2, "", now)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]WorktreeTotal{}
	for _, w := range rep.Worktrees {
		got[w.Worktree] = w
	}
	old := got["a (removed 2026-11-04)"]
	if !old.Removed || old.Total != 150 || old.Today != 50 || got["a"].Removed || got["a"].Total != 150 || got["b"].Total != 7 || len(got) != 3 {
		t.Errorf("worktrees %+v", rep.Worktrees)
	}
	for _, r := range rep.Rows {
		if r.Removed != strings.Contains(r.Worktree, "removed") {
			t.Errorf("row %+v", r)
		}
	}
}
