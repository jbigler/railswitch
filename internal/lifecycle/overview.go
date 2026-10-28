package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/unithash"
	"github.com/jbigler/railswitch/internal/workspace"
)

// OverviewService is one service of a worktree in `railswitch overview`:
// a status row without health (docs/json.md).
type OverviewService struct {
	Service string `json:"service"`
	Role    string `json:"role"`
	State   string `json:"state"` // as StatusRow.State
}

// OverviewWorktree is one worktree of `railswitch overview`.
type OverviewWorktree struct {
	Slug     string            `json:"slug"`
	ID       int               `json:"id"`
	Branch   string            `json:"branch"` // read from its git files; "" when detached, unreadable or gone
	Base     bool              `json:"base"`
	Stack    string            `json:"stack"` // StackState of Services
	Services []OverviewService `json:"services"`
	Agent    *AgentRow         `json:"agent"` // the running agent, else the first not off; nil: none
	Links    []render.Link     `json:"links"`
	Stale    bool              `json:"stale"` // a running container was created from an older unit (D85)
}

// Overview is one project's every worktree, as the dashboard polls it
// (`railswitch overview --json`, docs/json.md).
type Overview struct {
	Prefix    string             `json:"prefix"`
	Workspace string             `json:"workspace"`
	Worktrees []OverviewWorktree `json:"worktrees"` // registry order (ID)
}

// stackRoles are the roles of the long-running services that make a stack.
var stackRoles = []string{render.RoleApp, render.RoleWorker, render.RoleData}

// StackState sums up the stack services (roles app, worker, data) of
// services: failed if any unit is failed, up if all are active, stopped if
// none is active or activating (no stack services too), else partial.
func StackState(services []OverviewService) string {
	var n, active, live int
	for _, s := range services {
		if !slices.Contains(stackRoles, s.Role) {
			continue
		}
		n++
		switch st, _, _ := strings.Cut(s.State, "/"); st {
		case "failed":
			return "failed"
		case "active":
			active++
			live++
		case "activating":
			live++
		}
	}
	switch {
	case n == 0 || live == 0:
		return "stopped"
	case active == n:
		return "up"
	}
	return "partial"
}

// Overview reports every registered worktree of the project: its stack and
// service states from one batched `systemctl --user show`, on-demand
// services, agents and stale containers (D85) from one `podman ps -a`, its
// agent and links. It runs no git (the model is built without the host
// git identity, which nothing here needs) and no health check: the
// dashboard calls it every 2 s.
func (l *L) Overview(ctx context.Context) (Overview, error) {
	m, err := l.modelFor(workspace.LocalHost())
	if err != nil {
		return Overview{}, err
	}
	wts := m.Inputs.Worktrees
	var units []string
	for _, w := range wts {
		for _, s := range m.Services {
			if !s.OnDemand {
				units = append(units, unitOf(m, w.Slug, s.ID))
			}
		}
	}
	props, err := l.systemd().ShowMany(ctx, units, "ActiveState", "SubState")
	if err != nil {
		return Overview{}, err
	}
	key := unithash.Key(m.Prefix)
	hashes, err := l.podman().Labels(ctx, key) // running containers only
	if err != nil {
		return Overview{}, err
	}
	base, err := naming.Sanitize(m.Inputs.Project.Project.BaseBranch)
	if err != nil {
		return Overview{}, err
	}
	ov := Overview{Prefix: m.Prefix, Workspace: m.Inputs.Workspace, Worktrees: []OverviewWorktree{}}
	for _, w := range wts {
		in, err := m.Instance(w)
		if err != nil {
			return Overview{}, err
		}
		links, err := m.Links(in)
		if err != nil {
			return Overview{}, err
		}
		ow := OverviewWorktree{Slug: w.Slug, ID: w.ID, Branch: w.Branch, Base: w.Slug == base, Services: []OverviewService{}, Links: links}
		for _, s := range m.Services {
			st := unitState(props[unitOf(m, w.Slug, s.ID)])
			ctr := containerOf(m, w.Slug, s.ID)
			have, run := hashes[ctr]
			switch {
			case s.OnDemand:
				st = onDemandState(run)
			case run:
				want, err := l.renderedHash(ctr+".container", key)
				switch {
				case errors.Is(err, fs.ErrNotExist): // not rendered: nothing to compare
				case err != nil:
					return Overview{}, err
				default:
					ow.Stale = ow.Stale || have != want
				}
			}
			ow.Services = append(ow.Services, OverviewService{Service: s.ID, Role: s.Role, State: st})
		}
		ow.Stack = StackState(ow.Services)
		names, err := m.NamesFor(w.Slug)
		if err != nil {
			return Overview{}, err
		}
		var agents []string // running in this worktree, by exact name (D62)
		for _, a := range m.Inputs.Stack.Agents {
			ctr := containerOf(m, w.Slug, "agent-"+a.Name)
			if _, run := hashes[ctr]; run && slices.Contains(names.Containers, ctr) {
				agents = append(agents, a.Name)
			}
		}
		ow.Agent = chooseAgent(agentRows(m, in, agents))
		ov.Worktrees = append(ov.Worktrees, ow)
	}
	return ov, nil
}

// chooseAgent is a worktree's agent for the overview: the running one,
// else the first whose status is not off, else nil.
func chooseAgent(rows []AgentRow) *AgentRow {
	if i := slices.IndexFunc(rows, func(r AgentRow) bool { return r.Running }); i >= 0 {
		return &rows[i]
	}
	if i := slices.IndexFunc(rows, func(r AgentRow) bool { return r.State != "off" }); i >= 0 {
		return &rows[i]
	}
	return nil
}

// PrintOverview prints Overview: with asJSON one JSON document, else a
// table WORKTREE BRANCH STACK AGENT with each worktree's links indented
// under its row.
func (l *L) PrintOverview(ctx context.Context, asJSON bool) error {
	ov, err := l.Overview(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(l.Out).Encode(ov)
	}
	var table bytes.Buffer
	tw := tabwriter.NewWriter(&table, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "WORKTREE\tBRANCH\tSTACK\tAGENT")
	for _, w := range ov.Worktrees {
		slug, branch := w.Slug, safefile.Printable(w.Branch) // a container can write the branch
		if w.Base {
			slug += " (base)"
		}
		if branch == "" {
			branch = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", slug, branch, w.Stack, agentCell(w.Agent))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	lines := strings.SplitAfter(table.String(), "\n")
	if _, err := fmt.Fprint(l.Out, lines[0]); err != nil {
		return err
	}
	for i, w := range ov.Worktrees {
		if _, err := fmt.Fprint(l.Out, lines[i+1]); err != nil {
			return err
		}
		lw := tabwriter.NewWriter(l.Out, 0, 4, 2, ' ', 0)
		for _, k := range w.Links {
			fmt.Fprintf(lw, "  %s\t%s\n", k.Label, safefile.Printable(k.URL)) // {branch} expands into it
		}
		if err := lw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// agentCell is an agent's AGENT column: "<name>: <state>", marked
// "(running)" while its container runs; "-" without one.
func agentCell(a *AgentRow) string {
	if a == nil {
		return "-"
	}
	switch {
	case a.Running && a.State == "off":
		return a.Name + ": running"
	case a.Running:
		return a.Name + ": " + a.State + " (running)"
	}
	return a.Name + ": " + a.State
}
