package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
)

// AgentDefault is where an agent's project default came from (D135):
// <workspace>/.railswitch/agent-default/<agent>.toml.
type AgentDefault struct {
	From     string    `json:"from" toml:"from"`         // the worktree promoted
	Promoted time.Time `json:"promoted" toml:"promoted"` // when
}

// defaultFiles and defaultBytes bound a promote: an agent's config, not its data.
const (
	defaultFiles = 20000
	defaultBytes = 256 << 20
)

// PromoteResult is what PromoteAgentDefault did.
type PromoteResult struct {
	Agent     string
	Skipped   []string // paths not copied: symlinks and special files
	Requested []string // worktrees whose homes take the default at their next start
}

// PromoteAgentDefault makes the [config] paths of agent name's home in
// worktree slug the project's default for that agent, replacing the one
// before; with all, every other worktree's existing home takes it at its
// agent's next start (AgentDefaultRequest). New homes always do.
func (l *L) PromoteAgentDefault(ctx context.Context, slug, name string, all bool) (*PromoteResult, error) {
	m, in, a, err := l.defaultAgent(ctx, slug, name)
	if err != nil {
		return nil, err
	}
	home := filepath.Join(in.Values["agent.home"], a.Name)
	if fi, err := os.Lstat(home); err != nil || !fi.IsDir() {
		return nil, &UsageError{Msg: fmt.Sprintf("agent %s has no home in %s yet: run it there first (railswitch agent %s)", a.Name, slug, a.Name)}
	}
	root := filepath.Join(m.Inputs.Workspace, filepath.FromSlash(render.AgentDefaultDir))
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(root, "."+a.Name+".new-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	c := &safefile.TreeCopy{Files: defaultFiles, Bytes: defaultBytes}
	for _, p := range a.Agent.Config.Paths {
		if err := c.Copy(home, p, tmp); err != nil {
			return nil, fmt.Errorf("agent %s: copy %s: %w", a.Name, p, err)
		}
	}
	dest := filepath.Join(root, a.Name)
	old := filepath.Join(root, "."+a.Name+".old")
	if err := os.RemoveAll(old); err != nil {
		return nil, err
	}
	if err := os.Rename(dest, old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return nil, err
	}
	os.RemoveAll(old)
	var meta bytes.Buffer
	if err := toml.NewEncoder(&meta).Encode(AgentDefault{From: slug, Promoted: time.Now().UTC().Truncate(time.Second)}); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, a.Name+".toml"), meta.Bytes(), 0o600); err != nil {
		return nil, err
	}
	r := &PromoteResult{Agent: a.Name, Skipped: c.Skipped}
	if all {
		if r.Requested, err = requestDefault(m, a.Name, func(s string) bool { return s != slug }); err != nil {
			return r, err
		}
	}
	return r, nil
}

// ApplyAgentDefault has agent name's home in worktree slug (with all,
// every worktree's) take the project's default at the agent's next start:
// the worktrees whose homes will. A worktree whose agent never ran has no
// home yet; it starts from the default anyway.
func (l *L) ApplyAgentDefault(ctx context.Context, slug, name string, all bool) (string, []string, error) {
	m, _, a, err := l.defaultAgent(ctx, slug, name)
	if err != nil {
		return "", nil, err
	}
	if ReadAgentDefault(m, a.Name) == nil {
		return a.Name, nil, &UsageError{Msg: fmt.Sprintf("agent %s has no default yet: railswitch agent default promote %s, in the worktree whose config it should be", a.Name, a.Name)}
	}
	slugs, err := requestDefault(m, a.Name, func(s string) bool { return all || s == slug })
	return a.Name, slugs, err
}

// ReadAgentDefault is where agent's default came from, nil when it has
// none (or its record doesn't read).
func ReadAgentDefault(m *render.Model, agent string) *AgentDefault {
	data, err := safefile.Read(filepath.Join(m.Inputs.Workspace, filepath.FromSlash(path.Join(render.AgentDefaultDir, agent+".toml"))), safefile.Limit)
	if err != nil {
		return nil
	}
	var d AgentDefault
	if _, err := toml.Decode(string(data), &d); err != nil || d.From == "" {
		return nil
	}
	return &d
}

// defaultAgent loads worktree slug and agent name (or the only agent),
// which must have [config] paths.
func (l *L) defaultAgent(ctx context.Context, slug, name string) (*render.Model, *render.Instance, *pack.ChosenAgent, error) {
	m, in, err := l.load(ctx, slug)
	if err != nil {
		return nil, nil, nil, err
	}
	if name, err = l.agentName(m, name); err != nil {
		return nil, nil, nil, &UsageError{Msg: err.Error()}
	}
	i := slices.IndexFunc(m.Inputs.Stack.Agents, func(a *pack.ChosenAgent) bool { return a.Name == name })
	a := m.Inputs.Stack.Agents[i]
	if len(a.Agent.Config.Paths) == 0 {
		return nil, nil, nil, &UsageError{Msg: fmt.Sprintf("agent %s's package %s has no [config] paths: it has no default", name, a.Ref)}
	}
	return m, in, a, nil
}

// requestDefault puts AgentDefaultRequest in agent's existing home of
// every worktree pick accepts: those worktrees.
func requestDefault(m *render.Model, agent string, pick func(slug string) bool) ([]string, error) {
	var slugs []string
	for _, wt := range m.Inputs.Worktrees {
		if !pick(wt.Slug) {
			continue
		}
		in, err := m.Instance(wt)
		if err != nil {
			return slugs, err
		}
		home := filepath.Join(in.Values["agent.home"], agent)
		if fi, err := os.Lstat(home); err != nil || !fi.IsDir() {
			continue // no home yet: a new one asks by itself
		}
		// O_NONBLOCK: a FIFO the agent planted there fails at once instead
		// of holding the open until something reads it.
		f, err := os.OpenFile(filepath.Join(home, pack.AgentDefaultRequest), os.O_WRONLY|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
		if err != nil {
			return slugs, fmt.Errorf("%s: %w", wt.Slug, err)
		}
		f.Close()
		slugs = append(slugs, wt.Slug)
	}
	return slugs, nil
}
