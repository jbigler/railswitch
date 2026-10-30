package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
)

// Stack is a project's resolved packages: its pack, its chosen options and
// its enabled tools.
type Stack struct {
	Pack    *Pack
	PackRef Ref       // the ref [stack] pack names (Local for a workspace pack)
	Options []*Chosen // the [options] in OptionKeys order; "none" ones are absent
	Tools   []*Tool
	Agents  []*ChosenAgent // sorted by Name
	// AgentBase is the agent base image's files (Catalog.AgentBase) when
	// the stack has agents; nil otherwise.
	AgentBase fs.FS
}

// ChosenAgent is one [agents.<name>] of the project, resolved.
type ChosenAgent struct {
	Name    string            // the project's agent name
	Service string            // its service id: agent-<name>
	Run     string            // "on-demand" or "service"
	Agent   *Agent            // its package
	Ref     Ref               // its package's ref
	Env     map[string]string // the project's [agents.<name>] env
	Allow   []string          // package allow + project allow, deduplicated in order
}

// Chosen is the option an [options] key runs, its ref and the service id it
// runs as. A slot's key (database, cache, …) runs the option's own service
// id; any other key adds a service of that name.
type Chosen struct {
	Key     string // the [options] key: a slot, or an extra service's id
	Option  *Option
	Ref     Ref
	Service string
}

// Option is the option chosen for slot's key, or nil.
func (s *Stack) Option(slot string) *Chosen {
	for _, ch := range s.Options {
		if ch.Key == slot {
			return ch
		}
	}
	return nil
}

// OptionKeys are the [options] keys in their order: the slots in
// config.Slots order, then the other keys by name.
func OptionKeys(options map[string]string) []string {
	keys := slices.Clone(config.Slots)
	for _, k := range slices.Sorted(maps.Keys(options)) {
		if !slices.Contains(config.Slots, k) {
			keys = append(keys, k)
		}
	}
	return keys
}

