package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/agentstatus"
	"github.com/jbigler/railswitch/internal/agentusage"
	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
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
	// Usage: its current (or last) session's tokens, when its package's
	// status hook counts them; nil otherwise.
	Usage *agentstatus.Usage `json:"usage"`
	// Config: its package has [config] paths, so it can have a project
	// default (D135); Default is where that came from, nil without one.
	Config  bool          `json:"config"`
	Default *AgentDefault `json:"default"`
	// DefaultPending: its home in this worktree holds the request to take
	// the default, which its entrypoint applies at the agent's next start.
	DefaultPending bool `json:"default_pending"`
}

// UsageError is a lifecycle error in how the command was called.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Agent runs agent name of worktree slug ("": the project's only agent)
// with argv. It runs up first (C12), so the network, images, env files,
// units and preflight are in place. An on-demand agent then runs in a
// transient container, named as its unit's would be, once its secrets are
// set; a service agent's unit is started when it is not active, and its
// session joined, or created with argv (D142). Neither has a deadline:
// the user drives it. At most one agent runs in a worktree (D62): another
// one running is an error, never stopped for you, unless it is a service
// agent without a session (up started it), which is stopped. With noToken an
// on-demand agent runs without its package's secrets (tokenSecrets), so it
// uses the login kept in its home (D139).
func (l *L) Agent(ctx context.Context, slug, name string, argv []string, noToken bool) error {
	return l.agent(ctx, slug, name, argv, noToken, false)
}

// AgentDetached is Agent for a service agent without a terminal: it starts
// the agent's session in the background with argv and returns, for the
// dashboard to watch and type to (agent screen, agent send). A session
// already running is left alone without argv, refused with them; an
// on-demand agent, which needs a terminal, is refused.
func (l *L) AgentDetached(ctx context.Context, slug, name string, argv []string) error {
	return l.agent(ctx, slug, name, argv, false, true)
}

func (l *L) agent(ctx context.Context, slug, name string, argv []string, noToken, detach bool) error {
	name, err := l.CheckAgent(ctx, name)
	if err != nil {
		return err
	}
	id := "agent-" + name
	if err := l.up(ctx, slug, false, false); err != nil {
		return err
	}
	m, in, err := l.load(ctx, slug) // up may have re-rendered (new image tags)
	if err != nil {
		return err
	}
	s, err := service(m, id)
	if err != nil {
		return err
	}
	ctr := containerOf(m, slug, id)
	running, err := l.RunningAgents(ctx, m, slug)
	if err != nil {
		return err
	}
	others := slices.DeleteFunc(slices.Clone(running), func(n string) bool { return n == name })
	idle, others, err := l.idleAgents(ctx, m, slug, others)
	if err != nil {
		return err
	}
	switch {
	case len(others) == 1:
		return fmt.Errorf("agent %s is running in %s; one agent per worktree: railswitch agent stop %s", others[0], slug, others[0])
	case len(others) > 1:
		return fmt.Errorf("agents %s are running in %s; one agent per worktree: railswitch agent stop", strings.Join(others, ", "), slug)
	case slices.Contains(running, name) && s.OnDemand:
		return fmt.Errorf("agent %s is already running in %s", name, slug)
	}
	if detach && s.OnDemand {
		return fmt.Errorf("agent %s runs on demand, in one terminal; starting it in the background is for a service agent", name)
	}
	var without []string
	if noToken {
		if !s.OnDemand {
			return fmt.Errorf("agent %s runs as a service, with its unit's secrets: --no-token is for an on-demand agent", name)
		}
		without = tokenSecrets(m, name)
	}
	if err := checkSecrets(ctx, l.podman(), m, slug, func(x *render.Service) bool { return x.ID == id }, without); err != nil {
		return err
	}
	if s.OnDemand {
		if err := l.stopIdleAgents(ctx, m, slug, idle); err != nil {
			return err
		}
		ep, err := pack.Expand(s.Entrypoint, in.Scope(id))
		if err != nil {
			return fmt.Errorf("%s: entrypoint: %w", id, err)
		}
		if len(argv) == 0 { // the package's [unit] exec, as the unit runs it
			for _, a := range s.Exec {
				w, err := pack.Expand(a, in.Scope(id))
				if err != nil {
					return fmt.Errorf("%s: exec: %w", id, err)
				}
				argv = append(argv, w)
			}
		}
		envFile := filepath.Join(m.Inputs.Workspace, ".railswitch", "env", slug, id+".env")
		args, err := l.transient(m, in, s, envFile, runOpts{Full: true, Name: ctr, Entrypoint: ep, Unit: true, Without: without}, argv)
		if err != nil {
			return err
		}
		return l.inContainer(ctx, args)
	}
	if err := l.stopIdleAgents(ctx, m, slug, idle); err != nil {
		return err
	}
	if err := l.startAgentUnit(ctx, slug, ctr); err != nil {
		return err
	}
	live, err := l.agentSession(ctx, ctr)
	if err != nil {
		return err
	}
	if live && len(argv) > 0 {
		return fmt.Errorf("agent %s has a session in %s, and arguments start a new one: join it without them, or railswitch agent stop %s first", name, slug, name)
	}
	if live && detach {
		return nil // it runs already
	}
	if len(argv) == 0 { // the package's exec words, as for an on-demand agent
		argv = s.Session.Exec
	}
	if s.Session.Entrypoint != "" {
		argv = append([]string{s.Session.Entrypoint}, argv...)
	}
	var session []string
	for _, a := range argv {
		w, err := pack.Expand(a, in.Scope(id))
		if err != nil {
			return fmt.Errorf("%s: session: %w", id, err)
		}
		session = append(session, w)
	}
	if detach { // no client: a fixed size, as a terminal would give it
		_, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: append(append([]string{"exec", ctr}, render.AgentTmux...),
			append([]string{"new-session", "-d", "-x", "160", "-y", "48", "-s", render.AgentSession, "-e", "COLORTERM=truecolor", "--"}, session...)...)})
		return err
	}
	return l.inContainer(ctx, agentAttach(ctr, append([]string{"new-session", "-A", "-s", render.AgentSession, "-e", "COLORTERM=truecolor", "--"}, session...)))
}

