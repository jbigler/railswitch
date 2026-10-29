package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/settings"
	"github.com/jbigler/railswitch/internal/tomlfile"
	"github.com/jbigler/railswitch/internal/workspace"
)

const settingsUsage = "usage: railswitch settings preview | apply --if-hash <hash> [--accept-install <review-hash>] [--restart]  (an edit list on stdin)"

// maxStdin caps what settings and project write read from stdin.
const maxStdin = 1 << 20

// railswitch settings preview|apply: staged project.toml edits (D84).
func cmdSettings(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", settingsUsage)
	}
	switch args[0] {
	case "preview":
		return settingsPreview(ctx, e, args[1:])
	case "apply":
		return settingsApply(ctx, e, args[1:])
	}
	return e.usageErr("%s", settingsUsage)
}

// readStdin is all of stdin, at most maxStdin bytes.
func (e *env) readStdin() ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(e.stdin, maxStdin+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxStdin {
		return nil, errors.New("stdin: more than 1 MiB")
	}
	return data, nil
}

func (e *env) readEdits() ([]settings.Edit, error) {
	data, err := e.readStdin()
	if err != nil {
		return nil, err
	}
	return settings.ParseEdits(strings.NewReader(string(data)))
}

// projectBytes is the workspace's project.toml.
func projectBytes(root string) ([]byte, error) {
	return os.ReadFile(filepath.Join(root, "project.toml"))
}

// settingsEnv is the project at -C ready to judge edits, with its current
// project.toml bytes. Which worktrees run their app is one batched systemd
// read.
func (e *env) settingsEnv(ctx context.Context) (settings.Env, []byte, error) {
	w, err := workspace.Load(ctx, e.dir, "")
	if err != nil {
		return settings.Env{}, nil, err
	}
	data, err := projectBytes(w.Root)
	if err != nil {
		return settings.Env{}, nil, err
	}
	se, err := e.envFor(ctx, w)
	return se, data, err
}

// envFor is w ready to judge changes: the machine's registry and host, and
// which of its worktrees' apps run (one systemctl show).
func (e *env) envFor(ctx context.Context, w *workspace.Workspace) (settings.Env, error) {
	host, err := workspace.ReadHost(ctx, e.run)
	if err != nil {
		return settings.Env{}, err
	}
	reg, err := machine.Read(w.Dirs.Data)
	if err != nil {
		return settings.Env{}, err
	}
	prefix := w.Project.Project.Prefix
	var units []string
	for _, wt := range reg.WorktreesOf(prefix) {
		units = append(units, naming.Container(prefix, wt.Slug, w.Project.Stack.App)+".service")
	}
	props, err := podman.Systemd{R: e.run}.ShowMany(ctx, units, "ActiveState")
	if err != nil {
		return settings.Env{}, err
	}
	running := func(unit string) bool { return props[unit]["ActiveState"] == "active" }
	return settings.Env{WS: w, Reg: reg, Host: host, Running: running}, nil
}

// railswitch settings preview: judge an edit list, write nothing.
func settingsPreview(ctx context.Context, e *env, args []string) int {
	fs := e.flags("settings preview")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("settings preview takes no arguments; the edit list is on stdin")
	}
	edits, err := e.readEdits()
	if err != nil {
		return e.fail(err)
	}
	se, data, err := e.settingsEnv(ctx)
	if err != nil {
		return e.fail(err)
	}
	p, err := se.Preview(data, edits)
	if err != nil {
		return e.fail(err)
	}
	install := previewInstall(ctx, se.WS.Root, p)
	if e.json {
		doc := struct {
			*settings.Preview
			Install *reviewDoc `json:"install"`
		}{p, install}
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	for _, r := range p.Edits {
		fmt.Fprintf(e.stdout, "edit %d: %s\n", r.Index, r.Impact)
		if r.Error != "" {
			fmt.Fprintf(e.stdout, "  error: %s\n", r.Error)
		}
		for _, reason := range r.Reasons {
			fmt.Fprintf(e.stdout, "  %s\n", reason)
		}
	}
	if len(p.Restart) > 0 {
		fmt.Fprintf(e.stdout, "restart: %s\n", strings.Join(p.Restart, ", "))
	}
	io.WriteString(e.stdout, p.Diff)
	if install != nil {
		fmt.Fprintf(e.stdout, "install review (apply with --accept-install %s):\n", install.Hash)
		printReview(e.stdout, install)
	}
	return 0
}

// previewInstall is the Install review of the project as p leaves it
// (D106); nil when it installs no library package, or when the edited
// project doesn't resolve (its edits say why).
func previewInstall(ctx context.Context, root string, p *settings.Preview) *reviewDoc {
	next, err := config.Parse([]byte(p.NewText))
	if err != nil || next.Validate() != nil {
		return nil
	}
	doc, _, err := installReview(ctx, root, next, nil, nil)
	if err != nil {
		return nil
	}
	return doc
}

