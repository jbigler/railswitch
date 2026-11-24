package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/prs"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
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

// TestStackLsOne: -w reads only that worktree's stack, --no-detail asks
// GitHub nothing per branch (#254), and an unknown worktree fails.
func TestStackLsOne(t *testing.T) {
	ws, _ := gitWS(t)
	fake := func() *systest.Fake {
		f := fakeHost()
		f.Passthrough = []string{"git"}
		f.On([]string{"gh", "extension", "list"}, sys.Result{Stdout: []byte("gh stack\tgithub/gh-stack\tv0.3.0\n")}, nil)
		f.On([]string{"gh", "stack", "view", "--json"}, sys.Result{Stdout: []byte(`{"trunk":"master","currentBranch":"feat","branches":[{"name":"feat","isCurrent":true,"pr":{"number":7,"state":"OPEN"}}]}`)}, nil)
		return f
	}
	prQuery := func(c []string) bool { return len(c) > 1 && c[0] == "gh" && c[1] == "pr" }
	f := fake()
	if code, _, errs := runWith(t, f, "-C", ws, "-w", "feat", "--json", "stack", "ls"); code != 0 || !slices.ContainsFunc(f.Calls, prQuery) {
		t.Fatalf("without --no-detail: exit %d, PR queried %v: %s", code, slices.ContainsFunc(f.Calls, prQuery), errs)
	}
	f = fake()
	code, out, errs := runWith(t, f, "-C", ws, "-w", "feat", "--json", "stack", "ls", "--no-detail")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var doc stackDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(doc.Worktrees) != 1 || doc.Worktrees[0].Slug != "feat" || doc.Worktrees[0].Stack == nil {
		t.Errorf("doc = %+v", doc)
	}
	if n := len(slices.DeleteFunc(slices.Clone(f.Calls), func(c []string) bool { return !slices.Equal(c, []string{"gh", "stack", "view", "--json"}) })); n != 1 {
		t.Errorf("gh stack view ran %d times; want once, for feat", n)
	}
	if slices.ContainsFunc(f.Calls, prQuery) {
		t.Errorf("--no-detail queried PRs: %q", f.Calls)
	}
	if code, _, _ := runWith(t, fake(), "-C", ws, "-w", "nope", "--json", "stack", "ls"); code == 0 {
		t.Error("an unknown worktree read")
	}
}

func TestStackText(t *testing.T) {
	// A merged branch below doesn't count; the current one is 2 of 2.
	r := stackRow{Stack: &prs.Stack{Trunk: "main", Current: "c", Number: 12, Branches: []prs.StackBranch{
		{Name: "a", Merged: true}, {Name: "b"}, {Name: "c", Current: true, NeedsRebase: true},
	}}}
	if got := stackText(r); got != "#12, base main, 3 branches (1 merged), restack needed" {
		t.Errorf("stackText = %q", got)
	}
}

// stackFake has gh-stack installed and feat in a stack: master, feat
// (current), next.
func stackFake() *systest.Fake {
	f := fakeHost()
	f.Passthrough = []string{"git"}
	f.On([]string{"gh", "extension", "list"}, sys.Result{Stdout: []byte("gh stack\tgithub/gh-stack\tv0.3.0\n")}, nil)
	f.On([]string{"gh", "stack", "view", "--json"}, sys.Result{Stdout: []byte(`{"trunk":"master","currentBranch":"feat","branches":[
		{"name":"feat","isCurrent":true},{"name":"next"},{"name":"old","isMerged":true}]}`)}, nil)
	return f
}

// TestStackSwitch: git switch through the shadow, the real worktree's HEAD
// moves; git refuses a branch another worktree has; gh stack's other
// commands are gh stack's own, not railswitch's.
func TestStackSwitch(t *testing.T) {
	ws, _ := gitWS(t)
	gitIn(t, filepath.Join(ws, "worktrees", "master"), "branch", "next")
	if code, _, _ := runWith(t, stackFake(), "-C", ws, "-w", "feat", "stack", "switch", "-x"); code != 2 {
		t.Errorf("switch -x: exit %d; want 2", code)
	}
	if code, _, _ := runWith(t, stackFake(), "-C", ws, "-w", "feat", "stack", "switch", "master"); code == 0 {
		t.Error("switched to the base worktree's branch")
	}
	f := stackFake()
	code, _, errs := runWith(t, f, "-C", ws, "-w", "feat", "stack", "switch", "next")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	head, err := os.ReadFile(filepath.Join(ws, "worktrees", "master", ".git", "worktrees", "feat", "HEAD"))
	if got := strings.TrimSpace(string(head)); err != nil || got != "ref: refs/heads/next" {
		t.Errorf("feat's HEAD = %q", got)
	}
	i := slices.IndexFunc(f.Calls, func(c []string) bool { return len(c) > 1 && c[0] == "git" && c[1] == "switch" })
	if i < 0 || !slices.ContainsFunc(f.Cmds[i].Env, func(kv string) bool { return strings.HasPrefix(kv, "GIT_COMMON_DIR=") }) {
		t.Errorf("git switch ran without the shadow: %v", f.Calls)
	}
	if slices.ContainsFunc(f.Calls, func(c []string) bool { return c[0] == "gh" }) {
		t.Errorf("a switch ran gh: %v", f.Calls)
	}
	for _, verb := range []string{"up", "down", "push", "rebase"} {
		if code, _, _ := runWith(t, stackFake(), "-C", ws, "-w", "feat", "stack", verb); code != 2 {
			t.Errorf("stack %s: exit %d; want 2 (no such command)", verb, code)
		}
	}
}

