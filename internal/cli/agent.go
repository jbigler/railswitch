package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/jbigler/railswitch/internal/lifecycle"
)

const agentUsage = "usage: railswitch agent [<name>] [-- args...] | railswitch [--json] agent ls"

// railswitch agent [<name>] [-- args…]: run the worktree's agent (up
// first); railswitch agent ls: every agent and its status (D58).
func cmdAgent(ctx context.Context, e *env, args []string) int {
	name, argv, code, ok := agentArgs(e, args)
	if !ok {
		return code
	}
	if name == "ls" && len(argv) > 0 {
		return e.usageErr("agent ls takes no arguments")
	}
	l, slug, code := e.lifecycle(ctx, "agent")
	if code != 0 {
		return code
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
	if err := l.Agent(ctx, slug, name, argv); err != nil {
		var u *lifecycle.UsageError
		if errors.As(err, &u) {
			return e.usageErr("%s", u.Msg)
		}
		return e.failCommand(err)
	}
	return 0
}

// agentArgs splits args at the first "--", before any flag parsing: what
// precedes it is at most one agent name ("ls" lists them), what follows it
// goes to the agent verbatim.
func agentArgs(e *env, args []string) (name string, argv []string, code int, ok bool) {
	if i := slices.Index(args, "--"); i >= 0 {
		args, argv = args[:i], args[i+1:]
	}
	fs := e.flags("agent")
	if code, ok := parse(fs, args); !ok {
		return "", nil, code, false
	}
	if fs.NArg() > 1 {
		return "", nil, e.usageErr("%s", agentUsage), false
	}
	return fs.Arg(0), argv, 0, true
}

// printAgents prints agent ls: JSON with --json, else a table whose STATE
// is "reply" when the agent asks the user something.
func (e *env) printAgents(rows []lifecycle.AgentRow) error {
	if e.json {
		return json.NewEncoder(e.stdout).Encode(rows)
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPACKAGE\tRUN\tSTATE\tMESSAGE")
	for _, r := range rows {
		state := r.State
		if r.Reply {
			state = "reply"
		}
		msg := printable(r.Message)
		if msg == "" {
			msg = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Name, r.Package, r.Run, state, msg)
	}
	return tw.Flush()
}

// printable is s with each control character (a container wrote it: no
// terminal escapes, no line breaks) replaced by a space.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
