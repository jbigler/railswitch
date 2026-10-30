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

func TestStackText(t *testing.T) {
	// A merged branch below doesn't count; the current one is 2 of 2.
	r := stackRow{Stack: &prs.Stack{Trunk: "main", Current: "c", Branches: []prs.StackBranch{
		{Name: "a", Merged: true}, {Name: "b"}, {Name: "c", Current: true, NeedsRebase: true},
	}}}
	if got := stackText(r); got != "2/2 on main, restack needed" {
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

// TestStackSwitch: only another open branch of the stack, checked out by
// git through the shadow: the real worktree's HEAD moves.
func TestStackSwitch(t *testing.T) {
	ws, _ := gitWS(t)
	gitIn(t, filepath.Join(ws, "master"), "branch", "next")
	gitIn(t, filepath.Join(ws, "master"), "branch", "old")
	for _, b := range []string{"master", "old", "feat", "-x"} {
		if code, _, _ := runWith(t, stackFake(), "-C", ws, "-w", "feat", "stack", "switch", b); code != 2 {
			t.Errorf("switch %s: exit %d; want 2", b, code)
		}
	}
	f := stackFake()
	code, _, errs := runWith(t, f, "-C", ws, "-w", "feat", "stack", "switch", "next")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	head, err := os.ReadFile(filepath.Join(ws, "master", ".git", "worktrees", "feat", "HEAD"))
	if got := strings.TrimSpace(string(head)); err != nil || got != "ref: refs/heads/next" {
		t.Errorf("feat's HEAD = %q", got)
	}
	i := slices.IndexFunc(f.Calls, func(c []string) bool { return len(c) > 1 && c[0] == "git" && c[1] == "switch" })
	if i < 0 || !slices.ContainsFunc(f.Cmds[i].Env, func(kv string) bool { return strings.HasPrefix(kv, "GIT_COMMON_DIR=") }) {
		t.Errorf("git switch ran without the shadow: %v", f.Calls)
	}
}

// TestStackPush: gh stack push in the worktree, through the shadow, no
// prompt.
func TestStackPush(t *testing.T) {
	ws, _ := gitWS(t)
	f := stackFake()
	code, _, errs := runWith(t, f, "-C", ws, "-w", "feat", "stack", "push")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	i := slices.IndexFunc(f.Calls, func(c []string) bool { return slices.Equal(c, []string{"gh", "stack", "push"}) })
	if i < 0 {
		t.Fatalf("no gh stack push: %v", f.Calls)
	}
	c := f.Cmds[i]
	if c.Dir != filepath.Join(ws, "feat") || !slices.Contains(c.Env, "GH_PROMPT_DISABLED=1") ||
		!slices.ContainsFunc(c.Env, func(kv string) bool { return strings.HasPrefix(kv, "GIT_COMMON_DIR=") }) {
		t.Errorf("push cmd = dir %s env %v", c.Dir, c.Env)
	}
}

// TestStackRebaseNewTerm and TestTerm: a new terminal in the worktree,
// running the rebase (a shell when it stops) or the user's shell.
func TestStackRebaseNewTerm(t *testing.T) {
	ws, _ := gitWS(t)
	oldExe, oldLaunch := executable, launchTerm
	t.Cleanup(func() { executable, launchTerm = oldExe, oldLaunch })
	executable = func() (string, error) { return "/bin/railswitch", nil }
	var gotDir string
	var gotArgv []string
	launchTerm = func(_ context.Context, _ *env, _, dir string, argv []string) error {
		gotDir, gotArgv = dir, argv
		return nil
	}
	if code, _, errs := runWith(t, stackFake(), "-C", ws, "-w", "feat", "stack", "rebase", "--new-term", "--continue"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	want := []string{"/bin/railswitch", "-C", ws, "-w", "feat", "stack", "rebase", "--shell-on-stop", "--continue"}
	if gotDir != filepath.Join(ws, "feat") || !slices.Equal(gotArgv, want) {
		t.Errorf("launched %s %q; want %q", gotDir, gotArgv, want)
	}
	t.Setenv("SHELL", "/usr/bin/fish")
	if code, _, errs := runWith(t, stackFake(), "-C", ws, "-w", "feat", "term"); code != 0 {
		t.Fatalf("term: exit %d: %s", code, errs)
	}
	if gotDir != filepath.Join(ws, "feat") || !slices.Equal(gotArgv, []string{"/usr/bin/fish"}) {
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

// TestStackRebase: on the repository itself, hooks off, no shadow; a
// config key a container could have planted refuses it.
func TestStackRebase(t *testing.T) {
	ws, _ := gitWS(t)
	f := stackFake()
	code, _, errs := runWith(t, f, "-C", ws, "-w", "feat", "stack", "rebase", "--continue")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	i := slices.IndexFunc(f.Calls, func(c []string) bool { return slices.Equal(c, []string{"gh", "stack", "rebase", "--continue"}) })
	if i < 0 {
		t.Fatalf("no gh stack rebase: %v", f.Calls)
	}
	c := f.Cmds[i]
	if c.Dir != filepath.Join(ws, "feat") || !slices.Contains(c.Env, "GIT_CONFIG_VALUE_1=/dev/null") ||
		slices.ContainsFunc(c.Env, func(kv string) bool { return strings.HasPrefix(kv, "GIT_COMMON_DIR=") }) {
		t.Errorf("rebase cmd = dir %s env %v", c.Dir, c.Env)
	}

	gitIn(t, filepath.Join(ws, "master"), "config", "alias.st", "!touch /tmp/x")
	f = stackFake()
	code, _, errs = runWith(t, f, "-C", ws, "-w", "feat", "stack", "rebase")
	if code == 0 || !strings.Contains(errs, "alias.st") {
		t.Errorf("planted alias: exit %d: %s", code, errs)
	}
	if slices.ContainsFunc(f.Calls, func(c []string) bool { return len(c) > 2 && c[0] == "gh" && c[2] == "rebase" }) {
		t.Error("gh stack rebase ran")
	}
}