// D230: stack checkout runs gh stack checkout <pr> in the worktree through
// the shadow, git limited to safe transports and no prompt; a bad number
// is a usage error; without gh stack it says how to install it.
func TestStackCheckout(t *testing.T) {
	ws, _ := gitWS(t)
	for _, bad := range []string{"0", "-3", "x", "12abc"} {
		if code, _, _ := runWith(t, stackFake(), "-C", ws, "-w", "feat", "stack", "checkout", bad); code != 2 {
			t.Errorf("checkout %q: exit %d; want 2", bad, code)
		}
	}
	if code, _, _ := runWith(t, stackFake(), "-C", ws, "stack", "checkout", "7"); code != 2 {
		t.Errorf("checkout outside a worktree: exit %d; want 2", code)
	}
	none := fakeHost()
	none.Passthrough = []string{"git"}
	if code, _, errs := runWith(t, none, "-C", ws, "-w", "feat", "stack", "checkout", "7"); code != 1 || !strings.Contains(errs, "gh extension install github/gh-stack") {
		t.Errorf("no gh stack: exit %d: %s", code, errs)
	}
	f := stackFake()
	code, _, errs := runWith(t, f, "-C", ws, "-w", "feat", "stack", "checkout", "7")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	i := slices.IndexFunc(f.Calls, func(c []string) bool { return slices.Equal(c, []string{"gh", "stack", "checkout", "7"}) })
	if i < 0 {
		t.Fatalf("no gh stack checkout 7: %v", f.Calls)
	}
	c := f.Cmds[i]
	if c.Dir != filepath.Join(ws, "worktrees", "feat") {
		t.Errorf("ran in %s", c.Dir)
	}
	for _, want := range []string{"GIT_COMMON_DIR=", "GIT_ALLOW_PROTOCOL=https:ssh:git:file", "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0"} {
		if !slices.ContainsFunc(c.Env, func(kv string) bool { return strings.HasPrefix(kv, want) }) {
			t.Errorf("env lacks %s", want)
		}
	}
	f.On([]string{"gh", "stack", "checkout"}, sys.Result{ExitCode: 1, Stderr: []byte("no stack holds #8")}, fmt.Errorf("gh: %w (exit 1)", sys.ErrExit))
	if code, _, errs := runWith(t, f, "-C", ws, "-w", "feat", "stack", "checkout", "8"); code != 1 || !strings.Contains(errs, "no stack holds #8") {
		t.Errorf("a failed checkout: exit %d: %s", code, errs)
	}
}

// TestTerm: a new terminal in the worktree running the user's shell.
func TestTerm(t *testing.T) {
	ws, _ := gitWS(t)
	old := launchTerm
	t.Cleanup(func() { launchTerm = old })
	var gotDir string
	var gotArgv []string
	launchTerm = func(_ context.Context, _ *env, _, dir string, argv []string) error {
		gotDir, gotArgv = dir, argv
		return nil
	}
	t.Setenv("SHELL", "/usr/bin/fish")
	if code, _, errs := runWith(t, stackFake(), "-C", ws, "-w", "feat", "term"); code != 0 {
		t.Fatalf("term: exit %d: %s", code, errs)
	}
	if gotDir != filepath.Join(ws, "worktrees", "feat") || !slices.Equal(gotArgv, []string{"/usr/bin/fish"}) {
		t.Errorf("term launched %s %q", gotDir, gotArgv)
	}
}

func TestUserShell(t *testing.T) {
	t.Setenv("SHELL", "")
	old := passwdFile
	t.Cleanup(func() { passwdFile = old })
	passwdFile = filepath.Join(t.TempDir(), "passwd")
	line := fmt.Sprintf("me:x:%d:%d::/home/me:/usr/bin/zsh\n", os.Getuid(), os.Getgid())
	if err := os.WriteFile(passwdFile, []byte("root:x:0:0::/root:/bin/bash\n"+line), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := userShell(); got != "/usr/bin/zsh" && os.Getuid() != 0 {
		t.Errorf("userShell = %q", got)
	}
}
