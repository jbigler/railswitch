package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/prs"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
	"github.com/jbigler/railswitch/internal/worktree"
)

const tidyUsage = "usage: railswitch [--json] tidy [--all] [--days <n>] | tidy [--all] [--days <n>] --rm [--force] <id>..."

// tidyDoc is `tidy --json` (docs/json.md): what could go, and why.
type tidyDoc struct {
	Days     int        `json:"days"`
	Items    []tidyItem `json:"items"`
	Bytes    int64      `json:"bytes"`
	Warnings []string   `json:"warnings"`
}

// tidyItem is one candidate: a worktree (wt:<prefix>/<slug>) or an image
// (image:<name>).
type tidyItem struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"` // "worktree" or "image"
	Project string   `json:"project"`
	Name    string   `json:"name"` // the slug, or the image's name
	Branch  string   `json:"branch"`
	Reasons []string `json:"reasons"` // pr-merged, pr-closed, stopped; unused
	PR      int      `json:"pr"`
	Stack   string   `json:"stack"`
	// IdleSince is when the stopped stack was last seen doing anything:
	// its units' last stop (IdleFrom "systemd"), else HEAD's commit time
	// ("commit"); "" when unknown.
	IdleSince string `json:"idle_since"`
	IdleFrom  string `json:"idle_from"`
	Dirty     bool   `json:"dirty"`
	Bytes     int64  `json:"bytes"`
	Partial   bool   `json:"partial"` // the walk stopped at maxWalk: bytes is a lower bound
}

