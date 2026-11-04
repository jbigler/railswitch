package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/agentusage"
	"github.com/jbigler/railswitch/internal/sys"
)

// D162: agent usage records the counters it reads, then reports the
// growth; a drop is a new session.
func TestAgentUsageCLI(t *testing.T) {
	ws, dirs := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	status := filepath.Join(ws, ".railswitch", "status", "feat", "claude.json")
	report := func(input int, args ...string) map[string]any {
		t.Helper()
		data := fmt.Sprintf(`{"v":1,"state":"done","message":"","ts":%d,"usage":{"input":%d,"output":1,"cache_read":0,"cache_write":0}}`, time.Now().Unix(), input)
		if err := os.WriteFile(status, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		code, out, errs := runIn(t, append([]string{"-C", ws, "--json"}, args...)...)
		if code != 0 {
			t.Fatalf("agent usage: exit %d: %s", code, errs)
		}
		var rep map[string]any
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return rep
	}
	report(100, "agent", "usage", "--all")
	report(150, "-w", "feat", "agent", "usage")
	rep := report(20, "agent", "usage", "--all", "--days", "3")
	wts, _ := rep["worktrees"].([]any)
	if len(wts) != 1 || rep["days"] != float64(3) || len(rep["daily"].([]any)) != 3 {
		t.Fatalf("report %v", rep)
	}
	w := wts[0].(map[string]any)
	if w["worktree"] != "feat" || w["total"] != float64(170+2) || w["input"] != float64(170) || w["cost"] != nil || w["runaway"] != false {
		t.Errorf("feat %v", w)
	}
	// The overview, the dashboard's poll, records too.
	if err := os.WriteFile(status, fmt.Appendf(nil, `{"v":1,"state":"done","message":"","ts":%d,"usage":{"input":60,"output":1,"cache_read":0,"cache_write":0}}`, time.Now().Unix()), 0o600); err != nil {
		t.Fatal(err)
	}
	f := fakeHost()
	f.On([]string{"podman", "ps"}, sys.Result{Stdout: []byte("[]")}, nil)
	if code, _, errs := runWith(t, f, "-C", ws, "--json", "overview"); code != 0 {
		t.Fatalf("overview: exit %d: %s", code, errs)
	}
	if got, err := agentusage.Read(agentusage.Path(dirs.Data, "filial"), 1, "", time.Now()); err != nil || len(got.Worktrees) != 1 || got.Worktrees[0].Input != 210 {
		t.Errorf("after overview %+v %v", got, err)
	}
	code, out, _ := runIn(t, "-C", ws, "-w", "feat", "agent", "usage")
	if lines := strings.Split(strings.TrimRight(out, "\n"), "\n"); code != 0 || len(lines) != 2 || !strings.HasPrefix(lines[0], "DAY") || !strings.Contains(lines[1], "feat") {
		t.Errorf("text: exit %d\n%s", code, out)
	}
	if code, _, _ := runIn(t, "-C", ws, "-w", "feat", "agent", "usage", "--days", "0"); code != 2 {
		t.Errorf("--days 0: exit %d; want 2", code)
	}
}
