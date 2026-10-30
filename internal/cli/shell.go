package cli

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/term"
	"github.com/jbigler/railswitch/internal/workspace"
)

// railswitch term: a new terminal tab or window with the user's shell in
// the worktree's folder, on the host.
func cmdTerm(ctx context.Context, e *env, args []string) int {
	fs := e.flags("term")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("term takes no arguments")
	}
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return e.fail(err)
	}
	if w.Slug == "" {
		return e.usageErr("term: not in a worktree; run it in <workspace>/<worktree>/ or pass -w <worktree>")
	}
	return e.launchIn(ctx, w.Slug, filepath.Join(w.Root, w.Slug), []string{userShell()})
}

// launchIn opens argv in a new terminal tab or window in dir: 0, or the
// exit code of a run in the current window.
func (e *env) launchIn(ctx context.Context, title, dir string, argv []string) int {
	if err := launchTerm(ctx, e, title, dir, argv); err != nil {
		if x, ok := errors.AsType[*term.ExitError](err); ok {
			return x.Code
		}
		return e.failCommand(err)
	}
	return 0
}

// passwdFile is read for the login shell when $SHELL is unset (a
// dashboard job's environment); tests replace it.
var passwdFile = "/etc/passwd"

// userShell is $SHELL, else the user's login shell from passwd, else
// /bin/sh.
func userShell() string {
	if sh := os.Getenv("SHELL"); filepath.IsAbs(sh) {
		return sh
	}
	if f, err := os.Open(passwdFile); err == nil {
		defer f.Close()
		uid := strconv.Itoa(os.Getuid())
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Split(sc.Text(), ":")
			if len(fields) == 7 && fields[2] == uid && filepath.IsAbs(fields[6]) {
				return fields[6]
			}
		}
	}
	return "/bin/sh"
}