// AgentWatch joins service agent name's session in worktree slug as a
// viewer: its screen, live, without typing into it (D142).
func (l *L) AgentWatch(ctx context.Context, slug, name string) error {
	ctr, err := l.CheckWatch(ctx, slug, name)
	if err != nil {
		return err
	}
	return l.inContainer(ctx, agentAttach(ctr, []string{"attach-session", "-r", "-t", "=" + render.AgentSession}))
}

// CheckWatch returns the container of the agent AgentWatch would watch, or
// why it can't; --new-term calls it before opening a terminal.
func (l *L) CheckWatch(ctx context.Context, slug, name string) (string, error) {
	name, ctr, err := l.serviceAgent(ctx, slug, name, "--watch")
	if err != nil {
		return "", err
	}
	if ok, err := l.podman().Running(ctx, ctr); err != nil {
		return "", err
	} else if !ok {
		return "", fmt.Errorf("agent %s is not running in %s", name, slug)
	}
	if live, err := l.agentSession(ctx, ctr); err != nil {
		return "", err
	} else if !live {
		return "", fmt.Errorf("agent %s has no session in %s to watch: railswitch agent %s starts one", name, slug, name)
	}
	return ctr, nil
}

// serviceAgent resolves agent name ("": the default one) and its container
// in worktree slug; an on-demand agent is refused, as what (the command)
// needs a service agent's session.
func (l *L) serviceAgent(ctx context.Context, slug, name, what string) (string, string, error) {
	m, err := l.model(ctx)
	if err != nil {
		return "", "", err
	}
	if name, err = l.agentName(m, name); err != nil {
		return "", "", err
	}
	s, err := service(m, "agent-"+name)
	if err != nil {
		return "", "", err
	}
	if s.OnDemand {
		return "", "", fmt.Errorf("agent %s runs on demand, in one terminal; %s is for a service agent", name, what)
	}
	return name, containerOf(m, slug, s.ID), nil
}

// agentAttach is the podman exec of a session client on service agent ctr's
// session server, with args (a new-session or attach-session).
func agentAttach(ctr string, args []string) []string {
	return append([]string{"exec", "-it", "--detach-keys=", "-e", render.AgentClientTERM, "--preserve-fds", "1", ctr},
		hangup(append(slices.Clone(render.AgentTmux), args...))...)
}

