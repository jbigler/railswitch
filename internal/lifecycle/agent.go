package lifecycle

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/agentstatus"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
)

// AgentRow is one agent of `railswitch agent ls` (and one element of its
// --json, docs/json.md): its [agents] entry and its status file (D58).
type AgentRow struct {
	Name    string `json:"name"`
	Package string `json:"package"` // its package ref, e.g. "<name>@1", "local:<name>@1"
	Run     string `json:"run"`     // "on-demand" or "service"
	State   string `json:"state"`   // the status file's state; "off" when it is unusable
	Message string `json:"message"`
	TS      int64  `json:"ts"`
	Reply   bool   `json:"reply"`   // agentstatus.Status.Reply: the agent asks the user something
	Running bool   `json:"running"` // its container runs in the worktree (D62)
}

// UsageError is a lifecycle error in how the command was called.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Agent runs agent name of worktree slug ("": the project's only agent)
// with argv. It runs up first (C12), so the network, images, env files,
// units and preflight are in place. An on-demand agent then runs in a
// transient container, named as its unit's would be, once its secrets are
// set; a service agent's unit is started when it is not active, and its
// container attached to. argv is refused for a service agent. Neither has a
// deadline: the user drives it. At most one agent runs in a worktree (D62):
// another one running is an error, never stopped for you.
func (l *L) Agent(ctx context.Context, slug, name string, argv []string) error {
	m, err := l.model(ctx)
	if err != nil {
		return err
	}
	if name, err = l.agentName(m, name); err != nil {
		return err
	}
	id := "agent-" + name
	s, err := service(m, id)
	if err != nil {
		return err
	}
	if !s.OnDemand && len(argv) > 0 {
		return &UsageError{Msg: fmt.Sprintf("agent %s runs as a service; arguments go in its package or [agents.%s]", name, name)}
	}
	if err := l.Up(ctx, slug); err != nil {
		return err
	}
	m, in, err := l.load(ctx, slug) // up may have re-rendered (new image tags)
	if err != nil {
		return err
	}
	if s, err = service(m, id); err != nil {
		return err
	}
	ctr := containerOf(m, slug, id)
	running, err := l.RunningAgents(ctx, m, slug)
	if err != nil {
		return err
	}
	others := slices.DeleteFunc(slices.Clone(running), func(n string) bool { return n == name })
	switch {
	case len(others) == 1:
		return fmt.Errorf("agent %s is running in %s; one agent per worktree: railswitch agent stop %s", others[0], slug, others[0])
	case len(others) > 1:
		return fmt.Errorf("agents %s are running in %s; one agent per worktree: railswitch agent stop", strings.Join(others, ", "), slug)
	case slices.Contains(running, name) && s.OnDemand:
		return fmt.Errorf("agent %s is already running in %s", name, slug)
	}
	if s.OnDemand {
		if err := checkSecrets(ctx, l.podman(), m, slug, func(x *render.Service) bool { return x.ID == id }); err != nil {
			return err
		}
		ep, err := pack.Expand(s.Entrypoint, in.Scope(id))
		if err != nil {
			return fmt.Errorf("%s: entrypoint: %w", id, err)
		}
		envFile := filepath.Join(m.Inputs.Workspace, ".railswitch", "env", slug, id+".env")
		args, err := l.transient(m, in, s, envFile, runOpts{Full: true, Name: ctr, Entrypoint: ep}, argv)
		if err != nil {
			return err
		}
		return l.interactive(ctx, sys.NoTimeout, "podman", args)
	}
	sd := l.systemd()
	unit := ctr + ".service"
	st, err := sd.UnitState(ctx, unit)
	if err != nil {
		return err
	}
	if st.Active != "active" {
		if sd.Timeout, err = l.startTimeout([]string{ctr}); err != nil {
			return err
		}
		if err := sd.Start(ctx, unit); err != nil {
			return l.startFailure(ctx, sd, slug, []string{unit}, err)
		}
	}
	return l.interactive(ctx, sys.NoTimeout, "podman", []string{"attach", ctr})
}

