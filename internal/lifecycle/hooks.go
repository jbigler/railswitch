package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
)

// createHookTimeout bounds each [hooks] create command (a seed can be slow).
const createHookTimeout = 30 * time.Minute

// createHooks runs the project's [hooks] create in slug's running app, in
// order, when the worktree's CreatePending marker is there (D126), then
// removes the marker. A failing hook keeps it: the next up runs them all
// again.
func (l *L) createHooks(ctx context.Context, m *render.Model, slug string) error {
	marker := render.CreatePending(m.Inputs.Workspace, slug)
	if _, err := os.Lstat(marker); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	ctr := containerOf(m, slug, m.Services[0].ID)
	for _, c := range m.Inputs.Project.Hooks.Create {
		fmt.Fprintf(l.Out, "hook create: %s\n", safefile.Printable(c))
		if _, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: []string{"exec", "-w", "/app", ctr, "sh", "-ec", c}, Stdout: l.Out, Stderr: l.Err, Timeout: createHookTimeout}); err != nil {
			return fmt.Errorf("[hooks] create %q in %s: %w (the next up runs the create hooks again)", safefile.Printable(c), ctr, err)
		}
	}
	return os.Remove(marker)
}
