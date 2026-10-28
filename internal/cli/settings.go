package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/settings"
	"github.com/jbigler/railswitch/internal/tomlfile"
	"github.com/jbigler/railswitch/internal/workspace"
)

const settingsUsage = "usage: railswitch settings preview | apply --if-hash <hash> [--restart]  (an edit list on stdin)"

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
	host, err := workspace.ReadHost(ctx, e.run)
	if err != nil {
		return settings.Env{}, nil, err
	}
	reg, err := machine.Read(w.Dirs.Data)
	if err != nil {
		return settings.Env{}, nil, err
	}
	prefix := w.Project.Project.Prefix
	var units []string
	for _, wt := range reg.WorktreesOf(prefix) {
		units = append(units, naming.Container(prefix, wt.Slug, w.Project.Stack.App)+".service")
	}
	props, err := podman.Systemd{R: e.run}.ShowMany(ctx, units, "ActiveState")
	if err != nil {
		return settings.Env{}, nil, err
	}
	running := func(unit string) bool { return props[unit]["ActiveState"] == "active" }
	return settings.Env{WS: w, Reg: reg, Host: host, Running: running}, data, nil
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
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(p); err != nil {
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
	return 0
}

// railswitch settings apply --if-hash <h> [--restart]: write the edited
// project.toml, lock and render, all or none.
func settingsApply(ctx context.Context, e *env, args []string) int {
	fs := e.flags("settings apply")
	hash := fs.String("if-hash", "", "the project.toml hash the preview saw (required)")
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
	if err := e.commitProject(ctx, se.WS.Root, data, []byte(p.NewText)); err != nil {
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

// commitProject replaces root's project.toml (old) with next, then locks
// and renders. On any failure it writes old and the old stack.lock back, so
// the workspace is as it was; the units a failed render already wrote are
// not touched.
func (e *env) commitProject(ctx context.Context, root string, old, next []byte) (err error) {
	path := filepath.Join(root, "project.toml")
	lockPath := filepath.Join(root, "stack.lock")
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	oldLock, lockErr := os.ReadFile(lockPath)
	if lockErr != nil && !errors.Is(lockErr, os.ErrNotExist) {
		return lockErr
	}
	if err := tomlfile.WriteAtomic(path, next, fi.Mode().Perm()); err != nil {
		return err
	}
	defer func() {
		if err == nil {
			return
		}
		rerr := tomlfile.WriteAtomic(path, old, fi.Mode().Perm())
		if lockErr == nil {
			rerr = errors.Join(rerr, tomlfile.WriteAtomic(lockPath, oldLock, 0o644))
		}
		if rerr != nil {
			err = errors.Join(err, fmt.Errorf("restoring project.toml: %w", rerr))
		}
	}()
	if err := workspace.Lock(ctx, root); err != nil {
		return err
	}
	w, err := workspace.Load(ctx, root, "")
	if err != nil {
		return err
	}
	d, err := e.render(ctx, w, false)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "wrote %s\n", path)
	fmt.Fprintf(e.stdout, "wrote %s\n", lockPath)
	e.printRendered(d)
	return nil
}
