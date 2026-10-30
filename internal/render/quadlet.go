package render

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/unithash"
)

// Marker starts the first line of every rendered unit file. It is defined in
// machine, which renders the shared proxy unit and cannot import render.
const Marker = machine.Marker

// traefikUnit is the machine-wide proxy (§4).
const traefikUnit = machine.ProxyService

// dozzleUnit is the machine-wide log viewer, at logs.localhost.
const dozzleUnit = machine.DozzleService

// Quadlet renders the project's Quadlet files, keyed by filename: the proxy
// network, and for every worktree its dev network and one .container per
// service but an on-demand one. Every value railswitch did not write itself
// as a literal key is escaped for systemd (% → %%, $ → $$); nothing is left
// for systemd to expand.
// Every .container is stamped with its <prefix>.unit-hash label (unithash).
// Output that fails Lint is an error wrapping every Finding.
func Quadlet(m *Model) (map[string][]byte, error) {
	out := map[string][]byte{}
	put := func(name string, u *unit) error {
		b, err := u.bytes()
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		out[name] = b
		return nil
	}
	if err := put(naming.ProxyNetworkUnit(m.Prefix), proxyNetwork(m)); err != nil {
		return nil, err
	}
	for _, wt := range m.Inputs.Worktrees {
		in, err := m.Instance(wt)
		if err != nil {
			return nil, fmt.Errorf("worktree %s: %w", wt.Slug, err)
		}
		if err := put(naming.DevNetworkUnit(m.Prefix, wt.Slug), devNetwork(m, wt.Slug)); err != nil {
			return nil, err
		}
		routes, err := Routes(in)
		if err != nil {
			return nil, fmt.Errorf("worktree %s: %w", wt.Slug, err)
		}
		for i := range m.Services {
			s := &m.Services[i]
			if s.OnDemand {
				continue
			}
			u, err := containerUnit(in, s, routes)
			if err != nil {
				return nil, fmt.Errorf("worktree %s: service %s: %w", wt.Slug, s.ID, err)
			}
			name := naming.Container(m.Prefix, wt.Slug, s.ID) + ".container"
			if err := put(name, u); err != nil {
				return nil, err
			}
			// The env file and the hook script are part of what the container
			// is created from, so an [env] or [hooks] change marks the
			// running container stale too.
			// (No hook adds no input: an unchanged unit keeps its hash.)
			inputs := append([][]byte{EnvFile(in.Env[s.ID])}, s.HookScripts...)
			out[name] = unithash.Stamp(out[name], unithash.Key(m.Prefix), inputs...)
		}
	}
	for name, b := range out {
		out[name] = machine.NoNetworkWait(b)
	}
	if fs := Lint(out); len(fs) > 0 {
		errs := make([]error, len(fs))
		for i, f := range fs {
			errs[i] = f
		}
		return nil, fmt.Errorf("rendered units fail lint: %w", errors.Join(errs...))
	}
	return out, nil
}

// markerLine is line 1 of a unit; slug "" is the project-wide proxy network.
func markerLine(m *Model, slug string) string {
	where := "prefix " + m.Prefix
	if slug != "" {
		where += ", worktree " + slug
	}
	return Marker + filepath.Join(m.Inputs.Workspace, "project.toml") + " (" + where + "). Edit project.toml, not this file."
}

// markerScope is the last parenthesised group of the marker line.
var markerScope = regexp.MustCompile(`\(([^()]*)\)[^()]*$`)

// UnitOwner reads a unit's line 1 (parseMarker): ok, it is a railswitch
// unit of the machine or of project prefix.
func UnitOwner(line string) (prefix string, machine, ok bool) {
	prefix, _, machine, ok = parseMarker(line)
	return prefix, machine, ok
}

// parseMarker reads a unit's line 1. ok: it is a railswitch marker; machine:
// a machine-wide unit; else the unit's project prefix and worktree slug (""
// for a project-wide unit).
func parseMarker(line string) (prefix, slug string, machine, ok bool) {
	if !strings.HasPrefix(line, Marker) {
		return "", "", false, false
	}
	m := markerScope.FindStringSubmatch(line)
	if m == nil {
		return "", "", false, false
	}
	if m[1] == "machine" {
		return "", "", true, true
	}
	for part := range strings.SplitSeq(m[1], ", ") {
		if v, ok := strings.CutPrefix(part, "prefix "); ok {
			prefix = v
		} else if v, ok := strings.CutPrefix(part, "worktree "); ok {
			slug = v
		}
	}
	return prefix, slug, false, true
}

