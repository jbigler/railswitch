package lifecycle

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
)

// PanicRow is what Panic did to one running container (docs/json.md).
type PanicRow struct {
	Project     string `json:"project"`
	Worktree    string `json:"worktree"`
	Container   string `json:"container"`
	Quarantined bool   `json:"quarantined"`
	Paused      bool   `json:"paused"`
	Error       string `json:"error,omitempty"`
}

// quarantineRules replace a container's firewall table (the hook's
// wt_firewall) with one that lets nothing out but loopback, replies and
// connections already open included.
const quarantineRules = `table inet wt_firewall
delete table inet wt_firewall
table inet wt_firewall {
  chain output {
    type filter hook output priority filter; policy drop;
    oif "lo" accept
  }
}
`

// panicStep bounds each podman call of Panic: a hung container must not
// hold up the others.
const panicStep = 10 * time.Second

// Panic is `railswitch panic` for this project (D181, after terok's): every
// running container of every registered worktree, by exact name, loses its
// network (quarantineRules, loaded from outside it as the firewall hook
// does), then every running agent container is paused, so its processes
// stop and it stays as it is to be looked at. Each step is best effort:
// one that fails is in its row and the rest go on. Containers without a
// name (exec --fresh) are not reached. A restart of the worktree brings it
// back: new containers, new rules.
func (l *L) Panic(ctx context.Context) ([]PanicRow, error) {
	m, err := l.model(ctx)
	if err != nil {
		return nil, err
	}
	p := l.podman()
	var rows []PanicRow
	var agents []int // rows of running agents, paused after every quarantine
	for _, wt := range m.Inputs.Worktrees {
		for _, s := range m.Services {
			name := containerOf(m, wt.Slug, s.ID)
			row := PanicRow{Project: m.Prefix, Worktree: wt.Slug, Container: name}
			pid, status, err := p.PID(ctx, name)
			if err != nil { // podman can't say: report it, never skip it unseen
				row.Error = "inspect: " + err.Error()
				rows = append(rows, row)
				continue
			}
			if pid == 0 || (status != "running" && status != "paused") {
				continue // not running: nothing to stop
			}
			if _, err := l.R.Run(ctx, sys.Cmd{
				Name:    "podman",
				Args:    []string{"unshare", "nsenter", "-t", strconv.Itoa(pid), "-n", "nft", "-f", "-"},
				Stdin:   strings.NewReader(quarantineRules),
				Timeout: panicStep,
			}); err != nil {
				row.Error = "quarantine: " + err.Error()
			} else {
				row.Quarantined = true
			}
			if s.Role == render.RoleAgent {
				if status == "paused" {
					row.Paused = true
				} else {
					agents = append(agents, len(rows))
				}
			}
			rows = append(rows, row)
		}
	}
	// Every network is cut before the first pause: pausing is slower.
	for _, i := range agents {
		r := &rows[i]
		if err := p.Pause(ctx, r.Container); err != nil {
			r.Error = strings.TrimPrefix(r.Error+"; pause: "+err.Error(), "; ")
		} else {
			r.Paused = true
		}
	}
	return rows, nil
}
