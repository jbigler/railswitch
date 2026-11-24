//go:build integration

package gitshadow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/prs"
	"github.com/jbigler/railswitch/internal/sys"
)

// D230, against the real gh-stack: run through a NewForWrite shadow, gh stack writes
// its catalog into the shadow, not the repository; Persist carries it back.
// Needs gh with the gh-stack extension; nothing reaches GitHub (init only).
func TestGhStackThroughShadow(t *testing.T) {
	if err := exec.Command("gh", "stack", "--help").Run(); err != nil {
		t.Skip("gh stack is not installed")
	}
	base, _ := repo(t)
	gitDir := filepath.Join(base, ".git")
	ctx := context.Background()
	sh, err := NewForWrite(ctx, sys.Exec{}, gitDir)
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Remove()
	for _, b := range []string{"jb/a", "jb/b"} {
		git(t, base, sh.Env(os.Environ()), "branch", b)
	}
	cmd := exec.Command("gh", "stack", "init", "--base", "main", "jb/a", "jb/b")
	cmd.Dir = base
	cmd.Env = append(sh.Env(os.Environ()), "GH_PROMPT_DISABLED=1")
	out, err := cmd.CombinedOutput()
	t.Logf("gh stack init: %v\n%s", err, out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(gitDir, "gh-stack")); err == nil {
		t.Fatal("gh stack wrote the catalog into the repository through the shadow")
	}
	fi, err := os.Lstat(filepath.Join(sh.Dir, "gh-stack"))
	if err != nil || !fi.Mode().IsRegular() {
		ents, _ := os.ReadDir(sh.Dir)
		t.Fatalf("no regular catalog in the shadow (%v %v); shadow has %v", fi, err, ents)
	}
	if err := sh.Persist(ctx, sys.Exec{}, gitDir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "gh-stack"))
	if err != nil || !strings.Contains(string(data), "jb/b") {
		t.Fatalf("catalog not carried back: %v %s", err, data)
	}
	// branch ls reads the real catalog's format.
	if got := prs.StackBranches(gitDir); len(got) != 2 {
		t.Errorf("StackBranches = %v, want jb/a and jb/b; the catalog:\n%s", got, data)
	}
	// A new shadow (as stack ls builds one) links the carried-back
	// catalog: gh stack sees the stack.
	sh2, err := New(ctx, sys.Exec{}, gitDir)
	if err != nil {
		t.Fatal(err)
	}
	defer sh2.Remove()
	view := exec.Command("gh", "stack", "view", "--json")
	view.Dir = base
	view.Env = append(sh2.Env(os.Environ()), "GH_PROMPT_DISABLED=1")
	vo, err := view.CombinedOutput()
	t.Logf("gh stack view: %v\n%s", err, vo)
	if err != nil || !strings.Contains(string(vo), "jb/a") {
		t.Errorf("gh stack view after Persist doesn't show the stack")
	}
}