func proxyNetwork(m *Model) *unit {
	n := m.Inputs.Machine.Subnet
	u := &unit{Header: []string{markerLine(m, "")}}
	u.add("Unit", "Description", "Proxy network of project "+m.Prefix)
	u.add("Network", "NetworkName", naming.ProxyNetwork(m.Prefix))
	u.add("Network", "Subnet", machine.Subnet(n))
	u.add("Network", "Gateway", machine.Gateway(n))
	u.add("Network", "IPRange", machine.IPRange(n))
	u.add("Network", "Options", "mtu=1400", "isolate=strict")
	return u
}

func devNetwork(m *Model, slug string) *unit {
	u := &unit{Header: []string{markerLine(m, slug)}}
	u.add("Unit", "Description", "Dev network of "+m.Prefix+" worktree "+slug)
	u.add("Network", "NetworkName", naming.DevNetwork(m.Prefix, slug))
	u.add("Network", "Options", "mtu=1400", "isolate=strict")
	return u
}

// RenderedRoute is one Traefik route of a worktree as its unit labels it.
type RenderedRoute struct {
	Service string // the routed service's id
	Route   Route  // the pack's route
	Router  string // Traefik's router (and service) name: <prefix>-<slug>-<route name>
	Host    string // the expanded host, e.g. "s3.master.demo.localhost"
}

// Routes are every route of the worktree, in model order: what its units
// label for Traefik.
func Routes(in *Instance) ([]RenderedRoute, error) {
	var out []RenderedRoute
	for _, s := range in.m.Services {
		for _, r := range s.Routes {
			h, err := pack.Expand(r.Host, in.Scope(s.ID))
			if err != nil {
				return nil, fmt.Errorf("%s: route %s: %w", s.ID, r.Name, err)
			}
			out = append(out, RenderedRoute{Service: s.ID, Route: r, Router: in.m.Prefix + "-" + in.Worktree.Slug + "-" + r.Name, Host: h})
		}
	}
	return out, nil
}

// routedHosts are the distinct hosts of routes, in order.
func routedHosts(routes []RenderedRoute) []string {
	var hosts []string
	for _, r := range routes {
		if !slices.Contains(hosts, r.Host) {
			hosts = append(hosts, r.Host)
		}
	}
	return hosts
}

