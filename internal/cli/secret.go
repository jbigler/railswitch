package cli

import (
	"bytes"
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
	"text/tabwriter"
	"time"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/workspace"
)

const secretUsage = `usage: railswitch secret add <name> --service <id>... [--env VAR] [--machine]
       railswitch secret set <name> [--restart]
       railswitch secret ls
       railswitch secret rm <name>`

// maxSecretValue is podman's limit on a secret's size.
const maxSecretValue = 512000

// railswitch secret add|set|ls|rm: project secrets stored as podman
// secrets (D33). A value only ever travels on podman's stdin.
func cmdSecret(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", secretUsage)
	}
	switch args[0] {
	case "add":
		return secretAdd(ctx, e, args[1:])
	case "set":
		return secretSet(ctx, e, args[1:])
	case "ls":
		return secretLs(ctx, e, args[1:])
	case "rm":
		return secretRm(ctx, e, args[1:])
	}
	return e.usageErr("%s", secretUsage)
}

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

func secretAdd(ctx context.Context, e *env, args []string) int {
	fs := e.flags("secret add")
	var services stringList
	fs.Var(&services, "service", "service `id` that receives the secret (repeatable)")
	envVar := fs.String("env", "", "deliver as env `VAR` instead of the file /run/secrets/<name>")
	mach := fs.Bool("machine", false, "machine scope: podman secret railswitch_<name>, shared by every project")
	name, code, ok := parseNamed(e, fs, args, secretUsage)
	if !ok {
		return code
	}
	if len(services) == 0 {
		return e.usageErr("secret add: list at least one --service")
	}
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return e.fail(err)
	}
	if _, ok := w.Project.Secrets[name]; ok {
		return e.fail(fmt.Errorf("secret %s is already declared in project.toml; set its value with railswitch secret set %s", name, name))
	}
	m, err := e.model(ctx, w)
	if err != nil {
		return e.fail(err)
	}
	ids := make([]string, len(m.Services))
	for i, s := range m.Services {
		ids[i] = s.ID
	}
	for _, id := range services {
		if !slices.Contains(ids, id) {
			return e.usageErr("secret add: no service %s (services: %s)", id, strings.Join(ids, ", "))
		}
	}
	sec := config.Secret{Services: services, Env: *envVar}
	if *mach {
		sec.Scope = "machine"
	}
	// Build the model again with the declaration added, so render's checks
	// (an env var collision, D33) refuse it before anything is written.
	cand := *w.Project
	cand.Secrets = maps.Clone(w.Project.Secrets)
	if cand.Secrets == nil {
		cand.Secrets = map[string]config.Secret{}
	}
	cand.Secrets[name] = sec
	if err := cand.Validate(); err != nil {
		return e.fail(err)
	}
	w.Project = &cand
	if m, err = e.model(ctx, w); err != nil {
		return e.fail(err)
	}
	value, err := e.readSecretValue(ctx, name)
	if err != nil {
		return e.fail(err)
	}
	defer clear(value)
	if err := config.AddSecret(filepath.Join(w.Root, "project.toml"), name, sec); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "declared [secrets.%s] in project.toml\n", name)
	return e.storeSecret(ctx, w, m, name, sec, value, false)
}

func secretSet(ctx context.Context, e *env, args []string) int {
	fs := e.flags("secret set")
	restart := fs.Bool("restart", false, "restart the running containers that receive it")
	name, code, ok := parseNamed(e, fs, args, secretUsage)
	if !ok {
		return code
	}
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return e.fail(err)
	}
	sec, code, ok := e.declared(w, "set", name)
	if !ok {
		return code
	}
	m, err := e.model(ctx, w)
	if err != nil {
		return e.fail(err)
	}
	value, err := e.readSecretValue(ctx, name)
	if err != nil {
		return e.fail(err)
	}
	defer clear(value)
	return e.storeSecret(ctx, w, m, name, sec, value, *restart)
}

// declared returns project secret name, or a usage error naming the
// declared ones.
func (e *env) declared(w *workspace.Workspace, cmd, name string) (config.Secret, int, bool) {
	sec, ok := w.Project.Secrets[name]
	if !ok {
		names := slices.Sorted(maps.Keys(w.Project.Secrets))
		return sec, e.usageErr("secret %s: %s is not declared in project.toml [secrets] (declared: %s); declare it with railswitch secret add",
			cmd, name, strings.Join(names, ", ")), false
	}
	return sec, 0, true
}

