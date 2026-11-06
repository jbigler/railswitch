package web

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The usage views' ranges (D187): the switch offers usageRanges days,
// usageDefault without a choice; the dashboard reads usageMax days once
// and cuts each range from them.
var usageRanges = []int{7, 14, 30}

const (
	usageDefault = 14
	usageMax     = 30
)

// contextMax scales the session context meter. No agent package reports
// its model's context window, so the meter assumes 200k and says so
// ("of 200k"); this is the one place that number lives.
const contextMax = 200_000

// parseDays is a ?days= value: one of usageRanges, else usageDefault.
func parseDays(s string) int {
	if n, err := strconv.Atoi(s); err == nil && slices.Contains(usageRanges, n) {
		return n
	}
	return usageDefault
}

// usageDoc is `agent usage --all --json` (D162, D187), the fields the
// page uses.
type usageDoc struct {
	Today     string     `json:"today"`
	Daily     []usageDay `json:"daily"`
	Rows      []usageRow `json:"rows"`
	Worktrees []usageWT  `json:"worktrees"`
}

type usageDay struct {
	Day string `json:"day"`
}

type usageRow struct {
	Day        string   `json:"day"`
	Worktree   string   `json:"worktree"`
	Removed    bool     `json:"removed"`
	Input      int64    `json:"input"`
	Output     int64    `json:"output"`
	CacheRead  int64    `json:"cache_read"`
	CacheWrite int64    `json:"cache_write"`
	Cost       *float64 `json:"cost"`
}

// work is what the views count: new tokens (input and cache writes) and
// output; cache reads are cheap rereads of what was new once.
func (r usageRow) work() int64 { return r.Input + r.CacheWrite + r.Output }

type usageWT struct {
	Worktree        string `json:"worktree"`
	Today           int64  `json:"today"`
	Median          int64  `json:"median"`
	Runaway         bool   `json:"runaway"`
	Removed         bool   `json:"removed"`
	PriorDays       int    `json:"prior_days"`
	MedianNewOutput int64  `json:"median_new_output"`
}

func readUsage(ctx context.Context, c CLI, ws string) (usageDoc, error) {
	return readJSON[usageDoc](ctx, c, "-C", ws, "agent", "usage", "--all", "--days", strconv.Itoa(usageMax))
}

// window is the last days days of doc, oldest first.
func (doc *usageDoc) window(days int) []string {
	var out []string
	for _, d := range doc.Daily[max(0, len(doc.Daily)-days):] {
		out = append(out, d.Day)
	}
	return out
}

// rangeLink is a day range of the switch.
type rangeLink struct {
	N   int
	URL string
	On  bool
}

// usageLinks are the switch's ranges, each base with its ?days= and the
// sort kept.
func usageLinks(base string, k sortKey, days int) []rangeLink {
	var out []rangeLink
	for _, n := range usageRanges {
		q := url.Values{}
		if k != (sortKey{By: "name"}) {
			q.Set("sort", k.String())
		}
		if n != usageDefault {
			q.Set("days", strconv.Itoa(n))
		}
		u := base
		if len(q) > 0 {
			u += "?" + q.Encode()
		}
		out = append(out, rangeLink{N: n, URL: u, On: n == days})
	}
	return out
}

