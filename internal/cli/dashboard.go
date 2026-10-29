package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jbigler/railswitch/internal/dashconf"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/workspace"
)

const (
	dashboardUsage   = "usage: railswitch dashboard [colors …|serve]"
	colorsUsage      = "usage: railswitch dashboard colors [set|reset …]"
	colorsSetUsage   = "usage: railswitch dashboard colors set [--dark] [--base <preset>] [<token>=#rrggbb…]"
	colorsResetUsage = "usage: railswitch dashboard colors reset [--dark]"
)

// railswitch dashboard: print the dashboard's login link (and how to start
// it when its unit isn't active); `dashboard colors …` edits its colours;
// `dashboard serve` is the dashboard.
func cmdDashboard(ctx context.Context, e *env, args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "colors":
			return dashboardColors(e, args[1:])
		case "serve":
			return dashboardServe(ctx, e, args[1:])
		}
	}
	fs := e.flags("dashboard")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("%s", dashboardUsage)
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	tok, err := machine.DashboardToken(dirs)
	if err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "%slogin?token=%s\n", machine.DashboardURL, tok)
	// The link stands whatever follows: the dashboard is written, enabled
	// (it starts at every login, with no project needed) and started.
	sd := podman.Systemd{R: e.run}
	changed, err := workspace.WriteDashboardUnit(dirs)
	if err == nil && changed {
		err = sd.DaemonReload(ctx)
	}
	if err != nil {
		fmt.Fprintf(e.stderr, "railswitch: dashboard unit: %v\n", err)
		return 0
	}
	props, err := sd.Show(ctx, machine.DashboardService, "ActiveState", "UnitFileState")
	if err != nil {
		fmt.Fprintf(e.stderr, "railswitch: dashboard state unknown: %v\n", err)
		return 0
	}
	started := false
	if props["ActiveState"] != "active" || props["UnitFileState"] != "enabled" {
		if err := sd.EnableNow(ctx, machine.DashboardService); err != nil {
			fmt.Fprintf(e.stderr, "railswitch: starting the dashboard: %v (systemctl --user enable --now %s)\n", err, machine.DashboardService)
			return 0
		}
		started = true
	}
	// A started unit isn't a dashboard that answers: another program on
	// its port (an older dashboard) gets the browser, and this one fails.
	if p := waitDashboard(ctx); !p.Ours {
		fmt.Fprintf(e.stderr, "railswitch: the link won't open the dashboard: %s\nnext: %s\n", p, p.Fix())
		return 1
	}
	if started {
		fmt.Fprintln(e.stdout, "started the dashboard; it starts at every login from now on")
	}
	return 0
}

// waitDashboard probes the dashboard until it answers, for a few seconds
// while nothing does (a unit just started); another program's answer is
// final.
func waitDashboard(ctx context.Context) machine.Probe {
	deadline := time.Now().Add(5 * time.Second)
	for {
		p := machine.ProbeDashboard(ctx, dashboardAddr)
		if p.Ours || p.Other != "" || !time.Now().Before(deadline) {
			return p
		}
		select {
		case <-ctx.Done():
			return p
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// colorsMode is one mode of `dashboard colors --json` (docs/json.md).
type colorsMode struct {
	Base      string            `json:"base"`
	Overrides map[string]string `json:"overrides"`
	Colors    map[string]string `json:"colors"`
}

type colorsReport struct {
	Light    colorsMode `json:"light"`
	Dark     colorsMode `json:"dark"`
	Warnings struct {
		Light []string `json:"light"`
		Dark  []string `json:"dark"`
	} `json:"warnings"`
}

// railswitch dashboard colors [set|reset …]: show or change dashboard.toml.
func dashboardColors(e *env, args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "set":
			return colorsSet(e, args[1:])
		case "reset":
			return colorsReset(e, args[1:])
		}
	}
	fs := e.flags("dashboard colors")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("%s", colorsUsage)
	}
	_, f, err := loadColors()
	if err != nil {
		return e.fail(err)
	}
	r := colorsReport{
		Light: colorsMode{Base: f.Light.Base, Overrides: f.Light.Overrides, Colors: f.Colors(false)},
		Dark:  colorsMode{Base: f.Dark.Base, Overrides: f.Dark.Overrides, Colors: f.Colors(true)},
	}
	r.Warnings.Light = append([]string{}, dashconf.Warnings(r.Light.Colors)...)
	r.Warnings.Dark = append([]string{}, dashconf.Warnings(r.Dark.Colors)...)
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(r); err != nil {
			return e.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "MODE\tTOKEN\tCOLOUR\tFROM")
	for _, m := range []struct {
		name string
		mode colorsMode
	}{{"light", r.Light}, {"dark", r.Dark}} {
		for _, tok := range append(append([]string{}, dashconf.Tokens...), dashconf.Fixed...) {
			from := m.mode.Base
			if _, ok := m.mode.Overrides[tok]; ok {
				from = "set"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.name, tok, m.mode.Colors[tok], from)
		}
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	for _, w := range r.Warnings.Light {
		fmt.Fprintf(e.stdout, "warning: light %s\n", w)
	}
	for _, w := range r.Warnings.Dark {
		fmt.Fprintf(e.stdout, "warning: dark %s\n", w)
	}
	return 0
}

// loadColors reads dashboard.toml from the machine config dir.
func loadColors() (machine.Dirs, *dashconf.File, error) {
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return dirs, nil, err
	}
	f, err := dashconf.Load(dirs)
	return dirs, f, err
}

// railswitch dashboard colors set [--dark] [--base p] [tok=#hex…]: change
// one mode's preset or tokens; warns of text below 4.5:1 on bg.
func colorsSet(e *env, args []string) int {
	fs := e.flags("dashboard colors set")
	dark := fs.Bool("dark", false, "change the dark mode (default: light)")
	base := fs.String("base", "", "the mode's `preset`: "+strings.Join(dashconf.PresetNames, ", "))
	overrides := map[string]string{}
	for {
		if code, ok := parse(fs, args); !ok {
			return code
		}
		if fs.NArg() == 0 {
			break
		}
		tok, val, ok := strings.Cut(fs.Arg(0), "=")
		if !ok {
			return e.usageErr("%s", colorsSetUsage)
		}
		overrides[tok] = val
		args = fs.Args()[1:]
	}
	if *base == "" && len(overrides) == 0 {
		return e.usageErr("%s", colorsSetUsage)
	}
	dirs, f, err := loadColors()
	if err != nil {
		return e.fail(err)
	}
	if err := f.Set(*dark, *base, overrides); err != nil {
		return e.fail(err)
	}
	if err := f.Save(dirs); err != nil {
		return e.fail(err)
	}
	mode := "light"
	if *dark {
		mode = "dark"
	}
	for _, w := range dashconf.Warnings(f.Colors(*dark)) {
		fmt.Fprintf(e.stderr, "railswitch: warning: %s %s\n", mode, w)
	}
	return 0
}

// railswitch dashboard colors reset [--dark]: drop one mode's token
// changes; its preset stays.
func colorsReset(e *env, args []string) int {
	fs := e.flags("dashboard colors reset")
	dark := fs.Bool("dark", false, "reset the dark mode (default: light)")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("%s", colorsResetUsage)
	}
	dirs, f, err := loadColors()
	if err != nil {
		return e.fail(err)
	}
	f.Reset(*dark)
	if err := f.Save(dirs); err != nil {
		return e.fail(err)
	}
	return 0
}
