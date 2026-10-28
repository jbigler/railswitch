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
	RoleAgent  = "agent" // an agent package's container (D54)
)

// Worktree is one registered worktree: <workspace>/<slug>/ (D22). Share is
// set while it is shared on the tailnet (D46).
type Worktree struct {
	Slug        string
	ID          int // machine-wide; ports are derived from it
	Dir, Branch string
	Share       *machine.ShareEntry
}

// Host is what render reads from the host. GitName/GitEmail: host `git config
// --global user.name/user.email`, read by workspace.Render. NetworkDir:
// netavark's network config dir (<podman GraphRoot>/networks), set by
// workspace.Render for the firewall hook descriptor.
type Host struct {
	CPUs                                int
	Home, NetworkDir, GitName, GitEmail string
	Missing                             map[string]bool // optional machine mount sources absent at render time (D57)
}

// MachineMount is a config.toml [[mount]] of this project (D57). Index is
// its position in config.toml, for messages.
type MachineMount struct {
	Service, Source, Target string
	RO, Optional            bool
	Relabel                 bool // SELinux z; false renders ro or no options
	Index                   int
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
	Mounts    []MachineMount // machine-local mounts (D57), already filtered to this project
}

// Model is a project's services, independent of any worktree. Every string
// field of a Service is a template (internal/placeholder grammar) expanded per
// worktree with Instance.Scope; literal text from outside packages (paths,
// host values) is stored with "{" escaped as "{{".
type Model struct {
	Prefix   string
	Services []Service // ordered: app, deps, worker, data (slot order), tools, agents (Stack.Agents order)
	Inputs   Inputs
	Warnings []string // non-fatal findings, e.g. a suggested option addon missing from [image] addons
	Assets   []Asset  // every package asset a service mounts; Task 11's Plan writes them

	baseDir    string                    // <workspace>/<sanitize(base_branch)>
	chain      []*containerfile          // [image] base, then each addon
	tools      map[string]*containerfile // tool service id → containerfiles/Containerfile.<tool>
	standalone map[string]*containerfile // option service id → its standalone Containerfile
	agentBase  *containerfile            // containerfiles/Containerfile.agent-base; nil without agents
	agents     map[string]*containerfile // agent package (agentPkg) → containerfiles/Containerfile.<pkg>
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
	Command    string   // the app's server, deps's install or an option's command
	Networks   []string // "dev", "proxy"
	Publish    []Port   // host port = Base + worktree ID, on 127.0.0.1
	Volumes    []Volume
	Mounts     []Mount
	Routes     []Route
	Health     *Health
	Requires   []string // service ids
	Wants      []string
	Firewall   []string // profiles; ["self"] = no hook
	// FirewallMode is the firewall hook's mode for s: "" = the project's
	// [firewall] mode; an agent's is always "enforce" (D55).
	FirewallMode string
	// FirewallExtra are hosts the firewall hook allows s besides its
	// profiles': nvim's config repo host (D54).
	FirewallExtra []string
	Oneshot       bool
	// OnDemand: the service has no Quadlet unit and no app wants it; it runs
	// as a transient container (an on-demand agent).
	OnDemand bool
	// Worktree: the service mounts the worktree, so it runs as uid 1000 with
	// keep-id.
	Worktree bool
	Workdir  string // "/app" for a worktree service: a transient run's -w
	Unit     UnitExtras
	EnvSpec  []EnvVar // unexpanded templates with layer provenance, lowest layer first
	// ShareEnv is env layer 7 (D46), applied over EnvSpec only in a shared
	// worktree's Instance: an unshared one never meets a {share.*} placeholder.
	ShareEnv []EnvVar

	scheme string // the {scheme} of this option service (pack db_scheme[<option>]); "" = none
}

// ImageKind says where a service's image comes from.
type ImageKind int

const (
	ImagePull       ImageKind = iota + 1 // a registry ref
	ImageBase                            // the worktree's base image (D31)
	ImageBuild                           // a tool's image, built on the base (D31)
	ImageStandalone                      // an option's own build, not on the chain (playwright)
	ImageAgent                           // an agent package's image, built on the agent base image
)