func containerUnit(in *Instance, s *Service, routes []RenderedRoute) (*unit, error) {
	m := in.m
	prefix, slug, ws := m.Prefix, in.Worktree.Slug, m.Inputs.Workspace
	sc := in.Scope(s.ID)
	var errs []error
	x := func(tmpl string) string {
		v, err := pack.Expand(tmpl, sc)
		if err != nil {
			errs = append(errs, err)
		}
		return v
	}
	svcUnit := func(id string) string { return naming.Container(prefix, slug, id) + ".service" }
	onProxy := s.onProxy()

	u := &unit{Header: []string{markerLine(m, slug), "# role: " + s.Role}}

	// [Unit]. The networks' Requires=/After= come from Quadlet itself (D27).
	u.add("Unit", "Description", fmt.Sprintf("%s %s of worktree %s", prefix, s.ID, slug))
	var requires, wants []string
	for _, id := range s.Requires {
		requires = append(requires, svcUnit(id))
	}
	for _, id := range s.Wants {
		wants = append(wants, svcUnit(id))
	}
	if s.Role == RoleApp || len(s.Routes) > 0 {
		wants = append(wants, traefikUnit)
	}
	if s.Role == RoleApp {
		wants = append(wants, dozzleUnit, machine.DashboardService)
	}
	if len(requires) > 0 {
		u.add("Unit", "Requires", strings.Join(requires, " "))
	}
	if len(wants) > 0 {
		u.add("Unit", "Wants", strings.Join(wants, " "))
	}
	if after := slices.Concat(requires, wants); len(after) > 0 {
		u.add("Unit", "After", strings.Join(after, " "))
	}

	// [Container]
	const c = "Container"
	image := in.Values["image."+s.ID]
	if image == "" {
		return nil, errors.New("no image")
	}
	u.add(c, "Image", image)
	u.add(c, "ContainerName", naming.Container(prefix, slug, s.ID))
	networks := in.Networks(s)
	u.add(c, "Network", naming.DevNetworkUnit(prefix, slug)+":alias="+s.ID)
	if onProxy {
		u.add(c, "Network", naming.ProxyNetworkUnit(prefix))
	}
	if s.Worktree {
		u.add(c, "UserNS", WorktreeUserNS)
		u.add(c, "User", WorktreeUser)
	}
	// Quadlet splits EnvironmentFile= into words; Volume=, AddHost=,
	// Entrypoint= and HealthCmd= it reads whole.
	u.add(c, "EnvironmentFile", quoteWord(filepath.Join(ws, ".railswitch", "env", slug, s.ID+".env")))
	hosts, err := in.AddHosts(s)
	if err != nil {
		return nil, err
	}
	u.add(c, "AddHost", hosts...)
	for _, p := range s.Publish {
		host, ctr := in.Values[p.Env], strconv.Itoa(p.Container)
		if p.SamePort {
			ctr = host
		}
		u.add(c, "PublishPort", "127.0.0.1:"+host+":"+ctr)
	}
	vols, err := in.VolumeSpecs(s)
	if err != nil {
		return nil, err
	}
	u.add(c, "Volume", vols...)
	u.add(c, "Secret", in.SecretSpecs(s.ID)...)

	ux := s.Unit
	if ux.ShmSize != "" {
		u.add(c, "ShmSize", ux.ShmSize)
	}
	if ux.RunInit {
		u.add(c, "RunInit", "true")
	}
	u.add(c, "AddCapability", ux.AddCapability...)
	if s.DropsAllCaps() {
		u.add(c, "DropCapability", "all")
	}
	u.add(c, "NoNewPrivileges", "true")
	u.add(c, "Memory", ux.Memory)
	u.add(c, "PidsLimit", strconv.Itoa(ux.PidsLimit))

	if len(s.Routes) > 0 {
		label := func(v string) { u.add(c, "Label", quoteWord(v)) }
		label("traefik.enable=true")
		label("traefik.docker.network=" + naming.ProxyNetwork(prefix))
		for _, rr := range routes {
			if rr.Service != s.ID {
				continue
			}
			r, name := rr.Route, rr.Router
			label("traefik.http.routers." + name + ".rule=" + machine.RouterRule(rr.Host, m.Inputs.Machine.Subnet))
			label("traefik.http.routers." + name + ".entrypoints=web")
			label("traefik.http.routers." + name + ".service=" + name)
			label("traefik.http.services." + name + ".loadbalancer.server.port=" + strconv.Itoa(r.Port))
			if r.RootRedirect != "" {
				// Its own router, for GET and HEAD of / only: a redirect on
				// the route itself sent the RustFS console's login (POST /,
				// answered 307, which keeps the method) to the console page,
				// so login failed. Traefik ranks the longer rule first.
				root := name + "-root"
				label("traefik.http.routers." + root + ".rule=(" + machine.RouterRule(rr.Host, m.Inputs.Machine.Subnet) + ") && Path(`/`) && (Method(`GET`) || Method(`HEAD`))")
				label("traefik.http.routers." + root + ".entrypoints=web")
				label("traefik.http.routers." + root + ".service=" + name)
				label("traefik.http.routers." + root + ".middlewares=" + root)
				label("traefik.http.middlewares." + root + ".redirectregex.regex=^http://([^/]+)/?$")
				label("traefik.http.middlewares." + root + ".redirectregex.replacement=http://${1}" + x(r.RootRedirect))
			}
		}
	}

	if h := s.Health; h != nil {
		u.add(c, "HealthCmd", x(h.Cmd))
		if h.Interval != "" {
			u.add(c, "HealthInterval", h.Interval)
		}
		if h.Timeout != "" {
			u.add(c, "HealthTimeout", h.Timeout)
		}
		if h.Retries > 0 {
			u.add(c, "HealthRetries", strconv.Itoa(h.Retries))
		}
		if h.StartPeriod != "" {
			u.add(c, "HealthStartPeriod", h.StartPeriod)
		}
		// systemctl start returns once the container is healthy.
		u.add(c, "Notify", "healthy")
	}

	if s.Firewalled() {
		u.add(c, "GlobalArgs", quoteWord("--hooks-dir="+HooksDir(ws)))
		var ann []string
		for _, a := range in.FirewallAnnotations(s, networks) {
			ann = append(ann, quoteWord(a))
		}
		u.add(c, "Annotation", ann...)
	}

	var args []string
	if n := in.CPUs(s); n != "" {
		args = append(args, "--cpus="+n)
	}
	if ux.TTY {
		args = append(args, "--tty", "--interactive")
	} else if ux.Interactive {
		args = append(args, "--interactive")
	}
	if len(args) > 0 {
		u.add(c, "PodmanArgs", strings.Join(args, " "))
	}

	if s.Oneshot {
		// The unit's own journal is the log: passthrough hands it stdout.
		u.add(c, "LogDriver", "passthrough")
	} else {
		// journald, not k8s-file: units run with --rm, and k8s-file's log
		// goes with the container, so a container that fails at start
		// leaves no trace of why. journald keeps it in the unit's journal,
		// caps its size, and still serves podman logs and Dozzle.
		u.add(c, "LogDriver", "journald")
	}
	if s.Entrypoint != "" {
		u.add(c, "Entrypoint", x(s.Entrypoint))
	}
	switch {
	case len(s.Exec) > 0:
		words := make([]string, len(s.Exec))
		for i, a := range s.Exec {
			words[i] = quoteWord(x(a))
		}
		u.add(c, "Exec", strings.Join(words, " "))
	case s.Command != "":
		u.add(c, "Exec", x(s.Command))
	}

	// [Service]
	const sv = "Service"
	if s.Oneshot {
		// Never RemainAfterExit: every start of the app re-runs it.
		u.add(sv, "Type", "oneshot")
		u.add(sv, "TimeoutStartSec", "900")
	} else {
		u.add(sv, "Restart", "on-failure")
		u.add(sv, "RestartSec", "5")
		if ux.SuccessExitStatus != 0 {
			u.add(sv, "SuccessExitStatus", strconv.Itoa(ux.SuccessExitStatus))
		}
	}
	if s.Role == RoleApp {
		t, err := appStartTimeout(s.Health)
		if err != nil {
			return nil, err
		}
		u.add(sv, "TimeoutStartSec", strconv.Itoa(t))
	}
	if s.Role == RoleData {
		// A database flushes to disk when it stops: podman's 10 s before
		// SIGKILL cut a busy Postgres short, and the crash recovery that
		// follows (or a cold start of a large one) outlasts systemd's 90 s.
		u.add(c, "StopTimeout", strconv.Itoa(dataStopTimeout))
		u.add(sv, "TimeoutStopSec", strconv.Itoa(dataStopTimeout+30))
		u.add(sv, "TimeoutStartSec", strconv.Itoa(dataStartTimeout))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return u, nil
}

// A data service's seconds to stop cleanly (podman's StopTimeout; systemd
// waits 30 more) and to start, recovery included.
const (
	dataStopTimeout  = 60
	dataStartTimeout = 300
)

// appStartTimeout is max(180, start period + 60) seconds: a start period
// longer than TimeoutStartSec breaks Notify=healthy (podman #27290).
func appStartTimeout(h *Health) (int, error) {
	t := 180
	if h == nil || h.StartPeriod == "" {
		return t, nil
	}
	d, err := time.ParseDuration(h.StartPeriod)
	if err != nil {
		return 0, fmt.Errorf("health start period %q: %w", h.StartPeriod, err)
	}
	secs := int((d + time.Minute + time.Second - 1) / time.Second)
	return max(t, secs), nil
}

// quoteWord double-quotes s for a key systemd/Quadlet splits into words,
// when s is empty or holds whitespace, quotes or a backslash.
func quoteWord(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// unit is a unit file under construction: comment lines, then sections of
// key=value lines in the order added.
type unit struct {
	Header   []string
	Sections []*unitSection
	err      error
}

type unitSection struct {
	Name  string
	Lines []unitLine
}

type unitLine struct{ Key, Value string }

var specifiers = strings.NewReplacer("%", "%%", "$", "$$")

// add appends key=value for each value, escaping % and $ for systemd. A value
// with a line break is an error.
func (u *unit) add(section, key string, values ...string) {
	var sec *unitSection
	for _, s := range u.Sections {
		if s.Name == section {
			sec = s
		}
	}
	if sec == nil {
		sec = &unitSection{Name: section}
		u.Sections = append(u.Sections, sec)
	}
	for _, v := range values {
		if strings.ContainsAny(v, "\r\n") {
			u.err = errors.Join(u.err, fmt.Errorf("%s=%q: a unit value cannot hold a line break", key, v))
			continue
		}
		sec.Lines = append(sec.Lines, unitLine{key, specifiers.Replace(v)})
	}
}

var unitTemplate = template.Must(template.New("unit").Parse(
	`{{range .Header}}{{.}}
{{end}}{{range .Sections}}
[{{.Name}}]
{{range .Lines}}{{.Key}}={{.Value}}
{{end}}{{end}}`))

func (u *unit) bytes() ([]byte, error) {
	if u.err != nil {
		return nil, u.err
	}
	var b bytes.Buffer
	if err := unitTemplate.Execute(&b, u); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
