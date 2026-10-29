package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// HideLinksTOML is p's top-level hide_links line, "" when it hides none: a
// top-level key, written before the first table.
func HideLinksTOML(p *Project) string {
	if len(p.HideLinks) == 0 {
		return ""
	}
	return "hide_links = " + stringArray(p.HideLinks) + "\n"
}

// LinesTOML renders the tables of p that a template carries (D108) and
// project new writes, each only when set: [stack], [options], [versions],
// [env], [services.<id>], [tasks], [[links]], [[routes]], [tools.nvim],
// [agents.<name>], [firewall] and [hooks]. Not [project], [worktrees], [secrets],
// [share], [image] or [template]: each caller writes the ones it has.
func LinesTOML(p *Project) string {
	var b strings.Builder
	kv := func(k, v string) { fmt.Fprintf(&b, "%s = %s\n", key(k), QuoteString(v)) }
	table := func(header string, m map[string]string) {
		if len(m) == 0 {
			return
		}
		b.WriteString("\n" + header + "\n")
		for _, k := range slices.Sorted(maps.Keys(m)) {
			kv(k, m[k])
		}
	}
	b.WriteString("\n[stack]\n")
	kv("pack", p.Stack.Pack)
	kv("app", p.Stack.App)
	b.WriteString("\n[options]                  # one per slot; \"none\" turns a slot off; another key adds a service of that name\n")
	keys := slices.Clone(Slots)
	for _, k := range slices.Sorted(maps.Keys(p.Options)) {
		if !slices.Contains(Slots, k) {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		if v, ok := p.Options[k]; ok {
			kv(k, v)
		}
	}
	table("[versions]                 # the options' image pins", p.Versions)
	table("[env]", p.Env)
	for _, id := range slices.Sorted(maps.Keys(p.Services)) {
		s := p.Services[id]
		if s.Memory != "" || s.PidsLimit != 0 || s.Health != "" {
			fmt.Fprintf(&b, "\n[services.%s]\n", key(id))
			if s.Memory != "" {
				kv("memory", s.Memory)
			}
			if s.PidsLimit != 0 {
				fmt.Fprintf(&b, "pids_limit = %d\n", s.PidsLimit)
			}
			if s.Health != "" {
				kv("health", s.Health)
			}
		}
		table(fmt.Sprintf("[services.%s.env]", key(id)), s.Env)
	}
	table("[tasks]", p.Tasks)
	for _, l := range p.Links {
		b.WriteString("\n[[links]]\n")
		kv("label", l.Label)
		kv("url", l.URL)
	}
	for _, r := range p.Routes {
		b.WriteString("\n[[routes]]\n")
		kv("name", r.Name)
		kv("service", r.Service)
		fmt.Fprintf(&b, "port = %d\n", r.Port)
	}
	if n := p.Tools.Nvim; n != nil {
		b.WriteString("\n[tools.nvim]               # config_repo = \"<git url>\": cloned into the empty config volume (D54)\n")
		if n.ConfigRepo != "" {
			kv("config_repo", n.ConfigRepo)
		}
		if n.ConfigRef != "" {
			kv("config_ref", n.ConfigRef)
		}
	}
	for _, n := range slices.Sorted(maps.Keys(p.Agents)) {
		a := p.Agents[n]
		fmt.Fprintf(&b, "\n[agents.%s]\n", key(n))
		kv("use", a.Use)
		if a.Run != "" {
			kv("run", a.Run)
		}
		if len(a.Allow) > 0 {
			b.WriteString("allow = " + stringArray(a.Allow) + "\n")
		}
		table(fmt.Sprintf("[agents.%s.env]", key(n)), a.Env)
	}
	if f := p.Firewall; f.Mode != "" || len(f.Allow) > 0 {
		b.WriteString("\n[firewall]\n")
		if f.Mode != "" {
			kv("mode", f.Mode)
		}
		for _, prof := range slices.Sorted(maps.Keys(f.Allow)) {
			fmt.Fprintf(&b, "allow.%s = %s\n", key(prof), stringArray(f.Allow[prof]))
		}
	}
	if h := p.Hooks; len(h.Deps)+len(h.Start)+len(h.Create) > 0 {
		b.WriteString("\n[hooks]                    # commands run inside the containers (D126)\n")
		for _, l := range []struct {
			k string
			v []string
		}{{"deps", h.Deps}, {"start", h.Start}, {"create", h.Create}} {
			if len(l.v) > 0 {
				b.WriteString(l.k + " = " + stringArray(l.v) + "\n")
			}
		}
	}
	return b.String()
}

// SecretTOML is the body of a secret declaration: s's non-empty fields.
func SecretTOML(s Secret) string {
	var b strings.Builder
	if len(s.Services) > 0 {
		b.WriteString("services = " + stringArray(s.Services) + "\n")
	}
	for _, kv := range [][2]string{{"env", s.Env}, {"scope", s.Scope}, {"git_host", s.GitHost}, {"git_user", s.GitUser}} {
		if kv[1] != "" {
			b.WriteString(kv[0] + " = " + QuoteString(kv[1]) + "\n")
		}
	}
	if s.GitSigning {
		b.WriteString("git_signing = true\n")
	}
	return b.String()
}

// stringArray is s as a one-line TOML array of strings.
func stringArray(s []string) string {
	elems := make([]string, len(s))
	for i, v := range s {
		elems[i] = QuoteString(v)
	}
	return "[" + strings.Join(elems, ", ") + "]"
}
