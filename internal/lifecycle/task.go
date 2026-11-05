package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/workspace"
)

// Tasks are the project's tasks: the pack's [tasks] with project.toml's
// [tasks] over them (D79), by name.
func (l *L) Tasks(ctx context.Context) ([]render.Task, error) {
	m, err := l.model(ctx)
	if err != nil {
		return nil, err
	}
	return m.Tasks(), nil
}

// TaskNamed is task name; an unknown name is a *UsageError.
func (l *L) TaskNamed(ctx context.Context, name string) (render.Task, error) {
	tasks, err := l.Tasks(ctx)
	if err != nil {
		return render.Task{}, err
	}
	for _, t := range tasks {
		if t.Name == name {
			return t, nil
		}
	}
	return render.Task{}, &UsageError{Msg: "unknown task " + name + "; railswitch task ls"}
}

// Task runs task name of worktree slug's app as exec does (its running
// container, else a transient one; always a transient one for a Fresh
// task, D171), as `sh -c '<command> "$@"' <name>
// <args…>`: the arguments reach the command as its own, unexpanded. A
// non-zero exit is an *ExitError with the task's code. A run of TestTask
// without arguments, the whole suite, is recorded with its exit code
// (D158); a run that didn't end on its own (podman failed, it was
// cancelled) is not.
func (l *L) Task(ctx context.Context, slug, name string, args []string) error {
	t, err := l.TaskNamed(ctx, name)
	if err != nil {
		return err
	}
	argv := append([]string{"sh", "-c", t.Command + ` "$@"`, name}, args...)
	if name != TestTask || len(args) > 0 {
		return l.Exec(ctx, slug, t.Fresh, argv)
	}
	run := TestRun{StartedAt: time.Now().UTC(), Commit: workspace.HeadCommit(naming.WorktreeDir(l.WS.Root, slug))}
	err = l.Exec(ctx, slug, t.Fresh, argv)
	var x *ExitError
	switch {
	case ctx.Err() != nil:
		return err
	case errors.As(err, &x):
		run.ExitCode = x.Code
	case err != nil:
		return err
	}
	run.EndedAt = time.Now().UTC()
	if werr := writeTestRun(l.WS.Root, slug, run); werr != nil {
		fmt.Fprintf(l.Err, "railswitch: the test result was not recorded: %v\n", werr)
	}
	return err
}
