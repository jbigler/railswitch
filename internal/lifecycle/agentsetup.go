package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
)

// SetupAgainResult is what AgentSetupAgain did.
type SetupAgainResult struct {
	Agent   string
	Cleared []string // worktrees whose home runs the setup again at the agent's next start
	Running []string // of those, the ones where the agent runs now
}

// AgentSetupAgain has agent name's [agents.<name>] setup (D133) run again
// at its next start in worktree slug (all: every worktree), by removing
// the setup markers from its home there (D187). A worktree whose agent
// never ran has no home: its setup runs at the first start anyway.
func (l *L) AgentSetupAgain(ctx context.Context, slug, name string, all bool) (*SetupAgainResult, error) {
	m, err := l.model(ctx)
	if err != nil {
		return nil, err
	}
	if name, err = l.agentName(m, name); err != nil {
		return nil, &UsageError{Msg: err.Error()}
	}
	if len(m.Inputs.Project.Agents[name].Setup) == 0 {
		return nil, &UsageError{Msg: fmt.Sprintf("agent %s has no setup commands ([agents.%s] setup in project.toml)", name, name)}
	}
	r := &SetupAgainResult{Agent: name}
	for _, wt := range m.Inputs.Worktrees {
		if !all && wt.Slug != slug {
			continue
		}
		in, err := m.Instance(wt)
		if err != nil {
			return r, err
		}
		ok, err := clearSetupMarkers(filepath.Join(in.Values["agent.home"], name))
		if err != nil {
			return r, fmt.Errorf("%s: %w", wt.Slug, err)
		}
		if !ok {
			continue
		}
		r.Cleared = append(r.Cleared, wt.Slug)
		running, err := l.RunningAgents(ctx, m, wt.Slug)
		if err != nil {
			return r, err
		}
		for _, n := range running {
			if n == name {
				r.Running = append(r.Running, wt.Slug)
			}
		}
	}
	return r, nil
}

// AgentSetupLog is agent name's last setup run's output in worktree slug
// (render.AgentSetupLog in its home): the agent's name, and nil when there
// is none. The agent writes its home: the log is read as hostile (no
// symlink, bounded); print it through safefile.Printable.
func (l *L) AgentSetupLog(ctx context.Context, slug, name string) (string, []byte, error) {
	m, in, err := l.load(ctx, slug)
	if err != nil {
		return "", nil, err
	}
	if name, err = l.agentName(m, name); err != nil {
		return "", nil, &UsageError{Msg: err.Error()}
	}
	data, err := safefile.Read(filepath.Join(in.Values["agent.home"], name, render.AgentSetupLog), safefile.Limit)
	if errors.Is(err, fs.ErrNotExist) {
		return name, nil, nil
	}
	return name, data, err
}

// clearSetupMarkers removes the setup markers from home, an agent's home,
// false when it has none (no such dir, or a symlink). The agent writes its
// home, so a marker may be a symlink: os.RemoveAll removes the link, never
// what it points at, and never follows one inside a dir it removes.
func clearSetupMarkers(home string) (bool, error) {
	if fi, err := os.Lstat(home); err != nil || !fi.IsDir() {
		return false, nil
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if n := e.Name(); n == render.AgentSetupDone || strings.HasPrefix(n, render.AgentSetupMarker) {
			if err := os.RemoveAll(filepath.Join(home, n)); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}