// railswitch settings apply --if-hash <h> [--accept-install <r>]
// [--restart]: write the edited project.toml, lock and render, all or none.
func settingsApply(ctx context.Context, e *env, args []string) int {
	fs := e.flags("settings apply")
	hash := fs.String("if-hash", "", "the project.toml hash the preview saw (required)")
	acceptHash := fs.String("accept-install", "", "accept the install review with this hash (settings preview --json install.hash)")
	restart := fs.Bool("restart", false, "restart the worktrees the preview lists")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 || *hash == "" {
		return e.usageErr("%s", settingsUsage)
	}
	edits, err := e.readEdits()
	if err != nil {
		return e.fail(err)
	}
	se, data, err := e.settingsEnv(ctx)
	if err != nil {
		return e.fail(err)
	}
	if workspace.Hash(data) != *hash {
		return e.fail(errChanged)
	}
	p, err := se.Preview(data, edits)
	if err != nil {
		return e.fail(err)
	}
	blocked := false
	for _, r := range p.Edits {
		if r.Impact == settings.ImpactBlocked {
			fmt.Fprintf(e.stderr, "railswitch: edit %d is blocked: %s\n", r.Index, r.Error)
			blocked = true
		}
	}
	if blocked {
		return 1
	}
	accept := &workspace.Accept{}
	if *acceptHash != "" {
		next, err := config.Parse([]byte(p.NewText))
		if err != nil {
			return e.fail(err)
		}
		if accept, err = e.acceptInstall(ctx, se.WS.Root, next, nil, *acceptHash); err != nil {
			return e.fail(err)
		}
	}
	fi, err := os.Stat(filepath.Join(se.WS.Root, "project.toml"))
	if err != nil {
		return e.fail(err)
	}
	changes := []fileChange{{Rel: "project.toml", Data: []byte(p.NewText), Mode: fi.Mode().Perm()}}
	for _, rel := range slices.Sorted(maps.Keys(p.NewFiles)) {
		// An option's addon the edits eject (D123), committed with it.
		changes = append(changes, fileChange{Rel: rel, Data: p.NewFiles[rel], Mode: 0o644})
	}
	if err := e.commit(ctx, se.WS.Root, changes, accept); err != nil {
		return e.fail(err)
	}
	if *restart {
		return e.restartAll(ctx, p.Restart)
	}
	return 0
}

var errChanged = errors.New("project.toml changed since the preview")

// restartAll restarts slugs in order, stopping at the first failure.
func (e *env) restartAll(ctx context.Context, slugs []string) int {
	if len(slugs) == 0 {
		return 0
	}
	l, code := e.projectLifecycle(ctx)
	if code != 0 {
		return code
	}
	for _, slug := range slugs {
		if err := l.Restart(ctx, slug); err != nil {
			return e.fail(fmt.Errorf("restart %s: %w", slug, err))
		}
		fmt.Fprintf(e.stdout, "restarted %s\n", slug)
	}
	return 0
}

// commitProject replaces root's project.toml with next, keeping its mode,
// then locks accepting what accept says and renders (commit).
func (e *env) commitProject(ctx context.Context, root string, next []byte, accept *workspace.Accept) error {
	fi, err := os.Stat(filepath.Join(root, "project.toml"))
	if err != nil {
		return err
	}
	return e.commit(ctx, root, []fileChange{{Rel: "project.toml", Data: next, Mode: fi.Mode().Perm()}}, accept)
}

// fileChange is one file commit writes: Data at Rel (workspace-relative,
// slash-separated) with Mode, or Rel removed when Data is nil.
type fileChange struct {
	Rel  string
	Data []byte
	Mode os.FileMode
}

// commit writes changes under root, then with lock (non-nil) writes
// stack.lock accepting what it says (workspace.Lock, which stores every
// package it pins), then renders. On any failure it puts back every file it changed, stack.lock
// included (bytes and mode; a file that did not exist is removed, with the
// directories made for it), so the workspace is as it was; the units a
// failed render already wrote are not touched.
func (e *env) commit(ctx context.Context, root string, changes []fileChange, lock *workspace.Accept) (err error) {
	var undo []func() error
	defer func() {
		if err == nil {
			return
		}
		var rerr error
		for i := len(undo) - 1; i >= 0; i-- {
			rerr = errors.Join(rerr, undo[i]())
		}
		if rerr != nil {
			err = errors.Join(err, fmt.Errorf("restoring the workspace: %w", rerr))
		}
	}()
	var wrote []string
	for _, c := range changes {
		restore, err := keep(root, filepath.Join(root, filepath.FromSlash(c.Rel)))
		if err != nil {
			return err
		}
		undo = append(undo, restore)
		line, err := applyChange(root, c)
		if err != nil {
			return err
		}
		wrote = append(wrote, line)
	}
	if lock != nil {
		lockPath := filepath.Join(root, "stack.lock")
		restore, err := keep(root, lockPath)
		if err != nil {
			return err
		}
		undo = append(undo, restore)
		if err := workspace.Lock(ctx, root, *lock); err != nil {
			return err
		}
		wrote = append(wrote, "wrote "+lockPath)
	}
	w, err := workspace.Load(ctx, root, "")
	if err != nil {
		return err
	}
	d, err := e.render(ctx, w, false)
	if err != nil {
		return err
	}
	for _, line := range wrote {
		fmt.Fprintln(e.stdout, line)
	}
	e.printRendered(d)
	return nil
}

