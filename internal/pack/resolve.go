package pack

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
)

// Stack is a project's resolved packages: its pack, the option chosen for
// each slot and its enabled tools.
type Stack struct {
	Pack    *Pack
	Options map[string]*Chosen // slot → chosen; "none" (or absent) slots are absent
	Tools   []*Tool
	Agents  []*ChosenAgent // sorted by Name
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

// Chosen is the option a slot runs, and the service id it runs as.
type Chosen struct {
	Option  *Option
	Service string
}

// agentBaseFile is the agent base image's Containerfile, which every agent
// package's image builds on.
const agentBaseFile = "Containerfile.agent-base"

// Resolve picks the project's pack, options, tools and agents from the catalog. A
// slot project.toml leaves out is "none". Every problem is returned at once
// (errors.Join).
func (c *Catalog) Resolve(p *config.Project) (*Stack, error) {
	ref, err := ParseRef(p.Stack.Pack)
	if err != nil {
		return nil, fmt.Errorf("stack.pack: %w", err)
	}
	pk := c.Packs[ref]
	if pk == nil {
		return nil, fmt.Errorf("stack.pack: unknown pack %s (available: %s)", ref, refList(slices.Collect(maps.Keys(c.Packs))))
	}

	s := &Stack{Pack: pk, Options: map[string]*Chosen{}}
	var errs []error
	for _, slot := range config.Slots {
		v := p.Options[slot]
		if v == "" || v == "none" {
			continue
		}
		r, err := ParseRef(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("options.%s: %w", slot, err))
			continue
		}
		o := c.Options[r]
		if o == nil || o.Slot != slot {
			errs = append(errs, fmt.Errorf("options.%s: unknown option %s (available for %s: %s)", slot, r, slot, c.slotRefs(slot)))
			continue
		}
		if !slices.Contains(o.WorksWith, "*") && !slices.Contains(o.WorksWith, pk.Name) {
			errs = append(errs, fmt.Errorf("options.%s: %s does not work with pack %s (works with: %s)", slot, r, pk.Name, strings.Join(o.WorksWith, ", ")))
			continue
		}
		s.Options[slot] = &Chosen{Option: o, Service: o.Service}
	}
	for _, slot := range config.Slots {
		ch := s.Options[slot]
		if ch == nil {
			continue
		}
		for _, need := range ch.Option.Needs {
			if s.Options[need] == nil {
				errs = append(errs, fmt.Errorf("options.%s: %s needs %s, but options.%s is none", slot, ch.Option.Name, need, need))
			}
		}
	}

	if p.Tools.Nvim {
		if t := c.Tools["nvim"]; t != nil {
			s.Tools = append(s.Tools, t)
		} else {
			errs = append(errs, errors.New("tools.nvim: the catalog has no nvim tool"))
		}
	}

	for _, name := range slices.Sorted(maps.Keys(p.Agents)) {
		pa := p.Agents[name]
		r, err := ParseAgentRef(pa.Use)
		if err != nil {
			errs = append(errs, fmt.Errorf("agents.%s.use: %w", name, err))
			continue
		}
		a := c.Agents[r]
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
	for _, slot := range config.Slots {
		if ch := s.Options[slot]; ch != nil {
			claim(ch.Service, fmt.Sprintf("options.%s (%s)", slot, ch.Option.Name))
		}
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
	claimFile(agentBaseFile, "the agent base image", agentBaseFile)
	for _, slot := range config.Slots {
		if ch := s.Options[slot]; ch != nil {
			o := ch.Option
			who := fmt.Sprintf("options.%s (%s)", slot, o.Name)
			pkg := "option " + Ref{Name: o.Name, Version: o.Version}.String()
			if o.AppAddon != nil {
				claimFile(o.AppAddon.File, who, pkg)
			}
			claimFile(o.Image.Build, who, pkg)
		}
	}
	for _, t := range s.Tools {
		claimFile(t.Image.File, "tools."+t.Name, "tool "+t.Name)
	}
	for _, a := range s.Agents {
		claimFile(a.Agent.Image.File, fmt.Sprintf("agents.%s (%s)", a.Name, a.Ref), "agent "+a.Ref.String())
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
		if r.Version == 0 {
			out[i] = r.Name
		} else {
			out[i] = r.String()
		}
	}
	slices.Sort(out)
	return strings.Join(out, ", ")
}
