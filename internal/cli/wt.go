package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/lifecycle"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/workspace"
	"github.com/jbigler/railswitch/internal/worktree"
)

const wtUsage = `usage: railswitch wt add [--name <name>] [--up] <branch|PR number|new branch>
       railswitch wt rm [--force] <path|branch|name>
       railswitch wt ls`

// railswitch wt add|rm|ls: the project's worktrees.
func cmdWt(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", wtUsage)
	}
	switch args[0] {
	case "add":
		return wtAdd(ctx, e, args[1:])
	case "rm":
		return wtRm(ctx, e, args[1:])
	case "ls":
		return wtLs(ctx, e, args[1:])
	}
	return e.usageErr("%s", wtUsage)
}

// manager loads the workspace and wires a worktree.Manager to it.
func (e *env) manager(ctx context.Context) (*worktree.Manager, error) {
	w, err := workspace.Load(ctx, e.dir, "")
	if err != nil {
		return nil, err
	}
	tty := false
	if f, ok := e.stdin.(*os.File); ok && isTerminal(f) {
		tty = true
	}
	return &worktree.Manager{
		R:         e.run,
		WS:        w.Root,
		Pfx:       w.Project.Project.Prefix,
		Data:      w.Dirs.Data,
		Proc:      procRoot,
		Base:      w.Project.Project.BaseBranch,
		SeedFiles: w.Project.Project.SeedFiles,
		In:        e.stdin,
		Out:       e.stdout,
		TTY:       tty,
		Render: func(ctx context.Context) error {
			_, err := w.Render(ctx, e.run, false)
			return err
		},
		Names: func(slug string) (render.Names, error) {
			m, err := e.model(ctx, w)
			if err != nil {
				return render.Names{}, err
			}
			return m.NamesFor(slug)
		},
		Ports: func(reg *machine.Registry) (map[string]int, error) {
			// The bases only: the host values change no port.
			m, err := w.Model(reg, render.Host{})
			if err != nil {
				return nil, err
			}
			return m.PortBases(), nil
		},
		Unshare: func(ctx context.Context, slug string) error {
			l := &lifecycle.L{R: e.run, WS: w, Out: e.stdout, Err: e.stderr, In: e.stdin}
			return l.DropShare(ctx, slug)
		},
		WorktreeVolumes: worktree.VolumeDirs(w.Stack.Pack),
	}, nil
}

// railswitch wt add [--name <name>] [--up] <target>: add a worktree; with
// --up, then run up in it.
func wtAdd(ctx context.Context, e *env, args []string) int {
	fs := e.flags("wt add")
	name := fs.String("name", "", "the worktree's `name` (default: the branch, sanitized)")
	up := fs.Bool("up", false, "run up in the new worktree")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 {
		return e.usageErr("%s", wtUsage)
	}
	m, err := e.manager(ctx)
	if err != nil {
		return e.fail(err)
	}
	w, err := m.Add(ctx, fs.Arg(0), *name)
	if err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "added worktree %s (ID %d, branch %s) at %s\n", w.Slug, w.ID, w.Branch, w.Dir)
	if !*up {
		return 0
	}
	e.slug = w.Slug // up in the new worktree, wherever -C points
	l, slug, code := e.lifecycle(ctx, "up")
	if code != 0 {
		return code
	}
	if err := l.Up(ctx, slug); err != nil {
		return e.fail(err)
	}
	return 0
}

func wtRm(ctx context.Context, e *env, args []string) int {
	fs := e.flags("wt rm")
	force := fs.Bool("force", false, "remove a dirty worktree, discarding its changes, and its nvim and agent homes (agent logins)")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 {
		return e.usageErr("%s", wtUsage)
	}
	m, err := e.manager(ctx)
	if err != nil {
		return e.fail(err)
	}
	if err := m.Remove(ctx, fs.Arg(0), *force); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "removed worktree %s\n", fs.Arg(0))
	return 0
}

func wtLs(ctx context.Context, e *env, args []string) int {
	fs := e.flags("wt ls")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("%s", wtUsage)
	}
	m, err := e.manager(ctx)
	if err != nil {
		return e.fail(err)
	}
	rows, err := m.List(ctx)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(rows); err != nil {
			return e.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SLUG\tID\tBRANCH\tDIRTY\tDIR")
	for _, r := range rows {
		dirty := ""
		if r.Dirty {
			dirty = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Slug, strconv.Itoa(r.ID), r.Branch, dirty, r.Dir)
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	return 0
}