// model builds w's model with every registered worktree.
func (e *env) model(ctx context.Context, w *workspace.Workspace) (*render.Model, error) {
	host, err := workspace.ReadHost(ctx, e.run)
	if err != nil {
		return nil, err
	}
	reg, err := machine.Open(w.Dirs.Data)
	if err != nil {
		return nil, err
	}
	defer reg.Close()
	return w.Model(reg.Registry, host)
}

// readSecretValue reads one secret value: on a terminal from a no-echo
// prompt, else all of stdin with one trailing newline stripped. An empty
// value or one holding a newline is refused.
func (e *env) readSecretValue(ctx context.Context, name string) ([]byte, error) {
	var value []byte
	if f, ok := e.stdin.(*os.File); ok && isTerminal(f) {
		fmt.Fprintf(e.stderr, "Value for %s: ", name)
		v, err := readNoEcho(ctx, f)
		fmt.Fprintln(e.stderr)
		if err != nil {
			return nil, err
		}
		value = v
	} else {
		v, err := io.ReadAll(io.LimitReader(e.stdin, maxSecretValue+2))
		if err != nil {
			return nil, err
		}
		value = bytes.TrimSuffix(v, []byte("\n"))
	}
	if err := ctx.Err(); err != nil {
		clear(value)
		return nil, err
	}
	switch {
	case len(value) == 0:
		return nil, fmt.Errorf("secret %s: the value is empty", name)
	case bytes.ContainsAny(value, "\n"):
		clear(value)
		return nil, fmt.Errorf("secret %s: the value holds a line break; a secret value is one line", name)
	case len(value) > maxSecretValue:
		clear(value)
		return nil, fmt.Errorf("secret %s: the value is longer than podman's limit of %d bytes", name, maxSecretValue)
	}
	return value, nil
}

// storeSecret creates or replaces the podman secret of name with value under
// the machine's driver (D37), then names the running containers that keep
// the old value, restarting them with restart.
func (e *env) storeSecret(ctx context.Context, w *workspace.Workspace, m *render.Model, name string, sec config.Secret, value []byte, restart bool) int {
	cfg, err := machine.LoadConfig(w.Dirs)
	if err != nil {
		return e.fail(err)
	}
	p := podman.Podman{R: e.run}
	pname := render.SecretName(m.Prefix, name, sec)
	exists, err := p.SecretExists(ctx, pname)
	if err != nil {
		return e.fail(err)
	}
	d := podman.Driver{Name: cfg.Secrets.Driver, Opts: cfg.Secrets.DriverOpts}
	// podman's --replace fails on a missing secret (#29612). A value stored
	// under another driver is removed first, so its old copy is gone; podman
	// secret rm does not check for containers using it, and running ones
	// keep the copy they got at creation.
	replace := exists
	if exists {
		stored, err := p.SecretDriver(ctx, pname)
		if err != nil {
			return e.fail(err)
		}
		if stored != configuredDriver(cfg) {
			if err := p.SecretRemove(ctx, pname); err != nil {
				return e.fail(fmt.Errorf("secret %s: removing the %s copy: %w", name, stored, err))
			}
			fmt.Fprintf(e.stdout, "removed podman secret %s stored with driver %s\n", pname, stored)
			replace = false
		}
	}
	if err := p.SecretCreate(ctx, pname, bytes.NewReader(value), replace, d); err != nil {
		return e.fail(fmt.Errorf("secret %s: %w", name, err))
	}
	fmt.Fprintf(e.stdout, "secret %s stored in podman secret %s\n", name, pname)

	running, err := runningReceivers(ctx, p, m, sec)
	if err != nil {
		return e.fail(err)
	}
	if len(running) == 0 {
		return 0
	}
	if !restart {
		fmt.Fprintf(e.stdout, "running containers keep the old value until restarted: %s\n(railswitch secret set %s --restart restarts them)\n",
			strings.Join(running, ", "), name)
		return 0
	}
	units := make([]string, len(running))
	for i, c := range running {
		units[i] = c + ".service"
	}
	if err := (podman.Systemd{R: e.run}).Restart(ctx, units...); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "restarted %s\n", strings.Join(running, ", "))
	return 0
}