// ImageSpec is a service's image. Its concrete ref per worktree is
// Instance.Values["image.<svc>"].
type ImageSpec struct {
	Kind ImageKind
	Ref  string            // ImagePull: registry ref template
	File string            // ImageBuild, ImageStandalone, ImageAgent: absolute path under <ws>/containerfiles/
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
	Cache  bool   // rebuildable content: losing it is no data loss (pack data = "cache")
	Shared bool   // shared by every worktree: <prefix>_<name>; else <prefix>-<slug>-<name>
	Seed   string // file under <workspace>/db-dumps/ imported when the volume is created (D18)
}

// Mount is a bind mount; Source and Target are templates. Origin is "" for
// package mounts and `[[mount]] #<i> service = "<svc>"` for machine mounts.
// A BaseOnly mount is rendered in the base worktree only.
type Mount struct {
	Source, Target, Options string
	Origin                  string
	BaseOnly                bool
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
	RoleApp: {"4g", 4096}, RoleWorker: {"2g", 4096}, RoleDeps: {"4g", 4096}, RoleData: {"2g", 1024}, RoleTool: {"2g", 4096}, RoleAgent: {"4g", 4096},
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
		m:      &Model{Prefix: p.Project.Prefix, Inputs: in, baseDir: filepath.Join(in.Workspace, base), tools: map[string]*containerfile{}, standalone: map[string]*containerfile{}, agents: map[string]*containerfile{}},
		assets: map[string]bool{},
		opts:   map[string]*pack.Option{},
		tools:  map[string]*pack.Tool{},
		agents: map[string]*pack.ChosenAgent{},
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
	if err := b.checkHideLinks(); err != nil {
		return nil, err
	}
	b.addonWarnings()
	return b.m, nil
}

type builder struct {
	in     Inputs
	m      *Model
	assets map[string]bool              // Rel of each listed asset
	opts   map[string]*pack.Option      // option service id → its option
	tools  map[string]*pack.Tool        // tool service id → its tool
	agents map[string]*pack.ChosenAgent // agent service id → its agent
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
		ID: "deps", Role: RoleDeps, Image: baseImage, Oneshot: true, Command: pk.App.Install,
		Firewall: []string{"deps"},
		Worktree: true,
		// No .git: nothing an install needs (D11). In the base worktree /app
		// holds it, so it is read-only there, its hooks too (D60).
		Mounts:  append([]Mount{{Source: "{worktree.dir}", Target: "/app", Options: "z"}}, appGit("ro,z")...),
		Volumes: b.appVolumes(),
		Unit:    unitExtras(pack.Unit{}, RoleDeps, p.Services["deps"]),
	}
	var errs []error
	errs = append(errs, b.entrypoint(&app, pk.Name, pk.Files, pk.App.Entrypoint))
	errs = append(errs, b.entrypoint(&deps, pk.Name, pk.Files, pk.App.DepsEntrypoint))

	var workers, data, toolOpts, tools []Service
	for _, ch := range st.Options {
		s, err := b.optionService(ch, slotRoles[ch.Option.Slot])
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
		if n := b.in.Project.Tools.Nvim; t.Name == "nvim" && n != nil {
			if h, ok := naming.GitHost(n.ConfigRepo); ok {
				s.FirewallExtra = []string{h}
			}
		}
		tools = append(tools, s)
	}
	var agents []Service
	for _, a := range st.Agents {
		s, err := b.agentService(a)
		errs = append(errs, err)
		b.agents[s.ID] = a
		agents = append(agents, s)
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
	// D62: no agent is wanted; one starts only through `railswitch agent`.
	for i := range workers {
		workers[i].Requires = slices.Clone(app.Requires)
	}

	b.m.Services = slices.Concat([]Service{app, deps}, workers, data, tools, agents)
	if err := b.projectRoutes(); err != nil {
		return err
	}
	for i := range b.m.Services {
		s := &b.m.Services[i]
		s.Networks = []string{"dev"}
		// D49: app, workers, tool packages and agents (C6) reach
		// Traefik-routed hosts (S3) through the proxy network; routed
		// services sit there for Traefik.
		switch {
		case s.Role == RoleApp, s.Role == RoleWorker, s.Role == RoleAgent, b.tools[s.ID] != nil, len(s.Routes) > 0:
			s.Networks = append(s.Networks, "proxy")
		}
		if s.Worktree {
			s.Workdir = "/app"
		}
		s.Mounts = append(s.Mounts, b.machineMounts(s.ID)...)
	}
	return nil
}

