package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/jbigler/railswitch/internal/lifecycle"
)

const nvimUsage = "usage: railswitch nvim [--new-term] [<file>[:<line>[:<col>]]] | railswitch nvim update | railswitch nvim reset-config [--yes]"

// lookPath finds a host program for lifecycle (the host's nvim); tests
// replace it.
var lookPath = exec.LookPath

// railswitch nvim [--new-term] [<file>[:<line>[:<col>]]]: attach to the
// worktree's nvim server (C4), first opening file there (nvimTarget),
// or with --new-term in a new terminal tab or window; nvim update:
// update the shared config from [tools.nvim] config_repo; nvim
// reset-config [--yes]: empty the shared config volume (D54).
func cmdNvim(ctx context.Context, e *env, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "update":
		if len(args) != 0 {
			return e.usageErr("%s", nvimUsage)
		}
		l, slug, code := e.lifecycle(ctx, "nvim update")
		if code != 0 {
			return code
		}
		if err := l.NvimUpdate(ctx, slug); err != nil {
			return e.fail(err)
		}
		return 0
	case "reset-config":
		fs := e.flags("nvim reset-config")
		yes := fs.Bool("yes", false, "delete the config volume without asking")
		if code, ok := parse(fs, args); !ok {
			return code
		}
		if fs.NArg() != 0 {
			return e.usageErr("%s", nvimUsage)
		}
		l, code := e.projectLifecycle(ctx)
		if code != 0 {
			return code
		}
		if err := l.NvimResetConfig(ctx, *yes); err != nil {
			return e.fail(err)
		}
		return 0
	}
	return cmdNvimAttach(ctx, e, append([]string{sub}, args...))
}

// cmdNvimAttach is railswitch nvim [--new-term] [<file>[:<line>[:<col>]]].
func cmdNvimAttach(ctx context.Context, e *env, args []string) int {
	if len(args) > 0 && args[0] == "" {
		args = args[1:]
	}
	fs := e.flags("nvim")
	newTerm := fs.Bool("new-term", false, "open nvim in a new terminal tab or window")
	pos, code, ok := parseMixed(fs, args)
	if !ok {
		return code
	}
	if len(pos) > 1 {
		return e.usageErr("%s", nvimUsage)
	}
	l, slug, code := e.lifecycle(ctx, "nvim")
	if code != 0 {
		return code
	}
	dir := filepath.Join(l.WS.Root, slug)
	var target *lifecycle.NvimTarget
	if len(pos) == 1 {
		cwd, _ := os.Getwd()
		t, err := nvimTarget(pos[0], dir, cwd)
		if err != nil {
			return e.usageErr("%v", err)
		}
		target = &t
	}
	if *newTerm {
		exe, err := executable()
		if err != nil {
			return e.fail(err)
		}
		argv := []string{exe, "-C", l.WS.Root, "-w", slug, "nvim"}
		if target != nil {
			argv = append(argv, fmt.Sprintf("%s:%d:%d", target.Path, target.Line, target.Col))
		}
		return e.launchIn(ctx, slug+" nvim", dir, argv)
	}
	if err := l.Nvim(ctx, slug, target); err != nil {
		return e.failCommand(err)
	}
	return 0
}

// nvimLineRe splits "<file>:<line>[:<col>]" from anything a tool prints
// after it (a Ruby backtrace's ":in 'method'", a compiler's ": message"):
// the shortest file that leaves a line number behind it.
var nvimLineRe = regexp.MustCompile(`^(.+?):(\d+)(?::(\d+))?(?::\D.*|:)?$`)

// nvimTarget is arg, a file in worktree dir with an optional line and
// column, as the server sees it: /app/<path in the worktree>. The file may
// be given as the container sees it (/app/…), as an absolute host path in
// the worktree, or relative: to the worktree root when the file is there
// (what test runners print), else to cwd when that is in the worktree,
// else to the root (a new file). Anything outside the worktree is refused.
func nvimTarget(arg, dir, cwd string) (lifecycle.NvimTarget, error) {
	var t lifecycle.NvimTarget
	file := arg
	if m := nvimLineRe.FindStringSubmatch(arg); m != nil {
		file = m[1]
		t.Line, _ = strconv.Atoi(m[2])
		t.Col, _ = strconv.Atoi(m[3])
	}
	if strings.ContainsFunc(file, unicode.IsControl) {
		return t, fmt.Errorf("nvim: %q is not a file name", arg)
	}
	var rel string
	switch {
	case file == "/app" || strings.HasPrefix(file, "/app/"):
		rel = strings.TrimPrefix(strings.TrimPrefix(file, "/app"), "/")
	case filepath.IsAbs(file):
		r, err := filepath.Rel(dir, filepath.Clean(file))
		if err != nil {
			return t, err
		}
		rel = r
	default:
		rel = file
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			if r, err := filepath.Rel(dir, filepath.Join(cwd, file)); err == nil && filepath.IsLocal(r) {
				if _, err := os.Stat(filepath.Join(cwd, file)); err == nil {
					rel = r
				}
			}
		}
	}
	rel = filepath.Clean(rel)
	if rel != "." && !filepath.IsLocal(rel) {
		return t, fmt.Errorf("nvim: %s is outside the worktree %s", file, dir)
	}
	t.Path = path.Join("/app", filepath.ToSlash(rel))
	return t, nil
}
