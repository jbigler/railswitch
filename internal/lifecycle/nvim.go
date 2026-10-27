package lifecycle

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
)

const (
	nvimService = "nvim"
	nvimPort    = "NVIM_PORT"                  // the nvim tool's [[ports]] env
	nvimVolume  = "nvim_config"                // the tool's [[shared]] config volume
	nvimConfig  = "/home/appuser/.config/nvim" // its mount in the container

	// nvimHookTimeout bounds a user's nvim-attach or nvim-detach hook.
	nvimHookTimeout = 30 * time.Second
	// nvimGitTimeout bounds each git command of nvim update (a fetch).
	nvimGitTimeout = 5 * time.Minute
	// nvimProbeTimeout bounds one readiness probe of the nvim server.
	nvimProbeTimeout = 5 * time.Second
)

// Nvim attaches to worktree slug's nvim server, starting its unit when it
// is not active (C4): the user's nvim-attach hook, then the host's
// `nvim --remote-ui` on the server's loopback port (inside the container
// through podman exec when the host has no nvim), then the nvim-detach
// hook. A hook's failure only warns. The attach has no deadline.
func (l *L) Nvim(ctx context.Context, slug string) error {
	m, in, s, ctr, err := l.nvimStarted(ctx, slug)
	if err != nil {
		return err
	}
	var port render.Port
	if i := slices.IndexFunc(s.Publish, func(p render.Port) bool { return p.Env == nvimPort }); i >= 0 {
		port = s.Publish[i]
	} else {
		return fmt.Errorf("%s: the nvim tool publishes no %s", m.Prefix, nvimPort)
	}
	server := "127.0.0.1:" + in.Values[nvimPort]
	l.nvimHook(ctx, "nvim-attach", server)
	if l.LookPath != nil {
		if _, lerr := l.LookPath("nvim"); lerr == nil {
			err = l.interactive(ctx, sys.NoTimeout, "nvim", []string{"--remote-ui", "--server", server})
			l.nvimHook(ctx, "nvim-detach", server)
			return err
		}
	}
	args := []string{"exec", "-i"}
	if l.TTY {
		args = append(args, "-t")
	}
	args = append(args, ctr, "nvim", "--remote-ui", "--server", "127.0.0.1:"+strconv.Itoa(port.Container))
	err = l.interactive(ctx, sys.NoTimeout, "podman", args)
	l.nvimHook(ctx, "nvim-detach", server)
	return err
}

// nvimHook runs the user's hook <ConfigDir>/hooks/<name> when it is an
// executable file, with RAILSWITCH_NVIM_SERVER set to server; a failure
// is a warning.
func (l *L) nvimHook(ctx context.Context, name, server string) {
	if l.ConfigDir == "" {
		return
	}
	path := filepath.Join(l.ConfigDir, "hooks", name)
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return
	}
	_, err = l.R.Run(ctx, sys.Cmd{
		Name: path, Env: append(os.Environ(), "RAILSWITCH_NVIM_SERVER="+server),
		Stdout: l.Err, Stderr: l.Err, Timeout: nvimHookTimeout,
	})
	if err != nil {
		fmt.Fprintf(l.Err, "warning: %s: %v\n", path, err)
	}
}

// nvimStarted loads worktree slug's nvim service and starts its unit when
// it is not active; it returns the model, the instance, the service and
// its container.
func (l *L) nvimStarted(ctx context.Context, slug string) (*render.Model, *render.Instance, *render.Service, string, error) {
	m, in, err := l.load(ctx, slug)
	if err != nil {
		return nil, nil, nil, "", err
	}
	if m.Inputs.Project.Tools.Nvim == nil {
		return nil, nil, nil, "", fmt.Errorf("project %s has no nvim: add [tools.nvim] to project.toml", m.Prefix)
	}
	s, err := service(m, nvimService)
	if err != nil {
		return nil, nil, nil, "", err
	}
	ctr := containerOf(m, slug, nvimService)
	sd := l.systemd()
	unit := ctr + ".service"
	st, err := sd.UnitState(ctx, unit)
	if err != nil {
		return nil, nil, nil, "", err
	}
	if st.Active != "active" {
		if sd.Timeout, err = l.startTimeout([]string{ctr}); err != nil {
			return nil, nil, nil, "", err
		}
		if err := sd.Start(ctx, unit); err != nil {
			return nil, nil, nil, "", l.startFailure(ctx, sd, slug, []string{unit}, err)
		}
	}
	if err := l.nvimReady(ctx, ctr); err != nil {
		return nil, nil, nil, "", err
	}
	return m, in, s, ctr, nil
}

