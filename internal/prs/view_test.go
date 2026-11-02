package prs

import (
	"strings"
	"testing"
)

// D152: the agent gets the unresolved threads (or the named ones) as
// quoted text: no control characters, within the limit, the rest counted.
func TestAgentText(t *testing.T) {
	v := &View{Number: 7, Title: "Login\x1b[2J", URL: "https://github.com/o/r/pull/7", Threads: []Thread{
		{ID: "PRRT_a", Path: "a.go", Line: 3, Comments: []Comment{{Author: "bo", Body: "rename \x1b]0;pwn\x07 this"}, {Author: "ann", Body: "ok"}}},
		{ID: "PRRT_b", Path: "b.go", Line: 9, Resolved: true, Comments: []Comment{{Author: "bo", Body: "done already"}}},
		{ID: "PRRT_c", Path: "c.go", Outdated: true, Comments: []Comment{{Author: "cy", Body: strings.Repeat("long ", 400)}}},
	}}
	text, n, err := AgentText(v, nil, 4096)
	if err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	for _, want := range []string{"PR #7 (Login [2J)", "--- a.go:3", "bo: rename  ]0;pwn  this", "ann: ok", "--- c.go (outdated", " …"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "done already") || strings.ContainsAny(text, "\x1b\x07") || len(text) > 4096 {
		t.Errorf("text:\n%s", text)
	}
	// a tight limit: the first thread, the rest counted
	text, n, err = AgentText(v, nil, 400)
	if err != nil || n != 1 || !strings.Contains(text, "1 more threads did not fit") || len(text) > 400+200 {
		t.Errorf("tight: %d %v\n%s", n, err, text)
	}
	// named threads, resolved ones too; an unknown one is an error
	if text, n, err := AgentText(v, []string{"PRRT_b"}, 4096); err != nil || n != 1 || !strings.Contains(text, "done already") {
		t.Errorf("named: %d %v %s", n, err, text)
	}
	if _, _, err := AgentText(v, []string{"PRRT_z"}, 4096); err == nil {
		t.Error("an unknown thread: no error")
	}
	if _, _, err := AgentText(&View{Number: 8}, nil, 4096); err == nil || !strings.Contains(err.Error(), "no unresolved") {
		t.Errorf("none: %v", err)
	}
}
