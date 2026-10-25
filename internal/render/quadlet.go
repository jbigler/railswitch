package render

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
)

// Marker starts the first line of every rendered unit file.
const Marker = "# railswitch: rendered from "

// traefikUnit is the machine-wide proxy (§4).
const traefikUnit = "railswitch-traefik.service"

// Quadlet renders the project's Quadlet files, keyed by filename: the proxy
// network, and for every worktree its dev network and one .container per
// service. Every value railswitch did not write itself as a literal key is
// escaped for systemd (% → %%, $ → $$); nothing is left for systemd to expand.
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
		hosts, err := routedHosts(in)
		if err != nil {
			return nil, fmt.Errorf("worktree %s: %w", wt.Slug, err)
		}
		for i := range m.Services {
			s := &m.Services[i]
			u, err := containerUnit(in, s, hosts)
			if err != nil {
				return nil, fmt.Errorf("worktree %s: service %s: %w", wt.Slug, s.ID, err)
			}
			if err := put(naming.Container(m.Prefix, wt.Slug, s.ID)+".container", u); err != nil {
				return nil, err
			}
		}
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

func proxyNetwork(m *Model) *unit {
	n := m.Inputs.Machine.Subnet
	u := &unit{Header: []string{markerLine(m, "")}}
	u.add("Unit", "Description", "Proxy network of project "+m.Prefix)
	u.add("Network", "NetworkName", naming.ProxyNetwork(m.Prefix))
	u.add("Network", "Subnet", machine.Subnet(n))
	u.add("Network", "Gateway", machine.Gateway(n))
	u.add("Network", "IPRange", machine.IPRange(n))
	u.add("Network", "Options", "mtu=1400")
	return u
}

func devNetwork(m *Model, slug string) *unit {
	u := &unit{Header: []string{markerLine(m, slug)}}
	u.add("Unit", "Description", "Dev network of "+m.Prefix+" worktree "+slug)
	u.add("Network", "NetworkName", naming.DevNetwork(m.Prefix, slug))
	u.add("Network", "Options", "mtu=1400")
	return u
}

// routedHosts are the hosts of every route of the worktree, in model order.
func routedHosts(in *Instance) ([]string, error) {
	var hosts []string
	for _, s := range in.m.Services {
		for _, r := range s.Routes {
			h, err := pack.Expand(r.Host, in.Scope(s.ID))
			if err != nil {
				return nil, fmt.Errorf("%s: route %s: %w", s.ID, r.Name, err)
			}
			if !slices.Contains(hosts, h) {
				hosts = append(hosts, h)
			}
		}
	}
	return hosts, nil
}

func containerUnit(in *Instance, s *Service, hosts []string) (*unit, error) {
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
	onProxy := slices.Contains(s.Networks, "proxy")

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
	networks := []string{naming.DevNetwork(prefix, slug)}
	u.add(c, "Network", naming.DevNetworkUnit(prefix, slug)+":alias="+s.ID)
	if onProxy {
		networks = append(networks, naming.ProxyNetwork(prefix))
		u.add(c, "Network", naming.ProxyNetworkUnit(prefix))
	}
	if s.Worktree {
		u.add(c, "UserNS", "keep-id:uid=1000,gid=1000")
		u.add(c, "User", "1000:1000")
	}
	// Quadlet splits EnvironmentFile= into words; Volume=, AddHost=,
	// Entrypoint= and HealthCmd= it reads whole.
	u.add(c, "EnvironmentFile", quoteWord(filepath.Join(ws, ".railswitch", "env", slug, s.ID+".env")))
	if onProxy {
		for _, h := range hosts {
			u.add(c, "AddHost", h+":"+in.Values["traefik.ip"])
		}
	}
	for _, p := range s.Publish {
		u.add(c, "PublishPort", "127.0.0.1:"+in.Values[p.Env]+":"+strconv.Itoa(p.Container))
	}
	for _, mt := range s.Mounts {
		v := x(mt.Source) + ":" + x(mt.Target)
		if mt.Options != "" {
			v += ":" + mt.Options
		}
		u.add(c, "Volume", v)
	}
	for _, vol := range s.Volumes {
		name, err := in.VolumeName(s.ID, vol)
		if err != nil {
			return nil, err
		}
		v := name + ":" + x(vol.Mount)
		if vol.Chown {
			v += ":U"
		}
		u.add(c, "Volume", v)
	}

	ux := s.Unit
	if ux.ShmSize != "" {
		u.add(c, "ShmSize", ux.ShmSize)
	}
	if ux.RunInit {
		u.add(c, "RunInit", "true")
	}
	u.add(c, "AddCapability", ux.AddCapability...)
	// D34: data images switch users in their entrypoints and keep podman's
	// defaults; a service that adds capabilities (the browser) drops none.
	if s.Role != RoleData && len(ux.AddCapability) == 0 {
		u.add(c, "DropCapability", "all")
	}
	u.add(c, "NoNewPrivileges", "true")
	u.add(c, "Memory", ux.Memory)
	u.add(c, "PidsLimit", strconv.Itoa(ux.PidsLimit))

	if len(s.Routes) > 0 {
		label := func(v string) { u.add(c, "Label", quoteWord(v)) }
		label("traefik.enable=true")
		label("traefik.docker.network=" + naming.ProxyNetwork(prefix))
		for _, r := range s.Routes {
			name := prefix + "-" + slug + "-" + r.Name
			label("traefik.http.routers." + name + ".rule=Host(`" + x(r.Host) + "`)")
			label("traefik.http.routers." + name + ".entrypoints=web")
			label("traefik.http.routers." + name + ".service=" + name)
			label("traefik.http.services." + name + ".loadbalancer.server.port=" + strconv.Itoa(r.Port))
			if r.RootRedirect != "" {
				mw := name + "-root"
				label("traefik.http.routers." + name + ".middlewares=" + mw)
				label("traefik.http.middlewares." + mw + ".redirectregex.regex=^http://([^/]+)/?$")
				label("traefik.http.middlewares." + mw + ".redirectregex.replacement=http://${1}" + x(r.RootRedirect))
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

	if !(len(s.Firewall) == 1 && s.Firewall[0] == "self") {
		mode := m.Inputs.Project.Firewall.Mode
		if mode == "" {
			mode = "log"
		}
		u.add(c, "GlobalArgs", quoteWord("--hooks-dir="+filepath.Join(ws, ".railswitch", "firewall", "hooks.d")))
		u.add(c, "Annotation",
			quoteWord(prefix+".firewall="+strings.Join(s.Firewall, ",")),
			quoteWord(prefix+".firewall.networks="+strings.Join(networks, ",")),
			quoteWord(prefix+".firewall.mode="+mode))
	}

	var args []string
	cpus := m.Inputs.Host.CPUs
	switch s.Role {
	case RoleApp, RoleWorker, RoleDeps:
		args = append(args, "--cpus="+strconv.Itoa(max(1, cpus*3/4)))
	case RoleTool:
		args = append(args, "--cpus="+strconv.Itoa(max(1, cpus/4)))
	}
	if ux.TTY {
		args = append(args, "--tty", "--interactive")
	}
	if len(args) > 0 {
		u.add(c, "PodmanArgs", strings.Join(args, " "))
	}

	if s.Oneshot {
		// The unit's own journal is the log; k8s-file would go with the container.
		u.add(c, "LogDriver", "passthrough")
	} else {
		u.add(c, "LogDriver", "k8s-file")
		u.add(c, "LogOpt", "max-size=20m")
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
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return u, nil
}

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
