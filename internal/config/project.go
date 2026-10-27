// Package config reads and validates the workspace's project.toml.
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/placeholder"
	"github.com/jbigler/railswitch/internal/tomlfile"
)

// SchemaVersion is the project.toml schema this railswitch reads (D39).
const SchemaVersion = 1

// Slots are the option slots a project may fill in [options].
var Slots = []string{"database", "cache", "storage", "jobs", "mail", "browser"}

// Project is project.toml: the choices and overrides only (HANDOFF §3.2).
type Project struct {
	Schema    int                         `toml:"schema"` // must be 1 (D39)
	From      string                      `toml:"from"`
	Project   Meta                        `toml:"project"`
	Stack     StackRef                    `toml:"stack"`
	Options   map[string]string           `toml:"options"` // slot → ref, or "none"
	Versions  map[string]string           `toml:"versions"`
	Env       map[string]string           `toml:"env"`
	Share     Share                       `toml:"share"` // layer 7 (D46), while a worktree is shared on the tailnet
	Services  map[string]ServiceOverride  `toml:"services"`
	Worktrees map[string]WorktreeOverride `toml:"worktrees"`
	Secrets   map[string]Secret           `toml:"secrets"` // key = secret name; values live in podman secrets (D33)
	Links     []Link                      `toml:"links"`
	Tools     Tools                       `toml:"tools"`
	Firewall  Firewall                    `toml:"firewall"`
	Image     ImageConfig                 `toml:"image"`
	Agents    map[string]Agent            `toml:"agents"` // agent name → its package and settings
}

// ImageConfig is the workspace's base image chain (D31).
type ImageConfig struct {
	Base   string   `toml:"base"`   // required: "containerfiles/Containerfile.<name>"; railswitch project new ejects the pack's default here (D31)
	Addons []string `toml:"addons"` // "containerfiles/Containerfile.<name>", applied in this order on top of base
}

// Agent is one [agents.<name>] table.
type Agent struct {
	Use   string            `toml:"use"`   // "myagent@1" or "local:<name>@N"
	Run   string            `toml:"run"`   // "", "on-demand", "service"
	Env   map[string]string `toml:"env"`   // added to the agent's env
	Allow []string          `toml:"allow"` // extra firewall hosts for this agent
}

// Secret declares one user-provided secret (D33).
type Secret struct {
	Services []string `toml:"services"` // service ids that receive it ("agent" from Stage 4)
	Env      string   `toml:"env"`      // deliver as this env var; empty (default) = file /run/secrets/<name>
	Scope    string   `toml:"scope"`    // "project" (default): podman secret <prefix>_<name>; "machine": railswitch_<name>
	GitHost  string   `toml:"git_host"` // e.g. "github.com": receiving services use it for git over HTTPS (D33)
	GitUser  string   `toml:"git_user"` // default "x-access-token"
	// GitSigning: the value is an SSH signing key; receiving services sign
	// commits and tags with it (D61). File delivery only, never with GitHost.
	GitSigning bool `toml:"git_signing"`
}

// Meta is the [project] table.
type Meta struct {
	Name, Prefix, Repo string
	BaseBranch         string   `toml:"base_branch"`
	SeedFiles          []string `toml:"seed_files"`
}

// StackRef is the [stack] table: the pack ref and the app service id.
type StackRef struct{ Pack, App string }

// ServiceOverride is one [services.<id>] table.
type ServiceOverride struct {
	Env       map[string]string `toml:"env"`
	Memory    string            `toml:"memory"`     // e.g. "6g"; overrides the pack/option/role default (D34)
	PidsLimit int               `toml:"pids_limit"` // > 0; overrides the default (D34)
	Health    string            `toml:"health"`     // replaces the pack's app health command; "none" disables it (D34)
}

// WorktreeOverride is one [worktrees.<slug>] table.
type WorktreeOverride struct{ Versions map[string]string }

// Link is one [[links]] entry (HANDOFF §3.10).
type Link struct{ Label, URL string }

// Tools are the opt-in developer tools.
type Tools struct {
	Nvim *NvimTool `toml:"nvim"` // [tools.nvim]; nil = no nvim
}

// NvimTool is the [tools.nvim] table (D54): the git repo cloned into the
// empty <prefix>_nvim_config volume, and the branch or tag to check out.
type NvimTool struct {
	ConfigRepo string `toml:"config_repo"`
	ConfigRef  string `toml:"config_ref"`
}

// UnmarshalTOML decodes the [tools.nvim] table. The TOML decoder marks
// every key under a custom unmarshaler as decoded, so an unknown key is
// refused here; the old `[tools] nvim = <bool>` is refused naming the table
// (C1).
func (n *NvimTool) UnmarshalTOML(v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return errors.New("`[tools] nvim = <bool>` is now the table [tools.nvim] (D54); remove it or write [tools.nvim]")
	}
	for _, k := range sortedKeys(m) {
		var dst *string
		switch k {
		case "config_repo":
			dst = &n.ConfigRepo
		case "config_ref":
			dst = &n.ConfigRef
		default:
			return fmt.Errorf("tools.nvim.%s: unknown key (keys: config_repo, config_ref)", k)
		}
		s, ok := m[k].(string)
		if !ok {
			return fmt.Errorf("tools.nvim.%s: want a string, got %T", k, m[k])
		}
		*dst = s
	}
	return nil
}

