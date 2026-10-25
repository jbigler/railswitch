package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"text/tabwriter"
)

// StatusRow is one service of `railswitch status` (and one element of its
// --json, docs/json.md).
type StatusRow struct {
	Service string `json:"service"`
	Role    string `json:"role"`
	State   string `json:"state"`  // systemd "<ActiveState>/<SubState>", e.g. "active/running"
	Health  string `json:"health"` // podman's health status; "" without a health check or container
}

// Status reports each service of worktree slug: its unit's state and, for
// a service with a health check (the app included, D34), its health. With
// asJSON it prints the rows as one JSON array.
func (l *L) Status(ctx context.Context, slug string, asJSON bool) error {
	m, err := l.model(ctx)
	if err != nil {
		return err
	}
	if _, err := m.NamesFor(slug); err != nil { // refuses an unregistered slug
		return err
	}
	rows := []StatusRow{}
	for _, s := range m.Services {
		props, err := l.systemd().Show(ctx, unitOf(m, slug, s.ID), "ActiveState", "SubState")
		if err != nil {
			return err
		}
		r := StatusRow{Service: s.ID, Role: s.Role, State: props["ActiveState"] + "/" + props["SubState"]}
		if s.Health != nil {
			if r.Health, err = l.podman().Health(ctx, containerOf(m, slug, s.ID)); err != nil {
				return err
			}
		}
		rows = append(rows, r)
	}
	if asJSON {
		return json.NewEncoder(l.Out).Encode(rows)
	}
	tw := tabwriter.NewWriter(l.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tROLE\tUNIT-STATE\tHEALTH")
	for _, r := range rows {
		h := r.Health
		if h == "" {
			h = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Service, r.Role, r.State, h)
	}
	return tw.Flush()
}
