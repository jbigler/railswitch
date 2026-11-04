package web

import (
	"context"
	"fmt"
	"strconv"
)

// usageDays is how many days the dashboard's usage chart shows.
const usageDays = 14

// usageDoc is `agent usage --all --json` (D162), the fields the page uses.
type usageDoc struct {
	Today     string     `json:"today"`
	Daily     []usageDay `json:"daily"`
	Worktrees []usageWT  `json:"worktrees"`
}

type usageDay struct {
	Day   string   `json:"day"`
	Total int64    `json:"total"`
	Cost  *float64 `json:"cost"`
}

type usageWT struct {
	Worktree string   `json:"worktree"`
	Total    int64    `json:"total"`
	Today    int64    `json:"today"`
	Median   int64    `json:"median"`
	Cost     *float64 `json:"cost"`
	Runaway  bool     `json:"runaway"`
}

func readUsage(ctx context.Context, c CLI, ws string) (usageDoc, error) {
	return readJSON[usageDoc](ctx, c, "-C", ws, "agent", "usage", "--all", "--days", strconv.Itoa(usageDays))
}

// usageView is the project's usage chart: a bar per day, drawn as SVG
// attributes (the CSP allows no inline style).
type usageView struct {
	Summary string
	W, H    int
	Bars    []barView
}

type barView struct {
	X, Y, W, H   int
	Class, Title string
}

// chart geometry, in viewBox units.
const (
	chartW, chartH = 280, 56
	barGap         = 2
)

// newUsage is doc's chart; nil without usage in the window.
func newUsage(doc *usageDoc) *usageView {
	if doc == nil || len(doc.Daily) == 0 {
		return nil
	}
	var top, sum int64
	var cost *float64
	for _, d := range doc.Daily {
		top, sum = max(top, d.Total), sum+d.Total
		cost = addCost(cost, d.Cost)
	}
	if top == 0 {
		return nil
	}
	v := &usageView{W: chartW, H: chartH, Summary: fmt.Sprintf("%d days: %s tokens%s", len(doc.Daily), tokenText(sum), costSuffix(cost))}
	step := chartW / len(doc.Daily)
	for i, d := range doc.Daily {
		h := int(d.Total * chartH / top)
		if d.Total > 0 {
			h = max(h, 1)
		}
		class := "bar"
		if d.Day == doc.Today {
			class += " today"
		}
		v.Bars = append(v.Bars, barView{X: i * step, Y: chartH - h, W: step - barGap, H: h, Class: class,
			Title: fmt.Sprintf("%s: %s tokens%s", d.Day, tokenText(d.Total), costSuffix(d.Cost))})
	}
	return v
}

// usageLine is worktree slug's usage for its detail, "" without any.
func usageLine(doc *usageDoc, slug string) (line string, runaway bool) {
	if doc == nil {
		return "", false
	}
	for _, w := range doc.Worktrees {
		if w.Worktree == slug {
			return fmt.Sprintf("%s tokens today · %s in %d days%s", tokenText(w.Today), tokenText(w.Total), len(doc.Daily), costSuffix(w.Cost)), w.Runaway
		}
	}
	return "", false
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

func costSuffix(c *float64) string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf(" · $%.2f", *c)
}

// tokenText is n short: 950, 12.3k, 4.5M, 1.2B.
func tokenText(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return strconv.FormatInt(n, 10)
}
