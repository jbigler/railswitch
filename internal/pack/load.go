package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack/builtin"
)

// Catalog is every package available to a project.
type Catalog struct {
	Packs   map[Ref]*Pack
	Options map[Ref]*Option
	Tools   map[string]*Tool
	Agents  map[Ref]*Agent // built-in and workspace-local (Ref.Local) agent packages
}

// LoadBuiltins loads the packages embedded in railswitch.
func LoadBuiltins() (*Catalog, error) { return Load(builtin.FS) }

// LoadCatalog is a workspace's catalog: the built-ins plus the agent
// packages under root/agents/. Every place that resolves a workspace's
// stack loads it through here.
func LoadCatalog(root string) (*Catalog, error) {
	c, err := LoadBuiltins()
	if err != nil {
		return nil, err
	}
	if err := LoadLocalAgents(c, root); err != nil {
		return nil, err
	}
	return c, nil
}

// LoadLocalAgents adds root/agents/<name>/agent.toml to c as
// Ref{Local: true}. A root without agents/ adds nothing.
func LoadLocalAgents(c *Catalog, root string) error {
	if c.Agents == nil {
		c.Agents = map[Ref]*Agent{}
	}
	l := &loader{fsys: os.DirFS(root), bases: map[int]string{}, names: map[string]string{}}
	l.agents(c, true)
	if err := errors.Join(l.errs...); err != nil {
		return fmt.Errorf("%s: %w", root, err)
	}
	return nil
}

// Load reads packs/<name>/pack.toml, options/<name>/option.toml,
// tools/<name>/tool.toml and agents/<name>/agent.toml from fsys. A missing
// top-level directory is empty.
// Every problem found is returned at once (errors.Join), each naming its file.
func Load(fsys fs.FS) (*Catalog, error) {
	c := &Catalog{Packs: map[Ref]*Pack{}, Options: map[Ref]*Option{}, Tools: map[string]*Tool{}, Agents: map[Ref]*Agent{}}
	l := &loader{fsys: fsys, bases: map[int]string{}, names: map[string]string{}}

	for _, dir := range l.dirs("packs") {
		var p Pack
		if !l.decode(dir, "pack.toml", &p) {
			continue
		}
		p.Files = l.sub(dir)
		if l.checkPack(dir, &p) {
			c.Packs[Ref{Name: p.Name, Version: p.Version}] = &p
		}
	}
	for _, dir := range l.dirs("options") {
		var o Option
		if !l.decode(dir, "option.toml", &o) {
			continue
		}
		o.Files = l.sub(dir)
		if l.checkOption(dir, &o) {
			c.Options[Ref{Name: o.Name, Version: o.Version}] = &o
		}
	}
	for _, dir := range l.dirs("tools") {
		var t Tool
		if !l.decode(dir, "tool.toml", &t) {
			continue
		}
		t.Files = l.sub(dir)
		if t.Service == "" {
			t.Service = t.Name
		}
		if l.checkTool(dir, &t) {
			c.Tools[t.Name] = &t
		}
	}
	l.agents(c, false)
	var defaults []string
	for r, a := range c.Agents {
		if a.Default {
			defaults = append(defaults, r.String())
		}
	}
	if len(defaults) > 1 {
		slices.Sort(defaults)
		l.errs = append(l.errs, fmt.Errorf("agents: %s all set default = true; at most one may", strings.Join(defaults, ", ")))
	}
	if err := errors.Join(l.errs...); err != nil {
		return nil, err
	}
	return c, nil
}

// DefaultAgent is the built-in agent package that sets default = true.
func (c *Catalog) DefaultAgent() (Ref, bool) {
	for r, a := range c.Agents {
		if a.Default && !r.Local {
			return r, true
		}
	}
	return Ref{}, false
}

// agents loads agents/<name>/agent.toml into c, keyed with Local set to local.
func (l *loader) agents(c *Catalog, local bool) {
	for _, dir := range l.dirs("agents") {
		var a Agent
		if !l.decode(dir, "agent.toml", &a) {
			continue
		}
		a.Files = l.sub(dir)
		if l.checkAgent(dir, &a) {
			c.Agents[Ref{Name: a.Name, Version: a.Version, Local: local}] = &a
		}
	}
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type loader struct {
	fsys  fs.FS
	errs  []error
	bases map[int]string    // port base → "<kind> <name>" that declares it
	names map[string]string // port name → "<kind> <name>" that declares it
}

func (l *loader) add(file, format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf("%s: %s", file, fmt.Sprintf(format, args...)))
}