// railswitch tidy (D160): list the worktrees whose PR is merged or closed
// or whose stack has been stopped for --days, and the images gc would
// remove, with their sizes. --rm removes the named ones by the existing
// paths (wt rm, podman rmi by exact name), each judged again first: an
// item no longer a candidate, or dirty without --force, is refused.
func cmdTidy(ctx context.Context, e *env, args []string) int {
	fs := e.flags("tidy")
	all := fs.Bool("all", false, "every registered project, not just this one")
	days := fs.Int("days", 14, "a stopped stack is a candidate after `n` days idle")
	rm := fs.Bool("rm", false, "remove the items named (by id), each checked again first")
	force := fs.Bool("force", false, "with --rm: also remove the dirty worktrees named, discarding their changes and homes (wt rm --force)")
	ids, code, ok := parseMixed(fs, args)
	if !ok {
		return code
	}
	if *days < 1 || *rm == (len(ids) == 0) || *force && !*rm || *rm && e.json {
		return e.usageErr("%s", tidyUsage)
	}
	doc, err := e.tidy(ctx, *all, *days)
	if err != nil {
		return e.fail(err)
	}
	if *rm {
		return e.tidyRemove(ctx, doc, ids, *force)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tREASON\tSIZE\tNOTE")
	for _, it := range doc.Items {
		why := strings.Join(it.Reasons, ",")
		if it.PR != 0 {
			why += " #" + strconv.Itoa(it.PR)
		}
		var note []string
		if it.Dirty {
			note = append(note, "dirty: needs --force")
		}
		if it.IdleSince != "" {
			note = append(note, "idle since "+it.IdleSince[:10])
		}
		size := humanBytes(it.Bytes)
		if it.Partial {
			size = "≥" + size
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", it.ID, why, size, strings.Join(note, "; "))
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	if len(doc.Items) == 0 {
		fmt.Fprintln(e.stdout, "nothing to tidy")
	} else {
		fmt.Fprintf(e.stdout, "%d items, %s: railswitch tidy --rm <id>... removes them\n", len(doc.Items), humanBytes(doc.Bytes))
	}
	for _, w := range doc.Warnings {
		fmt.Fprintf(e.stderr, "warning: %s\n", w)
	}
	return 0
}

// tidy lists the candidates of the project (-C), or of every registered
// one.
func (e *env) tidy(ctx context.Context, all bool, days int) (tidyDoc, error) {
	doc := tidyDoc{Days: days, Items: []tidyItem{}, Warnings: []string{}}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return doc, err
	}
	var projects []string // workspaces
	prefix := ""
	if all {
		reg, err := readRegistry(dirs)
		if err != nil {
			return doc, err
		}
		for _, pe := range reg.Projects {
			projects = append(projects, pe.Workspace)
		}
	} else {
		w, err := workspace.Load(ctx, e.dir, "")
		if err != nil {
			return doc, err
		}
		projects, prefix = []string{w.Root}, w.Project.Project.Prefix
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	for _, ws := range projects {
		items, err := e.tidyWorktrees(ctx, ws, cutoff, &doc.Warnings)
		if err != nil {
			if !all {
				return doc, err
			}
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("project %s doesn't load: %v", ws, err))
		}
		doc.Items = append(doc.Items, items...)
	}
	// The images gc would remove: its judgement over every registered project.
	var gd gcDoc
	keep, err := e.gcKeep(ctx, dirs, &gd)
	if err != nil {
		return doc, err
	}
	if err := gcImages(ctx, podman.Podman{R: e.run}, keep, &gd); err != nil {
		return doc, err
	}
	doc.Warnings = append(doc.Warnings, gd.Warnings...)
	for _, img := range gd.Images {
		pfx, _ := podman.BuiltPrefix(img.Name)
		if all || pfx == prefix {
			doc.Items = append(doc.Items, tidyItem{ID: "image:" + img.Name, Kind: "image", Project: pfx, Name: img.Name, Reasons: []string{"unused"}, Bytes: img.Size})
		}
	}
	for _, it := range doc.Items {
		doc.Bytes += it.Bytes
	}
	return doc, nil
}

// tidyWorktrees is workspace ws's worktree candidates; no PR state (gh
// missing, not GitHub) is a warning.
func (e *env) tidyWorktrees(ctx context.Context, ws string, cutoff time.Time, warnings *[]string) ([]tidyItem, error) {
	pe := *e
	pe.dir, pe.slug = ws, ""
	l, code := pe.projectLifecycle(ctx)
	if code != 0 {
		return nil, errors.New("its workspace doesn't load")
	}
	pfx := l.WS.Project.Project.Prefix
	ov, err := l.Overview(ctx)
	if err != nil {
		return nil, err
	}
	mgr, err := pe.manager(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := mgr.List(ctx)
	if err != nil {
		return nil, err
	}
	var list []prs.PR
	var branches []string
	for _, w := range ov.Worktrees {
		if !w.Base && w.Branch != "" {
			branches = append(branches, w.Branch)
		}
	}
	if repo, err := prs.Repo(l.WS.Project.Project.Repo); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("%s: no PR states: %v", pfx, err))
	} else if list, err = prs.List(ctx, e.run, repo, branches); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("%s: no PR states: %v", pfx, err))
	}
	var out []tidyItem
	for _, w := range ov.Worktrees {
		i := slices.IndexFunc(rows, func(r worktree.Row) bool { return r.Slug == w.Slug })
		if w.Base || i < 0 {
			continue
		}
		r := rows[i]
		it := tidyItem{ID: "wt:" + pfx + "/" + w.Slug, Kind: "worktree", Project: pfx, Name: w.Slug, Branch: safefile.Printable(w.Branch), Reasons: []string{}, Stack: w.Stack, Dirty: r.Dirty}
		if w.Branch != "" && list != nil {
			if p := prs.ForBranch(list, w.Branch, e.headCommit(ctx, r.Dir)); p != nil && (p.State == "merged" || p.State == "closed") {
				it.Reasons, it.PR = append(it.Reasons, "pr-"+p.State), p.Number
			}
		}
		if w.Stack == "stopped" {
			since, from := e.idleSince(ctx, mgr.Names, w.Slug, r.Dir)
			if !since.IsZero() {
				it.IdleSince, it.IdleFrom = since.UTC().Format(time.RFC3339), from
			}
			if !since.IsZero() && since.Before(cutoff) {
				it.Reasons = append(it.Reasons, "stopped")
			}
		}
		if len(it.Reasons) == 0 {
			continue
		}
		it.Bytes, it.Partial = dirBytes(r.Dir)
		out = append(out, it)
	}
	return out, nil
}

