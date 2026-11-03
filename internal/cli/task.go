package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/lifecycle"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/workspace"
)

const taskUsage = "usage: railswitch task [--new-term] <name> [args...] | railswitch [--json] task ls | railswitch task set <name> <command> | railswitch task rm <name> | railswitch task restore <name>"

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
	if name == "set" || name == "rm" || name == "restore" {
		if *newTerm {
			return e.usageErr("%s", taskUsage)
		}
		return e.taskEdit(ctx, name, fs.Args()[1:])
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

// taskEdit is task set <name> <command>, task rm <name> and task restore
// <name> (D156): one project.toml [tasks] edit by the line editor, then
// commit. rm takes out the project's task, which brings back the pack's of
// that name, or hides a pack task the project doesn't set (name = "");
// restore takes out that "".
func (e *env) taskEdit(ctx context.Context, verb string, args []string) int {
	if want := map[string]int{"set": 2, "rm": 1, "restore": 1}[verb]; len(args) != want {
		return e.usageErr("%s", taskUsage)
	}
	name := args[0]
	w, err := workspace.Load(ctx, e.dir, "")
	if err != nil {
		return e.fail(err)
	}
	data, err := projectBytes(w.Root)
	if err != nil {
		return e.fail(err)
	}
	var cur render.Task
	for _, t := range render.MergeTasks(w.Stack.Pack.Tasks, w.Project.Tasks) {
		if t.Name == name {
			cur = t
		}
	}
	var next, done string
	switch {
	case verb == "set":
		if args[1] == "" {
			return e.usageErr("task set: the command is empty; railswitch task rm %s hides a pack task", name)
		}
		next, err = config.SetTask(string(data), name, args[1])
		done = "set"
	case verb == "restore" && cur.Source == "hidden":
		next, err = config.UnsetTask(string(data), name)
		done = "restored: the pack's command runs again"
	case verb == "restore":
		return e.fail(fmt.Errorf("task %s is not a hidden pack task", name))
	case cur.Source == "project":
		next, err = config.UnsetTask(string(data), name)
		done = "removed"
		if cur.Pack != "" {
			done = "removed: the pack's command runs again"
		}
	case cur.Source == "hidden":
		return e.fail(fmt.Errorf("task %s is hidden already; railswitch task restore %s", name, name))
	case cur.Source == "pack":
		next, err = config.SetTask(string(data), name, "")
		done = "hidden (name = \"\"); railswitch task restore " + name + " brings it back"
	default:
		return e.fail(fmt.Errorf("no task %s; railswitch task ls", name))
	}
	if err != nil {
		return e.fail(err)
	}
	p, err := config.Parse([]byte(next))
	if err != nil {
		return e.fail(err)
	}
	if err := p.Validate(); err != nil {
		return e.fail(fmt.Errorf("project.toml: %w", err))
	}
	if err := e.commitProject(ctx, w.Root, []byte(next), &workspace.Accept{}); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "task %s %s\n", name, done)
	return 0
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
	return e.launchIn(ctx, slug+" "+name, naming.WorktreeDir(root, slug), cmd)
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
