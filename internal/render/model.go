// Package render turns a project, its resolved stack and the machine
// registry into a model of every service (its env layers, image, mounts and
// unit settings), and per worktree into concrete values, env and builds.
package render

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/version"
)

// Service roles (HANDOFF §3.3).
const (
	RoleApp    = "app"
	RoleDeps   = "deps"
	RoleWorker = "worker"
	RoleData   = "data"
	RoleTool   = "tool"
)

// Worktree is one registered worktree: <workspace>/<slug>/ (D22).
type Worktree struct {
	Slug        string
	ID          int // machine-wide; ports are derived from it
	Dir, Branch string
}

// Host is what render reads from the host. GitName/GitEmail: host `git config
// --global user.name/user.email`, read by workspace.Render.
type Host struct {
	CPUs                                int
	Home, NetworkDir, GitName, GitEmail string
}

// Inputs is everything Build reads.
type Inputs struct {
	Workspace string
	Project   *config.Project
	Stack     *pack.Stack
	Lock      *config.Lock // nil: no stack.lock yet
	Machine   machine.ProjectEntry
	Worktrees []Worktree
	Generated map[string]string // D17 values from mise.local.toml
	Host      Host
}

// Model is a project's services, independent of any worktree. Every string
// field of a Service is a template (internal/placeholder grammar) expanded per
// worktree with Instance.Scope; literal text from outside packages (paths,
// host values) is stored with "{" escaped as "{{".
type Model struct {
	Prefix   string
	Services []Service // ordered: app, deps, worker, data (slot order), tools
	Inputs   Inputs
	Warnings []string // non-fatal findings, e.g. a suggested option addon missing from [image] addons
	Assets   []Asset  // every package asset a service mounts; Task 11's Plan writes them

	baseDir    string                    // <workspace>/<sanitize(base_branch)>
	chain      []*containerfile          // [image] base, then each addon
	tools      map[string]*containerfile // tool service id → containerfiles/Containerfile.<tool>
	standalone map[string]*containerfile // option service id → its standalone Containerfile
}

// Asset is a package file written into the workspace and bind-mounted.
type Asset struct {
	Rel  string      // workspace-relative: ".railswitch/assets/<package>/<path>"
	Data []byte      // the embedded package file's bytes
	Mode fs.FileMode // 0755 for scripts (*.sh), else 0644
}

// Service is one container of every worktree.
type Service struct {
	ID, Role   string
	Image      ImageSpec
	Exec       []string // the container's command and arguments ([unit] exec)
	Entrypoint string   // container path, e.g. "/usr/local/bin/entrypoint-app.sh"
	Command    string   // the app's server or an option's command
	Networks   []string // "dev", "proxy"
	Publish    []Port   // host port = Base + worktree ID, on 127.0.0.1
	Volumes    []Volume
	Mounts     []Mount
	Routes     []Route
	Health     *Health
	Requires   []string // service ids
	Wants      []string
	Firewall   []string // profiles; ["self"] = no hook
	Oneshot    bool
	// Worktree: the service mounts the worktree, so it runs as uid 1000 with
	// keep-id.
	Worktree bool
	Unit     UnitExtras
	EnvSpec  []EnvVar // unexpanded templates with layer provenance, lowest layer first

	scheme string // the {scheme} of this option service (pack db_scheme[<option>]); "" = none
}

// ImageKind says where a service's image comes from.
type ImageKind int

const (
	ImagePull       ImageKind = iota + 1 // a registry ref
	ImageBase                            // the worktree's base image (D31)
	ImageBuild                           // a tool's image, built on the base (D31)
	ImageStandalone                      // an option's own build, not on the chain (playwright)
)

// ImageSpec is a service's image. Its concrete ref per worktree is
// Instance.Values["image.<svc>"].
type ImageSpec struct {
	Kind ImageKind
	Ref  string            // ImagePull: registry ref template
	File string            // ImageBuild, ImageStandalone: absolute path under <ws>/containerfiles/
	Tag  string            // ImageStandalone: tag version part template
	Args map[string]string // ImageStandalone: build arg templates
}

// Port is a published port; Route a Traefik route (RootRedirect included);
// Health a container health check.
type (
	Port   = pack.Port
	Route  = pack.Route
	Health = pack.Health
)

// Volume is a named volume. Its full name is Instance.VolumeName.
type Volume struct {
	Name   string // name template without the prefix, e.g. "db-data-pg{postgres.major}"
	Mount  string // container path
	Chown  bool   // mount with :U
	Shared bool   // shared by every worktree: <prefix>_<name>; else <prefix>-<slug>-<name>
	Seed   string // file under <workspace>/db-dumps/ imported when the volume is created (D18)
}

