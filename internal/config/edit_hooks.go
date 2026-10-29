package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
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
	q := make([]string, len(cmds))
	for i, c := range cmds {
		q[i] = QuoteString(c)
	}
	value := "[" + strings.Join(q, ", ") + "]"
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
	q := make([]string, len(cmds))
	for i, c := range cmds {
		q[i] = QuoteString(c)
	}
	table := []string{"agents", name}
	s := d.find(table, "setup")
	switch {
	case s != nil && len(cmds) == 0:
		return d.removeStmt(s), nil
	case s != nil:
		return d.setValue(s, "["+strings.Join(q, ", ")+"]"), nil
	case len(cmds) == 0:
		return data, nil
	}
	return d.insertKey(table, "setup", "["+strings.Join(q, ", ")+"]"), nil
}
