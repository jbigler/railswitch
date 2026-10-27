package lifecycle

import (
	"context"

	"github.com/jbigler/railswitch/internal/render"
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

// TaskCommand is task name's command; an unknown name is a *UsageError.
func (l *L) TaskCommand(ctx context.Context, name string) (string, error) {
	tasks, err := l.Tasks(ctx)
	if err != nil {
		return "", err
	}
	for _, t := range tasks {
		if t.Name == name {
			return t.Command, nil
		}
	}
	return "", &UsageError{Msg: "unknown task " + name + "; railswitch task ls"}
}

// Task runs task name of worktree slug's app as exec does (its running
// container, else a transient one), as `sh -c '<command> "$@"' <name>
// <args…>`: the arguments reach the command as its own, unexpanded. A
// non-zero exit is an *ExitError with the task's code.
func (l *L) Task(ctx context.Context, slug, name string, args []string) error {
	cmd, err := l.TaskCommand(ctx, name)
	if err != nil {
		return err
	}
	return l.Exec(ctx, slug, false, append([]string{"sh", "-c", cmd + ` "$@"`, name}, args...))
}