// nvimReady waits until the nvim server in ctr answers an RPC: its
// entrypoint may still be cloning the config (D54) after the unit started.
// NvimPoll and NvimTimeout pace it (zero: 500ms, 2m).
func (l *L) nvimReady(ctx context.Context, ctr string) error {
	poll := cmp.Or(l.NvimPoll, 500*time.Millisecond)
	limit := cmp.Or(l.NvimTimeout, 2*time.Minute)
	deadline := time.Now().Add(limit)
	for {
		_, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: []string{"exec", ctr, "nvim", "--server", "127.0.0.1:7777", "--remote-expr", "1"}, Timeout: nvimProbeTimeout})
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the nvim server in %s did not answer within %s (its entrypoint may still be cloning the config): see railswitch logs nvim", ctr, limit)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// NvimUpdate updates the shared nvim config from [tools.nvim] config_repo
// inside worktree slug's nvim container (D54), starting it when needed:
// `git pull --ff-only`, or with config_ref a fetch of that ref, checked
// out detached. It refuses on local changes, and when the clone's origin
// is not config_repo (the fix is nvim reset-config).
func (l *L) NvimUpdate(ctx context.Context, slug string) error {
	m, err := l.model(ctx)
	if err != nil {
		return err
	}
	n := m.Inputs.Project.Tools.Nvim
	if n == nil || n.ConfigRepo == "" {
		return errors.New("nvim update: project.toml sets no [tools.nvim] config_repo")
	}
	_, _, _, ctr, err := l.nvimStarted(ctx, slug)
	if err != nil {
		return err
	}
	git := func(args ...string) (string, error) {
		argv := append([]string{"exec", ctr, "git", "-C", nvimConfig}, args...)
		res, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: argv, Timeout: nvimGitTimeout})
		if err != nil {
			return "", fmt.Errorf("nvim update: git %s: %w", strings.Join(args, " "), err)
		}
		return string(res.Stdout), nil
	}
	out, err := git("status", "--porcelain")
	if err != nil {
		return err
	}
	if out = strings.TrimRight(out, "\n"); out != "" {
		var paths []string
		for _, line := range strings.Split(out, "\n") {
			if len(line) > 3 {
				paths = append(paths, line[3:])
			}
		}
		return fmt.Errorf("nvim update: the nvim config has local changes (%s): commit or discard them in the nvim container first", strings.Join(paths, ", "))
	}
	origin, err := git("remote", "get-url", "origin")
	if err != nil {
		return err
	}
	if origin = strings.TrimSpace(origin); origin != n.ConfigRepo {
		return fmt.Errorf("nvim config is from %s, project.toml says %s: railswitch nvim reset-config", origin, n.ConfigRepo)
	}
	steps := [][]string{{"pull", "--ff-only"}}
	if n.ConfigRef != "" {
		steps = [][]string{{"fetch", "origin", n.ConfigRef}, {"checkout", "--detach", "FETCH_HEAD"}}
	}
	for _, s := range steps {
		out, err := git(s...)
		if err != nil {
			return err
		}
		fmt.Fprint(l.Out, out)
	}
	return nil
}

// NvimResetConfig empties the project's shared nvim config (D54), after a
// confirmation that defaults to No (yes skips it; without a terminal it
// refuses): it stops every registered worktree's nvim unit and removes the
// <prefix>_nvim_config volume, so the next nvim start clones config_repo.
func (l *L) NvimResetConfig(ctx context.Context, yes bool) error {
	m, err := l.model(ctx)
	if err != nil {
		return err
	}
	n := m.Inputs.Project.Tools.Nvim
	if n == nil {
		return fmt.Errorf("project %s has no nvim: add [tools.nvim] to project.toml", m.Prefix)
	}
	vol := naming.Shared(m.Prefix, nvimVolume)
	exists, err := l.podman().VolumeExists(ctx, vol)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Fprintf(l.Out, "volume %s does not exist: nothing to reset\n", vol)
		return nil
	}
	if !yes {
		if !l.TTY {
			return fmt.Errorf("nvim reset-config deletes volume %s: pass --yes (no terminal to confirm on)", vol)
		}
		fmt.Fprintf(l.Out, "Stop every worktree's nvim and delete volume %s, the shared nvim config? [y/N] ", vol)
		line, _ := bufio.NewReader(l.In).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(l.Out, "aborted")
			return nil
		}
	}
	var units []string
	for _, w := range m.Inputs.Worktrees {
		units = append(units, containerOf(m, w.Slug, nvimService)+".service")
	}
	if err := l.systemd().StopLoaded(ctx, units...); err != nil {
		return err
	}
	if err := l.podman().VolumeRemove(ctx, vol); err != nil {
		return err
	}
	if n.ConfigRepo != "" {
		fmt.Fprintf(l.Out, "the next nvim start clones %s\n", n.ConfigRepo)
	} else {
		fmt.Fprintln(l.Out, "the next nvim start finds an empty config volume")
	}
	return nil
}