// Share is the [share] table: Env overrides every layer of the services the
// project's [env] reaches, while a worktree is shared on the tailnet (D46).
type Share struct {
	Env map[string]string `toml:"env"`
}

// Firewall is the [firewall] table.
type Firewall struct {
	Mode  string              // "log" (default) | "enforce" | "off"
	Allow map[string][]string // profile → extra hosts
}

var (
	refRe           = regexp.MustCompile(`^[a-z][a-z0-9-]*@[1-9][0-9]{0,8}$`) // same grammar as pack.ParseRef
	envNameRe       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	secretNameRe    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
	memoryRe        = regexp.MustCompile(`^[0-9]+[kmg]$`)
	containerfileRe = regexp.MustCompile(`^containerfiles/Containerfile\.[a-z][a-z0-9-]*$`)
	bareKeyRe       = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	agentNameRe     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,24}$`) // agent-<name> fits naming's service id
	hostRe          = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
)

// LocalPrefix marks a workspace-local package ref, "local:<name>@N" (agents only).
const LocalPrefix = "local:"

// reservedAgents are names an agent cannot take: they are subcommands or
// image names of `railswitch agent`.
var reservedAgents = []string{"ls", "stop", "add", "base"}

// Load decodes path strictly: a key project.toml does not define is an error
// naming its dotted path. The schema is read first, on its own, so a file from
// a newer railswitch is refused as such even when it changes a known key's
// type. Load does not validate values; see Validate.
func Load(path string) (*Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return decode(path, data)
}

// decode is Load of path's contents data.
func decode(path string, data []byte) (*Project, error) {
	var p Project
	if err := tomlfile.Decode(data, SchemaVersion, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &p, nil
}

// Validate checks every field and returns all problems at once
// (errors.Join), each naming its dotted path.
func (p *Project) Validate() error {
	v := &validator{}

	switch {
	case p.Schema == 0:
		v.add("schema", "missing; add `schema = %d` at the top", SchemaVersion)
	case p.Schema > SchemaVersion:
		v.errs = append(v.errs, &tomlfile.NewerSchemaError{Schema: p.Schema, Max: SchemaVersion})
	case p.Schema != SchemaVersion:
		v.add("schema", "%d is not %d", p.Schema, SchemaVersion)
	}

	if p.From != "" {
		v.ref("from", p.From)
	}
	if err := naming.ValidPrefix(p.Project.Prefix); err != nil {
		v.add("project.prefix", "%v", err)
	}
	v.oneLine("project.name", p.Project.Name)
	v.oneLine("project.repo", p.Project.Repo)
	v.oneLine("project.base_branch", p.Project.BaseBranch)

	v.ref("stack.pack", p.Stack.Pack)
	if err := naming.ValidService(p.Stack.App); err != nil {
		v.add("stack.app", "%v", err)
	}

	for _, slot := range sortedKeys(p.Options) {
		path := "options." + key(slot)
		if !slices.Contains(Slots, slot) {
			v.add(path, "unknown slot (slots: %s)", strings.Join(Slots, ", "))
			continue
		}
		if ref := p.Options[slot]; ref != "none" {
			v.ref(path, ref)
		}
	}

	v.oneLines("versions", p.Versions)
	if n := p.Tools.Nvim; n != nil {
		v.oneLine("tools.nvim.config_repo", n.ConfigRepo)
		v.oneLine("tools.nvim.config_ref", n.ConfigRef)
	}
	v.env("env", p.Env)
	v.env("share.env", p.Share.Env)

	for _, id := range sortedKeys(p.Services) {
		path := "services." + key(id)
		if err := naming.ValidService(id); err != nil {
			v.add(path, "%v", err)
		}
		s := p.Services[id]
		v.env(path+".env", s.Env)
		if s.Memory != "" && !memoryRe.MatchString(s.Memory) {
			v.add(path+".memory", "%q must match %s, e.g. \"6g\"", s.Memory, memoryRe)
		}
		if s.PidsLimit < 0 {
			v.add(path+".pids_limit", "%d must be > 0", s.PidsLimit)
		}
		v.oneLine(path+".health", s.Health)
	}

	for _, slug := range sortedKeys(p.Worktrees) {
		path := "worktrees." + key(slug)
		if s, err := naming.Sanitize(slug); err != nil || s != slug {
			v.add(path, "not a worktree slug (lowercase letters, digits and -, at most %d chars)", naming.SlugMaxLen)
		}
		v.oneLines(path+".versions", p.Worktrees[slug].Versions)
	}

	signer := map[string]string{} // service id → its git_signing secret
	for _, name := range sortedKeys(p.Secrets) {
		path := "secrets." + key(name)
		if !secretNameRe.MatchString(name) {
			v.add(path, "secret name must match %s", secretNameRe)
		}
		s := p.Secrets[name]
		if len(s.Services) == 0 {
			v.add(path+".services", "list at least one service that receives the secret")
		}
		for i, id := range s.Services {
			if err := naming.ValidService(id); err != nil {
				v.add(fmt.Sprintf("%s.services[%d]", path, i), "%v", err)
			}
		}
		if s.Env != "" && !envNameRe.MatchString(s.Env) {
			v.add(path+".env", "%q is not an env var name (%s)", s.Env, envNameRe)
		}
		if s.Scope != "" && s.Scope != "project" && s.Scope != "machine" {
			v.add(path+".scope", "%q must be \"project\" or \"machine\"", s.Scope)
		}
		v.oneLine(path+".git_host", s.GitHost)
		v.oneLine(path+".git_user", s.GitUser)
		if s.GitSigning && s.Env != "" {
			v.add(path+".git_signing", "a signing key is file-delivered (/run/secrets/%s); drop env", name)
		}
		if s.GitSigning && s.GitHost != "" {
			v.add(path+".git_signing", "a signing key cannot also be a git_host token; declare the token as its own secret")
		}
		for _, id := range s.Services {
			if !s.GitSigning {
				break
			}
			if other, ok := signer[id]; ok {
				v.add(path+".git_signing", "service %s already gets the signing key %s; one git_signing secret per service", id, other)
				continue
			}
			signer[id] = name
		}
	}

	for i, l := range p.Links {
		path := fmt.Sprintf("links[%d]", i)
		v.oneLine(path+".label", l.Label)
		v.oneLine(path+".url", l.URL)
		for _, n := range placeholder.Names(l.URL) {
			if !placeholder.LinkNames[n] {
				v.add(path+".url", "unknown placeholder {%s} (allowed: {worktree.host}, {worktree.slug}, {branch})", n)
			}
		}
	}

	switch p.Firewall.Mode {
	case "", "log", "enforce", "off":
	default:
		v.add("firewall.mode", "%q must be \"log\", \"enforce\" or \"off\"", p.Firewall.Mode)
	}

	if p.Image.Base == "" {
		v.add("image.base", "missing; run `railswitch containerfile eject base`")
	} else {
		v.containerfile("image.base", p.Image.Base)
	}
	for i, a := range p.Image.Addons {
		v.containerfile(fmt.Sprintf("image.addons[%d]", i), a)
	}

	for _, name := range sortedKeys(p.Agents) {
		path := "agents." + key(name)
		switch {
		case !agentNameRe.MatchString(name):
			v.add(path, "agent name must match %s", agentNameRe)
		case slices.Contains(reservedAgents, name):
			v.add(path, "%q is a reserved agent name", name)
		}
		a := p.Agents[name]
		if r, ok := strings.CutPrefix(a.Use, LocalPrefix); ok {
			if !refRe.MatchString(r) {
				v.add(path+".use", "%q is not a ref name@N or local:name@N, e.g. \"myagent@1\"", a.Use)
			}
		} else {
			v.ref(path+".use", a.Use)
		}
		switch a.Run {
		case "", "on-demand", "service":
		default:
			v.add(path+".run", "%q must be \"on-demand\" or \"service\"", a.Run)
		}
		v.env(path+".env", a.Env)
		for i, h := range a.Allow {
			if !hostRe.MatchString(h) {
				v.add(fmt.Sprintf("%s.allow[%d]", path, i), "%q is not a host name", h)
			}
		}
	}

	return errors.Join(v.errs...)
}

type validator struct{ errs []error }

func (v *validator) add(path, format string, args ...any) {
	v.errs = append(v.errs, fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...)))
}

func (v *validator) ref(path, ref string) {
	if strings.HasPrefix(ref, LocalPrefix) {
		v.add(path, "%q: local: refs are supported for agents only until Stage 5 (D43)", ref)
		return
	}
	if !refRe.MatchString(ref) {
		v.add(path, "%q is not a ref name@N, e.g. \"postgres@1\"", ref)
	}
}

func (v *validator) oneLine(path, s string) {
	if strings.ContainsAny(s, "\r\n") {
		v.add(path, "value contains a newline")
	}
}

func (v *validator) oneLines(path string, m map[string]string) {
	for _, k := range sortedKeys(m) {
		v.oneLine(path+"."+key(k), m[k])
	}
}

func (v *validator) env(path string, m map[string]string) {
	for _, k := range sortedKeys(m) {
		if !envNameRe.MatchString(k) {
			v.add(path+"."+key(k), "not an env var name (%s)", envNameRe)
		}
		v.oneLine(path+"."+key(k), m[k])
	}
}

func (v *validator) containerfile(path, f string) {
	if !containerfileRe.MatchString(f) {
		v.add(path, "%q must be containerfiles/Containerfile.<name>", f)
	}
}

// key renders a map key as a TOML dotted-path segment.
func key(k string) string {
	if bareKeyRe.MatchString(k) {
		return k
	}
	return fmt.Sprintf("%q", k)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
