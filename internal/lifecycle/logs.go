package lifecycle

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
)

// service is m's service svc ("" = the app), or an error naming the
// services.
func service(m *render.Model, svc string) (*render.Service, error) {
	if svc == "" {
		return &m.Services[0], nil
	}
	i := slices.IndexFunc(m.Services, func(s render.Service) bool { return s.ID == svc })
	if i < 0 {
		ids := make([]string, len(m.Services))
		for j, s := range m.Services {
			ids[j] = s.ID
		}
		return nil, fmt.Errorf("no service %s (services: %s)", svc, strings.Join(ids, ", "))
	}
	return &m.Services[i], nil
}

// Logs streams service svc's logs ("" = the app) of worktree slug to Out: a
// oneshot's (deps) from its unit's journal, any other from podman logs.
// Following has no deadline; it ends with ctx (Ctrl-C).
func (l *L) Logs(ctx context.Context, slug, svc string, follow bool) error {
	m, err := l.model(ctx)
	if err != nil {
		return err
	}
	if _, err := m.NamesFor(slug); err != nil {
		return err
	}
	s, err := service(m, svc)
	if err != nil {
		return err
	}
	c := sys.Cmd{Stdout: l.Out, Stderr: l.Err}
	if follow {
		c.Timeout = sys.NoTimeout
	}
	if s.Oneshot {
		c.Name, c.Args = "journalctl", []string{"--user", "-u", unitOf(m, slug, s.ID)}
		if follow {
			c.Args = append(c.Args, "-f")
		}
		c.Args = append(c.Args, "--no-pager")
	} else {
		c.Name, c.Args = "podman", []string{"logs"}
		if follow {
			c.Args = append(c.Args, "-f")
		}
		c.Args = append(c.Args, containerOf(m, slug, s.ID))
	}
	_, err = l.R.Run(ctx, c)
	return err
}
