package render

import (
	"fmt"
	"regexp"
	"slices"
	"sort"

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
	Source  string `json:"source"` // "pack", "project" or, only in project show, "hidden"
	// Pack is the pack's command for this name, "" when the pack has none:
	// a project task with one overrides it.
	Pack string `json:"pack,omitempty"`
	// Fresh: fresh_tasks names it, so it runs in a new container, never
	// the running app (D171).
	Fresh bool `json:"fresh,omitempty"`
}

// stackLinks are the pack's links, then each chosen option's in
// config.Slots order, then each enabled tool's, each in file order.
func (m *Model) stackLinks() []pack.Link {
	st := m.Inputs.Stack
	links := slices.Clone(st.Pack.Links)
	for _, ch := range st.Options {
		links = append(links, ch.Option.Links...)
	}
	for _, t := range st.Tools {
		links = append(links, t.Links...)
	}
	return links
}

// checkFreshTasks fails on a fresh_tasks name no task that runs carries.
func (b *builder) checkFreshTasks() error {
	for _, name := range b.in.Project.FreshTasks {
		if !slices.ContainsFunc(b.m.Tasks(), func(t Task) bool { return t.Name == name }) {
			return fmt.Errorf("fresh_tasks: no task named %q", name)
		}
	}
	return nil
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

// Tasks are the tasks that run: MergeTasks without the hidden ones.
func (m *Model) Tasks() []Task {
	return slices.DeleteFunc(MergeTasks(m.Inputs.Stack.Pack.Tasks, m.Inputs.Project.Tasks, m.Inputs.Project.FreshTasks), func(t Task) bool { return t.Source == "hidden" })
}

// MergeTasks are the pack's [tasks] merged with the project's, sorted by
// name: a project task overrides the pack's, and a project task "" hides
// it (source "hidden", no command; D156). A project "" with no pack task
// is left out. A task fresh names is Fresh.
func MergeTasks(packTasks, projectTasks map[string]string, fresh []string) []Task {
	byName := map[string]Task{}
	for name, cmd := range packTasks {
		byName[name] = Task{Name: name, Command: cmd, Source: "pack", Pack: cmd}
	}
	for name, cmd := range projectTasks {
		t, inPack := byName[name]
		switch {
		case cmd != "":
			byName[name] = Task{Name: name, Command: cmd, Source: "project", Pack: t.Pack}
		case inPack:
			byName[name] = Task{Name: name, Source: "hidden", Pack: t.Pack}
		}
	}
	out := make([]Task, 0, len(byName))
	for _, t := range byName {
		t.Fresh = slices.Contains(fresh, t.Name)
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
