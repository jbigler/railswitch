package cli

import (
	"encoding/json"
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
