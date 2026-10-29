package prs

import (
	"context"
	"fmt"
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
