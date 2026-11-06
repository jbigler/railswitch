package lifecycle

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/podman"
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

// Panic is `railswitch panic` (D181, after terok's): every container of
// every registered worktree loses its network (quarantineRules, loaded from
// outside it as the firewall hook does), then every one of them is paused,
// so its processes stop, systemd can't restart it with fresh rules, and it
// stays as it is to be looked at. A container belongs to a worktree when
// its name starts exactly with "<prefix>-<slug>-": the units' containers
// and the transient ones (deps, exec --fresh, on-demand agents), which are
// named so. It needs no workspace, so a project that doesn't load is still
// reached. Each step is best effort: one that fails is in its row and the
// rest go on. A restart of the worktree brings it back: new containers,
// new rules.
func Panic(ctx context.Context, r sys.Runner, wts []machine.WorktreeEntry) ([]PanicRow, error) {
	p := podman.Podman{R: r}
	res, err := r.Run(ctx, sys.Cmd{Name: "podman", Args: []string{"ps", "-a", "--format", "{{.Names}}"}, Timeout: panicStep})
	if err != nil {
		return nil, err
	}
	var rows []PanicRow
	var running []int // rows of running containers, paused after every quarantine
	for _, name := range strings.Fields(string(res.Stdout)) {
		// The longest "<prefix>-<slug>-" wins: slug feat-x is not slug feat's.
		var wt *machine.WorktreeEntry
		for i, w := range wts {
			if strings.HasPrefix(name, w.Prefix+"-"+w.Slug+"-") && (wt == nil || len(w.Prefix+w.Slug) > len(wt.Prefix+wt.Slug)) {
				wt = &wts[i]
			}
		}
		if wt == nil {
			continue
		}
		row := PanicRow{Project: wt.Prefix, Worktree: wt.Slug, Container: name}
		pid, status, err := p.PID(ctx, name)
		if err != nil { // podman can't say: report it, never skip it unseen
			row.Error = "inspect: " + err.Error()
			rows = append(rows, row)
			continue
		}
		if pid == 0 || (status != "running" && status != "paused") {
			continue // not running: nothing to stop
		}
		if _, err := r.Run(ctx, sys.Cmd{
			Name:    "podman",
			Args:    []string{"unshare", "nsenter", "-t", strconv.Itoa(pid), "-n", "nft", "-f", "-"},
			Stdin:   strings.NewReader(quarantineRules),
			Timeout: panicStep,
		}); err != nil {
			row.Error = "quarantine: " + err.Error()
		} else {
			row.Quarantined = true
		}
		if status == "paused" {
			row.Paused = true
		} else {
			running = append(running, len(rows))
		}
		rows = append(rows, row)
	}
	// Every network is cut before the first pause: pausing is slower.
	for _, i := range running {
		r := &rows[i]
		if err := p.Pause(ctx, r.Container); err != nil {
			r.Error = strings.TrimPrefix(r.Error+"; pause: "+err.Error(), "; ")
		} else {
			r.Paused = true
		}
	}
	return rows, nil
}
