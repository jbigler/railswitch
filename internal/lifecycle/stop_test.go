package lifecycle

import (
	"errors"
	"testing"
)

// TestRestartService: a running service's unit alone is restarted, a
// service agent's too (D148); a stopped one, an on-demand agent, a
// oneshot or an unknown name is refused and nothing restarts.
func TestRestartService(t *testing.T) {
	e := setup(t)
	unit := "filial-feat-db.service"
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=active\n", unit)
	if err := e.l.RestartService(ctx, "feat", "db"); err != nil {
		t.Fatal(err)
	}
	calls := e.calls()
	if n := count(calls, "systemctl --user restart "+unit); n != 1 {
		t.Fatalf("restarts of %s: %d\n%s", unit, n, dump(calls))
	}
	if n := count(calls, "systemctl --user restart"); n != 1 {
		t.Errorf("other units restarted:\n%s", dump(calls))
	}

	e = setup(t)
	agent := "filial-feat-agent-probe.service"
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=active\n", agent)
	if err := e.l.RestartService(ctx, "feat", "agent-probe"); err != nil {
		t.Fatalf("a service agent: %v", err)
	}
	if n := count(e.calls(), "systemctl --user restart "+agent); n != 1 {
		t.Errorf("restarts of %s: %d", agent, n)
	}
	var u *UsageError
	if err := e.l.RestartService(ctx, "feat", "agent-claude"); !errors.As(err, &u) { // on-demand: no unit
		t.Errorf("an on-demand agent: %v; want a usage error", err)
	}

	e = setup(t)
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=inactive\n", unit)
	for _, svc := range []string{"db", "deps", "agent-claude", "nope", ""} {
		var u *UsageError
		if err := e.l.RestartService(ctx, "feat", svc); !errors.As(err, &u) {
			t.Errorf("%q: %v; want a usage error", svc, err)
		}
	}
	if n := count(e.calls(), "systemctl --user restart"); n != 0 {
		t.Errorf("%d restarts after refusals", n)
	}
}