// dirs lists the package directories under top, sorted.
func (l *loader) dirs(top string) []string {
	entries, err := fs.ReadDir(l.fsys, top)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		l.errs = append(l.errs, err)
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			l.add(path.Join(top, e.Name()), "not a package directory")
			continue
		}
		out = append(out, path.Join(top, e.Name()))
	}
	return out
}

func (l *loader) sub(dir string) fs.FS {
	s, err := fs.Sub(l.fsys, dir)
	if err != nil { // only for an invalid path, which dirs never yields
		panic(err)
	}
	return s
}

// decode strictly decodes dir/name into v.
func (l *loader) decode(dir, name string, v any) bool {
	file := path.Join(dir, name)
	data, err := fs.ReadFile(l.fsys, file)
	if err != nil {
		l.errs = append(l.errs, err)
		return false
	}
	md, err := toml.Decode(string(data), v)
	if err != nil {
		l.add(file, "%v", err)
		return false
	}
	if und := md.Undecoded(); len(und) > 0 {
		for _, k := range und {
			l.add(file, "unknown key %s", k)
		}
		return false
	}
	return true
}

// head checks name and version against the directory; it reports whether
// they are usable as a catalog key.
func (l *loader) head(file, dir, name string, version int, versioned bool) bool {
	ok := true
	if !nameRe.MatchString(name) {
		l.add(file, "name %q must match %s", name, nameRe)
		ok = false
	} else if path.Base(dir) != name {
		l.add(file, "name %q does not match its directory %s", name, dir)
		ok = false
	}
	if versioned && version < 1 {
		l.add(file, "version must be a positive integer")
		ok = false
	}
	return ok
}

// file checks that rel names a regular file in the package directory dir.
func (l *loader) file(file, field, dir, rel string) {
	if rel == "" {
		return
	}
	if !fs.ValidPath(rel) {
		l.add(file, "%s: %q is not a relative path inside the package", field, rel)
		return
	}
	fi, err := fs.Stat(l.fsys, path.Join(dir, rel))
	if err != nil {
		l.add(file, "%s: %s is missing from %s", field, rel, dir)
		return
	}
	if !fi.Mode().IsRegular() {
		l.add(file, "%s: %s is not a regular file", field, rel)
	}
}

func (l *loader) required(file, field, v string) {
	if v == "" {
		l.add(file, "%s is required", field)
	}
}

func (l *loader) ports(file, owner string, ports []Port) {
	for i, p := range ports {
		f := fmt.Sprintf("ports[%d]", i)
		if !nameRe.MatchString(p.Name) {
			l.add(file, "%s.name %q must match %s", f, p.Name, nameRe)
		}
		if !envNameRe.MatchString(p.Env) {
			l.add(file, "%s.env %q is not an env var name", f, p.Env)
		}
		if p.Base < 1024 || p.Base > 65535 {
			l.add(file, "%s.base %d is not a port above 1023", f, p.Base)
		}
		switch {
		case p.SamePort && p.Container != 0:
			l.add(file, "%s: same_port and container are exclusive (same_port: the container port is the host port)", f)
		case !p.SamePort && (p.Container < 1 || p.Container > 65535):
			l.add(file, "%s.container %d is not a port", f, p.Container)
		}
		// Host port = base + worktree ID, so a base belongs to one package
		// name (its versions may share it).
		if prev, ok := l.bases[p.Base]; ok && prev != owner {
			l.add(file, "%s.base %d is already used by %s", f, p.Base, prev)
		} else {
			l.bases[p.Base] = owner
		}
		// The registry records bases by port name, so a name belongs to one
		// package name too, and occurs once in its file.
		if j := slices.IndexFunc(ports[:i], func(q Port) bool { return q.Name == p.Name }); j >= 0 {
			l.add(file, "%s.name %q is already used by ports[%d]", f, p.Name, j)
		} else if prev, ok := l.names[p.Name]; ok && prev != owner {
			l.add(file, "%s.name %q is already used by %s", f, p.Name, prev)
		} else {
			l.names[p.Name] = owner
		}
	}
}

func (l *loader) volumes(file, field string, vs []Volume, seeds bool) {
	for i, v := range vs {
		f := fmt.Sprintf("%s[%d]", field, i)
		l.required(file, f+".name", v.Name)
		if !strings.HasPrefix(v.Mount, "/") {
			l.add(file, "%s.mount %q is not an absolute container path", f, v.Mount)
		}
		if v.Seed != "" && !seeds {
			l.add(file, "%s.seed: only an option's [[volumes]] can be seeded", f)
		}
		if v.Seed != "" && (!fs.ValidPath(v.Seed) || strings.Contains(v.Seed, "/")) {
			l.add(file, "%s.seed %q must be a file name in db-dumps/", f, v.Seed)
		}
	}
}