// projectRoutes adds project.toml's [[routes]] to their services (D67), then
// refuses two routes of the project with one router name or host: Traefik
// would route one of them at random. Hosts of different projects carry the
// prefix, so they never collide.
func (b *builder) projectRoutes() error {
	var errs []error
	for i, r := range b.in.Project.Routes {
		s := b.m.service(r.Service)
		switch {
		case s == nil:
			errs = append(errs, fmt.Errorf("routes[%d].service: no such service %q (services: %s)", i, r.Service, strings.Join(b.m.serviceIDs(), ", ")))
		case s.Oneshot || s.Role == RoleAgent:
			errs = append(errs, fmt.Errorf("routes[%d].service: %s has no long-running container to route to", i, r.Service))
		default:
			s.Routes = append(s.Routes, Route{Name: r.Name, Host: r.Name + ".{worktree.host}", Port: r.Port})
		}
	}
	routers, hosts := map[string]string{}, map[string]string{}
	for _, s := range b.m.Services {
		for _, r := range s.Routes {
			if prev, ok := routers[r.Name]; ok {
				errs = append(errs, fmt.Errorf("routes: router %q is used by %s and %s", r.Name, prev, s.ID))
			} else {
				routers[r.Name] = s.ID
			}
			if prev, ok := hosts[r.Host]; ok {
				errs = append(errs, fmt.Errorf("routes: host %q is used by %s and %s", r.Host, prev, s.ID))
			} else {
				hosts[r.Host] = s.ID
			}
		}
	}
	return errors.Join(errs...)
}

// machineMounts are the machine-local mounts of service id (D57), less the
// optional ones whose source is missing.
func (b *builder) machineMounts(id string) []Mount {
	var out []Mount
	for _, mm := range b.in.Mounts {
		if mm.Service != id || mm.Optional && b.in.Host.Missing[mm.Source] {
			continue
		}
		var o []string
		if mm.RO {
			o = append(o, "ro")
		}
		if mm.Relabel {
			o = append(o, "z")
		}
		opts := strings.Join(o, ",")
		out = append(out, Mount{Source: literal(mm.Source), Target: literal(mm.Target), Options: opts, Origin: fmt.Sprintf("[[mount]] #%d service = %q", mm.Index, mm.Service)})
	}
	return out
}

// worktreeMounts are the app's view of the worktree: itself at /app, and the
// base worktree's .git at its own path (a worktree's .git file points there).
// A service that may write .git ("z": tools and agents) also gets the
// worktree at its own host path (C11), so the gitdir paths git records in
// the base .git resolve in the container too and git never sees a worktree
// as prunable (D30). git-guard (D60): a writable .git gets its hooks
// read-only on top, and in the base worktree, where /app is the base
// checkout, /app/.git and /app/.git/hooks are mounted too (appGit).
func worktreeMounts(gitOptions string) []Mount {
	m := []Mount{{Source: "{worktree.dir}", Target: "/app", Options: "z"}}
	if gitOptions == "z" {
		m = append(m, Mount{Source: "{worktree.dir}", Target: "{worktree.dir}", Options: "z"})
	}
	m = append(m, Mount{Source: "{base.git}", Target: "{base.git}", Options: gitOptions})
	if gitOptions == "z" {
		m = append(m, Mount{Source: "{base.git}/hooks", Target: "{base.git}/hooks", Options: "ro,z"})
	}
	return append(m, appGit(gitOptions)...)
}

// appGit are git-guard's base-worktree mounts (D60): /app/.git with the
// service's git access, a mount point so it can't be renamed away and
// rebuilt (EBUSY), then /app/.git/hooks read-only on top.
func appGit(gitOptions string) []Mount {
	return []Mount{
		{Source: "{base.git}", Target: "/app/.git", Options: gitOptions, BaseOnly: true},
		{Source: "{base.git}/hooks", Target: "/app/.git/hooks", Options: "ro,z", BaseOnly: true},
	}
}

