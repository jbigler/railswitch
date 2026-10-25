package lifecycle

import (
	"context"

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