// Mount is a bind mount; Source and Target are templates.
type Mount struct {
	Source, Target, Options string
}

// UnitExtras are a service's unit settings. Memory and PidsLimit are
// resolved (D34): [services.<id>], else the pack/option/tool [unit], else the
// role default.
type UnitExtras struct {
	ShmSize           string
	AddCapability     []string
	RunInit           bool
	SuccessExitStatus int
	TTY               bool
	Memory            string
	PidsLimit         int
}

var roleLimits = map[string]struct {
	memory string
	pids   int
}{
	RoleApp: {"4g", 4096}, RoleWorker: {"2g", 4096}, RoleDeps: {"4g", 4096}, RoleData: {"2g", 1024}, RoleTool: {"2g", 4096},
}

var slotRoles = map[string]string{
	"database": RoleData, "cache": RoleData, "storage": RoleData, "jobs": RoleWorker, "mail": RoleTool, "browser": RoleTool,
}

// literal escapes s for use as template text.
func literal(s string) string { return strings.ReplaceAll(s, "{", "{{") }

// Build assembles the model. It reads the workspace's Containerfiles and
// entrypoint overrides; generated values are needed only by Instance.
func Build(in Inputs) (*Model, error) {
	p := in.Project
	if p.Project.BaseBranch == "" {
		return nil, errors.New("project.base_branch is required: the base worktree is <workspace>/<base_branch>/ (D22)")
	}
	base, err := naming.Sanitize(p.Project.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("project.base_branch: %w", err)
	}
	b := &builder{
		in:     in,
		m:      &Model{Prefix: p.Project.Prefix, Inputs: in, baseDir: filepath.Join(in.Workspace, base), tools: map[string]*containerfile{}, standalone: map[string]*containerfile{}},
		assets: map[string]bool{},
		opts:   map[string]*pack.Option{},
		tools:  map[string]*pack.Tool{},
	}
	if err := b.services(); err != nil {
		return nil, err
	}
	if err := b.checkRefs(); err != nil {
		return nil, err
	}
	b.envSpecs()
	if err := b.checkSecrets(); err != nil {
		return nil, err
	}
	if err := b.images(); err != nil {
		return nil, err
	}
	b.addonWarnings()
	return b.m, nil
}

type builder struct {
	in     Inputs
	m      *Model
	assets map[string]bool         // Rel of each listed asset
	opts   map[string]*pack.Option // option service id → its option
	tools  map[string]*pack.Tool   // tool service id → its tool
}

func (b *builder) ws(rel ...string) string {
	return filepath.Join(append([]string{b.in.Workspace}, rel...)...)
}