// configuredDriver is config.toml's secret driver; podman's default is file.
func configuredDriver(cfg *machine.Config) string {
	if cfg.Secrets.Driver == "" {
		return "file"
	}
	return cfg.Secrets.Driver
}

// runningReceivers are the running containers, in every registered
// worktree, of the services that receive sec.
func runningReceivers(ctx context.Context, p podman.Podman, m *render.Model, sec config.Secret) ([]string, error) {
	var running []string
	for _, wt := range m.Inputs.Worktrees {
		n, err := m.NamesFor(wt.Slug)
		if err != nil {
			return nil, err
		}
		for _, svc := range sec.Services {
			c := naming.Container(m.Prefix, wt.Slug, svc)
			if !slices.Contains(n.Containers, c) {
				continue
			}
			ok, err := p.Running(ctx, c)
			if err != nil {
				return nil, err
			}
			if ok {
				running = append(running, c)
			}
		}
	}
	return running, nil
}

// secretRow is one line of `railswitch secret ls` (and one element of its
// --json). It never holds a value.
type secretRow struct {
	Name          string   `json:"name"`
	Scope         string   `json:"scope"`         // "project" or "machine"
	PodmanSecret  string   `json:"podman_secret"` // the podman secret's name
	Delivery      string   `json:"delivery"`      // "file" or "env <VAR>"
	Driver        string   `json:"driver"`        // the stored driver when set, else the configured one
	Set           bool     `json:"set"`
	Created       *string  `json:"created"` // RFC 3339; null when not set
	Services      []string `json:"services"`
	DriverDiffers bool     `json:"driver_differs"` // stored with another driver than config.toml's
}

func secretLs(ctx context.Context, e *env, args []string) int {
	fs := e.flags("secret ls")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("secret ls takes no arguments")
	}
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return e.fail(err)
	}
	cfg, err := machine.LoadConfig(w.Dirs)
	if err != nil {
		return e.fail(err)
	}
	configured := configuredDriver(cfg)
	p := podman.Podman{R: e.run}
	rows := []secretRow{}
	for _, name := range slices.Sorted(maps.Keys(w.Project.Secrets)) {
		sec := w.Project.Secrets[name]
		r := secretRow{
			Name: name, Scope: "project", Delivery: "file", Driver: configured,
			PodmanSecret: render.SecretName(w.Project.Project.Prefix, name, sec),
			Services:     sec.Services,
		}
		if sec.Scope == "machine" {
			r.Scope = "machine"
		}
		if sec.Env != "" {
			r.Delivery = "env " + sec.Env
		}
		if r.Set, err = p.SecretExists(ctx, r.PodmanSecret); err != nil {
			return e.fail(err)
		}
		if r.Set {
			if r.Driver, err = p.SecretDriver(ctx, r.PodmanSecret); err != nil {
				return e.fail(err)
			}
			created, err := p.SecretCreated(ctx, r.PodmanSecret)
			if err != nil {
				return e.fail(err)
			}
			c := created.Format(time.RFC3339)
			r.Created = &c
			r.DriverDiffers = r.Driver != configured
		}
		rows = append(rows, r)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(rows); err != nil {
			return e.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSCOPE\tPODMAN SECRET\tDELIVERY\tDRIVER\tSTATE\tSERVICES\t")
	for _, r := range rows {
		state := "not set"
		if r.Set {
			state = "set " + *r.Created
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t", r.Name, r.Scope, r.PodmanSecret, r.Delivery, r.Driver, state, strings.Join(r.Services, ","))
		if r.DriverDiffers {
			fmt.Fprintf(tw, "driver differs: railswitch secret set %s to move it", r.Name)
		}
		fmt.Fprintln(tw)
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	return 0
}

func secretRm(ctx context.Context, e *env, args []string) int {
	name, code, ok := parseNamed(e, e.flags("secret rm"), args, secretUsage)
	if !ok {
		return code
	}
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return e.fail(err)
	}
	sec, code, ok := e.declared(w, "rm", name)
	if !ok {
		return code
	}
	pname := render.SecretName(w.Project.Project.Prefix, name, sec)
	if err := (podman.Podman{R: e.run}).SecretRemove(ctx, pname); err != nil {
		return e.fail(errors.Join(fmt.Errorf("secret %s", name), err))
	}
	fmt.Fprintf(e.stdout, "removed podman secret %s; [secrets.%s] stays in project.toml, so up refuses until railswitch secret set %s\n", pname, name, name)
	return 0
}
