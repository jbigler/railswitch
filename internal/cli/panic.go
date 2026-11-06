package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/lifecycle"
	"github.com/jbigler/railswitch/internal/machine"
)

// panicDoc is `panic --json` (docs/json.md).
type panicDoc struct {
	Containers []lifecycle.PanicRow `json:"containers"`
}

// railswitch panic: cut every running railswitch container on this machine
// off the network and pause it (D181). No confirmation: it is the
// button for when something is wrong, and a restart undoes it.
func cmdPanic(ctx context.Context, e *env, args []string) int {
	fs := e.flags("panic")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("usage: railswitch panic")
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	reg, err := machine.Read(dirs.Data)
	if errors.Is(err, os.ErrNotExist) {
		reg, err = &machine.Registry{}, nil
	}
	if err != nil {
		return e.fail(err)
	}
	// From the registry alone: a project whose workspace is gone or doesn't
	// load still has its containers cut.
	rows, err := lifecycle.Panic(ctx, e.run, reg.Worktrees)
	if err != nil {
		return e.fail(err)
	}
	doc := panicDoc{Containers: append([]lifecycle.PanicRow{}, rows...)}
	failed := false
	for _, r := range doc.Containers {
		failed = failed || r.Error != ""
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
	} else {
		tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "CONTAINER\tNETWORK\tSTATE\t")
		for _, r := range doc.Containers {
			net, state := "cut", "NOT PAUSED"
			if !r.Quarantined {
				net = "NOT CUT"
			}
			if r.Paused {
				state = "paused"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Container, net, state, r.Error)
		}
		if err := tw.Flush(); err != nil {
			return e.fail(err)
		}
		if len(doc.Containers) == 0 {
			fmt.Fprintln(e.stdout, "no running railswitch containers")
		} else {
			fmt.Fprintln(e.stdout, "Containers stay as they are. To bring a worktree back: railswitch -C <workspace> -w <worktree> restart")
		}
	}
	if failed {
		return 1
	}
	return 0
}