// services builds every service in model order.
func (b *builder) services() error {
	p, st := b.in.Project, b.in.Stack
	pk := st.Pack
	appID := p.Stack.App
	if appID == "" {
		appID = pk.App.Service
	}
	baseImage := ImageSpec{Kind: ImageBase}

	app := Service{
		ID: appID, Role: RoleApp, Image: baseImage, Exec: pk.Unit.Exec, Command: pk.App.Server,
		Publish:  pk.Ports,
		Routes:   []Route{{Name: "app", Host: "{worktree.host}", Port: pk.App.Port}},
		Health:   appHealth(pk.App, p.Services[appID].Health),
		Firewall: []string{"none"},
		Worktree: true,
		Mounts:   worktreeMounts("ro,z"),
		Volumes:  b.appVolumes(),
		Unit:     unitExtras(pk.Unit, RoleApp, p.Services[appID]),
	}
	deps := Service{
		ID: "deps", Role: RoleDeps, Image: baseImage, Oneshot: true,
		Firewall: []string{"deps"},
		Worktree: true,
		Mounts:   []Mount{{Source: "{worktree.dir}", Target: "/app", Options: "z"}}, // no .git: nothing an install needs (D11)
		Volumes:  b.appVolumes(),
		Unit:     unitExtras(pack.Unit{}, RoleDeps, p.Services["deps"]),
	}
	var errs []error
	errs = append(errs, b.entrypoint(&app, pk.Name, pk.Files, pk.App.Entrypoint))
	errs = append(errs, b.entrypoint(&deps, pk.Name, pk.Files, pk.App.DepsEntrypoint))

	var workers, data, toolOpts, tools []Service
	for _, slot := range config.Slots {
		ch := st.Options[slot]
		if ch == nil {
			continue
		}
		s, err := b.optionService(ch, slotRoles[slot])
		errs = append(errs, err)
		b.opts[s.ID] = ch.Option
		switch s.Role {
		case RoleWorker:
			workers = append(workers, s)
		case RoleData:
			data = append(data, s)
		default:
			toolOpts = append(toolOpts, s)
		}
	}
	for _, t := range st.Tools {
		s, err := b.toolService(t)
		errs = append(errs, err)
		b.tools[s.ID] = t
		tools = append(tools, s)
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	tools = append(toolOpts, tools...)

	// D23: the app needs deps and its data services; it only wants the tools.
	app.Requires = []string{deps.ID}
	for _, s := range data {
		app.Requires = append(app.Requires, s.ID)
	}
	for _, s := range tools {
		app.Wants = append(app.Wants, s.ID)
	}
	for i := range workers {
		workers[i].Requires = slices.Clone(app.Requires)
	}

	b.m.Services = slices.Concat([]Service{app, deps}, workers, data, tools)
	for i := range b.m.Services {
		s := &b.m.Services[i]
		s.Networks = []string{"dev"}
		if s.Role == RoleApp || len(s.Routes) > 0 {
			s.Networks = append(s.Networks, "proxy")
		}
	}
	return nil
}

// worktreeMounts are the app's view of the worktree: itself at /app, and the
// base worktree's .git at its own path (a worktree's .git file points there).
func worktreeMounts(gitOptions string) []Mount {
	return []Mount{
		{Source: "{worktree.dir}", Target: "/app", Options: "z"},
		{Source: "{base.git}", Target: "{base.git}", Options: gitOptions},
	}
}

// appVolumes are the pack's shared caches and per-worktree volumes.
func (b *builder) appVolumes() []Volume {
	pk := b.in.Stack.Pack
	var out []Volume
	for _, v := range pk.Caches {
		out = append(out, Volume{Name: v.Name, Mount: v.Mount, Chown: v.Chown, Shared: true})
	}
	for _, v := range pk.WorktreeVolumes {
		out = append(out, Volume{Name: v.Name, Mount: v.Mount, Chown: v.Chown})
	}
	return out
}

func (b *builder) optionService(ch *pack.Chosen, role string) (Service, error) {
	o, pk, p := ch.Option, b.in.Stack.Pack, b.in.Project
	s := Service{
		ID: ch.Service, Role: role, Exec: o.Unit.Exec, Command: o.Command,
		Publish: o.Ports, Routes: o.Routes,
		Firewall: o.Firewall,
		Unit:     unitExtras(o.Unit, role, p.Services[ch.Service]),
		scheme:   pk.DBScheme[o.Name],
	}
	if len(s.Firewall) == 0 {
		s.Firewall = []string{"none"}
	}
	if o.Health != nil {
		h := *o.Health
		s.Health = &h
	}
	switch {
	case o.Image.Pull != "":
		s.Image = ImageSpec{Kind: ImagePull, Ref: o.Image.Pull}
	case o.Image.Use == "app": // the app's image, mounts and volumes (worker role)
		s.Image = ImageSpec{Kind: ImageBase}
		s.Worktree = true
		s.Mounts = worktreeMounts("ro,z")
		s.Volumes = b.appVolumes()
	case o.Image.Build != "":
		s.Image = ImageSpec{Kind: ImageStandalone, File: b.ws("containerfiles", o.Image.Build), Tag: o.Image.Tag, Args: o.Image.Args}
	}
	for _, v := range o.Volumes {
		s.Volumes = append(s.Volumes, Volume{Name: v.Name, Mount: v.Mount, Chown: v.Chown, Seed: v.Seed})
	}
	for _, mt := range o.Mounts {
		if strings.HasPrefix(mt.Source, "/") || strings.HasPrefix(mt.Source, "{") {
			s.Mounts = append(s.Mounts, Mount{Source: mt.Source, Target: mt.Target, Options: mt.Options})
			continue
		}
		src, err := b.asset(o.Name, o.Files, mt.Source)
		if err != nil {
			return s, err
		}
		opts := mt.Options
		if opts == "" {
			opts = "ro,z"
		}
		s.Mounts = append(s.Mounts, Mount{Source: literal(src), Target: mt.Target, Options: opts})
	}
	return s, b.entrypoint(&s, o.Name, o.Files, o.Entrypoint)
}

func (b *builder) toolService(t *pack.Tool) (Service, error) {
	pk, p := b.in.Stack.Pack, b.in.Project
	s := Service{
		ID: t.Service, Role: RoleTool, Exec: t.Unit.Exec,
		Image:    ImageSpec{Kind: ImageBuild, File: b.ws("containerfiles", t.Image.File)},
		Publish:  t.Ports,
		Firewall: t.Firewall,
		Worktree: t.Worktree,
		Unit:     unitExtras(t.Unit, RoleTool, p.Services[t.Service]),
	}
	if len(s.Firewall) == 0 {
		s.Firewall = []string{"none"}
	}
	if t.Worktree {
		s.Mounts = worktreeMounts("z")
		for _, v := range pk.WorktreeVolumes {
			s.Volumes = append(s.Volumes, Volume{Name: v.Name, Mount: v.Mount, Chown: v.Chown})
		}
	}
	if t.Home {
		s.Mounts = append(s.Mounts, Mount{Source: homeSource, Target: "/home/appuser", Options: "z"})
	}
	if t.Caches {
		for _, v := range pk.Caches {
			s.Volumes = append(s.Volumes, Volume{Name: v.Name, Mount: v.Mount, Chown: v.Chown, Shared: true})
		}
	}
	for _, v := range t.Shared {
		s.Volumes = append(s.Volumes, Volume{Name: v.Name, Mount: v.Mount, Chown: v.Chown, Shared: true})
	}
	return s, b.entrypoint(&s, t.Name, t.Files, t.Entrypoint)
}

// asset lists the package file rel of pkg as a Model.Assets entry and returns
// its absolute host path.
func (b *builder) asset(pkg string, files fs.FS, rel string) (string, error) {
	data, err := fs.ReadFile(files, rel)
	if err != nil {
		return "", fmt.Errorf("%s: asset %s: %w", pkg, rel, err)
	}
	r := path.Join(".railswitch", "assets", pkg, rel)
	if !b.assets[r] {
		b.assets[r] = true
		mode := fs.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		b.m.Assets = append(b.m.Assets, Asset{Rel: r, Data: data, Mode: mode})
	}
	return b.ws(filepath.FromSlash(r)), nil
}

// entrypoint mounts the package file rel at /usr/local/bin/<basename> and
// makes it s's entrypoint. <ws>/entrypoints/<basename>, when it exists,
// replaces the package's file (D13).
func (b *builder) entrypoint(s *Service, pkg string, files fs.FS, rel string) error {
	if rel == "" {
		return nil
	}
	name := path.Base(rel)
	src := b.ws("entrypoints", name)
	fi, err := os.Stat(src)
	switch {
	case err == nil && !fi.Mode().IsRegular():
		return fmt.Errorf("%s: not a regular file", src)
	case errors.Is(err, fs.ErrNotExist):
		if src, err = b.asset(pkg, files, rel); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	s.Entrypoint = "/usr/local/bin/" + name
	s.Mounts = append(s.Mounts, Mount{Source: literal(src), Target: s.Entrypoint, Options: "ro,z"})
	return nil
}

// appHealth is the app's health check (D34): the pack's command, replaced by
// [services.<app>] health, or none with "none".
func appHealth(a pack.App, override string) *Health {
	cmd := a.Health
	if override != "" {
		cmd = override
	}
	if cmd == "" || cmd == "none" {
		return nil
	}
	return &Health{Cmd: cmd, Interval: "10s", Retries: 3, StartPeriod: a.HealthStartPeriod}
}

func unitExtras(u pack.Unit, role string, ov config.ServiceOverride) UnitExtras {
	e := UnitExtras{
		ShmSize: u.ShmSize, AddCapability: u.AddCapability, RunInit: u.RunInit,
		SuccessExitStatus: u.SuccessExitStatus, TTY: u.TTY,
		Memory: u.Memory, PidsLimit: u.PidsLimit,
	}
	def := roleLimits[role]
	if e.Memory == "" {
		e.Memory = def.memory
	}
	if e.PidsLimit == 0 {
		e.PidsLimit = def.pids
	}
	if ov.Memory != "" {
		e.Memory = ov.Memory
	}
	if ov.PidsLimit > 0 {
		e.PidsLimit = ov.PidsLimit
	}
	return e
}

func (m *Model) service(id string) *Service {
	for i := range m.Services {
		if m.Services[i].ID == id {
			return &m.Services[i]
		}
	}
	return nil
}

func (m *Model) serviceIDs() []string {
	ids := make([]string, len(m.Services))
	for i, s := range m.Services {
		ids[i] = s.ID
	}
	return ids
}

// checkRefs refuses a [services.<id>] or a secret's services entry that
// names no service of this stack.
func (b *builder) checkRefs() error {
	p := b.in.Project
	known := strings.Join(b.m.serviceIDs(), ", ")
	var errs []error
	for _, id := range sortedKeys(p.Services) {
		if b.m.service(id) == nil {
			errs = append(errs, fmt.Errorf("services.%s: no such service (services: %s)", id, known))
		}
	}
	for _, name := range sortedKeys(p.Secrets) {
		for _, id := range p.Secrets[name].Services {
			if b.m.service(id) == nil {
				errs = append(errs, fmt.Errorf("secrets.%s.services: no such service %s (services: %s)", name, id, known))
			}
		}
	}
	return errors.Join(errs...)
}

func (b *builder) addonWarnings() {
	p, st := b.in.Project, b.in.Stack
	for _, slot := range config.Slots {
		ch := st.Options[slot]
		if ch == nil || ch.Option.AppAddon == nil {
			continue
		}
		file := ch.Option.AppAddon.File
		if slices.Contains(p.Image.Addons, "containerfiles/"+file) {
			continue
		}
		name := strings.TrimPrefix(file, "Containerfile.")
		ref := pack.Ref{Name: ch.Option.Name, Version: ch.Option.Version}
		b.m.Warnings = append(b.m.Warnings, fmt.Sprintf(
			"%s suggests addon %s, which is not in [image] addons (railswitch containerfile eject %s), ignore this if your base already covers it",
			ref, name, name))
	}
}

// Instance is one worktree's concrete values, env and builds.
type Instance struct {
	Worktree Worktree
	Values   map[string]string   // every placeholder name → its concrete value for this worktree
	Env      map[string][]EnvVar // svc → effective process env, highest layer per name, provenance kept
	Builds   []BuildStep         // in build order: the base chain, then tool images, then standalone option images
	Warnings []string            // skipped version sources (a hostile or odd file), per worktree

	m *Model
}

// Instance resolves the model for one worktree. A generated value the
// services use but mise.local.toml lacks is an error.
func (m *Model) Instance(wt Worktree) (*Instance, error) {
	in, err := m.values(wt)
	if err != nil {
		return nil, err
	}
	if err := m.imageValues(in); err != nil {
		return nil, err
	}
	if err := m.expandEnv(in); err != nil {
		return nil, err
	}
	return in, nil
}

// values computes every Value except image.*: what NamesFor needs, with no
// Containerfile or generated value involved.
func (m *Model) values(wt Worktree) (*Instance, error) {
	p, st := m.Inputs.Project, m.Inputs.Stack
	pk := st.Pack
	in := &Instance{Worktree: wt, m: m}
	v := map[string]string{
		"worktree.slug":  wt.Slug,
		"worktree.id":    strconv.Itoa(wt.ID),
		"worktree.host":  wt.Slug + "." + m.Prefix + ".localhost",
		"worktree.dir":   wt.Dir,
		"worktree.home":  filepath.Join(m.Inputs.Workspace, filepath.FromSlash(homeDir), wt.Slug), // D30's per-worktree home
		"base.git":       filepath.Join(m.baseDir, ".git"),
		"traefik.ip":     machine.TraefikIP(m.Inputs.Machine.Subnet),
		"branch":         wt.Branch,
		"project.prefix": m.Prefix,
		"dumps":          filepath.Join(m.Inputs.Workspace, "db-dumps"),
	}
	in.Values = v
	override := p.Worktrees[wt.Slug].Versions
	warn := func(ws []string) {
		for _, w := range ws {
			in.Warnings = append(in.Warnings, wt.Slug+": "+w)
		}
	}
	setVersion := func(name, val, from string) error {
		if !version.ValidVersion.MatchString(val) {
			return fmt.Errorf("%s: %s = %q is not a valid version (%s)", from, name, val, version.ValidVersion)
		}
		v[name], v[name+".major"] = val, pack.Major(val)
		return nil
	}

	for _, name := range sortedKeys(pk.Runtimes) {
		val, from := override[name], fmt.Sprintf("worktrees.%s.versions", wt.Slug)
		if val == "" {
			ch := pk.Runtimes[name]
			r := version.Resolve(wt.Dir, ch.From, ch.Fallback, version.ValidVersion)
			warn(r.Warnings)
			val, from = r.Value, "runtimes."+name+" ("+r.Source+")"
		}
		if err := setVersion(name, val, from); err != nil {
			return nil, err
		}
	}
	for _, slot := range config.Slots {
		ch := st.Options[slot]
		if ch == nil || ch.Option.Pin == nil {
			continue
		}
		name, val, from := ch.Option.Pin.Name, ch.Option.Pin.Default, ch.Option.Name+" pin default"
		if x, ok := p.Versions[name]; ok {
			val, from = x, "versions"
		}
		if x, ok := override[name]; ok {
			val, from = x, fmt.Sprintf("worktrees.%s.versions", wt.Slug)
		}
		if err := setVersion(name, val, from); err != nil {
			return nil, err
		}
	}

	if pk.DBName.Fallback != "" || len(pk.DBName.From) > 0 {
		fb, err := pack.Expand(pk.DBName.Fallback, pack.MapScope{"project.prefix": m.Prefix})
		if err != nil {
			return nil, fmt.Errorf("db_name.fallback: %w", err)
		}
		r := version.Resolve(wt.Dir, pk.DBName.From, fb, version.ValidDBName)
		warn(r.Warnings)
		if !version.ValidDBName.MatchString(r.Value) {
			return nil, fmt.Errorf("db_name: %q is not a valid database name (%s)", r.Value, version.ValidDBName)
		}
		v["db"] = r.Value
	}

	for _, s := range m.Services {
		for _, port := range s.Publish {
			v[port.Env] = strconv.Itoa(port.Base + wt.ID)
		}
	}
	for _, name := range sortedKeys(p.Secrets) {
		v["secret."+name] = secretName(m.Prefix, name, p.Secrets[name])
	}
	m.envValues(in)
	return in, nil
}

// secretName is the podman secret holding a project secret (D33).
func secretName(prefix, name string, s config.Secret) string {
	if s.Scope == "machine" {
		return "railswitch_" + name
	}
	return naming.Shared(prefix, name)
}

// Scope is the placeholder scope of service svc's fields: Values plus
// {service} and {scheme}. It never resolves {generated.*}.
func (in *Instance) Scope(svc string) pack.Scope {
	sc := scope{values: in.Values, svc: svc}
	if s := in.m.service(svc); s != nil {
		sc.scheme = s.scheme
	}
	return sc
}

// VolumeName is the full name of service svc's volume v in this worktree:
// "<prefix>_<name>" for a shared volume, else "<prefix>-<slug>-<name>".
func (in *Instance) VolumeName(svc string, v Volume) (string, error) {
	name, err := pack.Expand(v.Name, in.Scope(svc))
	if err != nil {
		return "", fmt.Errorf("%s: volume %s: %w", svc, v.Name, err)
	}
	if v.Shared {
		return naming.Shared(in.m.Prefix, name), nil
	}
	return naming.WorktreeVolume(in.m.Prefix, in.Worktree.Slug, name), nil
}

// Names are the exact names of one worktree's objects.
type Names struct{ Units, Containers, Volumes, Networks []string }

// NamesFor returns the exact names of a registered worktree's objects:
// every "<prefix>-<slug>-<svc>.service" and "<prefix>-<slug>-dev-network.service",
// every container, its per-worktree volumes only (never a shared "<prefix>_…"
// one) and its dev network. An unregistered slug is an error.
func (m *Model) NamesFor(slug string) (Names, error) {
	i := slices.IndexFunc(m.Inputs.Worktrees, func(w Worktree) bool { return w.Slug == slug })
	if i < 0 {
		return Names{}, fmt.Errorf("worktree %s is not registered for project %s", slug, m.Prefix)
	}
	in, err := m.values(m.Inputs.Worktrees[i])
	if err != nil {
		return Names{}, err
	}
	var n Names
	for _, s := range m.Services {
		c := naming.Container(m.Prefix, slug, s.ID)
		n.Containers = append(n.Containers, c)
		n.Units = append(n.Units, c+".service")
		for _, v := range s.Volumes {
			if v.Shared {
				continue
			}
			name, err := in.VolumeName(s.ID, v)
			if err != nil {
				return Names{}, err
			}
			if !slices.Contains(n.Volumes, name) {
				n.Volumes = append(n.Volumes, name)
			}
		}
	}
	// Quadlet runs X.network as X-network.service.
	n.Units = append(n.Units, strings.TrimSuffix(naming.DevNetworkUnit(m.Prefix, slug), ".network")+"-network.service")
	slices.Sort(n.Volumes)
	n.Networks = []string{naming.DevNetwork(m.Prefix, slug)}
	return n, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
