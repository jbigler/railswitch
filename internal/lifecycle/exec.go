package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

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
			args = append(append(args, "-i", "--preserve-fds", "1", "-w", "/app", ctr), hangup(argv)...)
			return l.inContainer(ctx, args)
		}
	}
	envFile := filepath.Join(m.Inputs.Workspace, ".railswitch", "env", slug, app.ID+".env")
	args, err := l.transient(m, in, app, envFile, runOpts{}, argv)
	if err != nil {
		return err
	}
	return l.inContainer(ctx, args)
}

// hangupScript runs "$@" beside a watcher on fd 3, the read end of a pipe
// railswitch holds open (inContainer). podman ends neither an exec session
// nor a run when its client dies (a closed terminal window), so when
// railswitch exits, however it ends, the watcher hangs up the command's
// process group as a closed terminal would, and kills what ignores that.
const hangupScript = `(trap '' HUP; read -r _ <&3; kill -HUP 0; sleep 3; kill -KILL 0) </dev/null >/dev/null 2>&1 & exec 3<&-; exec "$@"`

// hangup is argv under hangupScript; the podman command around it passes
// --preserve-fds 1 and runs through inContainer.
func hangup(argv []string) []string {
	return append([]string{"sh", "-c", hangupScript, "sh"}, argv...)
}

// inContainer is interactive for a podman exec or run of hangup's argv: it
// holds the pipe whose read end is the container's fd 3 until podman exits.
func (l *L) inContainer(ctx context.Context, args []string) error {
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	defer r.Close()
	defer w.Close()
	return l.interactive(ctx, sys.NoTimeout, "podman", args, r)
}

// ExitError is the non-zero exit code of an interactive command (exec,
// deps); its stderr already went to the user.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return "exit status " + strconv.Itoa(e.Code) }

// interactive runs program name (podman, or the host's nvim) with args,
// the caller's stdin, stdout and stderr, and extra as fds 3 onward. A
// non-zero exit is an *ExitError carrying the command's exit code.
func (l *L) interactive(ctx context.Context, timeout time.Duration, name string, args []string, extra ...*os.File) error {
	res, err := l.R.Run(ctx, sys.Cmd{Name: name, Args: args, Stdin: l.In, Stdout: l.Out, Stderr: l.Err, Timeout: timeout, ExtraFiles: extra})
	if err != nil && errors.Is(err, sys.ErrExit) && res.ExitCode > 0 {
		return &ExitError{Code: res.ExitCode}
	}
	return err
}

// runOpts are what a transient run adds to the service's shared settings.
// The zero value (exec, deps) joins the dev network only and sets no name
// (a running container of the service already holds it) and no entrypoint.
type runOpts struct {
	// Full: every network of the service, with the same --add-host entries
	// as its unit (an agent reaches routed hosts through Traefik).
	Full bool
	// Name is the container's --name; "" sets none.
	Name string
	// Entrypoint overrides the image's entrypoint; "" keeps it.
	Entrypoint string
	// Unit adds the unit's --shm-size (an on-demand agent's run stands in
	// for its unit).
	Unit bool
}

// transient is the podman argv of a throwaway container of service s
// running argv: s's image, env file, secrets, volumes, hardening and
// firewall annotations (render's shared run settings), with no Traefik
// route, on the worktree's dev network only unless opts.Full. It refuses
// without the firewall hook descriptor.
func (l *L) transient(m *render.Model, in *render.Instance, s *render.Service, envFile string, opts runOpts, argv []string) ([]string, error) {
	ws := m.Inputs.Workspace
	if err := requireDescriptor(ws, m.Prefix); err != nil {
		return nil, err
	}
	image := in.Values["image."+s.ID]
	if image == "" {
		return nil, errors.New(s.ID + ": no image")
	}
	vols, err := in.VolumeSpecs(s)
	if err != nil {
		return nil, err
	}
	networks := in.Networks(s)[:1] // the dev network: no proxy, no route
	var hosts []string
	if opts.Full {
		networks = in.Networks(s)
		if hosts, err = in.AddHosts(s); err != nil {
			return nil, err
		}
	}
	// --init: a command as PID 1 would ignore hangupScript's SIGHUP.
	args := []string{"--hooks-dir", render.HooksDir(ws), "run", "--rm", "-i", "--init", "--preserve-fds", "1"}
	if l.TTY {
		args = append(args, "-t")
	}
	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}
	cmd := hangup(argv)
	if opts.Entrypoint != "" {
		// The watcher goes in front of the entrypoint: behind it, the
		// entrypoint would take hangup's sh -c as the command to run.
		cmd = hangup(append([]string{opts.Entrypoint}, argv...))
		args = append(args, "--entrypoint", cmd[0])
		cmd = cmd[1:]
	}
	args = append(args, "--label", "traefik.enable=false")
	for _, a := range in.FirewallAnnotations(s, networks) {
		args = append(args, "--annotation", a)
	}
	for _, n := range networks {
		args = append(args, "--network", n)
	}
	for _, h := range hosts {
		args = append(args, "--add-host", h)
	}
	if s.Worktree {
		args = append(args, "--userns", render.WorktreeUserNS, "--user", render.WorktreeUser)
	}
	for _, c := range s.Unit.AddCapability {
		args = append(args, "--cap-add", c)
	}
	if s.DropsAllCaps() {
		args = append(args, "--cap-drop", "all")
	}
	if opts.Unit && s.Unit.ShmSize != "" {
		args = append(args, "--shm-size", s.Unit.ShmSize)
	}
	if n := in.CPUs(s); n != "" {
		args = append(args, "--cpus", n)
	}
	args = append(args, "--security-opt", "no-new-privileges",
		"--memory", s.Unit.Memory, "--pids-limit", strconv.Itoa(s.Unit.PidsLimit),
		"--env-file", envFile)
	args = append(args, in.SecretArgs(s.ID)...)
	for _, v := range vols {
		args = append(args, "-v", v)
	}
	if s.Workdir != "" {
		args = append(args, "-w", s.Workdir)
	}
	args = append(args, image)
	return append(args, cmd...), nil
}
