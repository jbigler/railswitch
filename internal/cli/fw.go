package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/firewall"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
)

// fwTimeout bounds one firewall.sh run: show resolves each rejected address
// (2 s each), allow resolves each host.
const fwTimeout = 2 * time.Minute

const fwUsage = "usage: railswitch fw show [service] | railswitch fw allow <service> <host|ip>..."

// railswitch fw show [svc] | fw allow <svc> <host…>: run the rendered
// firewall.sh, which inspects or adjusts the live firewall of this
// worktree's containers (show: every worktree's at the workspace root).
func cmdFw(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr(fwUsage)
	}
	sub := args[0]
	fs := e.flags("fw " + sub)
	if code, ok := parse(fs, args[1:]); !ok {
		return code
	}
	rest := fs.Args()
	switch {
	case sub == "show" && len(rest) <= 1, sub == "allow" && len(rest) >= 2:
	default:
		return e.usageErr(fwUsage)
	}
	if len(rest) > 0 {
		if err := naming.ValidService(rest[0]); err != nil {
			return e.usageErr("fw %s: %v", sub, err)
		}
	}
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return e.fail(err)
	}
	if sub == "allow" && w.Slug == "" {
		return e.usageErr("fw allow: not in a worktree; run it in <workspace>/<worktree>/ or pass -w <worktree>")
	}
	dir := filepath.Join(w.Root, filepath.FromSlash(firewall.Dir))
	script := filepath.Join(dir, "firewall.sh")
	if _, err := os.Stat(script); err != nil {
		return e.fail(fmt.Errorf("%w: run railswitch render", err))
	}
	_, err = e.run.Run(ctx, sys.Cmd{
		Name: script,
		Args: append([]string{sub}, rest...),
		Env: append(os.Environ(),
			"FIREWALL_DIR="+dir,
			"PREFIX="+w.Project.Project.Prefix,
			"WORKTREE="+w.Slug,
			"SELF_FIREWALLED="+strings.Join(selfFirewalled(w.Stack), ",")),
		Stdout:  e.stdout,
		Stderr:  e.stderr,
		Timeout: fwTimeout,
	})
	if errors.Is(err, sys.ErrExit) {
		return 1 // firewall.sh printed why
	}
	if err != nil {
		return e.fail(err)
	}
	return 0
}

// selfFirewalled is the ids of st's services that run their own firewall
// (firewall = "self") and so get no hook.
func selfFirewalled(st *pack.Stack) []string {
	var ids []string
	for _, slot := range config.Slots {
		if ch := st.Options[slot]; ch != nil && slices.Contains(ch.Option.Firewall, "self") {
			ids = append(ids, ch.Service)
		}
	}
	for _, t := range st.Tools {
		if slices.Contains(t.Firewall, "self") {
			ids = append(ids, t.Service)
		}
	}
	return ids
}
