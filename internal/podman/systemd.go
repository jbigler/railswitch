package podman

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

// StartTimeout is the default bound of `systemctl --user start`/`restart`
// (they wait for image pulls and healthy dependencies) when Systemd.Timeout
// is unset; up derives its own from the units it starts.
const StartTimeout = 20 * time.Minute

// Systemd runs `systemctl --user` through R.
type Systemd struct {
	R       sys.Runner
	Timeout time.Duration // start and restart deadline; 0 = StartTimeout
}

func (s Systemd) startTimeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return StartTimeout
}

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

// EnableNow runs `systemctl --user enable --now <units…>`: started now and
// at every login.
func (s Systemd) EnableNow(ctx context.Context, units ...string) error {
	return s.units(ctx, s.startTimeout(), "enable", append([]string{"--now"}, units...))
}

// Start runs `systemctl --user start <units…>`.
func (s Systemd) Start(ctx context.Context, units ...string) error {
	return s.units(ctx, s.startTimeout(), "start", units)
}

// StartOnly runs `systemctl --user start --job-mode=ignore-requirements
// <units…>`: the units start without pulling in their Requires= (an
// exited oneshot among them would run again).
func (s Systemd) StartOnly(ctx context.Context, units ...string) error {
	return s.units(ctx, s.startTimeout(), "start", append([]string{"--job-mode=ignore-requirements"}, units...))
}

// stopTimeout bounds one `systemctl stop`: above the longest stop a unit
// may take (a data service's TimeoutStopSec, 90 s: a database flushing
// before it exits), so systemd, not this call, decides when a stop failed.
const stopTimeout = 2 * time.Minute

// Stop runs `systemctl --user stop <units…>` (they stop together, so one
// stopTimeout bounds them all).
func (s Systemd) Stop(ctx context.Context, units ...string) error {
	return s.units(ctx, stopTimeout, "stop", units)
}

// unitNotLoaded is systemctl's exit code for a unit it doesn't know.
const unitNotLoaded = 5

// StopLoaded runs `systemctl --user stop <unit>` for each unit in turn; a
// unit systemd doesn't know ("not loaded", exit 5: never rendered, or its
// file is gone) counts as stopped.
func (s Systemd) StopLoaded(ctx context.Context, units ...string) error {
	for _, u := range units {
		r, err := s.run(ctx, stopTimeout, "stop", u)
		if err != nil && !(errors.Is(err, sys.ErrExit) && r.ExitCode == unitNotLoaded) {
			return err
		}
	}
	return nil
}

// Restart runs (Timeout as Start) `systemctl --user restart <units…>`.
func (s Systemd) Restart(ctx context.Context, units ...string) error {
	return s.units(ctx, s.startTimeout(), "restart", units)
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

// ShowMany runs one `systemctl --user show -p Id,A,B <units…>` and parses
// its blocks (one per unit, separated by a blank line) into each unit's
// K=V map, keyed by its Id. No units: no call.
func (s Systemd) ShowMany(ctx context.Context, units []string, props ...string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	if len(units) == 0 {
		return out, nil
	}
	args := append([]string{"show", "-p", strings.Join(append([]string{"Id"}, props...), ",")}, units...)
	r, err := s.run(ctx, 0, args...)
	if err != nil {
		return nil, err
	}
	block := map[string]string{}
	flush := func() {
		if id := block["Id"]; id != "" {
			out[id] = block
		}
		block = map[string]string{}
	}
	for line := range strings.Lines(string(r.Stdout)) {
		line = strings.TrimRight(line, "\n")
		if line == "" {
			flush()
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			block[k] = v
		}
	}
	flush()
	return out, nil
}

// UnitState is what systemd knows of a unit.
type UnitState struct {
	Load       string // LoadState: "loaded", "not-found", …
	NeedReload bool   // NeedDaemonReload: its file changed since the last daemon-reload
	Active     string // ActiveState: "active", "inactive", "failed", …
	ExecStart  string // ExecStart as systemd shows it: the loaded command line
	After      string // After: the loaded ordering, space-separated units
}

// UnitState reads unit's LoadState, NeedDaemonReload, ActiveState,
// ExecStart and After.
func (s Systemd) UnitState(ctx context.Context, unit string) (UnitState, error) {
	m, err := s.Show(ctx, unit, "LoadState", "NeedDaemonReload", "ActiveState", "ExecStart", "After")
	if err != nil {
		return UnitState{}, err
	}
	return UnitState{Load: m["LoadState"], NeedReload: m["NeedDaemonReload"] == "yes", Active: m["ActiveState"], ExecStart: m["ExecStart"], After: m["After"]}, nil
}

// activeEnterLayout is ActiveEnterTimestamp under --timestamp=us+utc.
const activeEnterLayout = "Mon 2006-01-02 15:04:05.000000 UTC"

// ActiveSince is when unit last entered the active state, read exactly
// (microseconds, UTC) with `systemctl --user show --timestamp=us+utc -p
// ActiveEnterTimestamp <unit>`; the zero time when it never has.
func (s Systemd) ActiveSince(ctx context.Context, unit string) (time.Time, error) {
	r, err := s.run(ctx, 0, "show", "--timestamp=us+utc", "-p", "ActiveEnterTimestamp", unit)
	if err != nil {
		return time.Time{}, err
	}
	v, ok := strings.CutPrefix(strings.TrimSpace(string(r.Stdout)), "ActiveEnterTimestamp=")
	if !ok {
		return time.Time{}, fmt.Errorf("systemctl show %s: no ActiveEnterTimestamp in %q", unit, r.Stdout)
	}
	if v == "" || v == "n/a" {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation(activeEnterLayout, v, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("systemctl show %s: ActiveEnterTimestamp %q: %w", unit, v, err)
	}
	return t, nil
}
