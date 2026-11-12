package web

import (
	"context"
	"encoding/json"
	"testing"
)

// A branch checked out outside the dashboard (git switch in a terminal,
// #191) has its PR and stack read at once, not at the next prEvery; an
// overview with the same branches asks for nothing.
func TestBranchChangeReadsPRs(t *testing.T) {
	f := newFixture(t)
	if err := f.s.loadProjects(context.Background()); err != nil {
		t.Fatal(err)
	}
	apply := func(branch string) {
		t.Helper()
		var ov overview
		if err := json.Unmarshal([]byte(ftestOverview(branch, "up", "null")), &ov); err != nil {
			t.Fatal(err)
		}
		f.s.applyOverview("ftest", ov, nil)
	}
	asked := func() bool {
		f.s.mu.Lock()
		defer f.s.mu.Unlock()
		p := f.s.state["ftest"]
		now := p.prNow
		p.prNow = false
		select {
		case <-f.s.wakePR:
			return now
		default:
			return false
		}
	}
	apply("jb/b")
	asked() // whatever the first overview left
	apply("jb/b")
	if asked() {
		t.Error("the same branch asked for the PRs")
	}
	apply("jb/other")
	if !asked() {
		t.Error("a new branch did not ask for the PRs at once")
	}
}
