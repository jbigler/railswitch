package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// CLI runs `railswitch <args>` (argv[0] is the dashboard's own executable)
// and returns stdout; a non-zero exit is an error with the stderr tail.
// The dashboard reads everything through it, so scripts see exactly what
// the page sees.
type CLI interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// The JSON documents of the read commands, as docs/json.md has them; only
// the fields the page uses.

type projectRow struct {
	Prefix           string `json:"prefix"`
	Workspace        string `json:"workspace"`
	Worktrees        int    `json:"worktrees"`
	WorkspaceMissing bool   `json:"workspace_missing"`
}

type overview struct {
	Prefix    string     `json:"prefix"`
	Workspace string     `json:"workspace"`
	Worktrees []worktree `json:"worktrees"`
}

type worktree struct {
	Slug     string    `json:"slug"`
	ID       int       `json:"id"`
	Branch   string    `json:"branch"`
	Base     bool      `json:"base"`
	Stack    string    `json:"stack"`
	Services []service `json:"services"`
	Agent    *agent    `json:"agent"`
	Links    []link    `json:"links"`
}

type service struct {
	Service string `json:"service"`
	Role    string `json:"role"`
	State   string `json:"state"`
}

type agent struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Message string `json:"message"`
	Reply   bool   `json:"reply"`
	Running bool   `json:"running"`
}

type link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Group string `json:"group"`
}

type prRow struct {
	Slug   string `json:"slug"`
	Branch string `json:"branch"`
	PR     *pr    `json:"pr"`
}

type pr struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	State  string `json:"state"`
	Checks string `json:"checks"`
	Review string `json:"review"`
}

type task struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Source  string `json:"source"`
}

type wtRow struct {
	Slug  string `json:"slug"`
	Dir   string `json:"dir"`
	Dirty bool   `json:"dirty"`
}

type colorsMode struct {
	Base      string            `json:"base"`
	Overrides map[string]string `json:"overrides"`
	Colors    map[string]string `json:"colors"`
}

type colorsDoc struct {
	Light    colorsMode `json:"light"`
	Dark     colorsMode `json:"dark"`
	Warnings struct {
		Light []string `json:"light"`
		Dark  []string `json:"dark"`
	} `json:"warnings"`
}

// readJSON runs `railswitch --json <args>` and decodes its document.
func readJSON[T any](ctx context.Context, c CLI, args ...string) (T, error) {
	var v T
	out, err := c.Run(ctx, append([]string{"--json"}, args...)...)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return v, fmt.Errorf("%s: %w", strings.Join(args, " "), err)
	}
	return v, nil
}

func readProjects(ctx context.Context, c CLI) ([]projectRow, error) {
	return readJSON[[]projectRow](ctx, c, "project", "ls")
}

func readOverview(ctx context.Context, c CLI, ws string) (overview, error) {
	return readJSON[overview](ctx, c, "-C", ws, "overview")
}

func readPRs(ctx context.Context, c CLI, ws string) ([]prRow, error) {
	return readJSON[[]prRow](ctx, c, "-C", ws, "pr", "ls")
}

func readTasks(ctx context.Context, c CLI, ws, slug string) ([]task, error) {
	return readJSON[[]task](ctx, c, "-C", ws, "-w", slug, "task", "ls")
}

func readWorktrees(ctx context.Context, c CLI, ws string) ([]wtRow, error) {
	return readJSON[[]wtRow](ctx, c, "-C", ws, "wt", "ls")
}

func readColors(ctx context.Context, c CLI) (colorsDoc, error) {
	return readJSON[colorsDoc](ctx, c, "dashboard", "colors")
}
