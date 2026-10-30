package lifecycle

import (
	"context"
	"fmt"

	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/render"
)

func containerOf(m *render.Model, slug, svc string) string {
	return naming.Container(m.Prefix, slug, svc)
}

// Stop stops every unit of worktree slug, by exact name. A unit systemd
// has not loaded (never rendered or started) is already stopped.
func (l *L) Stop(ctx context.Context, slug string) error {
	m, err := l.model(ctx)
	if err != nil {
		return err
	}
	names, err := m.NamesFor(slug)
	if err != nil {
		return err
	}
	return l.systemd().StopLoaded(ctx, names.Units...)
}

// Restart is Stop, then Up.
func (l *L) Restart(ctx context.Context, slug string) error {
	if err := l.Stop(ctx, slug); err != nil {
		return err
	}
	return l.Up(ctx, slug)
}

// RestartService restarts service svc of worktree slug alone (`systemctl
// --user restart` of its unit): its container is replaced, the rest of
// the stack keeps running. Only a running long-lived service: an agent,
// an on-demand service or a oneshot (deps) is refused, and so is a unit
// that isn't active (up starts it).
func (l *L) RestartService(ctx context.Context, slug, svc string) error {
	m, _, err := l.load(ctx, slug)
	if err != nil {
		return err
	}
	if svc == "" {
		return &UsageError{Msg: "restart: name a service"}
	}
	s, err := service(m, svc)
	if err != nil {
		return &UsageError{Msg: err.Error()}
	}
	if s.Role == render.RoleAgent || s.OnDemand || s.Oneshot {
		return &UsageError{Msg: fmt.Sprintf("restart: %s has no unit to restart (an agent, on-demand or one-shot service)", svc)}
	}
	unit := unitOf(m, slug, svc)
	st, err := l.systemd().UnitState(ctx, unit)
	if err != nil {
		return err
	}
	if st.Active != "active" && st.Active != "activating" {
		return &UsageError{Msg: fmt.Sprintf("restart: %s is not running in %s (%s): railswitch up starts it", svc, slug, st.Active)}
	}
	return l.systemd().Restart(ctx, unit)
}