// AgentList is every agent of the project, by name, with its status in
// worktree slug.
func (l *L) AgentList(ctx context.Context, slug string) ([]AgentRow, error) {
	m, in, err := l.load(ctx, slug)
	if err != nil {
		return nil, err
	}
	running, err := l.RunningAgents(ctx, m, slug)
	if err != nil {
		return nil, err
	}
	rows := []AgentRow{}
	for _, a := range m.Inputs.Stack.Agents {
		st := agentstatus.Read(filepath.Join(in.Values["status.dir"], a.Name+".json"))
		rows = append(rows, AgentRow{
			Name: a.Name, Package: a.Ref.String(), Run: a.Run,
			State: st.State, Message: st.Message, TS: st.TS, Reply: st.Reply(),
			Running: slices.Contains(running, a.Name),
		})
	}
	return rows, nil
}

// RunningAgents are the names of the agents whose containers run in
// worktree slug, in [agents] order. Only podman decides, on the exact
// container names of the worktree (NamesFor), never a status file. D62
// allows one; two launched at once can both start (no lock).
func (l *L) RunningAgents(ctx context.Context, m *render.Model, slug string) ([]string, error) {
	names, err := m.NamesFor(slug)
	if err != nil {
		return nil, err
	}
	p := l.podman()
	var running []string
	for _, a := range m.Inputs.Stack.Agents {
		ctr := containerOf(m, slug, "agent-"+a.Name)
		if !slices.Contains(names.Containers, ctr) {
			continue
		}
		ok, err := p.Running(ctx, ctr)
		if err != nil {
			return nil, err
		}
		if ok {
			running = append(running, a.Name)
		}
	}
	return running, nil
}

// AgentStop stops agent name of worktree slug ("": every running one): a
// service agent through its unit, an on-demand one with podman stop (its
// --rm removes it). Nothing running is not an error; a name that isn't
// running is, naming the ones that are.
func (l *L) AgentStop(ctx context.Context, slug, name string) error {
	m, err := l.model(ctx)
	if err != nil {
		return err
	}
	if name != "" {
		if name, err = l.agentName(m, name); err != nil {
			return err
		}
	}
	running, err := l.RunningAgents(ctx, m, slug)
	if err != nil {
		return err
	}
	switch {
	case len(running) == 0 && name == "":
		fmt.Fprintf(l.Out, "no agent is running in %s\n", slug)
		return nil
	case len(running) == 0:
		return fmt.Errorf("agent %s is not running in %s", name, slug)
	case name != "" && !slices.Contains(running, name):
		return fmt.Errorf("agent %s is not running in %s; running: %s", name, slug, strings.Join(running, ", "))
	case name != "":
		running = []string{name}
	}
	for _, n := range running {
		id := "agent-" + n
		s, err := service(m, id)
		if err != nil {
			return err
		}
		ctr := containerOf(m, slug, id)
		if s.OnDemand {
			err = l.podman().Stop(ctx, ctr)
		} else {
			err = l.systemd().Stop(ctx, ctr+".service")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// agentName is the agent name names: name itself when the project has it,
// else, for "", its only agent. Any other case is an error listing them.
func (l *L) agentName(m *render.Model, name string) (string, error) {
	var names []string
	for _, a := range m.Inputs.Stack.Agents {
		if a.Name == name {
			return name, nil
		}
		names = append(names, a.Name)
	}
	switch {
	case len(names) == 0:
		return "", fmt.Errorf("project %s has no agents: add one under [agents] in project.toml", m.Prefix)
	case name == "" && len(names) == 1:
		return names[0], nil
	case name == "":
		return "", fmt.Errorf("project %s has several agents: name one of %s", m.Prefix, strings.Join(names, ", "))
	}
	return "", fmt.Errorf("project %s has no agent %s (agents: %s)", m.Prefix, name, strings.Join(names, ", "))
}
