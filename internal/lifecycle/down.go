package lifecycle

import (
	"bufio"
	"context"
	"fmt"
	"strings"
)

// Down stops worktree slug and removes its containers and per-worktree
// volumes, by exact name, after a confirmation that defaults to No (yes
// skips it; without a terminal it refuses). A shared worktree's tailnet
// ports are turned off first (DropShare). Shared "<prefix>_…" volumes and
// images are never touched.
func (l *L) Down(ctx context.Context, slug string, yes bool) error {
	m, err := l.model(ctx)
	if err != nil {
		return err
	}
	names, err := m.NamesFor(slug)
	if err != nil {
		return err
	}
	if !yes {
		if !l.TTY {
			return fmt.Errorf("down deletes %d volumes of %s: pass --yes (no terminal to confirm on)", len(names.Volumes), slug)
		}
		fmt.Fprintf(l.Out, "Delete %d volumes of %s? [y/N] ", len(names.Volumes), slug)
		line, _ := bufio.NewReader(l.In).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(l.Out, "aborted")
			return nil
		}
	}
	if err := l.dropShare(ctx, m, slug); err != nil {
		return err
	}
	if err := l.systemd().StopLoaded(ctx, names.Units...); err != nil {
		return err
	}
	p := l.podman()
	for _, c := range names.Containers {
		if err := p.RemoveContainer(ctx, c); err != nil {
			return err
		}
	}
	for _, v := range names.Volumes {
		if err := p.RemoveVolume(ctx, v); err != nil {
			return err
		}
	}
	return nil
}
