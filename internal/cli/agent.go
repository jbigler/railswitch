package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/lifecycle"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/term"
)

const agentUsage = "usage: railswitch agent [--new-term] [--no-token | --watch] [<name>] [-- args...] | railswitch [--json] agent ls | railswitch agent stop [<name>] | railswitch agent update [<name>] | railswitch agent add <name> [--use <ref>] [--run on-demand|service] | railswitch agent default promote|apply [<name>] [--all]"

// executable is the running railswitch binary, re-executed by --new-term;
// tests replace it.
var executable = os.Executable

// launchTerm opens argv in a new terminal tab or window (--new-term);
// tests replace it.
var launchTerm = func(ctx context.Context, e *env, title, dir string, argv []string) error {
	tty := isTerminal(os.Stdin) && isTerminal(os.Stdout)
	l := term.Launcher{R: e.run, Env: os.Getenv, LookPath: exec.LookPath, ProcRoot: procRoot, TTY: tty, Err: e.stderr}
	return l.Launch(ctx, title, dir, argv)
}

// railswitch agent [--new-term] [--watch] [<name>] [-- args…]: run the
// worktree's agent (up first), join a service agent's session, or watch it
// (D142); with --new-term re-run this command in a new terminal tab or
// window; railswitch agent ls: every agent and its status
// (D58); railswitch agent stop [<name>]: stop the worktree's running agent
// (D62); railswitch agent update [<name>]: rebuild its image without the
// cache (D138); railswitch agent add: see cmdAgentAdd (D63); railswitch agent
// default: see cmdAgentDefault (D135).
func cmdAgent(ctx context.Context, e *env, args []string) int {
	switch {
	case len(args) > 0 && args[0] == "stop":
		return cmdAgentStop(ctx, e, args[1:])
	case len(args) > 0 && args[0] == "update":
		return cmdAgentUpdate(ctx, e, args[1:])
	case len(args) > 0 && args[0] == "add":
		return cmdAgentAdd(ctx, e, args[1:])
	case len(args) > 0 && args[0] == "default":
		return cmdAgentDefault(ctx, e, args[1:])
	}
	a, code, ok := agentArgs(e, args)
	if !ok {
		return code
	}
	if a.name == "ls" && (len(a.argv) > 0 || a.newTerm || a.noToken || a.watch) {
		return e.usageErr("agent ls takes no arguments")
	}
	if a.watch && (len(a.argv) > 0 || a.noToken) {
		return e.usageErr("agent --watch takes no arguments and no --no-token: it joins the session as it runs")
	}
	l, slug, code := e.lifecycle(ctx, "agent")
	if code != 0 {
		return code
	}
	if a.newTerm {
		// Checked here: the new terminal may close before its error is read.
		check := func() error { _, err := l.CheckAgent(ctx, a.name); return err }
		if a.watch {
			check = func() error { _, err := l.CheckWatch(ctx, slug, a.name); return err }
		}
		if err := check(); err != nil {
			return e.fail(err)
		}
		return e.agentNewTerm(ctx, l.WS.Root, slug, a)
	}
	if a.name == "ls" {
		rows, err := l.AgentList(ctx, slug)
		if err != nil {
			return e.fail(err)
		}
		if err := e.printAgents(rows); err != nil {
			return e.fail(err)
		}
		return 0
	}
	if a.watch {
		if err := l.AgentWatch(ctx, slug, a.name); err != nil {
			return e.failCommand(err)
		}
		return 0
	}
	if err := l.Agent(ctx, slug, a.name, a.argv, a.noToken); err != nil {
		var u *lifecycle.UsageError
		if errors.As(err, &u) {
			return e.usageErr("%s", u.Msg)
		}
		return e.failCommand(err)
	}
	return 0
}

