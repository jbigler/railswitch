package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/lifecycle"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/workspace"
)

// panicDoc is `panic --json` (docs/json.md).
type panicDoc struct {
	Containers []lifecycle.PanicRow `json:"containers"`
	Projects   []panicProject       `json:"projects"` // a project that could not be reached
}

type panicProject struct {
	Prefix string `json:"prefix"`
	Error  string `json:"error"`
}

// railswitch panic: cut every running railswitch container on this machine
// off the network and pause every agent (D181). No confirmation: it is the
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
	doc := panicDoc{Containers: []lifecycle.PanicRow{}, Projects: []panicProject{}}
	for _, p := range reg.Projects {
		if _, err := os.Stat(filepath.Join(p.Workspace, "project.toml")); err != nil {
			doc.Projects = append(doc.Projects, panicProject{p.Prefix, "workspace missing: " + p.Workspace})
			continue
		}
		w, err := workspace.Load(ctx, p.Workspace, "")
		if err == nil {
			l := &lifecycle.L{R: e.run, WS: w, Out: e.stdout, Err: e.stderr, In: e.stdin}
			var rows []lifecycle.PanicRow
			if rows, err = l.Panic(ctx); err == nil {
				doc.Containers = append(doc.Containers, rows...)
			}
		}
		if err != nil {
			doc.Projects = append(doc.Projects, panicProject{p.Prefix, err.Error()})
		}
	}
	failed := len(doc.Projects) > 0
	for _, r := range doc.Containers {
		failed = failed || r.Error != ""
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
	} else {
		tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "CONTAINER\tNETWORK\tAGENT\t")
		for _, r := range doc.Containers {
			net, agent := "cut", ""
			if !r.Quarantined {
				net = "NOT CUT"
			}
			if r.Paused {
				agent = "paused"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Container, net, agent, r.Error)
		}
		for _, p := range doc.Projects {
			fmt.Fprintf(tw, "%s (project)\t\t\t%s\n", p.Prefix, p.Error)
		}
		if err := tw.Flush(); err != nil {
			return e.fail(err)
		}
		if len(doc.Containers) == 0 && len(doc.Projects) == 0 {
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
