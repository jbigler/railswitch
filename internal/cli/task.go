package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/lifecycle"
	"github.com/jbigler/railswitch/internal/render"
)

const taskUsage = "usage: railswitch task [--new-term] <name> [args...] | railswitch [--json] task ls"

// railswitch task ls: the worktree's tasks (D79); railswitch task
// [--new-term] <name> [args…]: run one in the app as exec does, the
// arguments after the name (a -- right after it is dropped) passed to it,
// or with --new-term re-run this command in a new terminal tab or window.
func cmdTask(ctx context.Context, e *env, args []string) int {
	fs := e.flags("task")
	newTerm := fs.Bool("new-term", false, "open the task in a new terminal tab or window")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() == 0 {
		return e.usageErr("%s", taskUsage)
	}
	name, rest := fs.Arg(0), fs.Args()[1:]
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	if name == "ls" && (len(rest) > 0 || *newTerm) {
		return e.usageErr("task ls takes no arguments")
	}
	l, slug, code := e.lifecycle(ctx, "task")
	if code != 0 {
		return code
	}
	if name == "ls" {
		tasks, err := l.Tasks(ctx)
		if err != nil {
			return e.fail(err)
		}
		if err := e.printTasks(tasks); err != nil {
			return e.fail(err)
		}
		return 0
	}
	if *newTerm {
		// Checked here: the new terminal may close before its error is read.
		if _, err := l.TaskCommand(ctx, name); err != nil {
			return e.taskErr(err)
		}
		return e.taskNewTerm(ctx, l.WS.Root, slug, name, rest)
	}
	return e.taskErr(l.Task(ctx, slug, name, rest))
}

// taskErr is a task run's exit code: 2 for a usage error, the task's own
// code for its non-zero exit.
func (e *env) taskErr(err error) int {
	if err == nil {
		return 0
	}
	var u *lifecycle.UsageError
	if errors.As(err, &u) {
		return e.usageErr("%s", u.Msg)
	}
	return e.failCommand(err)
}

// taskNewTerm re-executes `railswitch -C <root> -w <slug> task <name>
// [args…]` in a new terminal tab or window, in the worktree's dir.
func (e *env) taskNewTerm(ctx context.Context, root, slug, name string, args []string) int {
	exe, err := executable()
	if err != nil {
		return e.fail(err)
	}
	cmd := []string{exe, "-C", root, "-w", slug, "task", name}
	if len(args) > 0 && args[0] == "--" { // kept: the re-run drops one
		cmd = append(cmd, "--")
	}
	cmd = append(cmd, args...)
	return e.launchIn(ctx, slug+" "+name, filepath.Join(root, slug), cmd)
}

// printTasks prints task ls: JSON with --json, else a NAME SOURCE COMMAND
// table.
func (e *env) printTasks(tasks []render.Task) error {
	if e.json {
		return json.NewEncoder(e.stdout).Encode(tasks)
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSOURCE\tCOMMAND")
	for _, t := range tasks {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", t.Name, t.Source, t.Command)
	}
	return tw.Flush()
}
