package cli

import (
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
)

func TestProjectRmUsage(t *testing.T) {
	lockedWS(t)
	for _, args := range [][]string{{"project"}, {"project", "ls"}, {"project", "rm"}, {"project", "rm", "a", "b"}} {
		if code, _, _ := runIn(t, args...); code != 2 {
			t.Errorf("railswitch %s: exit %d; want 2", strings.Join(args, " "), code)
		}
	}
}

// TestProjectRm: flags after the prefix; non-TTY without --yes refuses;
// with --yes the project is unregistered and its units are gone.
func TestProjectRm(t *testing.T) {
	ws, dirs := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	if code, _, errs := runIn(t, "project", "rm", "filial"); code != 1 || !strings.Contains(errs, "--yes") {
		t.Fatalf("no --yes: exit %d, stderr %q", code, errs)
	}
	code, out, errs := runIn(t, "project", "rm", "filial", "--yes")
	if code != 0 || !strings.Contains(out, "removed project filial") {
		t.Fatalf("exit %d\n%s%s", code, out, errs)
	}
	reg, err := machine.Read(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Projects) != 0 || len(reg.Worktrees) != 0 {
		t.Errorf("registry %+v", reg)
	}
	if code, _, errs := runIn(t, "project", "rm", "filial", "--yes"); code != 1 || !strings.Contains(errs, "not registered") {
		t.Fatalf("second rm: exit %d, stderr %q", code, errs)
	}
}
