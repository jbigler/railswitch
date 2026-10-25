package pack

import (
	"fmt"
	"io/fs"
)

// The Go mirrors of pack.toml, option.toml and tool.toml (D12). Decoding is
// strict: a key not defined here is a Load error.
//
// String values are templates: "{name}" placeholders (internal/placeholder
// grammar) are expanded per worktree by Task 8. Everything else in a value is
// literal; escaping for unit files (% and $) is the unit writer's job, so no
// value here is pre-escaped.
//
// A relative file path (an entrypoint, a Containerfile, a [[mounts]] source)
// names a file in the package's own directory, which Load checks exists.

// Pack is one stack pack: pack.toml plus its directory.
type Pack struct {
	Name    string `toml:"name"`
	Version int    `toml:"version"`

	App   App       `toml:"app"`
	Deps  Deps      `toml:"deps"`
	Image PackImage `toml:"image"` // the base image (D31): app, deps and worker run it

	// Runtimes are the version chains whose results are build args and
	// image tag parts, keyed by runtime name ("ruby", "node", "playwright").
	Runtimes map[string]Chain `toml:"runtimes"`
	DBName   Chain            `toml:"db_name"` // the database name chain; its result is {db}

	Env      map[string]string `toml:"env"`       // layer 1 (HANDOFF §3.7): only what the framework itself reads (D42)
	DBScheme map[string]string `toml:"db_scheme"` // database option name → URL scheme, the {scheme} placeholder

	Caches          []Volume `toml:"caches"`           // shared across worktrees: <prefix>_<name>
	WorktreeVolumes []Volume `toml:"worktree_volumes"` // per worktree: <prefix>-<slug>-<name>
	Ports           []Port   `toml:"ports"`            // published by the app service

	Defaults map[string]string `toml:"defaults"` // slot → option ref or "none", for railswitch new
	Tasks    map[string]string `toml:"tasks"`    // task name → command run in the app image
	Links    []Link            `toml:"links"`    // suggested worktree links (HANDOFF §3.10)
	Unit     Unit              `toml:"unit"`     // extras for the app unit

	Files fs.FS `toml:"-"` // the pack's directory
}

// App is the pack's [app] table: the app service.
type App struct {
	Service           string `toml:"service"` // default app service id; project.toml [stack] app overrides it
	Server            string `toml:"server"`  // the app's command
	Port              int    `toml:"port"`    // the port Traefik routes {worktree.host} to
	Install           string `toml:"install"` // what deps runs; lockfile-frozen (D35)
	Health            string `toml:"health"`  // app health command (D34); empty = none
	HealthStartPeriod string `toml:"health_start_period"`
	Entrypoint        string `toml:"entrypoint"`      // package file mounted as the app's entrypoint
	DepsEntrypoint    string `toml:"deps_entrypoint"` // package file mounted as the deps service's entrypoint
}

// Deps is the pack's [deps] table.
type Deps struct {
	// FrozenEnv is set on the deps service only; `railswitch deps` leaves it
	// out so a lockfile change is possible on request (D35).
	FrozenEnv map[string]string `toml:"frozen_env"`
}

// PackImage is the pack's default base image (D31). File is ejected to
// containerfiles/ by railswitch new; railswitch never builds it directly.
type PackImage struct {
	File string            `toml:"file"` // e.g. "Containerfile.base"
	Tag  string            `toml:"tag"`  // tag version part, e.g. "ruby{ruby}-node{node}" (D9)
	OS   string            `toml:"os"`   // OS family; an option addon's os must match it (D31)
	Args map[string]string `toml:"args"` // build args
}

// Chain is a version or value chain: the first source that yields a value
// wins, else Fallback. Source syntax is read by internal/version.
type Chain struct {
	From     []string `toml:"from"`
	Fallback string   `toml:"fallback"`
}

// Volume is a named volume mounted at Mount.
type Volume struct {
	Name  string `toml:"name"`  // name suffix; may hold placeholders ("gems_ruby{ruby}")
	Mount string `toml:"mount"` // container path
	Chown bool   `toml:"chown"` // mount with :U (a volume under a bind mount has no image content to copy up)
	Seed  string `toml:"seed"`  // option volumes only: file under <workspace>/db-dumps/ imported when the volume is created (D18)
}

// Port is a published port: host port Base + worktree ID, container port
// Container, its host port given to services as env Env.
type Port struct {
	Name      string `toml:"name"`
	Env       string `toml:"env"`
	Base      int    `toml:"base"`
	Container int    `toml:"container"`
}

// Link is a suggested worktree link (HANDOFF §3.10).
type Link struct {
	Label string `toml:"label"`
	URL   string `toml:"url"`
}

// Mount is a bind mount. A relative Source is a package file (Task 8 writes
// it under .railswitch/assets/<package>/); anything else is a template that
// expands to an absolute host path, e.g. "{dumps}".
type Mount struct {
	Source  string `toml:"source"`
	Target  string `toml:"target"`
	Options string `toml:"options"` // e.g. "ro,z"
}

// Route is a Traefik route to the service.
type Route struct {
	Name string `toml:"name"` // router suffix: <prefix>-<slug>-<name>
	Host string `toml:"host"` // e.g. "s3.{worktree.host}"
	Port int    `toml:"port"` // container port
	// RootRedirect, when set, redirects a request for the host's bare root
	// to this path (a Traefik redirectregex middleware).
	RootRedirect string `toml:"root_redirect"`
}

