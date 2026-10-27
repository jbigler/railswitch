package prs

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

func TestRepo(t *testing.T) {
	for _, u := range []string{
		"https://github.com/o/n",
		"https://github.com/o/n.git",
		"git@github.com:o/n.git",
		"git@github.com:o/n",
		"ssh://git@github.com/o/n",
		"ssh://git@github.com/o/n.git",
	} {
		got, err := Repo(u)
		if err != nil || got != "o/n" {
			t.Errorf("Repo(%q) = %q, %v; want o/n", u, got, err)
		}
	}
	u := "https://gitlab.com/o/n.git"
	_, err := Repo(u)
	if err == nil || err.Error() != u+" is not a GitHub repo" {
		t.Errorf("Repo(%q) err = %v", u, err)
	}
	for _, bad := range []string{"https://github.com/o", "https://github.com/o/n/x", "git@github.com:/n"} {
		if _, err := Repo(bad); err == nil {
			t.Errorf("Repo(%q) want error", bad)
		}
	}
}

func TestListArgv(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"gh"}, sys.Result{Stdout: []byte("[]")}, nil)
	if _, err := List(context.Background(), f, "o/n", "/ws"); err != nil {
		t.Fatal(err)
	}
	if len(f.Cmds) != 1 {
		t.Fatalf("calls = %d", len(f.Cmds))
	}
	c := f.Cmds[0]
	want := []string{"gh", "pr", "list", "--repo", "o/n", "--state", "all", "--limit", "200", "--json",
		"number,title,url,headRefName,isCrossRepository,isDraft,state,reviewDecision,statusCheckRollup"}
	if !slices.Equal(f.Calls[0], want) {
		t.Errorf("argv = %q", f.Calls[0])
	}
	if c.Dir != "/ws" || c.Timeout != 30*time.Second || !slices.Contains(c.Env, "GH_PROMPT_DISABLED=1") {
		t.Errorf("cmd = %+v", c)
	}
	if len(c.Env) != len(os.Environ())+1 {
		t.Errorf("env should be os.Environ() + GH_PROMPT_DISABLED")
	}
}

func TestParse(t *testing.T) {
	data, err := os.ReadFile("testdata/gh-pr-list.json")
	if err != nil {
		t.Fatal(err)
	}
	f := &systest.Fake{}
	f.On([]string{"gh", "pr", "list"}, sys.Result{Stdout: data}, nil)
	got, err := List(context.Background(), f, "o/n", "/ws")
	if err != nil {
		t.Fatal(err)
	}
	want := []PR{
		{1, "Draft", "https://github.com/o/n/pull/1", "draft", "draft", "pass", "none"},
		{2, "Approved", "https://github.com/o/n/pull/2", "approved", "open", "pass", "approved"},
		{3, "Changes", "https://github.com/o/n/pull/3", "changes", "open", "fail", "changes_requested"},
		{4, "Running", "https://github.com/o/n/pull/4", "running", "open", "pending", "pending"},
		{5, "Status pending", "https://github.com/o/n/pull/5", "status", "open", "pending", "none"},
		{6, "Merged", "https://github.com/o/n/pull/6", "merged", "merged", "pass", "approved"},
		{7, "Closed", "https://github.com/o/n/pull/7", "closed", "closed", "fail", "none"},
		{8, "No checks", "https://github.com/o/n/pull/8", "nochecks", "open", "none", "none"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	// #9 is a fork's PR on its own "main": never the PR of our main.
	if p := ForBranch(got, "main"); p != nil {
		t.Errorf("fork PR matched main: %+v", p)
	}
}

func TestForBranch(t *testing.T) {
	prs := []PR{
		{Number: 10, Branch: "a", State: "merged"},
		{Number: 12, Branch: "a", State: "open"},
		{Number: 11, Branch: "a", State: "closed"},
		{Number: 3, Branch: "b", State: "closed"},
		{Number: 5, Branch: "b", State: "closed"},
		{Number: 1, Branch: "c", State: "draft"},
		{Number: 9, Branch: "c", State: "merged"},
	}
	for branch, want := range map[string]int{"a": 12, "b": 5, "c": 1} {
		if p := ForBranch(prs, branch); p == nil || p.Number != want {
			t.Errorf("ForBranch(%q) = %+v, want #%d", branch, p, want)
		}
	}
	if p := ForBranch(prs, "zz"); p != nil {
		t.Errorf("ForBranch(zz) = %+v", p)
	}
}

func TestListError(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"gh"}, sys.Result{Stderr: []byte("gh: not logged in"), ExitCode: 1},
		fmt.Errorf("gh: %w (exit 1): gh: not logged in", sys.ErrExit))
	_, err := List(context.Background(), f, "o/n", "/ws")
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("err = %v", err)
	}
}
