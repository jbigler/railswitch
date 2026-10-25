// Package doctor checks that the host can run railswitch: podman and its
// quadlet generator, the podman socket, unprivileged port 80, the tools the
// firewall hook needs, the workspace's firewall descriptor and units, cgroup
// v2 and, with the pass secret driver (D37), pass, gpg and the GPG key.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/firewall"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
)

// Check is one doctor finding: Fix says how to repair a failed check ("" for
// a passed one). Its JSON form is `doctor --json`'s (docs/json.md).
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

// Host is where doctor reads the host's proc and sys files and looks up
// binaries; DefaultHost is the real host.
type Host struct {
	PortStart string   // ip_unprivileged_port_start
	Quadlet   []string // podman's quadlet generator: the first that exists
	Cgroup    string   // cgroup v2's root cgroup.controllers
	LookPath  func(name string) (string, error)
}

// DefaultHost is this machine.
var DefaultHost = Host{
	PortStart: "/proc/sys/net/ipv4/ip_unprivileged_port_start",
	Quadlet:   podman.QuadletPaths,
	Cgroup:    "/sys/fs/cgroup/cgroup.controllers",
	LookPath:  exec.LookPath,
}

// minPodman is the oldest podman major version railswitch supports.
const minPodman = 5

// tools are the binaries the firewall hook and its scripts run on the host.
var tools = []string{"nft", "nsenter", "getent", "jq", "curl"}

// Run runs every check on DefaultHost; see Host.Run.
func Run(ctx context.Context, r sys.Runner, ws *workspace.Workspace) []Check {
	return DefaultHost.Run(ctx, r, ws)
}

// Run runs every check, in a fixed order. The firewall descriptor and units
// checks need ws (nil outside a workspace: skipped); the pass, gpg and GPG
// key checks run only when the machine config's secret driver is pass.
func (h Host) Run(ctx context.Context, r sys.Runner, ws *workspace.Workspace) []Check {
	out := []Check{h.podman(ctx, r), h.quadlet(), socket(ctx, r), h.port()}
	for _, t := range tools {
		out = append(out, h.onPath(t, "install "+t+" (the egress firewall hook runs it on the host)"))
	}
	if ws != nil {
		out = append(out, descriptor(ws), units(ctx, r, ws))
	}
	out = append(out, h.cgroup())
	out = append(out, h.secrets(ctx, r, ws)...)
	for i := range out {
		if out[i].OK {
			out[i].Fix = ""
		}
	}
	return out
}

// Workspace is the failed check for a workspace that doesn't load (a bad
// project.toml, no stack.lock, a bad -w): its descriptor and units go
// unchecked.
func Workspace(err error) Check {
	c := Check{Name: "workspace", Detail: err.Error(), Fix: "fix the problem named in detail, then run railswitch doctor again"}
	if errors.Is(err, config.ErrNoLock) {
		c.Fix = "railswitch lock"
	}
	return c
}

func (h Host) podman(ctx context.Context, r sys.Runner) Check {
	c := Check{Name: "podman >= 5.0", Fix: "install podman 5.0 or newer"}
	res, err := r.Run(ctx, sys.Cmd{Name: "podman", Args: []string{"version", "--format", "{{.Client.Version}}"}})
	if err != nil {
		c.Detail = err.Error()
		return c
	}
	v := strings.TrimSpace(string(res.Stdout))
	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		c.Detail = fmt.Sprintf("podman reported version %q", v)
		return c
	}
	c.Detail = "podman " + v
	c.OK = n >= minPodman
	return c
}

func (h Host) quadlet() Check {
	c := Check{Name: "quadlet generator", Fix: "install podman's quadlet generator at " + strings.Join(h.Quadlet, " or ") + " (podman 5 ships it; some distributions package it as podman-quadlet)"}
	var errs []string
	for _, p := range h.Quadlet {
		if _, err := os.Stat(p); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		c.OK, c.Detail = true, p
		return c
	}
	c.Detail = strings.Join(errs, "; ")
	return c
}

func (h Host) cgroup() Check {
	return exists("cgroup v2", h.Cgroup, "boot with the unified cgroup v2 hierarchy (systemd.unified_cgroup_hierarchy=1)")
}

// exists checks that path exists.
func exists(name, path, fix string) Check {
	if _, err := os.Stat(path); err != nil {
		return Check{Name: name, Detail: err.Error(), Fix: fix}
	}
	return Check{Name: name, OK: true, Detail: path}
}

