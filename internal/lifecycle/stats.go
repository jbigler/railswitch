package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
)

// StatsRow is one service of `railswitch stats` (and one element of its
// --json, docs/json.md). A service whose container is not running has
// Running false and zero figures.
type StatsRow struct {
	Worktree   string  `json:"worktree"`
	Service    string  `json:"service"`
	Role       string  `json:"role"`
	Container  string  `json:"container"`
	Running    bool    `json:"running"`
	CPUPercent float64 `json:"cpu_percent"`
	MemUsed    int64   `json:"mem_used"`
	MemLimit   int64   `json:"mem_limit"`
	MemPercent float64 `json:"mem_percent"`
	PIDs       int     `json:"pids"`
	PIDsLimit  int     `json:"pids_limit"` // the unit's PidsLimit= (D34)
}

// Stats reports each service's CPU, memory and PIDs against its limits
// (D161) for worktree slug, or every worktree when slug is "": one `podman
// ps` for which containers run, then one `podman stats` of those by name.
func (l *L) Stats(ctx context.Context, slug string, asJSON bool) error {
	m, err := l.modelFor(workspace.LocalHost())
	if err != nil {
		return err
	}
	slugs := []string{slug}
	if slug == "" {
		slugs = nil
		for _, w := range m.Inputs.Worktrees {
			slugs = append(slugs, w.Slug)
		}
	} else if _, err := m.NamesFor(slug); err != nil { // refuses an unregistered slug
		return err
	}
	var stats map[string]podman.Stat
	var running map[string]string
	// A container that stops between the two calls fails podman stats:
	// list the running ones again and retry once.
	for try := 0; ; try++ {
		if running, err = l.podman().Labels(ctx, ""); err != nil {
			return err
		}
		var names []string
		for _, sl := range slugs {
			for _, s := range m.Services {
				if _, ok := running[containerOf(m, sl, s.ID)]; ok {
					names = append(names, containerOf(m, sl, s.ID))
				}
			}
		}
		if stats, err = l.podman().Stats(ctx, names); err == nil {
			break
		}
		if try > 0 || !errors.Is(err, sys.ErrExit) {
			return err
		}
	}
	rows := []StatsRow{}
	for _, sl := range slugs {
		for _, s := range m.Services {
			ctr := containerOf(m, sl, s.ID)
			st, ok := stats[ctr]
			rows = append(rows, StatsRow{Worktree: sl, Service: s.ID, Role: s.Role, Container: ctr, Running: ok,
				CPUPercent: st.CPUPercent, MemUsed: st.MemUsed, MemLimit: st.MemLimit, MemPercent: st.MemPercent,
				PIDs: st.PIDs, PIDsLimit: s.Unit.PidsLimit})
		}
	}
	if asJSON {
		return json.NewEncoder(l.Out).Encode(rows)
	}
	tw := tabwriter.NewWriter(l.Out, 0, 4, 2, ' ', 0)
	head := "SERVICE\tCPU\tMEMORY\tPIDS"
	if slug == "" {
		head = "WORKTREE\t" + head
	}
	fmt.Fprintln(tw, head)
	for _, r := range rows {
		if slug == "" {
			fmt.Fprintf(tw, "%s\t", r.Worktree)
		}
		if !r.Running {
			fmt.Fprintf(tw, "%s\t-\tnot running\t-\n", r.Service)
			continue
		}
		fmt.Fprintf(tw, "%s\t%.1f%%\t%s / %s (%.0f%%)\t%d / %d\n", r.Service, r.CPUPercent, size(r.MemUsed), size(r.MemLimit), r.MemPercent, r.PIDs, r.PIDsLimit)
	}
	return tw.Flush()
}

// size is n bytes in binary units, e.g. "312MiB", "1.5GiB".
func size(n int64) string {
	f, i := float64(n), 0
	for f >= 1024 && i < 4 {
		f /= 1024
		i++
	}
	return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", f), "0"), ".") + []string{"B", "KiB", "MiB", "GiB", "TiB"}[i]
}