// startAgentUnit starts service agent container ctr's unit when it is not
// active, then waits for its session server to answer.
func (l *L) startAgentUnit(ctx context.Context, slug, ctr string) error {
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
	return l.waitAgentServer(ctx, ctr)
}

// waitAgentServer waits up to 10 s for ctr's session server: list-sessions
// succeeds once it answers, sessions or none.
func (l *L) waitAgentServer(ctx context.Context, ctr string) error {
	var err error
	for range 50 {
		if _, err = l.R.Run(ctx, sys.Cmd{Name: "podman", Args: append(append([]string{"exec", ctr}, render.AgentTmux...), "list-sessions")}); err == nil {
			return nil
		}
		if !errors.Is(err, sys.ErrExit) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("%s: its session server does not answer (an agent base image from before D142? railswitch containerfile show agent-base): %w", ctr, err)
}

// errNoServer is agentSession's error for a session server that does not
// answer (still starting, or an old image).
var errNoServer = errors.New("its session server does not answer")

// agentSession reports whether service agent container ctr has its
// agent's session. Only the server's own answer (exit 1) means none: a failed exec
// or a server that doesn't answer must never make a live agent look idle.
func (l *L) agentSession(ctx context.Context, ctr string) (bool, error) {
	// has-session exits 1 for "no server" too: the server must answer first.
	if _, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: append(append([]string{"exec", ctr}, render.AgentTmux...), "list-sessions")}); err != nil {
		return false, fmt.Errorf("%s: can't tell whether its agent has a session: %w: %w", ctr, errNoServer, err)
	}
	res, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: append(append([]string{"exec", ctr}, render.AgentTmux...), "has-session", "-t", "="+render.AgentSession)})
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sys.ErrExit) && res.ExitCode == 1:
		return false, nil
	}
	return false, fmt.Errorf("%s: can't tell whether its agent has a session: %w", ctr, err)
}

// idleAgents splits running (agent names in worktree slug) into the
// service agents without a session (up started them, nothing runs in them)
// and the rest.
func (l *L) idleAgents(ctx context.Context, m *render.Model, slug string, running []string) (idle, busy []string, err error) {
	for _, n := range running {
		s, err := service(m, "agent-"+n)
		if err != nil {
			return nil, nil, err
		}
		if !s.OnDemand {
			live, err := l.agentSession(ctx, containerOf(m, slug, s.ID))
			if err != nil {
				return nil, nil, err
			}
			if !live {
				idle = append(idle, n)
				continue
			}
		}
		busy = append(busy, n)
	}
	return idle, busy, nil
}

// stopIdleAgents stops idle (service agents of worktree slug without a
// session), saying so: another agent is starting there.
func (l *L) stopIdleAgents(ctx context.Context, m *render.Model, slug string, idle []string) error {
	for _, n := range idle {
		ctr := containerOf(m, slug, "agent-"+n)
		// Checked again: a session may have started since idleAgents.
		live, err := l.agentSession(ctx, ctr)
		if err != nil {
			return err
		}
		if live {
			return fmt.Errorf("agent %s is running in %s; one agent per worktree: railswitch agent stop %s", n, slug, n)
		}
		fmt.Fprintf(l.Err, "stopping agent %s in %s: it has no session\n", n, slug)
		if err := l.systemd().Stop(ctx, ctr+".service"); err != nil {
			return err
		}
	}
	return nil
}

// defaultAgent is the agent run when none is named (D140): the one
// project.toml marks default = true, else the only one whose package is the
// built-in default (the agent project new adds).
func defaultAgent(m *render.Model) (string, bool) {
	return DefaultAgent(m.Inputs.Project, m.Inputs.Stack)
}

// DefaultAgent is defaultAgent of project p and its stack st.
func DefaultAgent(p *config.Project, st *pack.Stack) (string, bool) {
	var builtin []string
	for _, a := range st.Agents {
		if p.Agents[a.Name].Default {
			return a.Name, true
		}
		if a.Agent.Default {
			builtin = append(builtin, a.Name)
		}
	}
	if len(builtin) == 1 {
		return builtin[0], true
	}
	return "", false
}

