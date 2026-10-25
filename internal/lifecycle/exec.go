package lifecycle

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/firewall"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
)

// Exec runs argv in worktree slug's app: in its running container unless
// fresh, else in a transient container built from the app's service model.
// Neither form has a deadline (an interactive console).
func (l *L) Exec(ctx context.Context, slug string, fresh bool, argv []string) error {
	if len(argv) == 0 {
		return errors.New("exec: no command")
	}
	m, in, err := l.load(ctx, slug)
	if err != nil {
		return err
	}
	app := &m.Services[0]
	ctr := containerOf(m, slug, app.ID)
	if !fresh {
		running, err := l.podman().Running(ctx, ctr)
		if err != nil {
			return err
		}
		if running {
			args := []string{"exec"}
			if l.TTY {
				args = append(args, "-t")
			}
			args = append(append(args, "-i", "-w", "/app", ctr), argv...)
			return l.interactive(ctx, sys.NoTimeout, args)
		}
	}
	envFile := filepath.Join(m.Inputs.Workspace, ".railswitch", "env", slug, app.ID+".env")
	args, err := l.transient(m, in, app, envFile, argv)
	if err != nil {
		return err
	}
	return l.interactive(ctx, sys.NoTimeout, args)
}

// interactive runs podman args with the caller's stdin, stdout and stderr.
func (l *L) interactive(ctx context.Context, timeout time.Duration, args []string) error {
	_, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: args, Stdin: l.In, Stdout: l.Out, Stderr: l.Err, Timeout: timeout})
	return err
}

// transient is the podman argv of a throwaway container of service s
// running argv: s's image, env file, secrets, mounts, volumes, hardening and
// firewall annotations, on the worktree's dev network only, with no Traefik
// route. It refuses without the firewall hook descriptor.
func (l *L) transient(m *render.Model, in *render.Instance, s *render.Service, envFile string, argv []string) ([]string, error) {
	ws, prefix, slug := m.Inputs.Workspace, m.Prefix, in.Worktree.Slug
	if err := requireDescriptor(ws, prefix); err != nil {
		return nil, err
	}
	image := in.Values["image."+s.ID]
	if image == "" {
		return nil, errors.New(s.ID + ": no image")
	}
	sc := in.Scope(s.ID)
	var errs []error
	x := func(t string) string {
		v, err := pack.Expand(t, sc)
		errs = append(errs, err)
		return v
	}
	dev := naming.DevNetwork(prefix, slug)
	mode := m.Inputs.Project.Firewall.Mode
	if mode == "" {
		mode = "log"
	}
	args := []string{"--hooks-dir", filepath.Join(ws, firewall.Dir, "hooks.d"), "run", "--rm", "-i"}
	if l.TTY {
		args = append(args, "-t")
	}
	args = append(args,
		"--label", "traefik.enable=false",
		"--annotation", prefix+".firewall="+strings.Join(s.Firewall, ","),
		"--annotation", prefix+".firewall.networks="+dev,
		"--annotation", prefix+".firewall.mode="+mode,
		"--network", dev)
	if s.Worktree {
		args = append(args, "--userns", "keep-id:uid=1000,gid=1000", "--user", "1000:1000")
	}
	if s.Role != render.RoleData && len(s.Unit.AddCapability) == 0 {
		args = append(args, "--cap-drop", "all")
	}
	args = append(args, "--security-opt", "no-new-privileges",
		"--memory", s.Unit.Memory, "--pids-limit", strconv.Itoa(s.Unit.PidsLimit),
		"--env-file", envFile)
	args = append(args, in.SecretArgs(s.ID)...)
	for _, mt := range s.Mounts {
		v := x(mt.Source) + ":" + x(mt.Target)
		if mt.Options != "" {
			v += ":" + mt.Options
		}
		args = append(args, "-v", v)
	}
	for _, vol := range s.Volumes {
		name, err := in.VolumeName(s.ID, vol)
		errs = append(errs, err)
		v := name + ":" + x(vol.Mount)
		if vol.Chown {
			v += ":U"
		}
		args = append(args, "-v", v)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	args = append(args, "-w", "/app", image)
	return append(args, argv...), nil
}
