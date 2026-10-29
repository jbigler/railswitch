package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
)

// createHookTimeout bounds each [hooks] create command (a seed can be slow).
const createHookTimeout = 30 * time.Minute

// createHooks runs the project's [hooks] create in slug's running app, in
// order, while the worktree's CreatePending marker is there (D126). Each
// command that succeeds is recorded in the marker, so a later up starts at
// the first one not done (a list changed since starts where it differs);
// the marker goes when all have run. Without ready (the app's route never
// came up) they wait for the next up.
func (l *L) createHooks(ctx context.Context, m *render.Model, slug string, ready bool) error {
	marker := render.CreatePending(m.Inputs.Workspace, slug)
	data, err := os.ReadFile(marker)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	cmds := m.Inputs.Project.Hooks.Create
	if len(cmds) == 0 {
		return os.Remove(marker)
	}
	if !ready {
		fmt.Fprintf(l.Err, "warning: [hooks] create not run: the app isn't ready; the next up runs them\n")
		return nil
	}
	done := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(data) == 0 {
		done = nil
	}
	ctr := containerOf(m, slug, m.Services[0].ID)
	for i, c := range cmds {
		if i < len(done) && done[i] == c {
			fmt.Fprintf(l.Out, "hook create: %s (done before)\n", safefile.Printable(c))
			continue
		}
		done = done[:min(i, len(done))]
		fmt.Fprintf(l.Out, "hook create: %s\n", safefile.Printable(c))
		if _, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: []string{"exec", "-w", "/app", ctr, "sh", "-ec", c}, Stdout: l.Out, Stderr: l.Err, Timeout: createHookTimeout}); err != nil {
			return fmt.Errorf("[hooks] create %q in %s: %w (the next up runs it again, and those after it)", safefile.Printable(c), ctr, err)
		}
		done = append(done, c)
		// Atomic: a torn marker would run a finished hook again.
		tmp := marker + ".tmp"
		if err := os.WriteFile(tmp, []byte(strings.Join(done, "\n")+"\n"), 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, marker); err != nil {
			return err
		}
	}
	return os.Remove(marker)
}