// Health is a container health check.
type Health struct {
	Cmd         string `toml:"cmd"`
	Interval    string `toml:"interval"`
	Timeout     string `toml:"timeout"`
	Retries     int    `toml:"retries"`
	StartPeriod string `toml:"start_period"`
}

// Unit holds per-service unit settings.
type Unit struct {
	Exec              []string `toml:"exec"`           // the container's command and arguments
	ShmSize           string   `toml:"shm_size"`       // e.g. "256mb"
	AddCapability     []string `toml:"add_capability"` // only the browser (D34)
	RunInit           bool     `toml:"run_init"`
	SuccessExitStatus int      `toml:"success_exit_status"` // an extra exit status that counts as a clean stop
	TTY               bool     `toml:"tty"`                 // run with --tty --interactive
	Memory            string   `toml:"memory"`              // D34 default for this service, e.g. "2g"
	PidsLimit         int      `toml:"pids_limit"`          // D34 default for this service
}

// Profiles is a firewall setting: one profile name or a list of them. The
// profile "self" means the container runs its own firewall and gets no hook.
type Profiles []string

// UnmarshalTOML accepts `firewall = "self"` and `firewall = ["agent", "deps"]`.
func (p *Profiles) UnmarshalTOML(v any) error {
	switch v := v.(type) {
	case string:
		*p = Profiles{v}
	case []any:
		out := make(Profiles, len(v))
		for i, e := range v {
			s, ok := e.(string)
			if !ok {
				return fmt.Errorf("firewall: entry %d is not a string", i)
			}
			out[i] = s
		}
		*p = out
	default:
		return fmt.Errorf("firewall: want a profile name or a list of them, got %T", v)
	}
	return nil
}

// Option is one choice for a slot (HANDOFF §3.5): option.toml plus its directory.
type Option struct {
	Name    string `toml:"name"`
	Version int    `toml:"version"`

	Slot      string   `toml:"slot"`       // one of config.Slots
	Service   string   `toml:"service"`    // the service id it runs as
	WorksWith []string `toml:"works_with"` // pack names, or "*"
	Needs     []string `toml:"needs"`      // slots that must be filled (sidekiq needs cache)
	Generated []string `toml:"generated"`  // D17 values it uses as {generated.X}

	Pin      *Pin        `toml:"pin"` // the image version pin: [versions] <name>, default Default
	Image    OptionImage `toml:"image"`
	AppAddon *Addon      `toml:"app_addon"` // D31: default addon for the app's base chain
	Health   *Health     `toml:"health"`

	Entrypoint string   `toml:"entrypoint"` // package file mounted as the entrypoint
	Command    string   `toml:"command"`    // the service's command (an option running the app image)
	Firewall   Profiles `toml:"firewall"`   // empty = the role's default profile

	Gives   map[string]string `toml:"gives"` // layer 2, to app, worker and tools; never to its own service
	Env     map[string]string `toml:"env"`   // its own service's env
	Volumes []Volume          `toml:"volumes"`
	Mounts  []Mount           `toml:"mounts"`
	Ports   []Port            `toml:"ports"`
	Routes  []Route           `toml:"routes"`
	Links   []Link            `toml:"links"`
	Unit    Unit              `toml:"unit"`

	Files fs.FS `toml:"-"` // the option's directory
}

// Pin is an option's version pin.
type Pin struct {
	Name    string `toml:"name"`    // the [versions] key and placeholder name, e.g. "postgres"
	Default string `toml:"default"` // used when [versions] does not set it
}

// OptionImage is what an option runs: exactly one of Pull, Use or Build.
type OptionImage struct {
	Pull string `toml:"pull"` // registry ref template, e.g. "docker.io/library/postgres:{postgres}"
	Use  string `toml:"use"`  // "app": run the app's base image
	// Build is a standalone Containerfile (D31): not on the base chain, gets
	// no BASE_IMAGE, ejected to containerfiles/<Build> and found by filename.
	Build string            `toml:"build"`
	Tag   string            `toml:"tag"`  // Build only: tag version part
	Args  map[string]string `toml:"args"` // Build only: build args
}

// Addon is an option's default addon for the app's base chain (D31).
type Addon struct {
	File string `toml:"file"` // e.g. "Containerfile.pg-client"
	OS   string `toml:"os"`   // must match the pack's [image] os
}

// Tool is a developer tool built on the base image (D23): tool.toml plus its directory.
type Tool struct {
	Name    string    `toml:"name"`
	Service string    `toml:"service"` // service id; defaults to Name
	Image   ToolImage `toml:"image"`

	Entrypoint string   `toml:"entrypoint"`
	Firewall   Profiles `toml:"firewall"`

	// Worktree mounts the worktree at /app, the base worktree's .git, and the
	// pack's [[worktree_volumes]] (they live under /app), as the app has them.
	Worktree bool `toml:"worktree"`
	// Home mounts a per-worktree home directory at /home/appuser.
	Home bool `toml:"home"`
	// Caches mounts the pack's [[caches]] at their mount points.
	Caches bool     `toml:"caches"`
	Shared []Volume `toml:"shared"` // the tool's own volumes shared by every worktree: <prefix>_<name>

	Env   map[string]string `toml:"env"`
	Ports []Port            `toml:"ports"`
	Links []Link            `toml:"links"`
	Unit  Unit              `toml:"unit"`

	Files fs.FS `toml:"-"` // the tool's directory
}

// ToolImage is a tool's default Containerfile, built on the base chain (D31).
type ToolImage struct {
	File string `toml:"file"` // e.g. "Containerfile.nvim"
}
