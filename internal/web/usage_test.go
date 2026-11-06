package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ftestUsage is `agent usage --all --days 30 --json` of ftest on the
// fixture's day, 2026-10-27: b on 5 days, today far above its usual
// day; main; w1 to w7 with less, so two of them fall into "others"; and
// a removed worktree.
func ftestUsage() string {
	end := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	doc := map[string]any{"today": "2026-10-27", "days": 30}
	var daily []map[string]any
	for i := 29; i >= 0; i-- {
		daily = append(daily, map[string]any{"day": end.AddDate(0, 0, -i).Format(time.DateOnly)})
	}
	var rows []map[string]any
	row := func(daysAgo int, wt string, in, out, read, write int64, removed bool) {
		r := map[string]any{"day": end.AddDate(0, 0, -daysAgo).Format(time.DateOnly), "worktree": wt, "agent": "bot",
			"input": in, "output": out, "cache_read": read, "cache_write": write, "cost": nil}
		if removed {
			r["removed"] = true
		}
		rows = append(rows, r)
	}
	for _, d := range []int{9, 6, 3, 1} {
		row(d, "b", 20_000, 150_000, 40_000_000, 600_000, false)
	}
	row(0, "b", 50_000, 900_000, 90_000_000, 2_800_000, false)
	row(2, "main", 10_000, 300_000, 9_000_000, 400_000, false)
	row(0, "main", 5_000, 100_000, 2_000_000, 80_000, false)
	for i := 1; i <= 7; i++ {
		row(i, fmt.Sprintf("w%d", i), 1_000, int64(100_000-i*10_000), 1_000_000, 10_000, false)
	}
	row(20, "w1", 1_000, 50_000, 1_000_000, 10_000, false) // outside 14 days, inside 30
	row(4, "gone (removed 2026-10-25)", 2_000, 200_000, 3_000_000, 30_000, true)
	doc["daily"], doc["rows"] = daily, rows
	doc["worktrees"] = []map[string]any{
		{"worktree": "b", "today": 93_750_000, "median": 40_770_000, "runaway": true, "removed": false, "prior_days": 4, "median_new_output": 770_000},
		{"worktree": "main", "today": 2_185_000, "median": 9_710_000, "runaway": false, "removed": false, "prior_days": 1, "median_new_output": 710_000},
		{"worktree": "gone (removed 2026-10-25)", "today": 0, "median": 0, "runaway": false, "removed": true, "prior_days": 1, "median_new_output": 232_000},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return string(b)
}

const usageCmd = "--json -C /ws/ftest agent usage --all --days 30"

// bRunning is b's agent running a session that began 2 h 10 min before
// the fixture's clock, its context 148k.
var bRunning = `{"name":"bot","package":"bot@1","run":"on-demand","state":"working","message":"","ts":1,"reply":false,"running":true,
 "usage":{"input":1,"output":1,"cache_read":0,"cache_write":0,"session":"s1","context":148000,"started":` + fmt.Sprint(time.Date(2026, 10, 27, 9, 50, 0, 0, time.UTC).Unix()) + `}}`

// D162, D187: the project chart, the worktree's card and the runaway
// banner come from agent usage --json, read with the PRs, for the
// page's range.
func TestUsageViews(t *testing.T) {
	f := newFixture(t)
	f.cli.set(usageCmd, ftestUsage())
	f.cli.set("--json -C /ws/ftest overview", ftestOverview("jb/b", "up", bRunning))
	sub := f.s.subscribe("ftest", "b", parseSort("name"), usageDefault, "")
	defer f.s.unsubscribe(sub)
	f.s.pollOnce(context.Background())
	evs := drain(sub)
	usage, ok := byID(evs, "usage")
	if !ok || strings.Contains(usage.HTML, "style") {
		t.Fatalf("usage %q", usage.HTML)
	}
	for _, want := range []string{
		"Agent usage · 14 days: ",
		// b links to its page; w5 to w7, past the first six, are others.
		`<a href="/p/ftest/b" data-usage-wt="b"><rect class="u-c0"`,
		`class="u-other"`, `title="w5 61k, w6 51k, w7 41k"`, "3 others", "Oct 22 · others: w5 61k",
		// the removed one is muted, never linked
		`<rect class="u-removed"`, `<li class="muted"><span class="swatch u-removed" aria-hidden="true"></span><span class="mono name">gone (removed 2026-10-25)</span>`,
		`href="/p/ftest/b?days=7" data-days="7"`, `data-days="14" class="on" aria-current="true"`,
		"Oct 14", "Oct 27",
	} {
		if !strings.Contains(usage.HTML, want) {
			t.Errorf("usage lacks %q:\n%s", want, usage.HTML)
		}
	}
	if strings.Contains(usage.HTML, `data-usage-wt="gone`) || strings.Contains(usage.HTML, "u-c6") {
		t.Errorf("usage links a removed worktree or colours a seventh: %s", usage.HTML)
	}
	d, _ := byID(evs, "detail")
	for _, want := range []string{
		`<h2 class="lbl">Usage · this worktree</h2>`,
		`<tr class="yellow" title="Over 3 times its usual day"><th scope="row">⚠ Today</th><td>2.9M</td><td>900k</td><td class="muted">90M</td><td class="muted">not reported</td></tr>`,
		`<th scope="row">14 days</th><td>5.3M</td><td>1.5M</td>`,
		`class="u-new runaway"`, `class="u-out runaway"`,
		`<line class="usual"`, "Usual day: 770k new and output",
		"<span>5M</span><span>2.5M</span><span>0</span>",
		"This session&#39;s context · 2 h 10 min", "<b>148k</b> of 200k", `<rect class="fill" width="74" height="8"/>`,
		"Share of the project, 14 days",
	} {
		if !strings.Contains(d.HTML, want) {
			t.Errorf("detail lacks %q:\n%s", want, d.HTML)
		}
	}
	if b, _ := byID(evs, "banner"); !strings.Contains(b.HTML, "Agent usage of b is high today: 93.8M tokens") {
		t.Errorf("banner %q", b.HTML)
	}

	// 30 days: w1's older day joins; main has no usual day yet.
	sub30 := f.s.subscribe("ftest", "main", parseSort("name"), 30, "")
	defer f.s.unsubscribe(sub30)
	evs = drain(sub30)
	usage, _ = byID(evs, "usage")
	d, _ = byID(evs, "detail")
	if !strings.Contains(usage.HTML, "Agent usage · 30 days: ") || !strings.Contains(usage.HTML, `data-days="30" class="on"`) || !strings.Contains(usage.HTML, "Sep 28") {
		t.Errorf("30 days usage %s", usage.HTML)
	}
	for _, want := range []string{"Usual-day line appears with 3 earlier days of use (1 so far).", "No session running", "Share of the project, 30 days"} {
		if !strings.Contains(d.HTML, want) {
			t.Errorf("main's card lacks %q:\n%s", want, d.HTML)
		}
	}
}

func TestUsageCardEmpty(t *testing.T) {
	doc := &usageDoc{Today: "2026-10-27", Daily: []usageDay{{Day: "2026-10-26"}, {Day: "2026-10-27"}}}
	c := newUsageCard(doc, "b", 7, nil, nil, time.Now())
	if c == nil || c.Empty != "No usage in 2 days" || c.Share != nil || c.Context != nil {
		t.Errorf("card %+v", c)
	}
	if newUsageCard(nil, "b", 7, nil, nil, time.Now()) != nil {
		t.Error("a card before usage is read")
	}
	if v := newUsage(doc, 7, nil, nil); v != nil {
		t.Errorf("no usage: %+v", v)
	}
	if v := newUsage(nil, 7, nil, nil); v != nil {
		t.Errorf("nil: %+v", v)
	}
}

func TestUsageMaths(t *testing.T) {
	for n, want := range map[int64]string{950: "950", 12_300: "12.3k", 148_000: "148k", 4_500_000: "4.5M", 1_200_000_000: "1.2B"} {
		if got := tokenText(n); got != want {
			t.Errorf("tokenText(%d) = %q; want %q", n, got, want)
		}
	}
	for v, want := range map[int64]int64{0: 1, 1: 1, 7: 10, 11: 20, 180: 200, 201: 250, 3_700_000: 5_000_000, 5_000_000: 5_000_000, 6_000_000: 10_000_000} {
		if got := niceMax(v); got != want {
			t.Errorf("niceMax(%d) = %d; want %d", v, got, want)
		}
	}
	// Stacked segments meet: each starts where the one below ended.
	y1, h1 := stack(0, 33, 100, 110)
	y2, h2 := stack(33, 66, 100, 110)
	y3, h3 := stack(66, 100, 100, 110)
	if y1+h1 != 110 || y2+h2 != y1 || y3+h3 != y2 || y3 != 0 {
		t.Errorf("stack: %d+%d, %d+%d, %d+%d", y1, h1, y2, h2, y3, h3)
	}
	if _, h := stack(0, 1, 1_000_000, 110); h != 1 {
		t.Errorf("a tiny segment is %d high; want 1", h)
	}
	if x, w := barGeom(13, 14, 420); x != 390 || w != 23 {
		t.Errorf("barGeom = %d, %d", x, w)
	}
	for d, want := range map[time.Duration]string{0: "0 min", 45 * time.Minute: "45 min", 130 * time.Minute: "2 h 10 min", 50 * time.Hour: "2 d 2 h"} {
		if got := durationText(d); got != want {
			t.Errorf("durationText(%v) = %q; want %q", d, got, want)
		}
	}
	if percent(1, 3) != 33 || percent(5, 0) != 0 || percent(300_000, 200_000) != 100 {
		t.Error("percent")
	}
	for s, want := range map[string]int{"7": 7, "30": 30, "": 14, "15": 14, "x": 14} {
		if got := parseDays(s); got != want {
			t.Errorf("parseDays(%q) = %d", s, got)
		}
	}
	if q := pageQuery(parseSort("-pr"), 30); q != "?days=30&sort=-pr" {
		t.Errorf("pageQuery = %q", q)
	}
}

// The range links keep the sort and carry one query: days=7 and a sort
// that isn't the default.
func TestUsageRangeLinks(t *testing.T) {
	f := newFixture(t)
	f.cli.set(usageCmd, ftestUsage())
	sub := f.s.subscribe("ftest", "b", parseSort("-pr"), 7, "")
	defer f.s.unsubscribe(sub)
	f.s.pollOnce(context.Background())
	evs := drain(sub)
	for _, id := range []string{"usage", "detail"} {
		e, _ := byID(evs, id)
		for _, want := range []string{`href="/p/ftest/b?days=7&amp;sort=-pr" data-days="7" class="on"`, `href="/p/ftest/b?sort=-pr" data-days="14"`, `href="/p/ftest/b?days=30&amp;sort=-pr" data-days="30"`} {
			if !strings.Contains(e.HTML, want) {
				t.Errorf("%s lacks %q", id, want)
			}
		}
		if strings.Count(e.HTML, "?days=7?") > 0 || strings.Contains(e.HTML, "-pr?") {
			t.Errorf("%s has a second ?: %s", id, e.HTML)
		}
	}
	if u, _ := byID(evs, "usage"); !strings.Contains(u.HTML, `<a href="/p/ftest/b?days=7&amp;sort=-pr" data-usage-wt="b">`) {
		t.Errorf("segment link: %s", u.HTML)
	}
}