// applyChange writes or removes c under root, atomically, and says which.
func applyChange(root string, c fileChange) (string, error) {
	path := filepath.Join(root, filepath.FromSlash(c.Rel))
	if c.Data == nil {
		return "removed " + path, os.Remove(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return "wrote " + path, tomlfile.WriteAtomic(path, c.Data, c.Mode)
}

// keep records path as it is now and returns what puts it back: its bytes
// and mode, or, when it does not exist, its removal and that of the
// directories below root that do not exist yet either.
func keep(root, path string) (func() error, error) {
	fi, err := os.Lstat(path)
	switch {
	case err == nil && !fi.Mode().IsRegular():
		return nil, fmt.Errorf("%s: not a regular file", path)
	case err == nil:
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return func() error { return tomlfile.WriteAtomic(path, data, fi.Mode().Perm()) }, nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	top := "" // the outermost missing directory: MkdirAll makes it
	for dir := filepath.Dir(path); dir != root && len(dir) > len(root); dir = filepath.Dir(dir) {
		if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
			top = dir
		}
	}
	return func() error {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		if top != "" {
			err = errors.Join(err, os.RemoveAll(top))
		}
		return err
	}, nil
}

// judgedWrite is a file write judged by rendering it in memory: the new
// text's hash and what it changes, or why it does not render.
type judgedWrite struct {
	hash   string
	used   *bool            // pack file write: whether the project uses the package
	change *settings.Change // nil with err, or for a package the project does not use
	err    error
}

// writeDoc is `--json containerfile write --dry-run` and `--json pack file
// write --dry-run` (docs/json.md).
type writeDoc struct {
	Hash    string               `json:"hash"`
	Used    *bool                `json:"used,omitempty"`
	Steps   []settings.TagChange `json:"steps"`
	Restart []string             `json:"restart"`
	Errors  []string             `json:"errors"`
}

// printJudged prints a dry run: what w changes, or why it does not render,
// one error per line. Both exit 0, so the page can show either.
func (e *env) printJudged(w judgedWrite) int {
	doc := writeDoc{Hash: w.hash, Used: w.used, Steps: []settings.TagChange{}, Restart: []string{}, Errors: []string{}}
	switch {
	case w.err != nil:
		for _, line := range strings.Split(w.err.Error(), "\n") {
			if line != "" {
				doc.Errors = append(doc.Errors, line)
			}
		}
	case w.change != nil:
		doc.Steps, doc.Restart = w.change.Steps, w.change.Restart
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	for _, s := range doc.Steps {
		fmt.Fprintf(e.stdout, "%s %s: %s -> %s\n", s.Worktree, s.Name, s.OldTag, s.NewTag)
	}
	if len(doc.Restart) > 0 {
		fmt.Fprintf(e.stdout, "restart: %s\n", strings.Join(doc.Restart, ", "))
	}
	for _, msg := range doc.Errors {
		fmt.Fprintf(e.stdout, "error: %s\n", msg)
	}
	return 0
}

// commitJudged commits changes (commit) when w renders, prints each new
// tag, and with restart restarts w's running worktrees (restartAll).
func (e *env) commitJudged(ctx context.Context, root string, w judgedWrite, changes []fileChange, lock *workspace.Accept, restart bool) int {
	if w.err != nil {
		return e.fail(w.err)
	}
	if err := e.commit(ctx, root, changes, lock); err != nil {
		return e.fail(err)
	}
	for _, s := range w.change.Steps {
		if s.NewTag != "" {
			fmt.Fprintf(e.stdout, "%s %s %s\n", s.Worktree, s.Name, s.NewTag)
		}
	}
	if restart && e.restartAll(ctx, w.change.Restart) != 0 {
		return exitWrittenNotRestarted
	}
	return 0
}

// exitWrittenNotRestarted is a write's exit code when the file was saved but
// --restart failed, so the page can tell a kept save from a refused one.
const exitWrittenNotRestarted = 3