// idleSince is when worktree slug's stopped stack last did anything: the
// latest stop of its units systemd remembers, else HEAD's commit time;
// zero when neither is known.
func (e *env) idleSince(ctx context.Context, names func(string) (render.Names, error), slug, dir string) (time.Time, string) {
	if n, err := names(slug); err == nil {
		if t, err := (podman.Systemd{R: e.run}).InactiveSince(ctx, n.Units); err == nil && !t.IsZero() {
			return t, "systemd"
		}
	}
	// Hooks, fsmonitor and signature checks off: the worktree's git config is not trusted.
	res, err := e.run.Run(ctx, sys.Cmd{Name: "git", Args: []string{"-C", dir, "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "log", "-1", "--no-show-signature", "--format=%ct", "HEAD", "--"}})
	if err != nil {
		return time.Time{}, ""
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(string(res.Stdout)), 10, 64)
	if err != nil || sec <= 0 {
		return time.Time{}, ""
	}
	return time.Unix(sec, 0), "commit"
}

// maxWalk bounds dirBytes: a node_modules can hold a million files.
const maxWalk = 200_000

// dirBytes is the apparent size of dir's regular files, symlinks not
// followed; partial when the walk stopped after maxWalk entries.
func dirBytes(dir string) (n int64, partial bool) {
	seen := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable: not counted
		}
		if seen++; seen > maxWalk {
			partial = true
			return filepath.SkipAll
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n, partial
}

// tidyRemove removes the items ids names that doc (just read) still
// lists: worktrees by wt rm's path, then images by exact name. Exit 1
// when any is refused or fails; the others still go.
func (e *env) tidyRemove(ctx context.Context, doc tidyDoc, ids []string, force bool) int {
	failed := false
	refuse := func(format string, a ...any) {
		fmt.Fprintf(e.stderr, "railswitch: tidy: "+format+"\n", a...)
		failed = true
	}
	var picked []tidyItem
	for _, id := range ids {
		i := slices.IndexFunc(doc.Items, func(it tidyItem) bool { return it.ID == id })
		switch {
		case i < 0:
			refuse("%s is not a candidate now; kept", safefile.Printable(id))
		case doc.Items[i].Dirty && !force:
			refuse("%s has uncommitted changes or untracked files; kept (--force discards them)", id)
		case !slices.ContainsFunc(picked, func(it tidyItem) bool { return it.ID == id }):
			picked = append(picked, doc.Items[i])
		}
	}
	slices.SortStableFunc(picked, func(a, b tidyItem) int { return strings.Compare(b.Kind, a.Kind) }) // worktrees first
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	reg, err := readRegistry(dirs)
	if err != nil {
		return e.fail(err)
	}
	for _, it := range picked {
		if it.Kind == "image" {
			if err := (podman.Podman{R: e.run}).ImageRemove(ctx, it.Name); err != nil {
				refuse("image %s: %v", it.Name, err)
				continue
			}
			fmt.Fprintf(e.stdout, "removed image %s\n", it.Name)
			continue
		}
		i := slices.IndexFunc(reg.Projects, func(p machine.ProjectEntry) bool { return p.Prefix == it.Project })
		if i < 0 {
			refuse("%s: project %s is not registered", it.ID, it.Project)
			continue
		}
		pe := *e
		pe.dir, pe.slug = reg.Projects[i].Workspace, ""
		mgr, err := pe.manager(ctx)
		if err == nil {
			err = mgr.Remove(ctx, it.Name, force && it.Dirty)
		}
		if err != nil {
			refuse("%s: %v", it.ID, err)
			continue
		}
		fmt.Fprintf(e.stdout, "removed worktree %s\n", it.ID)
	}
	if failed {
		return 1
	}
	return 0
}