// tokenSecrets are the project's secrets that deliver agent name's package
// [[secrets]] (its token): those the agent receives into an env var its
// package declares, whatever they are called.
func tokenSecrets(m *render.Model, name string) []string {
	var out []string
	for _, a := range m.Inputs.Stack.Agents {
		if a.Name != name {
			continue
		}
		for pname, sec := range m.Inputs.Project.Secrets {
			if slices.Contains(sec.Services, a.Service) && slices.ContainsFunc(a.Agent.Secrets, func(ps pack.AgentSecret) bool { return ps.Env != "" && ps.Env == sec.Env }) {
				out = append(out, pname)
			}
		}
	}
	slices.Sort(out)
	return out
}

// CheckAgent resolves agent name ("": the project's only agent), as Agent
// does before up; --new-term calls it before opening a terminal.
func (l *L) CheckAgent(ctx context.Context, name string) (string, error) {
	m, err := l.model(ctx)
	if err != nil {
		return "", err
	}
	return l.agentName(m, name)
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
	return agentRows(m, in, running), nil
}

// agentRows are m's agents in [agents] order with their status in in's
// worktree; running names the ones whose containers run there.
func agentRows(m *render.Model, in *render.Instance, running []string) []AgentRow {
	rows := []AgentRow{}
	for _, a := range m.Inputs.Stack.Agents {
		st := agentstatus.Read(filepath.Join(in.Values["status.dir"], a.Name+".json"))
		rows = append(rows, AgentRow{
			Name: a.Name, Package: a.Ref.String(), Run: a.Run,
			State: st.State, Message: st.Message, TS: st.TS, Reply: st.Reply(), Usage: st.Usage,
			Running: slices.Contains(running, a.Name),
			Config:  len(a.Agent.Config.Paths) > 0, Default: ReadAgentDefault(m, a.Name),
			DefaultPending: defaultPending(filepath.Join(in.Values["agent.home"], a.Name)),
		})
	}
	return rows
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
		hint := "railswitch agent add <name> --use <ref>"
		if l.WS.Catalog != nil {
			if ref, ok := l.WS.Catalog.DefaultAgent(); ok {
				hint = "railswitch agent add " + ref.Name
			}
		}
		return "", fmt.Errorf("project %s has no agents: %s", m.Prefix, hint)
	case name == "" && len(names) == 1:
		return names[0], nil
	case name == "":
		if n, ok := defaultAgent(m); ok {
			return n, nil
		}
		return "", fmt.Errorf("project %s has several agents: name one of %s (or set default = true on one in project.toml)", m.Prefix, strings.Join(names, ", "))
	}
	return "", fmt.Errorf("project %s has no agent %s (agents: %s)", m.Prefix, name, strings.Join(names, ", "))
}

// usageSamples are the session counters of rows, worktree slug's agents,
// for the usage history (D162).
func usageSamples(samples []agentusage.Sample, slug string, rows []AgentRow) []agentusage.Sample {
	for _, r := range rows {
		if r.Usage != nil {
			samples = append(samples, agentusage.Sample{Worktree: slug, Agent: r.Name, Usage: *r.Usage, TS: r.TS})
		}
	}
	return samples
}

// usagePath is project prefix's usage history.
func (l *L) usagePath(prefix string) string { return agentusage.Path(l.WS.Dirs.Data, prefix) }

// AgentUsage records every worktree's agents' counters now, then reports
// the usage history of the last days days of worktree slug ("": every
// worktree), D162.
func (l *L) AgentUsage(ctx context.Context, slug string, days int) (agentusage.Report, error) {
	m, err := l.modelFor(workspace.LocalHost())
	if err != nil {
		return agentusage.Report{}, err
	}
	var samples []agentusage.Sample
	for _, w := range m.Inputs.Worktrees {
		in, err := m.Instance(w)
		if err != nil {
			return agentusage.Report{}, err
		}
		samples = usageSamples(samples, w.Slug, agentRows(m, in, nil))
	}
	now := time.Now()
	if err := agentusage.Record(l.usagePath(m.Prefix), samples, now); err != nil {
		return agentusage.Report{}, err
	}
	return agentusage.Read(l.usagePath(m.Prefix), days, slug, now)
}