// pageQuery is a page URL's query for sort k and days, "?…" or "".
func pageQuery(k sortKey, days int) string {
	q := url.Values{}
	if k != (sortKey{By: "name"}) {
		q.Set("sort", k.String())
	}
	if days != usageDefault {
		q.Set("days", strconv.Itoa(days))
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

// segView is a rect of a chart, in viewBox units; URL links it.
type segView struct {
	X, Y, W, H              int
	Class, Title, URL, Slug string
}

// barGeom is the x and width of bar i of n across width w.
func barGeom(i, n, w int) (x, bw int) {
	step := float64(w) / float64(n)
	gap := max(1, int(step*0.25))
	return int(float64(i) * step), max(1, int(step)-gap)
}

// stack is the y and height of a segment from cumulative value lo to hi
// on a chart h tall whose top is top: rounded at the edges, so stacked
// segments meet without drift. A non-empty segment is at least 1 high.
func stack(lo, hi, top int64, h int) (y, sh int) {
	yl := h - int(math.Round(float64(lo)*float64(h)/float64(top)))
	yh := h - int(math.Round(float64(hi)*float64(h)/float64(top)))
	if hi > lo && yh == yl {
		yh = yl - 1
	}
	return yh, yl - yh
}

// niceMax is v rounded up to 1, 2, 2.5 or 5 times a power of ten, so the
// axis reads in round numbers; 1 for nothing.
func niceMax(v int64) int64 {
	if v <= 0 {
		return 1
	}
	p := int64(1)
	for p*10 <= v {
		p *= 10
	}
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if n := int64(m * float64(p)); n >= v {
			return n
		}
	}
	return 10 * p
}

// dayText is YYYY-MM-DD as "Nov 5".
func dayText(d string) string {
	t, err := time.Parse(time.DateOnly, d)
	if err != nil {
		return d
	}
	return t.Format("Jan 2")
}

// The project chart (D187, design A): per day the new tokens and output
// of each worktree, stacked; the usageColours worktrees with the most in
// the range get a colour each, the rest one "others" segment; a removed
// worktree keeps a muted segment of its own, never linked.
const usageColours = 6

type usageView struct {
	Days        int
	Total       string
	Ranges      []rangeLink
	W, H        int
	Segs        []segView
	First, Last string
	Legend      []legendItem
}

type legendItem struct {
	Name, Total, Class, Title string
	Muted                     bool
}

// chart geometry, in viewBox units; the SVG is drawn this tall in CSS px.
const (
	chartW, chartH = 420, 110
	cardW, cardH   = 420, 120
)

// newUsage is doc's project chart for days days; nil without usage in
// them. Worktree segments link to url(slug).
func newUsage(doc *usageDoc, days int, ranges []rangeLink, link func(slug string) string) *usageView {
	if doc == nil || len(doc.Daily) == 0 {
		return nil
	}
	win := doc.window(days)
	at := map[string]int{}
	for i, d := range win {
		at[d] = i
	}
	type wt struct {
		name    string
		removed bool
		total   int64
		perDay  []int64
	}
	byName := map[string]*wt{}
	var total int64
	for _, r := range doc.Rows {
		i, ok := at[r.Day]
		if !ok || r.work() == 0 {
			continue
		}
		w := byName[r.Worktree]
		if w == nil {
			w = &wt{name: r.Worktree, removed: r.Removed, perDay: make([]int64, len(win))}
			byName[r.Worktree] = w
		}
		w.perDay[i] += r.work()
		w.total += r.work()
		total += r.work()
	}
	if total == 0 {
		return nil
	}
	var live, removed []*wt
	for _, w := range byName {
		if w.removed {
			removed = append(removed, w)
		} else {
			live = append(live, w)
		}
	}
	rank := func(a, b *wt) int { return cmp.Or(cmp.Compare(b.total, a.total), cmp.Compare(a.name, b.name)) }
	slices.SortFunc(live, rank)
	slices.SortFunc(removed, rank)
	// series are the segments of each bar, bottom up.
	type series struct {
		label, class, url, slug string
		perDay                  []int64
		names                   []*wt // others: its worktrees
	}
	var ss []series
	v := &usageView{Days: len(win), Total: tokenText(total), Ranges: ranges, W: chartW, H: chartH,
		First: dayText(win[0]), Last: dayText(win[len(win)-1])}
	for i, w := range live {
		if i < usageColours {
			c := fmt.Sprintf("u-c%d", i)
			ss = append(ss, series{label: w.name, class: c, url: link(w.name), slug: w.name, perDay: w.perDay})
			v.Legend = append(v.Legend, legendItem{Name: w.name, Total: tokenText(w.total), Class: c})
			continue
		}
		if i == usageColours {
			ss = append(ss, series{label: "others", class: "u-other", perDay: make([]int64, len(win))})
		}
		o := &ss[len(ss)-1]
		o.names = append(o.names, w)
		for d, n := range w.perDay {
			o.perDay[d] += n
		}
	}
	if len(live) > usageColours {
		o := ss[len(ss)-1]
		var sum int64
		var names []string
		for _, w := range o.names {
			sum += w.total
			names = append(names, w.name+" "+tokenText(w.total))
		}
		v.Legend = append(v.Legend, legendItem{Name: fmt.Sprintf("%d others", len(o.names)), Total: tokenText(sum), Class: "u-other", Title: strings.Join(names, ", "), Muted: true})
	}
	for _, w := range removed {
		ss = append(ss, series{label: w.name, class: "u-removed", perDay: w.perDay})
		v.Legend = append(v.Legend, legendItem{Name: w.name, Total: tokenText(w.total), Class: "u-removed", Muted: true})
	}
	var top int64
	for d := range win {
		var sum int64
		for _, s := range ss {
			sum += s.perDay[d]
		}
		top = max(top, sum)
	}
	for d, day := range win {
		x, bw := barGeom(d, len(win), chartW)
		var lo int64
		for _, s := range ss {
			n := s.perDay[d]
			if n == 0 {
				continue
			}
			y, h := stack(lo, lo+n, top, chartH)
			lo += n
			title := fmt.Sprintf("%s · %s: %s new and output", dayText(day), s.label, tokenText(n))
			if s.names != nil {
				var parts []string
				for _, w := range s.names {
					if w.perDay[d] > 0 {
						parts = append(parts, w.name+" "+tokenText(w.perDay[d]))
					}
				}
				title = fmt.Sprintf("%s · others: %s", dayText(day), strings.Join(parts, ", "))
			}
			v.Segs = append(v.Segs, segView{X: x, Y: y, W: bw, H: h, Class: s.class, Title: title, URL: s.url, Slug: s.slug})
		}
	}
	return v
}

// The worktree's usage card on its Agent tab (D187, design B).
type usageCard struct {
	Days    int
	Ranges  []rangeLink
	Table   []usageTableRow
	Runaway bool
	Empty   string // "No usage in N days"; the rest is then absent
	W, H    int
	Bars    []segView
	YTop    string // the axis: top, middle and 0
	YMid    string
	GridY   int
	Usual   *usualLine // nil: Note says when it appears
	Note    string
	Days3   []string // the day labels: first, middle, last
	Context *meterView
	Share   *meterView
}

type usageTableRow struct {
	Label, New, Output, CacheRead, Cost string
}

type usualLine struct {
	Y     int
	Title string
}

// meterView is a meter: Fill of 100.
type meterView struct {
	Label, Value, Of, Aria string
	Fill                   int
}

// newUsageCard is worktree slug's card for days days; a nil doc (usage
// not read yet) is no card. ag is the worktree's agent, for the running
// session's context.
func newUsageCard(doc *usageDoc, slug string, days int, ranges []rangeLink, ag *agent, now time.Time) *usageCard {
	if doc == nil || len(doc.Daily) == 0 {
		return nil
	}
	win := doc.window(days)
	c := &usageCard{Days: len(win), Ranges: ranges}
	if ag != nil && ag.Running && ag.Usage != nil && ag.Usage.Session != "" {
		label := "This session's context"
		if ag.Usage.Started > 0 {
			label += " · " + durationText(now.Sub(time.Unix(ag.Usage.Started, 0)))
		}
		n := ag.Usage.Context
		c.Context = &meterView{Label: label, Value: tokenText(n), Of: "of " + tokenText(contextMax), Fill: percent(n, contextMax),
			Aria: fmt.Sprintf("Context %s of %s", tokenText(n), tokenText(contextMax))}
	}
	at := map[string]int{}
	for i, d := range win {
		at[d] = i
	}
	newDay, outDay := make([]int64, len(win)), make([]int64, len(win))
	var today, sum usageRow
	var todayCost, sumCost *float64
	var project int64
	for _, r := range doc.Rows {
		i, ok := at[r.Day]
		if !ok {
			continue
		}
		project += r.work()
		if r.Worktree != slug {
			continue
		}
		newDay[i] += r.Input + r.CacheWrite
		outDay[i] += r.Output
		add := func(t *usageRow, cost **float64) {
			t.Input, t.Output, t.CacheRead, t.CacheWrite = t.Input+r.Input, t.Output+r.Output, t.CacheRead+r.CacheRead, t.CacheWrite+r.CacheWrite
			*cost = addCost(*cost, r.Cost)
		}
		add(&sum, &sumCost)
		if r.Day == doc.Today {
			add(&today, &todayCost)
		}
	}
	if sum.work()+sum.CacheRead == 0 {
		c.Empty = fmt.Sprintf("No usage in %d days", len(win))
		return c
	}
	var w usageWT
	for _, x := range doc.Worktrees {
		if x.Worktree == slug {
			w = x
		}
	}
	c.Runaway = w.Runaway
	row := func(label string, t usageRow, cost *float64) usageTableRow {
		return usageTableRow{Label: label, New: tokenText(t.Input + t.CacheWrite), Output: tokenText(t.Output), CacheRead: tokenText(t.CacheRead), Cost: costText(cost)}
	}
	c.Table = []usageTableRow{row("Today", today, todayCost), row(fmt.Sprintf("%d days", len(win)), sum, sumCost)}
	var top int64
	for i := range win {
		top = max(top, newDay[i]+outDay[i])
	}
	if w.PriorDays >= usualMinDays {
		top = max(top, w.MedianNewOutput)
	}
	top = niceMax(top)
	c.W, c.H, c.YTop, c.YMid, c.GridY = cardW, cardH, tokenText(top), tokenText(top/2), cardH/2
	for i, d := range win {
		x, bw := barGeom(i, len(win), cardW)
		cls := ""
		if d == doc.Today && c.Runaway {
			cls = " runaway"
		}
		title := fmt.Sprintf("%s: %s new, %s output", dayText(d), tokenText(newDay[i]), tokenText(outDay[i]))
		if newDay[i] > 0 {
			y, h := stack(0, newDay[i], top, cardH)
			c.Bars = append(c.Bars, segView{X: x, Y: y, W: bw, H: h, Class: "u-new" + cls, Title: title})
		}
		if outDay[i] > 0 {
			y, h := stack(newDay[i], newDay[i]+outDay[i], top, cardH)
			c.Bars = append(c.Bars, segView{X: x, Y: y, W: bw, H: h, Class: "u-out" + cls, Title: title})
		}
	}
	if w.PriorDays >= usualMinDays {
		y, _ := stack(0, w.MedianNewOutput, top, cardH)
		c.Usual = &usualLine{Y: y, Title: "Usual day: " + tokenText(w.MedianNewOutput) + " new and output"}
	} else {
		c.Note = fmt.Sprintf("Usual-day line appears with %d earlier days of use (%d so far).", usualMinDays, w.PriorDays)
	}
	c.Days3 = []string{dayText(win[0]), dayText(win[len(win)/2]), dayText(win[len(win)-1])}
	c.Share = &meterView{Label: fmt.Sprintf("Share of the project, %d days", len(win)), Value: fmt.Sprintf("%d%%", percent(sum.work(), project)),
		Of: fmt.Sprintf("· %s of %s", tokenText(sum.work()), tokenText(project)), Fill: percent(sum.work(), project),
		Aria: fmt.Sprintf("%d percent of the project", percent(sum.work(), project))}
	return c
}

// usualMinDays is how many earlier days with usage the usual day needs
// (agentusage's runawayMinDays).
const usualMinDays = 3

// percent is n of of, 0 to 100.
func percent(n, of int64) int {
	if of <= 0 {
		return 0
	}
	return int(min(100, max(0, math.Round(float64(n)*100/float64(of)))))
}

// durationText is d as "2 h 10 min", "45 min" or "3 d 2 h".
func durationText(d time.Duration) string {
	m := int(max(0, d.Minutes()))
	switch {
	case m >= 24*60:
		return fmt.Sprintf("%d d %d h", m/(24*60), m%(24*60)/60)
	case m >= 60:
		return fmt.Sprintf("%d h %d min", m/60, m%60)
	}
	return fmt.Sprintf("%d min", m)
}

// runawayText is the banner's line for a runaway worktree.
func runawayText(w usageWT) string {
	return fmt.Sprintf("Agent usage of %s is high today: %s tokens, over 3 times its usual day (%s).", w.Worktree, tokenText(w.Today), tokenText(w.Median))
}

func addCost(a, b *float64) *float64 {
	if b == nil {
		return a
	}
	c := *b
	if a != nil {
		c += *a
	}
	return &c
}

func costText(c *float64) string {
	if c == nil {
		return "not reported"
	}
	return fmt.Sprintf("$%.2f", *c)
}

// tokenText is n short: 950, 12.3k, 148k, 4.5M, 5M, 1.2B.
func tokenText(n int64) string {
	for _, u := range []struct {
		v      float64
		suffix string
	}{{1e9, "B"}, {1e6, "M"}, {1e3, "k"}} {
		if f := float64(n) / u.v; f >= 1 {
			if f >= 100 {
				return fmt.Sprintf("%.0f%s", f, u.suffix)
			}
			return strings.TrimSuffix(fmt.Sprintf("%.1f", f), ".0") + u.suffix
		}
	}
	return strconv.FormatInt(n, 10)
}
