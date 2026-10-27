package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/jbigler/railswitch/internal/gitguard"
	"github.com/jbigler/railswitch/internal/workspace"
)

const hooksUsage = "usage: railswitch hooks check | railswitch hooks backup [<hook>…] | railswitch hooks restore [--force]"

// railswitch hooks check|backup|restore: git-guard (D60). check lists what
// to review (exit 1 with findings); backup copies installed hooks of the
// base worktree to the workspace repo's githooks/ (commit them after);
// restore installs the backups, keeping a differing installed hook unless
// --force.
func cmdHooks(ctx context.Context, e *env, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "check":
		fs := e.flags("hooks check")
		if code, ok := parse(fs, args); !ok {
			return code
		}
		if fs.NArg() != 0 {
			return e.usageErr("%s", hooksUsage)
		}
		w, _, code := e.hooksWorkspace(ctx)
		if code != 0 {
			return code
		}
		findings, err := w.GitGuard(ctx, e.run)
		if err != nil {
			return e.fail(err)
		}
		for _, f := range findings {
			fmt.Fprintln(e.stdout, f)
		}
		if len(findings) > 0 {
			return 1
		}
		return 0
	case "backup":
		fs := e.flags("hooks backup")
		if code, ok := parse(fs, args); !ok {
			return code
		}
		w, base, code := e.hooksWorkspace(ctx)
		if code != 0 {
			return code
		}
		wrote, err := gitguard.Backup(w.Root, base, fs.Args())
		for _, n := range wrote {
			fmt.Fprintf(e.stdout, "%s: backed up to %s\n", n, filepath.Join(w.Root, gitguard.BackupDir, n))
		}
		if err != nil {
			return e.fail(err)
		}
		if len(wrote) == 0 {
			fmt.Fprintln(e.stdout, "backups already current")
		}
		return 0
	case "restore":
		fs := e.flags("hooks restore")
		force := fs.Bool("force", false, "replace installed hooks that differ from their backups")
		if code, ok := parse(fs, args); !ok {
			return code
		}
		if fs.NArg() != 0 {
			return e.usageErr("%s", hooksUsage)
		}
		w, base, code := e.hooksWorkspace(ctx)
		if code != 0 {
			return code
		}
		kept, err := gitguard.Restore(w.Root, base, *force)
		if err != nil {
			return e.fail(err)
		}
		for _, n := range kept {
			fmt.Fprintf(e.stderr, "%s: the installed hook differs from its backup; kept (railswitch hooks restore --force replaces it)\n", n)
		}
		if len(kept) > 0 {
			return 1
		}
		fmt.Fprintf(e.stdout, "hooks match %s\n", filepath.Join(w.Root, gitguard.BackupDir))
		return 0
	}
	return e.usageErr("%s", hooksUsage)
}

// hooksWorkspace loads the workspace and finds its base worktree.
func (e *env) hooksWorkspace(ctx context.Context) (*workspace.Workspace, string, int) {
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return nil, "", e.fail(err)
	}
	base, err := w.BaseDir()
	if err != nil {
		return nil, "", e.fail(err)
	}
	return w, base, 0
}
