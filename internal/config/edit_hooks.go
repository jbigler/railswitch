package config

import (
	"errors"
	"fmt"
	"slices"
)

// HookLists are the [hooks] lists (D126), in the order they run.
var HookLists = []string{"deps", "start", "create"}

// SetHooks sets [hooks] <list> to cmds, one command each, in order; no
// cmds removes the key. A project.toml without [hooks] gets one. A
// command Validate would refuse is an error here.
func SetHooks(data, list string, cmds []string) (string, error) {
	if !slices.Contains(HookLists, list) {
		return "", fmt.Errorf("hooks list %q is not one of %v", list, HookLists)
	}
	v := &validator{}
	for i, c := range cmds {
		v.hook(fmt.Sprintf("hooks.%s[%d]", list, i), c)
	}
	if err := errors.Join(v.errs...); err != nil {
		return "", err
	}
	d, err := parseEdit(data, "hooks", false, false)
	if err != nil {
		return "", err
	}
	value := stringArray(cmds)
	s := d.find([]string{"hooks"}, list)
	switch {
	case s != nil && len(cmds) == 0:
		return d.removeStmt(s), nil
	case s != nil:
		return d.setValue(s, value), nil
	case len(cmds) == 0:
		return data, nil
	}
	return d.insertKey([]string{"hooks"}, list, value), nil
}

// SetAgentSetup sets agent name's [agents.<name>] setup to cmds (D133), the
// whole list; no commands removes the key. Each command is checked as a
// hook is.
func SetAgentSetup(data, name string, cmds []string) (string, error) {
	v := &validator{}
	for i, c := range cmds {
		v.hook(fmt.Sprintf("agents.%s.setup[%d]", name, i), c)
	}
	if err := errors.Join(v.errs...); err != nil {
		return "", err
	}
	p, err := Parse([]byte(data))
	if err != nil {
		return "", fmt.Errorf("project.toml: %w", err)
	}
	if _, ok := p.Agents[name]; !ok {
		return "", fmt.Errorf("no agent %s in project.toml", name)
	}
	d, err := parseDoc(data)
	if err != nil {
		return "", fmt.Errorf("project.toml: %w", err)
	}
	table := []string{"agents", name}
	if err := d.plainAgent(name); err != nil {
		return "", err
	}
	s := d.find(table, "setup")
	switch {
	case s != nil && len(cmds) == 0:
		return d.removeStmt(s), nil
	case s != nil:
		return d.setValue(s, stringArray(cmds)), nil
	case len(cmds) == 0:
		return data, nil
	}
	return d.insertKey(table, "setup", stringArray(cmds)), nil
}

// plainAgent is nil when agent name is a plain [agents.<name>] table with
// plain keys: one written inline (agents = { … }) or as dotted keys can't
// take an inserted key.
func (d *doc) plainAgent(name string) error {
	plain := false
	for _, s := range d.stmts {
		if len(s.path) < 2 || s.path[0] != "agents" || s.path[1] != name {
			continue
		}
		switch {
		case s.header && !s.array && len(s.path) == 2:
			plain = true
		case s.header:
		case len(s.table) > 2, len(s.table) == 2 && len(s.key) == 1:
			// a key of a sub-table ([agents.<n>.env]) or a plain key here
		default:
			return &BlockedError{d.line(s.start), "agents." + name + " is not a plain table with plain keys (an inline table or a dotted key)"}
		}
	}
	if !plain {
		return &BlockedError{1, "agents." + name + " has no [agents." + name + "] table (it is written inline or as dotted keys)"}
	}
	return nil
}

// SetDefaultAgent makes agent name the project's default (D215): default =
// true in its [agents.<name>] table, and the key taken out of every other
// agent's.
func SetDefaultAgent(data, name string) (string, error) {
	p, err := Parse([]byte(data))
	if err != nil {
		return "", fmt.Errorf("project.toml: %w", err)
	}
	if _, ok := p.Agents[name]; !ok {
		return "", fmt.Errorf("no agent %s in project.toml", name)
	}
	for _, other := range sortedKeys(p.Agents) {
		d, err := parseDoc(data)
		if err != nil {
			return "", fmt.Errorf("project.toml: %w", err)
		}
		if other != name && !p.Agents[other].Default {
			continue // nothing to change in it, however it is written
		}
		if err := d.plainAgent(other); err != nil {
			return "", err
		}
		s := d.find([]string{"agents", other}, "default")
		switch {
		case other == name && s != nil:
			data = d.setValue(s, "true")
		case other == name:
			data = d.insertKey([]string{"agents", other}, "default", "true")
		case s != nil:
			data = d.removeStmt(s)
		}
	}
	return data, nil
}
