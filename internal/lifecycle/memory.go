package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/unithash"
)

// Shared agent memory (D136): the project's Dolt server, started by the
// first `railswitch agent` (or a memory command that writes), never by up,
// down or wt rm, and the bd calls railswitch makes itself.

// bdTimeout bounds one bd call railswitch makes (a transient container).
const bdTimeout = 2 * time.Minute

// errNoMemory is the error of a memory command in a project without [memory].
func errNoMemory(m *render.Model) error {
	return fmt.Errorf("project %s has no shared memory: add [memory] to project.toml (docs/guide/agents.md)", m.Prefix)
}

// memoryService is the project's memory server unit.
func memoryService(prefix string) string { return naming.MemoryUnit(prefix) + ".service" }

// MemoryUp starts the project's memory server unless it runs its current
// unit, then creates the beads database when the server has none. A
// project without [memory] has nothing to start.
func (l *L) MemoryUp(ctx context.Context, m *render.Model) error {
	if m.Inputs.Project.Memory == nil {
		return nil
	}
	sd := l.systemd()
	unit := memoryService(m.Prefix)
	ctr := naming.MemoryNetwork(m.Prefix)
	st, err := sd.UnitState(ctx, unit)
	if err != nil {
		return err
	}
	key := unithash.Key(m.Prefix)
	want, err := l.renderedHash(naming.MemoryUnit(m.Prefix)+".container", key)
	if err != nil {
		return fmt.Errorf("the memory unit is not rendered (railswitch render): %w", err)
	}
	if st.Load != "loaded" || st.NeedReload || unithash.FromExec(st.ExecStart, key) != want {
		if err := sd.DaemonReload(ctx); err != nil {
			return err
		}
	}
	if sd.Timeout, err = l.startTimeout([]string{naming.MemoryUnit(m.Prefix)}); err != nil {
		return err
	}
	switch stale, err := l.staleContainer(ctx, ctr, naming.MemoryUnit(m.Prefix)+".container", key); {
	case st.Active != "active":
		done := l.busy("starting the shared memory server "+ctr, "started "+ctr)
		err := sd.Start(ctx, unit)
		done(err)
		if err != nil {
			return fmt.Errorf("starting %s: %w (journalctl --user -u %s)", unit, err, unit)
		}
	case err != nil:
		return err
	case stale:
		if err := sd.Restart(ctx, unit); err != nil {
			return fmt.Errorf("restarting %s: %w", unit, err)
		}
	}
	return l.memoryInit(ctx, m)
}

// memoryInit creates the beads database (`bd init` in server mode, stealth,
// from outside any worktree, into a throwaway BEADS_DIR: railswitch writes
// the agents' own) when the server has none. bd's prefix for issue ids is
// the project's.
func (l *L) memoryInit(ctx context.Context, m *render.Model) error {
	out, err := l.doltSQL(ctx, m, "SHOW DATABASES")
	if err != nil {
		return err
	}
	if bytes.Contains(out, []byte(`"`+render.MemoryDatabase+`"`)) {
		return nil
	}
	_, err = l.bd(ctx, m, nil, "init", "--server", "--stealth", "--non-interactive", "--skip-hooks", "--skip-agents",
		"-p", m.Prefix, "--database", render.MemoryDatabase)
	if err != nil {
		return fmt.Errorf("creating the shared memory database: %w", err)
	}
	fmt.Fprintf(l.Out, "created the shared memory database of %s\n", m.Prefix)
	return nil
}

// doltSQL runs query in the running memory server (`dolt sql` there talks
// to the server) and returns its JSON result.
func (l *L) doltSQL(ctx context.Context, m *render.Model, query string) ([]byte, error) {
	res, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: []string{"exec", naming.MemoryNetwork(m.Prefix), "dolt", "sql", "-r", "json", "-q", query}})
	if err != nil {
		return nil, fmt.Errorf("the shared memory server: %w", err)
	}
	return res.Stdout, nil
}

// bdImage is the agent base image of the first worktree that has one
// built: bd is installed there (D136).
func (l *L) bdImage(ctx context.Context, m *render.Model) (string, error) {
	p := l.podman()
	for _, wt := range m.Inputs.Worktrees {
		in, err := m.Instance(wt)
		if err != nil {
			return "", err
		}
		img := in.Values["image.agent-base"]
		if img == "" {
			return "", fmt.Errorf("project %s has no agents: shared memory needs one", m.Prefix)
		}
		ok, err := p.ImageExists(ctx, img)
		if err != nil {
			return "", err
		}
		if ok {
			return img, nil
		}
	}
	return "", errors.New("no worktree has its agent images built yet: railswitch up (or railswitch agent) first")
}

// bd runs `bd args…` in a transient container from bdImage on the memory
// network only (internal: no way out), read-only, every capability
// dropped, as the agents' user, with the agents' server settings and
// BEADS_DIR read-only from the workspace; stdin may be nil. bd's
// --json output comes in its envelope ({"schema_version", "data"}). It
// returns stdout; a non-zero exit is an error carrying stderr's tail.
func (l *L) bd(ctx context.Context, m *render.Model, stdin io.Reader, args ...string) ([]byte, error) {
	img, err := l.bdImage(ctx, m)
	if err != nil {
		return nil, err
	}
	const dir = "/tmp/beads"
	run := []string{"run", "--rm", "--interactive",
		"--network", naming.MemoryNetwork(m.Prefix),
		"--userns", render.WorktreeUserNS, "--user", render.WorktreeUser,
		"--read-only", "--tmpfs", "/tmp", "--workdir", "/tmp",
		"--cap-drop", "all", "--security-opt", "no-new-privileges", "--memory", "1g", "--pids-limit", "512",
		"--volume", filepath.Join(m.Inputs.Workspace, render.MemoryDir, "beads") + ":" + dir + ":ro,z",
		"--entrypoint", "bd",
	}
	env := render.MemoryEnv("railswitch")
	env["BEADS_DIR"] = dir
	env["HOME"] = "/tmp"
	env["BD_JSON_ENVELOPE"] = "1"
	if len(args) > 0 && args[0] == "init" {
		env["BEADS_DIR"] = "/tmp/init/.beads" // init writes; the agents' stays as railswitch wrote it
	}
	for _, k := range slices.Sorted(maps.Keys(env)) {
		run = append(run, "--env", k+"="+env[k])
	}
	run = append(append(run, img), args...)
	res, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: run, Stdin: stdin, Timeout: bdTimeout})
	if err != nil {
		return nil, fmt.Errorf("bd %s: %w", strings.Join(args, " "), err)
	}
	return res.Stdout, nil
}