func (l *loader) env(file, field string, m map[string]string) {
	for _, k := range sortedKeys(m) {
		if !envNameRe.MatchString(k) {
			l.add(file, "%s: %q is not an env var name", field, k)
		}
	}
}

func (l *loader) links(file string, links []Link) {
	for i, k := range links {
		l.required(file, fmt.Sprintf("links[%d].label", i), k.Label)
		l.required(file, fmt.Sprintf("links[%d].url", i), k.URL)
	}
}

func (l *loader) health(file string, h *Health) {
	if h != nil {
		l.required(file, "health.cmd", h.Cmd)
	}
}

func (l *loader) checkPack(dir string, p *Pack) bool {
	file := path.Join(dir, "pack.toml")
	n := len(l.errs)
	ok := l.head(file, dir, p.Name, p.Version, true)

	if !nameRe.MatchString(p.App.Service) {
		l.add(file, "app.service %q must match %s", p.App.Service, nameRe)
	}
	l.required(file, "app.server", p.App.Server)
	if p.App.Port < 1 || p.App.Port > 65535 {
		l.add(file, "app.port %d is not a port", p.App.Port)
	}
	l.file(file, "app.entrypoint", dir, p.App.Entrypoint)
	l.file(file, "app.deps_entrypoint", dir, p.App.DepsEntrypoint)
	if p.App.Install == "" && p.App.DepsEntrypoint == "" {
		l.add(file, "app.install or app.deps_entrypoint is required: the deps service would install nothing")
	}
	l.env(file, "deps.frozen_env", p.Deps.FrozenEnv)

	l.required(file, "image.file", p.Image.File)
	l.file(file, "image.file", dir, p.Image.File)
	l.required(file, "image.tag", p.Image.Tag)
	l.required(file, "image.os", p.Image.OS)

	for _, name := range sortedKeys(p.Runtimes) {
		if !nameRe.MatchString(name) {
			l.add(file, "runtimes.%s: name must match %s", name, nameRe)
		}
		l.required(file, "runtimes."+name+".fallback", p.Runtimes[name].Fallback)
	}
	l.env(file, "env", p.Env)
	l.env(file, "share.env", p.Share.Env)
	l.volumes(file, "caches", p.Caches, false)
	l.volumes(file, "worktree_volumes", p.WorktreeVolumes, false)
	l.ports(file, "pack "+p.Name, p.Ports)
	for _, slot := range sortedKeys(p.Defaults) {
		if !slices.Contains(config.Slots, slot) {
			l.add(file, "defaults.%s: unknown slot (slots: %s)", slot, strings.Join(config.Slots, ", "))
		}
		if ref := p.Defaults[slot]; ref != "none" {
			if _, err := ParseRef(ref); err != nil {
				l.add(file, "defaults.%s: %v", slot, err)
			}
		}
	}
	l.links(file, p.Links)
	return ok && len(l.errs) == n
}

