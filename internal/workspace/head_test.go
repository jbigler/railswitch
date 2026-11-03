package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeadCommit(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	tmp := t.TempDir()
	write := func(rel, data string) {
		t.Helper()
		p := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("base/.git/HEAD", "ref: refs/heads/main\n")
	write("base/.git/refs/heads/main", a+"\n")
	// A linked worktree whose branch is only in the common dir's packed-refs.
	write("base/.git/worktrees/feat/HEAD", "ref: refs/heads/feat\n")
	write("base/.git/worktrees/feat/commondir", "../..\n")
	write("base/.git/packed-refs", "# pack-refs with: peeled\n"+a+" refs/heads/main\n"+b+" refs/heads/feat\n")
	write("feat/.git", "gitdir: "+filepath.Join(tmp, "base/.git/worktrees/feat")+"\n")
	write("detached/.git/HEAD", b+"\n")
	write("escape/.git/HEAD", "ref: refs/heads/../../../x\n")
	write("junk/.git/HEAD", "ref: refs/heads/main\n")
	write("junk/.git/refs/heads/main", "not a commit\n")
	write("unborn/.git/HEAD", "ref: refs/heads/none\n")
	for dir, want := range map[string]string{"base": a, "feat": b, "detached": b, "escape": "", "junk": "", "unborn": "", "missing": ""} {
		if got := HeadCommit(filepath.Join(tmp, dir)); got != want {
			t.Errorf("HeadCommit(%s) = %q, want %q", dir, got, want)
		}
	}
}
