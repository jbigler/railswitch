package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jbigler/railswitch/internal/doctor"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
)

// railswitch doctor: check the host (and the workspace, inside one); exit 1
// when a check fails. A warning (C8) never changes the exit code.
func cmdDoctor(ctx context.Context, e *env, args []string) int {
	fs := e.flags("doctor")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("doctor takes no arguments")
	}
	var ws *workspace.Workspace
	var checks []doctor.Check
	if _, err := workspace.Root(e.dir); err == nil {
		// A workspace that doesn't load is a failed check; the host is
		// still checked.
		if ws, err = workspace.Load(ctx, e.dir, e.slug); err != nil {
			ws, checks = nil, []doctor.Check{doctor.Workspace(err)}
		}
	}
	return e.printChecks(append(checks, doctor.Run(ctx, e.run, ws)...))
}

// printChecks prints doctor's checks, as JSON with --json, and returns its
// exit code: 1 when a check failed, a warning aside.
func (e *env) printChecks(checks []doctor.Check) int {
	code := 0
	for _, c := range checks {
		if !c.OK && !c.Warn {
			code = 1
		}
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(checks); err != nil {
			return e.fail(err)
		}
		return code
	}
	for _, c := range checks {
		status := "ok  "
		switch {
		case !c.OK && c.Warn:
			status = "warn"
		case !c.OK:
			status = "FAIL"
		}
		fmt.Fprintf(e.stdout, "%s  %s", status, c.Name)
		if c.Detail != "" {
			fmt.Fprintf(e.stdout, ": %s", c.Detail)
		}
		fmt.Fprintln(e.stdout)
		if !c.OK && c.Fix != "" {
			fmt.Fprintf(e.stdout, "      fix: %s\n", c.Fix)
		}
	}
	return code
}

const (
	// portSysctl lets rootless podman publish the proxy's port 80.
	portSysctl = "net.ipv4.ip_unprivileged_port_start=80"
	// sysctlConf keeps portSysctl across reboots.
	sysctlConf = "/etc/sysctl.d/90-railswitch.conf"
)

// railswitch allow-ports: after a [y/N] prompt, set portSysctl now and in
// sysctlConf, through sudo. It runs nothing without a yes.
func cmdAllowPorts(ctx context.Context, e *env, args []string) int {
	fs := e.flags("allow-ports")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("allow-ports takes no arguments")
	}
	fmt.Fprintf(e.stdout, "Rootless podman can publish port 80 (the proxy) once these run as root:\n"+
		"  sudo sysctl -w %s\n"+
		"  echo %s | sudo tee %s\n"+
		"Run them? [y/N] ", portSysctl, portSysctl, sysctlConf)
	line, err := bufio.NewReader(e.stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return e.fail(err)
	}
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		fmt.Fprintln(e.stdout, "\nnothing changed")
		return 1
	}
	// sudo may ask for a password on the tty: no deadline.
	if _, err := e.run.Run(ctx, sys.Cmd{
		Name: "sudo", Args: []string{"sysctl", "-w", portSysctl},
		Stdout: e.stdout, Stderr: e.stderr, Timeout: sys.NoTimeout,
	}); err != nil {
		return e.fail(err)
	}
	if _, err := e.run.Run(ctx, sys.Cmd{
		Name: "sudo", Args: []string{"tee", sysctlConf},
		Stdin: strings.NewReader(portSysctl + "\n"), Stdout: io.Discard, Stderr: e.stderr, Timeout: sys.NoTimeout,
	}); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "wrote %s\n", sysctlConf)
	return 0
}
