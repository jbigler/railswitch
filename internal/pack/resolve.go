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
}

// Chosen is the option a slot runs, and the service id it runs as.
type Chosen struct {
	Option  *Option
	Service string
}

// Resolve picks the project's pack, options and tools from the catalog. A
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