func (l *loader) checkOption(dir string, o *Option) bool {
	file := path.Join(dir, "option.toml")
	n := len(l.errs)
	ok := l.head(file, dir, o.Name, o.Version, true)

	if !slices.Contains(config.Slots, o.Slot) {
		l.add(file, "slot %q is not one of %s", o.Slot, strings.Join(config.Slots, ", "))
	}
	if !nameRe.MatchString(o.Service) {
		l.add(file, "service %q must match %s", o.Service, nameRe)
	}
	for _, s := range o.Needs {
		if !slices.Contains(config.Slots, s) {
			l.add(file, "needs: %q is not a slot", s)
		}
	}
	for _, g := range o.Generated {
		if !envNameRe.MatchString(g) {
			l.add(file, "generated: %q is not an env var name", g)
		}
	}
	if o.Pin != nil {
		if !nameRe.MatchString(o.Pin.Name) {
			l.add(file, "pin.name %q must match %s", o.Pin.Name, nameRe)
		}
		l.required(file, "pin.default", o.Pin.Default)
	}

	img := o.Image
	kinds := 0
	for _, s := range []string{img.Pull, img.Use, img.Build} {
		if s != "" {
			kinds++
		}
	}
	switch {
	case kinds != 1:
		l.add(file, "image: set exactly one of pull, use or build")
	case img.Use != "" && img.Use != "app":
		l.add(file, "image.use: %q is not \"app\"", img.Use)
	case img.Build != "":
		l.file(file, "image.build", dir, img.Build)
		l.required(file, "image.tag", img.Tag)
	}
	if img.Build == "" && (img.Tag != "" || len(img.Args) > 0) {
		l.add(file, "image: tag and args go with build only")
	}

	if o.AppAddon != nil {
		l.required(file, "app_addon.file", o.AppAddon.File)
		l.file(file, "app_addon.file", dir, o.AppAddon.File)
		l.required(file, "app_addon.os", o.AppAddon.OS)
	}
	l.health(file, o.Health)
	l.file(file, "entrypoint", dir, o.Entrypoint)
	l.env(file, "gives", o.Gives)
	l.env(file, "deps_env", o.DepsEnv)
	l.env(file, "agent_gives", o.AgentGives)
	l.env(file, "env", o.Env)
	l.env(file, "share.gives", o.Share.Gives)
	if d := o.Dump; d != nil {
		if len(d.Command) == 0 || slices.Contains(d.Command, "") {
			l.add(file, "dump.command must be a non-empty list of non-empty arguments")
		}
		l.required(file, "dump.file", d.File)
		if d.File != "" && (strings.Contains(d.File, "/") || d.File == "." || d.File == "..") {
			l.add(file, "dump.file %q must be a bare file name", d.File)
		}
	}
	l.volumes(file, "volumes", o.Volumes, true)
	l.mounts(file, dir, o.Mounts)
	l.ports(file, "option "+o.Name, o.Ports)
	for i, r := range o.Routes {
		f := fmt.Sprintf("routes[%d]", i)
		if !nameRe.MatchString(r.Name) {
			l.add(file, "%s.name %q must match %s", f, r.Name, nameRe)
		}
		l.required(file, f+".host", r.Host)
		if r.Port < 1 || r.Port > 65535 {
			l.add(file, "%s.port %d is not a port", f, r.Port)
		}
		if r.RootRedirect != "" && !strings.HasPrefix(r.RootRedirect, "/") {
			l.add(file, "%s.root_redirect %q must start with /", f, r.RootRedirect)
		}
	}
	l.links(file, o.Links)
	return ok && len(l.errs) == n
}

func (l *loader) mounts(file, dir string, ms []Mount) {
	for i, m := range ms {
		f := fmt.Sprintf("mounts[%d]", i)
		l.required(file, f+".source", m.Source)
		if !strings.HasPrefix(m.Target, "/") {
			l.add(file, "%s.target %q is not an absolute container path", f, m.Target)
		}
		if m.Source != "" && !strings.HasPrefix(m.Source, "/") && !strings.HasPrefix(m.Source, "{") {
			l.file(file, f+".source", dir, m.Source)
		}
	}
}

var (
	hostRe       = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	secretNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)
)

func (l *loader) checkAgent(dir string, a *Agent) bool {
	file := path.Join(dir, "agent.toml")
	n := len(l.errs)
	ok := l.head(file, dir, a.Name, a.Version, true)
	switch a.Run {
	case "", "on-demand", "service":
	default:
		l.add(file, "run %q must be \"on-demand\" or \"service\"", a.Run)
	}
	l.required(file, "image.file", a.Image.File)
	l.file(file, "image.file", dir, a.Image.File)
	l.file(file, "entrypoint", dir, a.Entrypoint)
	for i, h := range a.Allow {
		// meta:<name> is a published range set the firewall hook fetches (meta:github).
		if !hostRe.MatchString(strings.TrimPrefix(h, "meta:")) {
			l.add(file, "allow[%d]: %q is not a host name or meta:<name>", i, h)
		}
	}
	l.volumes(file, "shared", a.Shared, false)
	l.mounts(file, dir, a.Mounts)
	l.env(file, "env", a.Env)
	for i, s := range a.Secrets {
		if !secretNameRe.MatchString(s.Name) {
			l.add(file, "secrets[%d].name %q must match %s", i, s.Name, secretNameRe)
		}
		if !envNameRe.MatchString(s.Env) {
			l.add(file, "secrets[%d].env %q is not an env var name", i, s.Env)
		}
	}
	return ok && len(l.errs) == n
}

func (l *loader) checkTool(dir string, t *Tool) bool {
	file := path.Join(dir, "tool.toml")
	n := len(l.errs)
	ok := l.head(file, dir, t.Name, 0, false)
	if !nameRe.MatchString(t.Service) {
		l.add(file, "service %q must match %s", t.Service, nameRe)
	}
	l.required(file, "image.file", t.Image.File)
	l.file(file, "image.file", dir, t.Image.File)
	l.file(file, "entrypoint", dir, t.Entrypoint)
	l.volumes(file, "shared", t.Shared, false)
	l.env(file, "env", t.Env)
	l.ports(file, "tool "+t.Name, t.Ports)
	l.links(file, t.Links)
	return ok && len(l.errs) == n
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