func socket(ctx context.Context, r sys.Runner) Check {
	c := Check{Name: "podman.socket active", Fix: "systemctl --user enable --now podman.socket"}
	res, err := r.Run(ctx, sys.Cmd{Name: "systemctl", Args: []string{"--user", "is-active", "podman.socket"}})
	state := strings.TrimSpace(string(res.Stdout))
	switch {
	case err == nil && state == "active":
		c.OK, c.Detail = true, state
	case state != "":
		c.Detail = state
	case err != nil:
		c.Detail = err.Error()
	default:
		c.Detail = "systemctl reported no state"
	}
	return c
}

func (h Host) port() Check {
	c := Check{Name: "unprivileged port 80", Fix: "railswitch allow-ports"}
	data, err := os.ReadFile(h.PortStart)
	if err != nil {
		c.Detail = err.Error()
		return c
	}
	v := strings.TrimSpace(string(data))
	n, err := strconv.Atoi(v)
	if err != nil {
		c.Detail = fmt.Sprintf("%s: %q is not a number", h.PortStart, v)
		return c
	}
	c.Detail = "net.ipv4.ip_unprivileged_port_start = " + v
	c.OK = n <= 80
	return c
}

func (h Host) onPath(name, fix string) Check {
	p, err := h.LookPath(name)
	if err != nil {
		return Check{Name: name, Detail: err.Error(), Fix: fix}
	}
	return Check{Name: name, OK: true, Detail: p}
}

// descriptor checks the firewall's OCI hook descriptor, without which podman
// starts the project's containers unfirewalled.
func descriptor(ws *workspace.Workspace) Check {
	return exists("firewall descriptor", firewall.DescriptorPath(ws.Root, ws.Project.Project.Prefix), "railswitch render")
}

// units checks that render would change nothing. A stale stack.lock is fixed
// by railswitch lock, anything else by railswitch render.
func units(ctx context.Context, r sys.Runner, ws *workspace.Workspace) Check {
	c := Check{Name: "units current", Fix: "railswitch render"}
	d, err := ws.Render(ctx, r, true)
	if err != nil {
		c.Detail = err.Error()
		if errors.Is(err, workspace.ErrStaleLock) {
			c.Fix = "railswitch lock"
		}
		return c
	}
	var parts []string
	for _, p := range []struct {
		what  string
		names []string
	}{{"added", d.Added}, {"changed", d.Changed}, {"removed", d.Removed}} {
		if len(p.names) > 0 {
			parts = append(parts, p.what+" "+strings.Join(p.names, ", "))
		}
	}
	if len(parts) == 0 && d.Text == "" {
		c.OK, c.Detail = true, "render would change nothing"
		return c
	}
	c.Detail = "render would change: " + strings.Join(parts, "; ")
	return c
}

// secrets checks the pass secret driver's needs (D37): pass and gpg on PATH
// and the GPG key in driver_opts.key. With another driver, or no machine
// config, it checks nothing.
func (h Host) secrets(ctx context.Context, r sys.Runner, ws *workspace.Workspace) []Check {
	var dirs machine.Dirs
	if ws != nil {
		dirs = ws.Dirs
	} else {
		var err error
		if dirs, err = machine.DefaultDirs(); err != nil {
			return []Check{{Name: "machine config", Detail: err.Error(), Fix: "set HOME (or XDG_CONFIG_HOME) to an absolute path"}}
		}
	}
	cfg, err := machine.LoadConfig(dirs)
	if err != nil {
		return []Check{{Name: "machine config", Detail: err.Error(), Fix: "fix " + filepath.Join(dirs.Config, "config.toml")}}
	}
	if cfg.Secrets.Driver != "pass" {
		return nil
	}
	cfgPath := filepath.Join(dirs.Config, "config.toml")
	out := []Check{
		h.onPath("pass", "install pass (the pass secret driver in "+cfgPath+" stores secrets with it)"),
		h.onPath("gpg", "install gnupg (the pass secret driver encrypts with gpg)"),
	}
	c := Check{Name: "gpg secret key"}
	key := cfg.Secrets.DriverOpts["key"]
	if key == "" {
		c.Detail = "the pass secret driver has no key"
		c.Fix = "set [secrets] driver_opts.key in " + cfgPath + " to the id of a key `gpg --list-secret-keys` lists"
		return append(out, c)
	}
	var args []string
	if home := cfg.Secrets.DriverOpts["gpghomedir"]; home != "" {
		args = append(args, "--homedir", home)
	}
	args = append(args, "--list-secret-keys", key)
	if _, err := r.Run(ctx, sys.Cmd{Name: "gpg", Args: args}); err != nil {
		c.Detail = fmt.Sprintf("gpg has no secret key %s: %v", key, err)
		c.Fix = "import the secret key " + key + " into gpg, or set [secrets] driver_opts.key in " + cfgPath + " to a key `gpg --list-secret-keys` lists"
		return append(out, c)
	}
	c.OK, c.Detail = true, key
	return append(out, c)
}
