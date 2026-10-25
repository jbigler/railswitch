package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/placeholder"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/workspace"
)

// masked replaces a value that holds a generated (D17) value.
const masked = "••••••••"

func (e *env) fail(err error) int {
	fmt.Fprintf(e.stderr, "railswitch: %v\n", err)
	return 1
}

func (e *env) usageErr(format string, args ...any) int {
	fmt.Fprintf(e.stderr, "railswitch: "+format+"\n", args...)
	return 2
}

// flags is a subcommand's flag set, printing to stderr.
func (e *env) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("railswitch "+name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	return fs
}

// parse parses a subcommand's args; ok false means exit with code.
func parse(fs *flag.FlagSet, args []string) (code int, ok bool) {
	switch err := fs.Parse(args); err {
	case nil:
		return 0, true
	case flag.ErrHelp:
		return 0, false
	default:
		return 2, false
	}
}

// railswitch lock: resolve the stack and write stack.lock.
func cmdLock(ctx context.Context, e *env, args []string) int {
	fs := e.flags("lock")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("lock takes no arguments")
	}
	root, err := workspace.Root(e.dir)
	if err != nil {
		return e.fail(err)
	}
	if err := workspace.Lock(ctx, root); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "wrote %s\n", filepath.Join(root, "stack.lock"))
	return 0
}

// railswitch render [--check]: write every unit, env file and asset; with
// --check, write nothing and exit 1 when something would change.
func cmdRender(ctx context.Context, e *env, args []string) int {
	fs := e.flags("render")
	check := fs.Bool("check", false, "write nothing; print the diff and exit 1 when something would change")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("render takes no arguments")
	}
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return e.fail(err)
	}
	d, err := w.Render(ctx, e.run, *check)
	if err != nil {
		return e.fail(err)
	}
	if *check {
		io.WriteString(e.stdout, d.Text)
		if d.Text != "" {
			return 1
		}
		return 0
	}
	for _, c := range []struct {
		what  string
		names []string
	}{{"added", d.Added}, {"changed", d.Changed}, {"removed", d.Removed}} {
		for _, n := range c.names {
			fmt.Fprintf(e.stdout, "%s %s\n", c.what, n)
		}
	}
	return 0
}

// envRow is one line of `railswitch env` (and one element of its --json).
type envRow struct {
	Service string  `json:"service"`
	Name    string  `json:"name"`
	Value   *string `json:"value"` // nil: a podman secret, whose value railswitch never has
	Layer   string  `json:"layer"`
	Source  string  `json:"source"`
}

// instance loads the workspace and builds the model and the current
// worktree's instance; code is non-zero when it failed (reported).
func (e *env) instance(ctx context.Context, cmd string) (w *workspace.Workspace, m *render.Model, in *render.Instance, code int) {
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return nil, nil, nil, e.fail(err)
	}
	if w.Slug == "" {
		return nil, nil, nil, e.usageErr("%s: not in a worktree; run it in <workspace>/<worktree>/ or pass -w <worktree>", cmd)
	}
	host, err := workspace.ReadHost(ctx, e.run)
	if err != nil {
		return nil, nil, nil, e.fail(err)
	}
	reg, err := machine.Open(w.Dirs.Data)
	if err != nil {
		return nil, nil, nil, e.fail(err)
	}
	m, err = w.Model(reg.Registry, host)
	reg.Close()
	if err != nil {
		return nil, nil, nil, e.fail(err)
	}
	i := slices.IndexFunc(m.Inputs.Worktrees, func(wt render.Worktree) bool { return wt.Slug == w.Slug })
	if i < 0 {
		return nil, nil, nil, e.fail(fmt.Errorf("worktree %s is not registered for project %s", w.Slug, m.Prefix))
	}
	in, err = m.Instance(m.Inputs.Worktrees[i])
	if err != nil {
		return nil, nil, nil, e.fail(err)
	}
	return w, m, in, 0
}

// railswitch env [svc]: the current worktree's effective env, per service.
func cmdEnv(ctx context.Context, e *env, args []string) int {
	fs := e.flags("env")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() > 1 {
		return e.usageErr("usage: railswitch env [service]")
	}
	w, m, in, code := e.instance(ctx, "env")
	if code != 0 {
		return code
	}

	var svcs []string
	for _, s := range m.Services {
		svcs = append(svcs, s.ID)
	}
	if fs.NArg() == 1 {
		svc := fs.Arg(0)
		if !slices.Contains(svcs, svc) {
			return e.fail(fmt.Errorf("env: no service %s (services: %s)", svc, strings.Join(svcs, ", ")))
		}
		svcs = []string{svc}
	}
	rows := []envRow{}
	for _, svc := range svcs {
		rows = append(rows, envRows(w.Project, m, in, svc)...)
	}

	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(rows); err != nil {
			return e.fail(err)
		}
		return 0
	}
	for i, r := range rows {
		if len(svcs) > 1 && (i == 0 || rows[i-1].Service != r.Service) {
			if i > 0 {
				fmt.Fprintln(e.stdout)
			}
			fmt.Fprintf(e.stdout, "# %s\n", r.Service)
		}
		if r.Value == nil {
			fmt.Fprintf(e.stdout, "%s  # %s: %s\n", r.Name, r.Layer, r.Source)
		} else {
			fmt.Fprintf(e.stdout, "%s=%s  # %s: %s\n", r.Name, *r.Value, r.Layer, r.Source)
		}
	}
	return 0
}

// envRows is service svc's env, sorted by name: its effective env vars, a
// value holding a generated value masked, and every secret delivered to it
// as an env var, without a value.
func envRows(p *config.Project, m *render.Model, in *render.Instance, svc string) []envRow {
	var genVals []string // every generated value in mise.local.toml, used or not
	for _, v := range m.Inputs.Generated {
		if v != "" {
			genVals = append(genVals, v)
		}
	}
	var spec []render.EnvVar
	if i := slices.IndexFunc(m.Services, func(s render.Service) bool { return s.ID == svc }); i >= 0 {
		spec = m.Services[i].EnvSpec
	}
	var rows []envRow
	for _, v := range in.Env[svc] {
		val := v.Value
		if usesGenerated(spec, v) || slices.ContainsFunc(genVals, func(g string) bool { return strings.Contains(val, g) }) {
			val = masked
		}
		rows = append(rows, envRow{Service: svc, Name: v.Name, Value: &val, Layer: v.Layer.String(), Source: v.Source})
	}
	for _, name := range slices.Sorted(maps.Keys(p.Secrets)) {
		sec := p.Secrets[name]
		if sec.Env != "" && slices.Contains(sec.Services, svc) {
			rows = append(rows, envRow{Service: svc, Name: sec.Env, Layer: "secret", Source: "podman secret " + in.Values["secret."+name]})
		}
	}
	slices.SortStableFunc(rows, func(a, b envRow) int { return strings.Compare(a.Name, b.Name) })
	return rows
}

// usesGenerated reports whether the template v was expanded from names a
// {generated.*} placeholder.
func usesGenerated(spec []render.EnvVar, v render.EnvVar) bool {
	for _, s := range spec {
		if s.Name != v.Name || s.Layer != v.Layer || s.Source != v.Source {
			continue
		}
		if slices.ContainsFunc(placeholder.Names(s.Value), func(n string) bool { return strings.HasPrefix(n, "generated.") }) {
			return true
		}
	}
	return false
}
