package cli

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
)

func TestProjectRmUsage(t *testing.T) {
	lockedWS(t)
	for _, args := range [][]string{{"project"}, {"project", "bogus"}, {"project", "rm"}, {"project", "rm", "a", "b"}} {
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

// TestTopLevelNewGone: new and init live under project only.
func TestTopLevelNewGone(t *testing.T) {
	lockedWS(t)
	for _, cmd := range []string{"new", "init"} {
		if code, _, errs := runIn(t, cmd); code != 2 || !strings.Contains(errs, "unknown command") {
			t.Errorf("railswitch %s: exit %d, stderr %q", cmd, code, errs)
		}
	}
}

// lsTwo registers a second project "gone" whose workspace doesn't exist
// next to filial (two worktrees) and returns filial's workspace.
func lsTwo(t *testing.T) (ws, gone string) {
	t.Helper()
	ws, dirs := filialWS(t)
	gone = filepath.Join(t.TempDir(), "gone")
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	if _, err := reg.AddProject("gone", gone, nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	return ws, gone
}

func TestProjectLs(t *testing.T) {
	ws, gone := lsTwo(t)
	out := mustRun(t, "project", "ls")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "PREFIX") {
		t.Fatalf("output:\n%s", out)
	}
	for _, want := range [][]string{
		{"filial", ws, "10.214.0.0/24", "2"},
		{"gone", gone, "10.214.1.0/24", "0", "workspace missing"},
	} {
		found := false
		for _, l := range lines[1:] {
			if strings.Fields(l)[0] == want[0] {
				found = true
				for _, w := range want {
					if !strings.Contains(l, w) {
						t.Errorf("line %q lacks %q", l, w)
					}
				}
				if want[0] == "filial" && strings.Contains(l, "missing") {
					t.Errorf("line %q: filial's workspace exists", l)
				}
			}
		}
		if !found {
			t.Errorf("no line for %s:\n%s", want[0], out)
		}
	}
	if code, _, _ := runIn(t, "project", "ls", "x"); code != 2 {
		t.Errorf("project ls x: exit %d; want 2", code)
	}
}

func TestProjectLsJSON(t *testing.T) {
	ws, gone := lsTwo(t)
	var got []projectRow
	if err := json.Unmarshal([]byte(mustRun(t, "--json", "project", "ls")), &got); err != nil {
		t.Fatal(err)
	}
	want := []projectRow{
		{Prefix: "filial", Workspace: ws, Subnet: "10.214.0.0/24", Worktrees: 2, WorkspaceMissing: false},
		{Prefix: "gone", Workspace: gone, Subnet: "10.214.1.0/24", Worktrees: 0, WorkspaceMissing: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}
