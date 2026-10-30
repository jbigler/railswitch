package prs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

// What gh-stack v0.3.0 prints, with a PR added on the bottom branch.
const stackJSON = `{
  "trunk": "main",
  "currentBranch": "stk-b",
  "branches": [
    {"name": "stk-a", "base": "6b8b", "isCurrent": false, "isMerged": true, "isQueued": false, "needsRebase": false, "pr": {"number": 41, "state": "MERGED"}},
    {"name": "stk-b", "base": "7c25", "isCurrent": true, "isMerged": false, "isQueued": false, "needsRebase": true}
  ]
}`

func TestViewStack(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"gh", "stack", "view", "--json"}, sys.Result{Stdout: []byte(stackJSON)}, nil)
	s, err := ViewStack(context.Background(), f, "/ws/app/feat", []string{"GIT_COMMON_DIR=/shadow"})
	if err != nil {
		t.Fatal(err)
	}
	want := &Stack{Trunk: "main", Current: "stk-b", Branches: []StackBranch{
		{Name: "stk-a", Merged: true, PR: &StackPR{Number: 41, State: "merged"}},
		{Name: "stk-b", Current: true, NeedsRebase: true},
	}}
	if s.Trunk != want.Trunk || s.Current != want.Current || len(s.Branches) != 2 ||
		s.Branches[0].Name != "stk-a" || !s.Branches[0].Merged || s.Branches[0].PR == nil || *s.Branches[0].PR != *want.Branches[0].PR ||
		!s.Branches[1].Current || !s.Branches[1].NeedsRebase || s.Branches[1].PR != nil {
		t.Errorf("stack = %+v", s)
	}
	// It runs in the worktree with the environment it was given, no prompt.
	c := f.Cmds[0]
	if c.Dir != "/ws/app/feat" || c.Timeout == 0 || !slices.Contains(c.Env, "GIT_COMMON_DIR=/shadow") || !slices.Contains(c.Env, "GH_PROMPT_DISABLED=1") {
		t.Errorf("cmd = %+v", c)
	}
}

// A branch in no stack is nil; any other failure is an error.
func TestViewStackNone(t *testing.T) {
	for msg, want := range map[string]bool{
		`✗ current branch "main" is not part of a stack`: false,
		`branch "x" belongs to multiple stacks`:          false,
		"gh: not logged in":                              true,
	} {
		f := &systest.Fake{}
		f.On([]string{"gh", "stack"}, sys.Result{ExitCode: 1, Stderr: []byte(msg)}, fmt.Errorf("gh: %w (exit 1)", sys.ErrExit))
		s, err := ViewStack(context.Background(), f, "/w", nil)
		if s != nil || (err != nil) != want {
			t.Errorf("%q: stack %v, err %v; want error %v", msg, s, err, want)
		}
	}
}

func TestStackAvailable(t *testing.T) {
	for out, want := range map[string]bool{
		"gh stack\tgithub/gh-stack\tv0.3.0\n": true,
		"gh dash\tdlvhdr/gh-dash\tv4\n":       false,
		"":                                    false,
	} {
		f := &systest.Fake{}
		f.On([]string{"gh", "extension", "list"}, sys.Result{Stdout: []byte(out)}, nil)
		if got := StackAvailable(context.Background(), f); got != want {
			t.Errorf("%q: %v; want %v", strings.TrimSpace(out), got, want)
		}
	}
}

// On the trunk gh stack prints the stack with no branch current: none.
func TestViewStackOnTrunk(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"gh", "stack", "view", "--json"}, sys.Result{Stdout: []byte(strings.ReplaceAll(stackJSON, `"isCurrent": true`, `"isCurrent": false`))}, nil)
	if s, err := ViewStack(context.Background(), f, "/w", nil); s != nil || err != nil {
		t.Errorf("trunk: %+v, %v; want no stack", s, err)
	}
}

// TestDetail: open stack PRs get draft, checks and review from GitHub,
// matched by number and branch; a merged one is never asked for.
func TestDetail(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"gh", "pr", "list", "--repo", "o/n", "--head=b"}, sys.Result{Stdout: []byte(`[{"number":7,"headRefName":"b","isDraft":true,"state":"OPEN","reviewDecision":"","statusCheckRollup":[]}]`)}, nil)
	f.On([]string{"gh", "pr", "list", "--repo", "o/n", "--head=c"}, sys.Result{Stdout: []byte(`[{"number":8,"headRefName":"c","state":"OPEN","reviewDecision":"APPROVED","statusCheckRollup":[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"}]}]`)}, nil)
	st := &Stack{Branches: []StackBranch{
		{Name: "a", Merged: true, PR: &StackPR{Number: 6, State: "merged"}},
		{Name: "b", PR: &StackPR{Number: 7, State: "open"}},
		{Name: "c", PR: &StackPR{Number: 8, State: "open"}},
		{Name: "d"},
	}}
	if err := Detail(context.Background(), f, "o/n", []*Stack{st, st}); err != nil {
		t.Fatal(err)
	}
	if got := *st.Branches[1].PR; got != (StackPR{Number: 7, State: "draft", Checks: "none", Review: "none"}) {
		t.Errorf("b = %+v", got)
	}
	if got := *st.Branches[2].PR; got != (StackPR{Number: 8, State: "open", Checks: "pass", Review: "approved"}) {
		t.Errorf("c = %+v", got)
	}
	if *st.Branches[0].PR != (StackPR{Number: 6, State: "merged"}) || len(f.Calls) != 2 {
		t.Errorf("merged %+v; %d calls", *st.Branches[0].PR, len(f.Calls))
	}
}

// TestStackNumber: the number of the catalog's stack holding the branch;
// 0 for a branch in none, an unsubmitted stack, or a catalog that isn't a
// regular JSON file.
func TestStackNumber(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh-stack"), []byte(`{"schemaVersion":1,"stacks":[
		{"number":10350,"trunk":{"branch":"master"},"branches":[{"branch":"a"},{"branch":"b"}]},
		{"trunk":{"branch":"master"},"branches":[{"branch":"c"}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for b, want := range map[string]int{"b": 10350, "c": 0, "zz": 0} {
		if got := StackNumber(dir, b); got != want {
			t.Errorf("StackNumber(%s) = %d; want %d", b, got, want)
		}
	}
	link := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "gh-stack"), filepath.Join(link, "gh-stack")); err != nil {
		t.Fatal(err)
	}
	if got := StackNumber(link, "b"); got != 0 {
		t.Errorf("through a symlinked catalog: %d", got)
	}
}

// TestDetailCap: at most maxDetail branches are looked up, each once.
func TestDetailCap(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"gh"}, sys.Result{Stdout: []byte("[]")}, nil)
	st := &Stack{}
	for i := range maxDetail + 20 {
		st.Branches = append(st.Branches, StackBranch{Name: fmt.Sprintf("b%d", i), PR: &StackPR{Number: i + 1, State: "open"}})
	}
	if err := Detail(context.Background(), f, "o/n", []*Stack{st, st}); err != nil {
		t.Fatal(err)
	}
	if len(f.Calls) != maxDetail {
		t.Errorf("%d gh calls; want %d", len(f.Calls), maxDetail)
	}
}
