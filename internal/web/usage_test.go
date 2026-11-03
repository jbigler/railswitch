package web

import (
	"context"
	"strings"
	"testing"
)

const ftestUsage = `{"today":"2026-10-27","days":14,
"daily":[{"day":"2026-10-26","total":2000000,"cost":null},{"day":"2026-10-27","total":8000000,"cost":1.5}],
"rows":[],
"worktrees":[{"worktree":"b","total":10000000,"today":8000000,"median":2000000,"cost":1.5,"runaway":true}]}`

// D162: the usage chart, the worktree's line in its detail and the
// runaway banner come from agent usage --json, read with the PRs.
func TestUsageView(t *testing.T) {
	f := newFixture(t)
	f.cli.set("--json -C /ws/ftest agent usage --all --days 14", ftestUsage)
	sub := f.s.subscribe("ftest", "b", parseSort("name"), "")
	defer f.s.unsubscribe(sub)
	f.s.pollOnce(context.Background())
	evs := drain(sub)
	usage, ok := byID(evs, "usage")
	if !ok || strings.Count(usage.HTML, "<rect") != 2 || !strings.Contains(usage.HTML, `class="bar today" x="140" y="0" width="138" height="56"`) ||
		!strings.Contains(usage.HTML, `x="0" y="42" width="138" height="14"`) || !strings.Contains(usage.HTML, "2 days: 10.0M tokens · $1.50") || strings.Contains(usage.HTML, "style") {
		t.Errorf("usage %q", usage.HTML)
	}
	if d, _ := byID(evs, "detail"); !strings.Contains(d.HTML, "⚠ 8.0M tokens today · 10.0M in 2 days · $1.50") {
		t.Errorf("detail %q", d.HTML)
	}
	if b, _ := byID(evs, "banner"); !strings.Contains(b.HTML, "Agent usage of b is high today: 8.0M tokens") {
		t.Errorf("banner %q", b.HTML)
	}
}

func TestNewUsageEmpty(t *testing.T) {
	if v := newUsage(&usageDoc{Daily: []usageDay{{Day: "2026-10-27"}}}); v != nil {
		t.Errorf("no usage: %+v", v)
	}
	if v := newUsage(nil); v != nil {
		t.Errorf("nil: %+v", v)
	}
	for n, want := range map[int64]string{950: "950", 12_300: "12.3k", 4_500_000: "4.5M", 1_200_000_000: "1.2B"} {
		if got := tokenText(n); got != want {
			t.Errorf("tokenText(%d) = %q; want %q", n, got, want)
		}
	}
}
