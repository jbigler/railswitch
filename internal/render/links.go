package render

import (
	"fmt"
	"regexp"
	"slices"
	"sort"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
)

// schemeRe is a URL's leading scheme (RFC 3986): only there does "://"
// mean the URL has one ({worktree.host}/login?return=https://x has not).
var schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// Link is one resolved worktree link.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Group string `json:"group"` // "app", "project", "stack"
}

// Task is one task runnable in the app image.
type Task struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Source  string `json:"source"` // "pack" or "project"
}

// stackLinks are the pack's links, then each chosen option's in
// config.Slots order, then each enabled tool's, each in file order.
func (m *Model) stackLinks() []pack.Link {
	st := m.Inputs.Stack
	links := slices.Clone(st.Pack.Links)
	for _, slot := range config.Slots {
		if ch := st.Options[slot]; ch != nil {
			links = append(links, ch.Option.Links...)
		}
	}
	for _, t := range st.Tools {
		links = append(links, t.Links...)
	}
	return links
}

// checkHideLinks fails on a hide_links label no stack link carries.
func (b *builder) checkHideLinks() error {
	have := map[string]bool{}
	for _, l := range b.m.stackLinks() {
		have[l.Label] = true
	}
	for _, label := range b.in.Project.HideLinks {
		if !have[label] {
			return fmt.Errorf("hide_links: no stack link labelled %q", label)
		}
	}
	return nil
}

// Links are in's links: the app, the project's [[links]], then the stack's
// minus hide_links. Placeholders are expanded; a URL without a scheme gets
// http://.
func (m *Model) Links(in *Instance) ([]Link, error) {
	sc := pack.MapScope(in.Values)
	out := []Link{{Label: "App", URL: "http://" + in.Values["worktree.host"] + "/", Group: "app"}}
	add := func(label, url, group string) error {
		u, err := pack.Expand(url, sc)
		if err != nil {
			return fmt.Errorf("link %q: %w", label, err)
		}
		if !schemeRe.MatchString(u) {
			u = "http://" + u
		}
		out = append(out, Link{Label: label, URL: u, Group: group})
		return nil
	}
	for _, l := range m.Inputs.Project.Links {
		if err := add(l.Label, l.URL, "project"); err != nil {
			return nil, err
		}
	}
	for _, l := range m.stackLinks() {
		if slices.Contains(m.Inputs.Project.HideLinks, l.Label) {
			continue
		}
		if err := add(l.Label, l.URL, "stack"); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Tasks are the pack's [tasks] merged with the project's, sorted by name: a
// project task overrides the pack's, and a project task "" removes it.
func (m *Model) Tasks() []Task {
	byName := map[string]Task{}
	for name, cmd := range m.Inputs.Stack.Pack.Tasks {
		byName[name] = Task{Name: name, Command: cmd, Source: "pack"}
	}
	for name, cmd := range m.Inputs.Project.Tasks {
		if cmd == "" {
			delete(byName, name)
			continue
		}
		byName[name] = Task{Name: name, Command: cmd, Source: "project"}
	}
	out := make([]Task, 0, len(byName))
	for _, t := range byName {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
