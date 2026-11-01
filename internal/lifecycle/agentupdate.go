package lifecycle

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/render"
)

// AgentUpdate rebuilds agent name's image without the build cache (D138),
// so what its Containerfile downloads at build time (an agent's "latest"
// release) is fetched again. The image is the project's: every
// worktree's agent starts on it from then on; a running one keeps the old
// image until it stops. The replaced image is removed when no container
// uses it.
func (l *L) AgentUpdate(ctx context.Context, slug, name string) error {
	if err := l.WS.CheckLock(); err != nil {
		return err
	}
	m, in, err := l.load(ctx, slug)
	if err != nil {
		return err
	}
	if name, err = l.agentName(m, name); err != nil {
		return err
	}
	i := slices.IndexFunc(m.Inputs.Stack.Agents, func(a *pack.ChosenAgent) bool { return a.Name == name })
	step := "agent " + strings.TrimPrefix(m.Inputs.Stack.Agents[i].Agent.Image.File, "Containerfile.")
	j := slices.IndexFunc(in.Builds, func(s render.BuildStep) bool { return s.Name == step })
	if j < 0 {
		return fmt.Errorf("agent %s has no image build in worktree %s", name, slug)
	}
	// The images it builds on first, as up builds them.
	if err := l.build(ctx, in.Builds[:j], func(int) bool { return false }); err != nil {
		return err
	}
	s := in.Builds[j]
	p := l.podman()
	old, err := p.ImageID(ctx, s.Tag)
	if err != nil {
		return err
	}
	fmt.Fprintf(l.Out, "rebuilding %s (%s) without the cache\n", s.Name, s.Tag)
	if err := p.Build(ctx, podman.BuildArgs{File: s.File, Tag: s.Tag, Context: s.Context, Args: s.Args, Prefix: m.Prefix, Progress: l.Out, NoCache: true}); err != nil {
		return fmt.Errorf("build %s: %w", s.Name, err)
	}
	if now, err := p.ImageID(ctx, s.Tag); err == nil && old != "" && now != old {
		if p.ImageRemove(ctx, old) != nil {
			fmt.Fprintf(l.Out, "the old image is still used by a running agent: it runs the old version until it stops (railswitch agent stop)\n")
		}
	}
	fmt.Fprintf(l.Out, "updated agent %s: every worktree's next start runs the new image\n", name)
	return nil
}
