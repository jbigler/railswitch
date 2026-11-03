package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

// TestStatsCLI: stats samples only the running containers, by exact name,
// in one podman stats; the others are "not running" with their PIDs limit.
func TestStatsCLI(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	f := fakeHost()
	f.On([]string{"podman", "ps"}, sys.Result{Stdout: []byte(`[{"Names":["filial-feat-rails"],"State":"running"},{"Names":["filial-feat-db"],"State":"exited"}]`)}, nil)
	f.On([]string{"podman", "stats"}, sys.Result{Stdout: []byte(`[{"name":"filial-feat-rails","cpu_percent":"3.50%","mem_usage":"1.611GB / 2.147GB","mem_percent":"75.03%","pids":"12"}]`)}, nil)
	code, out, errs := runWith(t, f, "-C", ws, "-w", "feat", "--json", "stats")
	if code != 0 {
		t.Fatalf("stats: exit %d: %s", code, errs)
	}
	var rows []struct {
		Worktree, Service string
		Running           bool
		CPUPercent        float64 `json:"cpu_percent"`
		MemUsed           int64   `json:"mem_used"`
		MemLimit          int64   `json:"mem_limit"`
		PIDs              int     `json:"pids"`
		PIDsLimit         int     `json:"pids_limit"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) < 2 {
		t.Fatalf("rows %v %v: %s", rows, err, out)
	}
	for _, r := range rows {
		if r.Worktree != "feat" || r.PIDsLimit <= 0 {
			t.Errorf("row %+v", r)
		}
		switch {
		case r.Service == "rails" && (!r.Running || r.CPUPercent != 3.5 || r.MemUsed != 1_611_000_000 || r.MemLimit != 2_147_000_000 || r.PIDs != 12):
			t.Errorf("rails %+v", r)
		case r.Service != "rails" && r.Running:
			t.Errorf("%s running", r.Service)
		}
	}
	var stats []string
	for _, c := range f.Calls {
		if len(c) > 1 && c[1] == "stats" {
			stats = append(stats, strings.Join(c, " "))
		}
	}
	if len(stats) != 1 || stats[0] != "podman stats --no-stream --no-reset --format json filial-feat-rails" {
		t.Errorf("stats calls %q", stats)
	}
	if code, out, _ := runWith(t, f, "-C", ws, "stats", "--all"); code != 0 || !strings.HasPrefix(out, "WORKTREE") || !strings.Contains(out, "1.5GiB / 2GiB (75%)") || !strings.Contains(out, "not running") {
		t.Errorf("stats --all: exit %d\n%s", code, out)
	}
	if code, _, errs := runWith(t, f, "-C", ws, "stats"); code != 2 || !strings.Contains(errs, "not in a worktree") {
		t.Errorf("stats at root: exit %d: %s", code, errs)
	}
	// a container that stopped between ps and stats: listed again, tried once more
	g := fakeHost()
	g.On([]string{"podman", "ps"}, sys.Result{Stdout: []byte(`[{"Names":["filial-feat-rails"],"State":"running"}]`)}, nil)
	g.On([]string{"podman", "stats"}, sys.Result{ExitCode: 125}, fmt.Errorf("podman: %w (exit 125): container is not running", sys.ErrExit))
	n := 0
	if code, _, _ := runWith(t, g, "-C", ws, "-w", "feat", "stats"); code != 1 {
		t.Errorf("stats failing: exit %d", code)
	}
	for _, c := range g.Calls {
		if len(c) > 1 && c[1] == "stats" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("stats tried %d times, want 2", n)
	}
}