// Resolve picks the project's pack, options, tools and agents from the catalog. A
// slot project.toml leaves out is "none". Every problem is returned at once
// (errors.Join).
func (c *Catalog) Resolve(p *config.Project) (*Stack, error) {
	ref, err := ParseRef(p.Stack.Pack)
	if err != nil {
		return nil, fmt.Errorf("stack.pack: %w", err)
	}
	pk, err := c.Pack(ref)
	if err != nil {
		return nil, fmt.Errorf("stack.pack: %w", err)
	}
	if pk == nil {
		return nil, fmt.Errorf("stack.pack: unknown pack %s (available: %s)", ref, refList(slices.Collect(maps.Keys(c.Packs))))
	}

	s := &Stack{Pack: pk, PackRef: ref}
	var errs []error
	keyOf := map[string]string{} // option name → the key it fills
	for _, key := range OptionKeys(p.Options) {
		v := p.Options[key]
		if v == "" || v == "none" {
			continue
		}
		slot := slices.Contains(config.Slots, key)
		r, err := ParseRef(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("options.%s: %w", key, err))
			continue
		}
		o, err := c.Option(r)
		if err != nil {
			errs = append(errs, fmt.Errorf("options.%s: %w", key, err))
			continue
		}
		switch {
		case o == nil && !slot:
			errs = append(errs, fmt.Errorf("options.%s: unknown option %s", key, r))
			continue
		case o == nil || slot && o.Slot != key:
			errs = append(errs, fmt.Errorf("options.%s: unknown option %s (available for %s: %s)", key, r, key, c.slotRefs(key)))
			continue
		}
		if !slices.Contains(o.WorksWith, "*") && !slices.Contains(o.WorksWith, pk.Name) {
			errs = append(errs, fmt.Errorf("options.%s: %s does not work with pack %s (works with: %s)", key, r, pk.Name, strings.Join(o.WorksWith, ", ")))
			continue
		}
		// One key per option: a second copy would share its volume, port
		// and env names with the first.
		if other, ok := keyOf[o.Name]; ok {
			errs = append(errs, fmt.Errorf("options.%s: %s already fills options.%s; an option runs once per project", key, r, other))
			continue
		}
		keyOf[o.Name] = key
		svc := o.Service
		if !slot {
			svc = key
		}
		s.Options = append(s.Options, &Chosen{Key: key, Option: o, Ref: r, Service: svc})
	}
	// Two options giving one env name would leave the app one of them,
	// picked by the [options] order.
	gives := map[string]string{}
	for _, ch := range s.Options {
		o := ch.Option
		for _, table := range []map[string]string{o.Gives, o.DepsEnv, o.AgentGives, o.Share.Gives} {
			for _, k := range slices.Sorted(maps.Keys(table)) {
				who := fmt.Sprintf("options.%s (%s)", ch.Key, o.Name)
				if prev, ok := gives[k]; ok && prev != who {
					errs = append(errs, fmt.Errorf("env %s is given by %s and %s: drop one of them", k, prev, who))
				}
				gives[k] = who
			}
		}
	}
	for _, ch := range s.Options {
		for _, need := range ch.Option.Needs {
			if s.Option(need) == nil {
				errs = append(errs, fmt.Errorf("options.%s: %s needs %s, but options.%s is none", ch.Key, ch.Option.Name, need, need))
			}
		}
	}

	if p.Tools.Nvim != nil {
		if t := c.Tools["nvim"]; t != nil {
			s.Tools = append(s.Tools, t)
		} else {
			errs = append(errs, errors.New("tools.nvim: the catalog has no nvim tool"))
		}
	}

	for _, name := range slices.Sorted(maps.Keys(p.Agents)) {
		pa := p.Agents[name]
		r, err := ParseRef(pa.Use)
		if err != nil {
			errs = append(errs, fmt.Errorf("agents.%s.use: %w", name, err))
			continue
		}
		a, err := c.Agent(r)
		if err != nil {
			errs = append(errs, fmt.Errorf("agents.%s.use: %w", name, err))
			continue
		}
		if a == nil {
			errs = append(errs, fmt.Errorf("agents.%s.use: unknown agent package %s (available: %s)", name, r, refList(slices.Collect(maps.Keys(c.Agents)))))
			continue
		}
		run := pa.Run
		if run == "" {
			run = a.Run
		}
		if run == "" {
			run = "on-demand"
		}
		var allow []string
		for _, h := range slices.Concat(a.Allow, pa.Allow) {
			if !slices.Contains(allow, h) {
				allow = append(allow, h)
			}
		}
		s.Agents = append(s.Agents, &ChosenAgent{Name: name, Service: "agent-" + name, Run: run, Agent: a, Ref: r, Env: pa.Env, Allow: allow})
	}

	if len(s.Agents) > 0 {
		s.AgentBase = c.AgentBase
	}

	// Service ids name containers, units and DNS aliases: one owner each.
	app := p.Stack.App
	if app == "" {
		app = pk.App.Service
	}
	owner := map[string]string{}
	claim := func(id, who string) {
		if prev, ok := owner[id]; ok {
			errs = append(errs, fmt.Errorf("duplicate service id %s: %s and %s", id, prev, who))
			return
		}
		owner[id] = who
	}
	claim(app, "the app ([stack] app)")
	claim("deps", "the deps service")
	for _, ch := range s.Options {
		claim(ch.Service, fmt.Sprintf("options.%s (%s)", ch.Key, ch.Option.Name))
	}
	for _, t := range s.Tools {
		claim(t.Service, "tools."+t.Name)
	}
	for _, a := range s.Agents {
		claim(a.Service, "agents."+a.Name)
	}

	// Containerfile names are one namespace: ejected side by side under
	// containerfiles/. pkg identifies the package that owns a file, so two
	// agents of one package share theirs.
	type cfOwner struct{ who, pkg string }
	files := map[string]cfOwner{}
	claimFile := func(file, who, pkg string) {
		if file == "" {
			return
		}
		if prev, ok := files[file]; ok {
			if prev.pkg != pkg {
				errs = append(errs, fmt.Errorf("duplicate Containerfile %s: %s and %s", file, prev.who, who))
			}
			return
		}
		files[file] = cfOwner{who, pkg}
	}
	claimFile(pk.Image.File, "pack "+pk.Name, "pack "+ref.String())
	claimFile(AgentBaseFile, "the agent base image", AgentBaseFile)
	for _, ch := range s.Options {
		o := ch.Option
		who := fmt.Sprintf("options.%s (%s)", ch.Key, o.Name)
		pkg := "option " + ch.Ref.String()
		if o.AppAddon != nil {
			claimFile(o.AppAddon.File, who, pkg)
		}
		claimFile(o.Image.Build, who, pkg)
	}
	for _, t := range s.Tools {
		claimFile(t.Image.File, "tools."+t.Name, "tool "+t.Name)
	}
	for _, a := range s.Agents {
		claimFile(a.Agent.Image.File, fmt.Sprintf("agents.%s (%s)", a.Name, a.Ref), "agent "+a.Ref.String())
	}

	// Host port = base + worktree ID and the registry keys ports by name, so
	// each base and each name belongs to one package of the project. The
	// built-ins are checked against each other at load; a local package is
	// only checked here.
	baseOwner, nameOwner := map[int]string{}, map[string]string{}
	claimPorts := func(who string, ports []Port) {
		for _, pt := range ports {
			if prev, ok := baseOwner[pt.Base]; ok && prev != who {
				errs = append(errs, fmt.Errorf("ports: base %d is used by %s and %s", pt.Base, prev, who))
			} else {
				baseOwner[pt.Base] = who
			}
			if prev, ok := nameOwner[pt.Name]; ok && prev != who {
				errs = append(errs, fmt.Errorf("ports: name %q is used by %s and %s", pt.Name, prev, who))
			} else {
				nameOwner[pt.Name] = who
			}
		}
	}
	claimPorts("pack "+ref.String(), pk.Ports)
	for _, ch := range s.Options {
		claimPorts("option "+ch.Ref.String(), ch.Option.Ports)
	}
	for _, t := range s.Tools {
		claimPorts("tool "+t.Name, t.Ports)
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return s, nil
}

// slotRefs lists the refs that can fill slot, plus "none".
func (c *Catalog) slotRefs(slot string) string {
	var refs []Ref
	for r, o := range c.Options {
		if o.Slot == slot {
			refs = append(refs, r)
		}
	}
	return refList(append(refs, Ref{Name: "none"}))
}

func refList(refs []Ref) string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.String()
	}
	slices.Sort(out)
	return strings.Join(out, ", ")
}
