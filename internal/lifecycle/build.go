package lifecycle

import (
	"context"
	"slices"
)

// Build builds every image of worktree slug that is missing: with force,
// every one. With pull (D36) it first re-pulls every FROM ref by tag,
// records the new digests in stack.lock, prints each that changed, and
// rebuilds every step from the first one whose FROM digest changed; that is
// the only way an upstream base image update enters a build. A recorded
// digest is part of the build tags (D36), so the units are re-rendered
// onto the new tags; the next up restarts what runs the old ones.
func (l *L) Build(ctx context.Context, slug string, force, pull bool) error {
	if err := l.WS.CheckLock(); err != nil {
		return err
	}
	_, in, err := l.load(ctx, slug)
	if err != nil {
		return err
	}
	changed, wrote, err := l.pinBases(ctx, in, pull)
	if err != nil {
		return err
	}
	if wrote { // new FROM digests give new build tags (D36): the units follow
		if _, err := l.WS.Render(ctx, l.R, false); err != nil {
			return err
		}
		if _, in, err = l.load(ctx, slug); err != nil {
			return err
		}
	}
	first := len(in.Builds)
	for i, s := range in.Builds {
		if slices.ContainsFunc(s.From, func(r string) bool { return slices.Contains(changed, r) }) {
			first = i
			break
		}
	}
	return l.build(ctx, in.Builds, func(i int) bool { return force || i >= first })
}
