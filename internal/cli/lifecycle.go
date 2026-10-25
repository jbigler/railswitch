package cli

import (
	"context"
	"os"

	"github.com/jbigler/railswitch/internal/lifecycle"
	"github.com/jbigler/railswitch/internal/workspace"
)

// lifecycle loads the workspace and wires a lifecycle.L for the current
// worktree (-w <slug>, or the worktree the working directory is in).
func (e *env) lifecycle(ctx context.Context, cmd string) (*lifecycle.L, string, int) {
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return nil, "", e.fail(err)
	}
	if w.Slug == "" {
		return nil, "", e.usageErr("%s: not in a worktree; run it in <workspace>/<worktree>/ or pass -w <worktree>", cmd)
	}
	tty := false
	if f, ok := e.stdin.(*os.File); ok && isTerminal(f) {
		tty = true
	}
	return &lifecycle.L{R: e.run, WS: w, Out: e.stdout, Err: e.stderr, In: e.stdin, TTY: tty}, w.Slug, 0
}

// simple is a lifecycle command that takes no arguments.
func simple(name string, run func(*lifecycle.L, context.Context, string) error) command {
	return func(ctx context.Context, e *env, args []string) int {
		fs := e.flags(name)
		if code, ok := parse(fs, args); !ok {
			return code
		}
		if fs.NArg() != 0 {
			return e.usageErr("%s takes no arguments", name)
		}
		l, slug, code := e.lifecycle(ctx, name)
		if code != 0 {
			return code
		}
		if err := run(l, ctx, slug); err != nil {
			return e.fail(err)
		}
		return 0
	}
}

var (
	cmdUp      = simple("up", (*lifecycle.L).Up)
	cmdStop    = simple("stop", (*lifecycle.L).Stop)
	cmdRestart = simple("restart", (*lifecycle.L).Restart)
)

// railswitch down [--yes]: stop the worktree, remove its containers and
// per-worktree volumes.
func cmdDown(ctx context.Context, e *env, args []string) int {
	fs := e.flags("down")
	yes := fs.Bool("yes", false, "delete the volumes without asking")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("usage: railswitch down [--yes]")
	}
	l, slug, code := e.lifecycle(ctx, "down")
	if code != 0 {
		return code
	}
	if err := l.Down(ctx, slug, *yes); err != nil {
		return e.fail(err)
	}
	return 0
}

// railswitch status: each service's unit state and health.
func cmdStatus(ctx context.Context, e *env, args []string) int {
	fs := e.flags("status")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("status takes no arguments")
	}
	l, slug, code := e.lifecycle(ctx, "status")
	if code != 0 {
		return code
	}
	if err := l.Status(ctx, slug, e.json); err != nil {
		return e.fail(err)
	}
	return 0
}

// railswitch logs [-f] [service]: a service's logs (default: the app).
func cmdLogs(ctx context.Context, e *env, args []string) int {
	fs := e.flags("logs")
	follow := fs.Bool("f", false, "follow")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() > 1 {
		return e.usageErr("usage: railswitch logs [-f] [service]")
	}
	l, slug, code := e.lifecycle(ctx, "logs")
	if code != 0 {
		return code
	}
	if err := l.Logs(ctx, slug, fs.Arg(0), *follow); err != nil {
		return e.fail(err)
	}
	return 0
}

// railswitch exec [--fresh] -- <cmd>: run cmd in the app.
func cmdExec(ctx context.Context, e *env, args []string) int {
	fs := e.flags("exec")
	fresh := fs.Bool("fresh", false, "run in a new transient container, not the running app")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() == 0 {
		return e.usageErr("usage: railswitch exec [--fresh] -- <command> [args]")
	}
	l, slug, code := e.lifecycle(ctx, "exec")
	if code != 0 {
		return code
	}
	if err := l.Exec(ctx, slug, *fresh, fs.Args()); err != nil {
		return e.fail(err)
	}
	return 0
}

// railswitch build [--force] [--pull]: build the worktree's images.
func cmdBuild(ctx context.Context, e *env, args []string) int {
	fs := e.flags("build")
	force := fs.Bool("force", false, "rebuild images that exist")
	pull := fs.Bool("pull", false, "re-pull every FROM image, record the new digests and rebuild from the first changed one")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("usage: railswitch build [--force] [--pull]")
	}
	l, slug, code := e.lifecycle(ctx, "build")
	if code != 0 {
		return code
	}
	if err := l.Build(ctx, slug, *force, *pull); err != nil {
		return e.fail(err)
	}
	return 0
}

// railswitch deps -- <cmd>: run cmd in a transient deps container, unfrozen
// (D35), e.g. `railswitch deps -- bundle add rack-attack`.
func cmdDeps(ctx context.Context, e *env, args []string) int {
	fs := e.flags("deps")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() == 0 {
		return e.usageErr("usage: railswitch deps -- <command> [args]")
	}
	l, slug, code := e.lifecycle(ctx, "deps")
	if code != 0 {
		return code
	}
	if err := l.Deps(ctx, slug, fs.Args()); err != nil {
		return e.fail(err)
	}
	return 0
}
