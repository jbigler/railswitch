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
