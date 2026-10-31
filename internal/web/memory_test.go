package web

import (
	"strings"
	"testing"
)

const memoryJSON = `{"server":{"unit":"ftest-memory.service","container":"ftest_memory","image":"x","volume":"ftest_memory","state":"active"},
 "schema":66,"issues":{"total":3,"open":2,"in_progress":1,"blocked":1,"ready":1,"closed":0},"memories":2,
 "worktrees":[
  {"slug":"main","ready":[{"id":"ft-1","title":"Add <health>","status":"open","priority":2,"assignee":"","labels":["worktree:main"],"updated_at":""}],"claimed":[],"blocked":[]},
  {"slug":"b","ready":[],"claimed":[{"id":"ft-2","title":"Race me","status":"in_progress","priority":2,"assignee":"b/bot","labels":["worktree:b"],"updated_at":""}],"blocked":[{"id":"ft-3","title":"Wait","status":"open","priority":1,"assignee":"","labels":["worktree:b"],"updated_at":""}]}],
 "activity":[{"seq":4,"ts":"t4","op":"create","issue_id":"ft-1","actor":"main/bot","worktree":"main","agent":"bot"},{"seq":5,"ts":"t5","op":"update","issue_id":"ft-2","actor":"b/bot","worktree":"b","agent":"bot"}],
 "cursor":5,"error":""}`

// withMemory is an overview document with memory set (D136).
func withMemory(ov string) string {
	return strings.Replace(ov, `"workspace":"/ws/ftest",`, `"workspace":"/ws/ftest","memory":true,`, 1)
}

func TestMemoryPage(t *testing.T) {
	f := newFixture(t)
	if body := f.do(req("GET", "/p/ftest", nil)).Body.String(); strings.Contains(body, `href="/p/ftest/memory"`) {
		t.Error("the header links memory without [memory]")
	}
	f = newFixture(t)
	f.cli.set("--json -C /ws/ftest overview", withMemory(ftestOverview("jb/b", "up", agentJSON("idle", "", false))))
	f.cli.set("--json -C /ws/ftest memory", memoryJSON)
	contains(t, "project page", f.do(req("GET", "/p/ftest", nil)).Body.String(), `<a class="hb" href="/p/ftest/memory">memory</a>`)
	w := f.do(req("GET", "/p/ftest/memory", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	contains(t, "memory page", body,
		`data-view="memory"`,
		`<span class="mono">ftest_memory</span> · active · schema v66`,
		"3 issues: 2 open, 1 in progress, 1 blocked, 1 ready, 0 closed · 2 memories",
		`<td>main</td><td><span class="chip green">ready</span></td><td class="mono">ft-1</td><td>Add &lt;health&gt;</td>`,
		`<td>b</td><td><span class="chip yellow">claimed</span></td><td class="mono">ft-2</td><td>Race me</td><td class="mono">b/bot</td>`,
		`<span class="chip red">blocked</span></td><td class="mono">ft-3</td>`,
	)
	if i, j := strings.Index(body, `<td class="mono">5</td>`), strings.Index(body, `<td class="mono">4</td>`); i < 0 || j < 0 || i > j {
		t.Errorf("activity not newest first: %d %d", i, j)
	}
	noCSPViolation(t, "memory", body)
}

func TestMemoryLive(t *testing.T) {
	f := newFixture(t)
	f.cli.set("--json -C /ws/ftest overview", withMemory(ftestOverview("jb/b", "up", agentJSON("idle", "", false))))
	f.cli.set("--json -C /ws/ftest memory", memoryJSON)
	contains(t, "memory page", f.do(req("GET", "/p/ftest/memory", nil)).Body.String(),
		`<div id="memory-live" data-src="/p/ftest/memory/live" data-cursor="5">`)
	w := f.do(req("GET", "/p/ftest/memory/live?since=4", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, `<div id="memory-live"`) {
		t.Errorf("not the fragment alone: %.80s", body)
	}
	contains(t, "live", body, `<tr class="fresh"><td class="mono">5</td>`, `<tr><td class="mono">4</td>`, "2 memories")
	// Without a cursor (the page's own load) nothing is new.
	if body := f.do(req("GET", "/p/ftest/memory/live", nil)).Body.String(); strings.Contains(body, "fresh") {
		t.Error("rows marked new without a cursor")
	}
}
