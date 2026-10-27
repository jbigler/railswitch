package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

func TestStatusAndExecCLI(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	f := fakeHost()
	f.On([]string{"systemctl", "--user", "show"}, sys.Result{Stdout: []byte("ActiveState=inactive\nSubState=dead\n")}, nil)
	code, out, errs := runWith(t, f, "-C", ws, "-w", "feat", "--json", "status")
	if code != 0 {
		t.Fatalf("status: exit %d: %s", code, errs)
	}
	var rows []struct{ Service, Role, State, Health string }
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) == 0 || rows[0].State != "inactive/dead" {
		t.Fatalf("rows %v %v: %s", rows, err, out)
	}
	if code, _, errs := runWith(t, f, "-C", ws, "exec", "--", "ls"); code != 2 || !strings.Contains(errs, "not in a worktree") {
		t.Fatalf("exec at root: exit %d: %s", code, errs)
	}
	if code, _, _ := runWith(t, f, "-C", ws, "-w", "feat", "deps"); code != 2 {
		t.Fatalf("deps without a command: exit %d", code)
	}
}

func TestOverviewCLI(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	f := fakeHost()
	code, out, errs := runWith(t, f, "-C", ws, "--json", "overview")
	if code != 0 {
		t.Fatalf("overview: exit %d: %s", code, errs)
	}
	var ov struct {
		Prefix    string
		Worktrees []struct{ Slug, Stack string }
	}
	if err := json.Unmarshal([]byte(out), &ov); err != nil || ov.Prefix != "filial" || len(ov.Worktrees) == 0 || ov.Worktrees[0].Stack != "stopped" {
		t.Fatalf("overview %+v %v: %s", ov, err, out)
	}
	if code, _, _ := runWith(t, f, "-C", ws, "overview", "x"); code != 2 {
		t.Fatalf("overview x: exit %d; want 2", code)
	}
}

// TestExecDepsExitCode: exec and deps exit with the container command's
// exit code, without a railswitch error line.
func TestExecDepsExitCode(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	mustRun(t, "-C", ws, "render")
	f := fakeHost()
	f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}"}, sys.Result{Stdout: []byte("true\n")}, nil)
	exit3 := fmt.Errorf("podman: %w (exit 3)", sys.ErrExit)
	f.On([]string{"podman", "exec"}, sys.Result{ExitCode: 3}, exit3)
	f.On([]string{"podman", "--hooks-dir"}, sys.Result{ExitCode: 3}, exit3)
	for _, args := range [][]string{{"exec", "--", "false"}, {"deps", "--", "false"}} {
		code, _, errs := runWith(t, f, append([]string{"-C", ws, "-w", "feat"}, args...)...)
		if code != 3 || errs != "" {
			t.Errorf("%s: exit %d, stderr %q; want 3 and no stderr", args[0], code, errs)
		}
	}
}
