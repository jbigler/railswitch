package web

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// D159: the queue's forms run agent queue add|rm|clear (add's text on
// stdin), and pr agent --queue; bad input runs nothing.
func TestQueueActions(t *testing.T) {
	wsb := []string{testExe, "-C", "/ws/ftest", "-w", "b"}
	for _, c := range []struct {
		name, path string
		form       []string
		code       int
		argv       []string // nil: no job
		stdin      string
	}{
		{"add", "/p/ftest/b/queue/add", []string{"text", "fix the tests\r\nthen lint"}, 202, []string{"agent", "queue", "add"}, "fix the tests\r\nthen lint"},
		{"add issue", "/p/ftest/b/queue/add", []string{"issue", "#42", "text", "ignored"}, 202, []string{"agent", "queue", "add", "--issue", "42"}, ""},
		{"add nothing", "/p/ftest/b/queue/add", []string{"text", "  "}, 400, nil, ""},
		{"add bad issue", "/p/ftest/b/queue/add", []string{"issue", "--x"}, 400, nil, ""},
		{"add too long", "/p/ftest/b/queue/add", []string{"text", strings.Repeat("x", 8193)}, 413, nil, ""},
		{"rm", "/p/ftest/b/queue/rm", []string{"id", "3"}, 202, []string{"agent", "queue", "rm", "3"}, ""},
		{"rm bad", "/p/ftest/b/queue/rm", []string{"id", "-1"}, 400, nil, ""},
		{"clear", "/p/ftest/b/queue/clear", nil, 202, []string{"agent", "queue", "clear"}, ""},
		{"next is the dashboard's", "/p/ftest/b/queue/next", nil, 404, nil, ""},
		{"unknown worktree", "/p/ftest/nope/queue/clear", nil, 404, nil, ""},
		{"pr agent queue", "/p/ftest/b/pr/agent", []string{"number", "2141", "queue", "1"}, 202, []string{"pr", "agent", "2141", "--queue"}, ""},
	} {
		f := newFixture(t)
		w := f.do(req("POST", c.path, form(c.form...)))
		if w.Code != c.code {
			t.Errorf("%s: %d %q, want %d", c.name, w.Code, w.Body, c.code)
			continue
		}
		if c.argv == nil {
			time.Sleep(10 * time.Millisecond)
			if got := f.run.Argvs(); len(got) != 0 {
				t.Errorf("%s: ran %q", c.name, got)
			}
			continue
		}
		if got, want := f.run.wait(t, 1)[0], append(slices.Clone(wsb), c.argv...); !slices.Equal(got, want) {
			t.Errorf("%s: argv %q, want %q", c.name, got, want)
		}
		f.run.mu.Lock()
		in := string(f.run.stdins[0])
		f.run.mu.Unlock()
		if in != c.stdin {
			t.Errorf("%s: stdin %q, want %q", c.name, in, c.stdin)
		}
	}
}

// D159: the dashboard runs agent queue next for a worktree with a queue
// whose running agent has been idle (or its turn over) a while, once per
// status; never for one working, waiting on the user, or just idle.
func TestQueueDelivery(t *testing.T) {
	const queue = `[{"id":1,"text":"next task"}]`
	f := newFixture(t)
	if err := f.s.loadProjects(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := f.clock.Now().Unix()
	ov := func(state string, ts int64, reply bool, q string) overview {
		t.Helper()
		js := ftestOverview("jb/b", "up", fmt.Sprintf(`{"name":"bot","state":%q,"message":"","ts":%d,"reply":%t,"running":true}`, state, ts, reply))
		js = strings.Replace(js, `"links":[{"label":"App","url":"http://b.`, `"queue":`+q+`,"links":[{"label":"App","url":"http://b.`, 1)
		var o overview
		if err := json.Unmarshal([]byte(js), &o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	for _, c := range []struct {
		name string
		o    overview
	}{
		{"working", ov("working", now-60, false, queue)},
		{"permission", ov("permission", now-60, false, queue)},
		{"asks", ov("done", now-60, true, queue)},
		{"settling", ov("idle", now-2, false, queue)},
		{"no queue", ov("idle", now-60, false, `[]`)},
	} {
		f.s.applyOverview("ftest", c.o, nil)
		time.Sleep(10 * time.Millisecond)
		if got := f.run.Argvs(); len(got) != 0 {
			t.Fatalf("%s: ran %q", c.name, got)
		}
	}
	f.s.applyOverview("ftest", ov("idle", now-60, false, queue), nil)
	want := []string{testExe, "-C", "/ws/ftest", "-w", "b", "agent", "queue", "next"}
	if got := f.run.wait(t, 1); !slices.Equal(got[0], want) {
		t.Fatalf("idle: ran %q, want %q", got, want)
	}
	for hub := f.s.c.Jobs; hub.Running(); {
		time.Sleep(time.Millisecond)
	}
	// the same status again: not tried twice; a new one is
	f.s.applyOverview("ftest", ov("idle", now-60, false, queue), nil)
	time.Sleep(10 * time.Millisecond)
	if got := f.run.Argvs(); len(got) != 1 {
		t.Fatalf("same status: ran %q", got)
	}
	f.s.applyOverview("ftest", ov("done", now-30, false, queue), nil)
	if got := f.run.wait(t, 2); !slices.Equal(got[1], want) {
		t.Fatalf("turn over: ran %q", got)
	}
}

// D159: the detail shows the queue's items with their remove forms; the
// add dialog posts to the queue's add.
func TestQueueView(t *testing.T) {
	wt := worktree{Slug: "b", Stack: "up", Queue: []queueItem{{ID: 7, Text: "fix <the> tests\nsecond line"}}}
	html, err := render("detail", newDetail("ftest", row{WT: wt}, ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`href="/p/ftest/b/queue" data-dialog="agentqueue"`, `action="/p/ftest/b/queue/rm"`, `name="id" value="7"`, `#7</span> fix &lt;the&gt; tests <form`, `action="/p/ftest/b/queue/clear"`} {
		if !strings.Contains(html, want) {
			t.Errorf("detail lacks %q:\n%s", want, html)
		}
	}
	f := newFixture(t)
	w := f.do(req("GET", "/p/ftest/b/queue", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `action="/p/ftest/b/queue/add"`) {
		t.Errorf("dialog: %d %q", w.Code, w.Body)
	}
}