// appVolumes are the pack's shared caches and per-worktree volumes.
func (b *builder) appVolumes() []Volume {
	pk := b.in.Stack.Pack
	var out []Volume
	for _, v := range pk.Caches {
		out = append(out, Volume{Name: v.Name, Mount: v.Mount, Chown: v.Chown, Cache: v.Data == pack.DataCache, Shared: true})
	}
	for _, v := range pk.WorktreeVolumes {
		out = append(out, Volume{Name: v.Name, Mount: v.Mount, Chown: v.Chown, Cache: v.Data == pack.DataCache})
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
		s.Volumes = append(s.Volumes, Volume{Name: v.Name, Mount: v.Mount, Chown: v.Chown, Cache: v.Data == pack.DataCache, Seed: v.Seed})
	}
	if err := b.packageMounts(&s, o.Name, o.Files, o.Mounts); err != nil {
		return s, err
	}
	return s, b.entrypoint(&s, o.Name, o.Files, o.Entrypoint)
}

// packageMounts appends a package's [[mounts]] to s. A relative source is a
// package file, written as an asset (default options "ro,z"); an absolute or
// placeholder source is a template.
func (b *builder) packageMounts(s *Service, pkg string, files fs.FS, mounts []pack.Mount) error {
	for _, mt := range mounts {
		if strings.HasPrefix(mt.Source, "/") || strings.HasPrefix(mt.Source, "{") {
			s.Mounts = append(s.Mounts, Mount{Source: mt.Source, Target: mt.Target, Options: mt.Options})
			continue
		}
		src, err := b.asset(pkg, files, mt.Source)
		if err != nil {
			return err
		}
		opts := mt.Options
		if opts == "" {
			opts = "ro,z"
		}
		s.Mounts = append(s.Mounts, Mount{Source: literal(src), Target: mt.Target, Options: opts})
	}
	return nil
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
			s.Volumes = append(s.Volumes, Volume{Name: v.Name, Mount: v.Mount, Chown: v.Chown, Cache: v.Data == pack.DataCache})
		}
	}
	if t.Home {
		s.Mounts = append(s.Mounts, Mount{Source: homeSource, Target: appHome, Options: "z"})
	}
	if t.Caches {
		for _, v := range pk.Caches {
			s.Volumes = append(s.Volumes, Volume{Name: v.Name, Mount: v.Mount, Chown: v.Chown, Cache: v.Data == pack.DataCache, Shared: true})
		}
	}
	for _, v := range t.Shared {
		s.Volumes = append(s.Volumes, Volume{Name: v.Name, Mount: v.Mount, Chown: v.Chown, Cache: v.Data == pack.DataCache, Shared: true})
	}
	return s, b.entrypoint(&s, t.Name, t.Files, t.Entrypoint)
}

// statusMount is where an agent's container sees its worktree's status dir
// (D58): it writes RAILSWITCH_STATUS_FILE, <statusMount>/<agent>.json.
const statusMount = "/run/railswitch/status"

// agentPkg names agent package a's image, build step and assets: the <pkg>
// of its Containerfile.<pkg>. Resolve refuses two packages with one
// Containerfile name, so it is unique per package in a stack.
func agentPkg(a *pack.Agent) string { return strings.TrimPrefix(a.Image.File, "Containerfile.") }

