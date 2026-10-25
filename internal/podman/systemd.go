package podman

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

// StartTimeout bounds `systemctl --user start` (it waits for image pulls
// and healthy dependencies).
const StartTimeout = 10 * time.Minute

// Systemd runs `systemctl --user` through R.
type Systemd struct{ R sys.Runner }

func (s Systemd) run(ctx context.Context, timeout time.Duration, args ...string) (sys.Result, error) {
	return s.R.Run(ctx, sys.Cmd{Name: "systemctl", Args: append([]string{"--user"}, args...), Timeout: timeout})
}

func (s Systemd) units(ctx context.Context, timeout time.Duration, verb string, units []string) error {
	_, err := s.run(ctx, timeout, append([]string{verb}, units...)...)
	return err
}

// DaemonReload runs `systemctl --user daemon-reload`.
func (s Systemd) DaemonReload(ctx context.Context) error {
	_, err := s.run(ctx, 0, "daemon-reload")
	return err
}

// Start runs `systemctl --user start <units…>`.
func (s Systemd) Start(ctx context.Context, units ...string) error {
	return s.units(ctx, StartTimeout, "start", units)
}

// Stop runs `systemctl --user stop <units…>`.
func (s Systemd) Stop(ctx context.Context, units ...string) error {
	return s.units(ctx, 0, "stop", units)
}

// unitNotLoaded is systemctl's exit code for a unit it doesn't know.
const unitNotLoaded = 5

// StopLoaded runs `systemctl --user stop <unit>` for each unit in turn; a
// unit systemd doesn't know ("not loaded", exit 5: never rendered, or its
// file is gone) counts as stopped.
func (s Systemd) StopLoaded(ctx context.Context, units ...string) error {
	for _, u := range units {
		r, err := s.run(ctx, 0, "stop", u)
		if err != nil && !(errors.Is(err, sys.ErrExit) && r.ExitCode == unitNotLoaded) {
			return err
		}
	}
	return nil
}

// Restart runs (Timeout StartTimeout) `systemctl --user restart <units…>`.
func (s Systemd) Restart(ctx context.Context, units ...string) error {
	return s.units(ctx, StartTimeout, "restart", units)
}

// ResetFailed runs `systemctl --user reset-failed <units…>`.
func (s Systemd) ResetFailed(ctx context.Context, units ...string) error {
	return s.units(ctx, 0, "reset-failed", units)
}

// Show runs `systemctl --user show -p A,B <unit>` and parses its K=V lines.
func (s Systemd) Show(ctx context.Context, unit string, props ...string) (map[string]string, error) {
	r, err := s.run(ctx, 0, "show", "-p", strings.Join(props, ","), unit)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for line := range strings.Lines(string(r.Stdout)) {
		if k, v, ok := strings.Cut(strings.TrimRight(line, "\n"), "="); ok {
			m[k] = v
		}
	}
	return m, nil
}

// LoadState is unit's systemd LoadState, e.g. "loaded" or "not-found".
func (s Systemd) LoadState(ctx context.Context, unit string) (string, error) {
	r, err := s.run(ctx, 0, "show", "-p", "LoadState", "--value", unit)
	return strings.TrimSpace(string(r.Stdout)), err
}
