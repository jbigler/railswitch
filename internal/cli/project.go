package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/lifecycle"
	"github.com/jbigler/railswitch/internal/machine"
)

const projectUsage = "usage: railswitch project new|init|rm|ls …"

// railswitch project new|init|rm|ls: the project-level commands (D56).
func cmdProject(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", projectUsage)
	}
	switch args[0] {
	case "new":
		return cmdNew(ctx, e, args[1:])
	case "init":
		return cmdInit(ctx, e, args[1:])
	case "rm":
		return projectRm(ctx, e, args[1:])
	case "ls":
		return projectLs(e, args[1:])
	}
	return e.usageErr("%s", projectUsage)
}

const projectRmUsage = "usage: railswitch project rm <prefix> [--yes] [--purge]"

// railswitch project rm <prefix> [--yes] [--purge]: unregister a project
// and tear down its worktrees, units and networks (lifecycle.ProjectRemove).
func projectRm(ctx context.Context, e *env, args []string) int {
	fs := e.flags("project rm")
	yes := fs.Bool("yes", false, "remove without asking")
	purge := fs.Bool("purge", false, "also delete the project's shared volumes and <workspace>/.railswitch")
	prefix, code, ok := parseNamed(e, fs, args, projectRmUsage)
	if !ok {
		return code
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	tty := false // both ends a terminal, as lifecycle: no prompt into a pipe
	if in, ok := e.stdin.(*os.File); ok && isTerminal(in) {
		if out, ok := e.stdout.(*os.File); ok && isTerminal(out) {
			tty = true
		}
	}
	if err := lifecycle.ProjectRemove(ctx, e.run, dirs, e.stdout, e.stdin, tty, prefix, *yes, *purge); err != nil {
		return e.fail(err)
	}
	return 0
}

// projectRow is one `project ls` entry; its JSON is documented in docs/json.md.
type projectRow struct {
	Prefix           string `json:"prefix"`
	Workspace        string `json:"workspace"`
	Subnet           string `json:"subnet"`
	Worktrees        int    `json:"worktrees"`
	WorkspaceMissing bool   `json:"workspace_missing"`
}

// railswitch project ls: every registered project. It reads the registry
// without locking or creating it.
func projectLs(e *env, args []string) int {
	fs := e.flags("project ls")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("usage: railswitch project ls")
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	reg, err := machine.Read(dirs.Data)
	if errors.Is(err, os.ErrNotExist) { // nothing registered yet
		reg, err = &machine.Registry{}, nil
	}
	if err != nil {
		return e.fail(err)
	}
	rows := []projectRow{}
	for _, p := range reg.Projects {
		_, serr := os.Stat(filepath.Join(p.Workspace, "project.toml"))
		rows = append(rows, projectRow{
			Prefix:           p.Prefix,
			Workspace:        p.Workspace,
			Subnet:           machine.Subnet(p.Subnet),
			Worktrees:        len(reg.WorktreesOf(p.Prefix)),
			WorkspaceMissing: serr != nil,
		})
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(rows); err != nil {
			return e.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PREFIX\tWORKSPACE\tSUBNET\tWORKTREES\t")
	for _, r := range rows {
		note := ""
		if r.WorkspaceMissing {
			note = "workspace missing"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Prefix, r.Workspace, r.Subnet, strconv.Itoa(r.Worktrees), note)
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	return 0
}