// agentService is agent a's container (D54): a worktree service on the agent
// package's image, firewalled under its own profile agent-<name> in enforce
// mode (D55, C10), interactive, with the worktree, the status dir, a
// per-worktree home dir and the pack's volumes.
func (b *builder) agentService(a *pack.ChosenAgent) (Service, error) {
	pk, ag := b.in.Stack.Pack, a.Agent
	pkg := agentPkg(ag)
	s := Service{
		ID: a.Service, Role: RoleAgent, Exec: ag.Unit.Exec,
		Image:        ImageSpec{Kind: ImageAgent, File: b.ws("containerfiles", ag.Image.File)},
		Firewall:     []string{"agent-" + a.Name},
		FirewallMode: "enforce",
		OnDemand:     a.Run == "on-demand",
		Worktree:     true,
		Unit:         unitExtras(ag.Unit, RoleAgent, b.in.Project.Services[a.Service]),
	}
	s.Unit.TTY = true // an agent is a terminal program, attached to or not
	s.Mounts = append(worktreeMounts("z"),
		Mount{Source: "{status.dir}", Target: statusMount, Options: "rw,z"},
		Mount{Source: agentHomeSource + "/" + a.Name, Target: appHome, Options: "z"})
	if err := b.packageMounts(&s, pkg, ag.Files, ag.Mounts); err != nil {
		return s, err
	}
	for _, v := range pk.WorktreeVolumes {
		s.Volumes = append(s.Volumes, Volume{Name: v.Name, Mount: v.Mount, Chown: v.Chown, Cache: v.Data == pack.DataCache})
	}
	for _, v := range pk.Caches {
		s.Volumes = append(s.Volumes, Volume{Name: v.Name, Mount: v.Mount, Chown: v.Chown, Cache: v.Data == pack.DataCache, Shared: true})
	}
	for _, v := range ag.Shared {
		s.Volumes = append(s.Volumes, Volume{Name: v.Name, Mount: v.Mount, Chown: v.Chown, Cache: v.Data == pack.DataCache, Shared: true})
	}
	return s, b.entrypoint(&s, pkg, ag.Files, ag.Entrypoint)
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

// Agents are the agent services, in Stack.Agents order (by name).
func (m *Model) Agents() []*Service {
	var out []*Service
	for i := range m.Services {
		if m.Services[i].Role == RoleAgent {
			out = append(out, &m.Services[i])
		}
	}
	return out
}

func (m *Model) serviceIDs() []string {
	ids := make([]string, len(m.Services))
	for i, s := range m.Services {
		ids[i] = s.ID
	}
	return ids
}

// PortBases is every published port's base by port name: the [[ports]] of
// the pack, options and tools the project uses.
func (m *Model) PortBases() map[string]int {
	bases := map[string]int{}
	for _, s := range m.Services {
		for _, p := range s.Publish {
			bases[p.Name] = p.Base
		}
	}
	return bases
}

// SharePort is a port served on the tailnet while its worktree is shared
// (D46): tailnet HTTPS port Port serves loopback port Port.
type SharePort struct {
	Service, Name string // the publishing service and the port's name
	Port          int    // base + worktree ID
}

// SharePorts are worktree id's ports with share = true, in model order (the
// app's first).
func (m *Model) SharePorts(id int) []SharePort {
	var out []SharePort
	for _, s := range m.Services {
		for _, p := range s.Publish {
			if p.Share {
				out = append(out, SharePort{Service: s.ID, Name: p.Name, Port: p.Base + id})
			}
		}
	}
	return out
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
	for _, ch := range st.Options {
		if ch.Option.AppAddon == nil {
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
	Builds   []BuildStep         // in build order: the base chain, tool images, agent-base and agent images, then standalone option images
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

// Mounts are s's bind mounts in this worktree: BaseOnly ones only in the
// base worktree.
func (in *Instance) Mounts(s *Service) []Mount {
	if filepath.Clean(in.Worktree.Dir) == filepath.Clean(in.m.baseDir) {
		return s.Mounts
	}
	var out []Mount
	for _, mt := range s.Mounts {
		if !mt.BaseOnly {
			out = append(out, mt)
		}
	}
	return out
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
		"worktree.home":  filepath.Join(m.Inputs.Workspace, filepath.FromSlash(homeDir), wt.Slug),      // D30's per-worktree home
		"agent.home":     filepath.Join(m.Inputs.Workspace, filepath.FromSlash(agentHomeDir), wt.Slug), // <agent.home>/<agent>: an agent's home (D55)
		"base.git":       filepath.Join(m.baseDir, ".git"),
		"traefik.ip":     machine.TraefikIP(m.Inputs.Machine.Subnet),
		"branch":         wt.Branch,
		"project.prefix": m.Prefix,
		"dumps":          filepath.Join(m.Inputs.Workspace, "db-dumps"),
		"status.dir":     filepath.Join(m.Inputs.Workspace, filepath.FromSlash(statusDir), wt.Slug), // D58: the agents' status files
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

	// from records where each runtime's value came from, for a pin of the
	// same name below.
	from := map[string]string{}
	for _, name := range sortedKeys(pk.Runtimes) {
		val, src := override[name], fmt.Sprintf("worktrees.%s.versions", wt.Slug)
		if val == "" {
			ch := pk.Runtimes[name]
			r := version.Resolve(wt.Dir, ch.From, ch.Fallback, version.ValidVersion)
			warn(r.Warnings)
			if r.Source == "none" { // no source and no fallback: empty (D69)
				v[name], v[name+".major"] = "", ""
				continue
			}
			val, src = r.Value, "runtimes."+name+" ("+r.Source+")"
		}
		if err := setVersion(name, val, src); err != nil {
			return nil, err
		}
		from[name] = src
	}
	// An option's pin, high to low: [worktrees.<slug>.versions], [versions],
	// the pack's runtime of the same name (rails reads playwright from
	// Gemfile.lock), the pin's default (D68).
	for _, ch := range st.Options {
		if ch.Option.Pin == nil {
			continue
		}
		name, val, src := ch.Option.Pin.Name, ch.Option.Pin.Default, ch.Option.Name+" pin default"
		if s, ok := from[name]; ok {
			val, src = v[name], s
		}
		if x, ok := p.Versions[name]; ok {
			val, src = x, "versions"
		}
		if x, ok := override[name]; ok {
			val, src = x, fmt.Sprintf("worktrees.%s.versions", wt.Slug)
		}
		if err := setVersion(name, val, src); err != nil {
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
	if sh := wt.Share; sh != nil {
		v["share.host"] = sh.Host
		for _, sp := range m.SharePorts(wt.ID) {
			v["share.url."+sp.Name] = "https://" + sh.Host + ":" + strconv.Itoa(sp.Port)
		}
	}
	for _, name := range sortedKeys(p.Secrets) {
		v["secret."+name] = SecretName(m.Prefix, name, p.Secrets[name])
	}
	m.envValues(in)
	return in, nil
}

// SecretName is the podman secret holding project secret name (D33).
func SecretName(prefix, name string, s config.Secret) string {
	if s.Scope == "machine" {
		return "railswitch_" + name
	}
	return naming.Shared(prefix, name)
}

// SecretSpecs are the podman --secret specs (Quadlet Secret= values) of
// every project secret service svc receives, in name order: an env var when
// the secret sets env, else the file /run/secrets/<name> (D33).
func (in *Instance) SecretSpecs(svc string) []string {
	p := in.m.Inputs.Project
	var specs []string
	for _, name := range sortedKeys(p.Secrets) {
		sec := p.Secrets[name]
		if !slices.Contains(sec.Services, svc) {
			continue
		}
		spec := in.Values["secret."+name]
		if sec.Env != "" {
			spec += ",type=env,target=" + sec.Env
		} else {
			spec += ",type=mount,target=/run/secrets/" + name + ",uid=1000,gid=1000,mode=0400"
		}
		specs = append(specs, spec)
	}
	return specs
}

// SecretArgs are service svc's secrets as podman run flags, for a transient
// container (exec) that must see what the service's unit gets.
func (in *Instance) SecretArgs(svc string) []string {
	var args []string
	for _, s := range in.SecretSpecs(svc) {
		args = append(args, "--secret", s)
	}
	return args
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

// Names are the exact names of one worktree's objects. Shared are the
// shared "<prefix>_…" volumes its services mount, which only project rm
// --purge removes; Volumes never holds one.
type Names struct{ Units, Containers, Volumes, Shared, Networks []string }

// NamesFor returns the exact names of a registered worktree's objects:
// "<prefix>-<slug>-<svc>.service" of every service but an on-demand one and
// "<prefix>-<slug>-dev-network.service", every container (on-demand ones
// too), its per-worktree volumes, the shared volumes and its dev network.
// An unregistered slug is an error.
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
		if !s.OnDemand { // an on-demand service has no unit; its container and volumes are still the worktree's
			n.Units = append(n.Units, c+".service")
		}
		for _, v := range s.Volumes {
			name, err := in.VolumeName(s.ID, v)
			if err != nil {
				return Names{}, err
			}
			vols := &n.Volumes
			if v.Shared {
				vols = &n.Shared
			}
			if !slices.Contains(*vols, name) {
				*vols = append(*vols, name)
			}
		}
	}
	// Quadlet runs X.network as X-network.service.
	n.Units = append(n.Units, strings.TrimSuffix(naming.DevNetworkUnit(m.Prefix, slug), ".network")+"-network.service")
	slices.Sort(n.Volumes)
	slices.Sort(n.Shared)
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