// railswitch agent update [<name>]: rebuild the agent's image without the
// build cache, fetching what it downloads again (D138).
func cmdAgentUpdate(ctx context.Context, e *env, args []string) int {
	fs := e.flags("agent update")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() > 1 {
		return e.usageErr("%s", agentUsage)
	}
	l, slug, code := e.lifecycle(ctx, "agent")
	if code != 0 {
		return code
	}
	if err := l.AgentUpdate(ctx, slug, fs.Arg(0)); err != nil {
		return e.failCommand(err)
	}
	return 0
}

// railswitch agent stop [<name>]: stop the worktree's running agent, which
// must be name when given.
func cmdAgentStop(ctx context.Context, e *env, args []string) int {
	fs := e.flags("agent stop")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() > 1 || slices.Contains(args, "--") {
		return e.usageErr("%s", agentUsage)
	}
	l, slug, code := e.lifecycle(ctx, "agent")
	if code != 0 {
		return code
	}
	if err := l.AgentStop(ctx, slug, fs.Arg(0)); err != nil {
		return e.failCommand(err)
	}
	return 0
}

// agentArgs splits args at the first "--", before any flag parsing: what
// precedes it is --new-term and at most one agent name ("ls" lists them),
// what follows it goes to the agent verbatim.
func agentArgs(e *env, args []string) (a agentCall, code int, ok bool) {
	if i := slices.Index(args, "--"); i >= 0 {
		args, a.argv = args[:i], args[i+1:]
	}
	fs := e.flags("agent")
	fs.BoolVar(&a.newTerm, "new-term", false, "open the agent in a new terminal tab or window")
	fs.BoolVar(&a.noToken, "no-token", false, "run without the agent package's token secret: it uses the login in its home (D139)")
	fs.BoolVar(&a.watch, "watch", false, "watch a service agent's session without typing into it (D142)")
	rest, code, ok := parseMixed(fs, args) // flags after the name too
	if !ok {
		return agentCall{}, code, false
	}
	if len(rest) > 1 {
		return agentCall{}, e.usageErr("%s", agentUsage), false
	}
	if len(rest) == 1 {
		a.name = rest[0]
	}
	return a, 0, true
}

// agentCall is a railswitch agent command line.
type agentCall struct {
	name                    string
	argv                    []string
	newTerm, noToken, watch bool
}

// agentNewTerm re-executes `railswitch -C <root> -w <slug> agent [<name>]
// [-- argv]` in a new terminal tab or window, in the worktree's dir.
func (e *env) agentNewTerm(ctx context.Context, root, slug string, a agentCall) int {
	exe, err := executable()
	if err != nil {
		return e.fail(err)
	}
	cmd := []string{exe, "-C", root, "-w", slug, "agent"}
	if a.noToken {
		cmd = append(cmd, "--no-token")
	}
	if a.watch {
		cmd = append(cmd, "--watch")
	}
	title := slug + " agent"
	if a.name != "" {
		cmd = append(cmd, a.name)
		title = slug + " " + a.name
	}
	if len(a.argv) > 0 {
		cmd = append(append(cmd, "--"), a.argv...)
	}
	return e.launchIn(ctx, title, naming.WorktreeDir(root, slug), cmd)
}

// printAgents prints agent ls: JSON with --json, else a table whose RUNNING
// marks the running agent with "*", and whose STATE is "reply" when the
// agent asks the user something, or "running" for a running agent whose
// status file says nothing ("off").
func (e *env) printAgents(rows []lifecycle.AgentRow) error {
	if e.json {
		return json.NewEncoder(e.stdout).Encode(rows)
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPACKAGE\tRUN\tRUNNING\tSTATE\tMESSAGE")
	for _, r := range rows {
		state, running := r.State, "-"
		if r.Running {
			running = "*"
			if state == "off" {
				state = "running"
			}
		}
		if r.Reply {
			state = "reply"
		}
		msg := safefile.Printable(r.Message)
		if msg == "" {
			msg = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, r.Package, r.Run, running, state, msg)
	}
	return tw.Flush()
}
