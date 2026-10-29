package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/prs"
	"github.com/jbigler/railswitch/internal/sys"
)

func TestStackLs(t *testing.T) {
	ws, _ := gitWS(t)
	// Without the extension no worktree is asked.
	f := fakeHost()
	f.Passthrough = []string{"git"}
	f.On([]string{"gh", "extension", "list"}, sys.Result{Stdout: []byte("gh dash\tdlvhdr/gh-dash\tv4\n")}, nil)
	code, out, errs := runWith(t, f, "-C", ws, "--json", "stack", "ls")
	if code != 0 || !strings.Contains(out, `"available":false`) {
		t.Fatalf("no extension: exit %d %s%s", code, out, errs)
	}
	if slices.ContainsFunc(f.Calls, func(c []string) bool { return len(c) > 1 && c[0] == "gh" && c[1] == "stack" }) {
		t.Error("gh stack ran without the extension")
	}

	// With it: master in no stack, feat in one, one failure kept per row.
	f = fakeHost()
	f.Passthrough = []string{"git"}
	f.On([]string{"gh", "extension", "list"}, sys.Result{Stdout: []byte("gh stack\tgithub/gh-stack\tv0.3.0\n")}, nil)
	f.On([]string{"gh", "stack", "view", "--json"}, sys.Result{ExitCode: 1, Stderr: []byte(`current branch "master" is not part of a stack`)}, fmt.Errorf("gh: %w (exit 1)", sys.ErrExit))
	code, out, errs = runWith(t, f, "-C", ws, "--json", "stack", "ls")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var doc stackDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !doc.Available || len(doc.Worktrees) == 0 || doc.Worktrees[0].Slug != "master" || doc.Worktrees[0].Stack != nil || doc.Worktrees[0].Error != "" {
		t.Errorf("doc = %+v", doc)
	}
}

func TestStackText(t *testing.T) {
	// A merged branch below doesn't count; the current one is 2 of 2.
	r := stackRow{Stack: &prs.Stack{Trunk: "main", Current: "c", Branches: []prs.StackBranch{
		{Name: "a", Merged: true}, {Name: "b"}, {Name: "c", Current: true, NeedsRebase: true},
	}}}
	if got := stackText(r); got != "2/2 on main, restack needed" {
		t.Errorf("stackText = %q", got)
	}
}
