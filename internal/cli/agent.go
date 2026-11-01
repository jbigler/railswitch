package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/lifecycle"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/term"
)

const agentUsage = "usage: railswitch agent [--new-term] [--no-token] [<name>] [-- args...] | railswitch [--json] agent ls | railswitch agent stop [<name>] | railswitch agent update [<name>] | railswitch agent add <name> [--use <ref>] [--run on-demand|service] | railswitch agent default promote|apply [<name>] [--all]"

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

// railswitch agent [--new-term] [<name>] [-- args…]: run the worktree's
// agent (up first), or with --new-term re-run this command in a new
// terminal tab or window; railswitch agent ls: every agent and its status
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
	name, argv, newTerm, noToken, code, ok := agentArgs(e, args)
	if !ok {
		return code
	}
	if name == "ls" && (len(argv) > 0 || newTerm || noToken) {
		return e.usageErr("agent ls takes no arguments")
	}
	l, slug, code := e.lifecycle(ctx, "agent")
	if code != 0 {
		return code
	}
	if newTerm {
		// Checked here: the new terminal may close before its error is read.
		if _, err := l.CheckAgent(ctx, name, argv); err != nil {
			var u *lifecycle.UsageError
			if errors.As(err, &u) {
				return e.usageErr("%s", u.Msg)
			}
			return e.fail(err)
		}
		return e.agentNewTerm(ctx, l.WS.Root, slug, name, argv, noToken)
	}
	if name == "ls" {
		rows, err := l.AgentList(ctx, slug)
		if err != nil {
			return e.fail(err)
		}
		if err := e.printAgents(rows); err != nil {
			return e.fail(err)
		}
		return 0
	}
	if err := l.Agent(ctx, slug, name, argv, noToken); err != nil {
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
func agentArgs(e *env, args []string) (name string, argv []string, newTerm, noToken bool, code int, ok bool) {
	if i := slices.Index(args, "--"); i >= 0 {
		args, argv = args[:i], args[i+1:]
	}
	fs := e.flags("agent")
	fs.BoolVar(&newTerm, "new-term", false, "open the agent in a new terminal tab or window")
	fs.BoolVar(&noToken, "no-token", false, "run without the agent package's token secret: it uses the login in its home (D139)")
	rest, code, ok := parseMixed(fs, args) // flags after the name too
	if !ok {
		return "", nil, false, false, code, false
	}
	if len(rest) > 1 {
		return "", nil, false, false, e.usageErr("%s", agentUsage), false
	}
	if len(rest) == 1 {
		name = rest[0]
	}
	return name, argv, newTerm, noToken, 0, true
}

// agentNewTerm re-executes `railswitch -C <root> -w <slug> agent [<name>]
// [-- argv]` in a new terminal tab or window, in the worktree's dir.
func (e *env) agentNewTerm(ctx context.Context, root, slug, name string, argv []string, noToken bool) int {
	exe, err := executable()
	if err != nil {
		return e.fail(err)
	}
	cmd := []string{exe, "-C", root, "-w", slug, "agent"}
	if noToken {
		cmd = append(cmd, "--no-token")
	}
	title := slug + " agent"
	if name != "" {
		cmd = append(cmd, name)
		title = slug + " " + name
	}
	if len(argv) > 0 {
		cmd = append(append(cmd, "--"), argv...)
	}
	return e.launchIn(ctx, title, filepath.Join(root, slug), cmd)
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
