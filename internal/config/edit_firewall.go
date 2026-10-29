package config

import (
	"fmt"
	"slices"
)

// FirewallProfiles are the [firewall] allow lists the project edits: the
// app and its workers (D119), deps installs, every agent.
var FirewallProfiles = []string{"app", "deps", "agent"}

// parseFirewall splits data and checks that [firewall] is the shape the
// firewall edits work on: one [firewall] table holding mode = "…" and
// allow.<profile> = [ … ] keys (as railswitch writes it). It returns the
// statement of allow.<profile> (nil: none, or prof "").
func parseFirewall(data, prof string) (*doc, *stmt, error) {
	d, err := parseDoc(data)
	if err != nil {
		return nil, nil, fmt.Errorf("project.toml: %w", err)
	}
	var allow *stmt
	for i, s := range d.stmts {
		if len(s.path) == 0 || s.path[0] != "firewall" {
			continue
		}
		switch {
		case s.header && len(s.path) == 1 && !s.array:
		case !s.header && slices.Equal(s.table, []string{"firewall"}) && slices.Equal(s.key, []string{"mode"}):
		case !s.header && slices.Equal(s.table, []string{"firewall"}) && len(s.key) == 2 && s.key[0] == "allow":
			if s.key[1] == prof {
				allow = &d.stmts[i]
			}
		default:
			return nil, nil, &BlockedError{d.line(s.start), "firewall is not one [firewall] table with mode and allow.<profile> = [...] keys"}
		}
	}
	if allow != nil && d.data[allow.value[0]] != '[' {
		return nil, nil, &BlockedError{d.line(allow.start), "firewall.allow." + prof + " is not an array"}
	}
	return d, allow, nil
}

// SetFirewallMode sets [firewall] mode; "" removes it (the default: enforce
// with agents, else log).
func SetFirewallMode(data, mode string) (string, error) {
	switch mode {
	case "", "log", "enforce", "off":
	default:
		return "", fmt.Errorf("firewall mode %q must be log, enforce or off", mode)
	}
	d, _, err := parseFirewall(data, "")
	if err != nil {
		return "", err
	}
	s := d.find([]string{"firewall"}, "mode")
	switch {
	case s != nil && mode == "":
		return d.removeStmt(s), nil
	case s != nil:
		return d.setValue(s, QuoteString(mode)), nil
	case mode == "":
		return data, nil
	}
	return d.insertKey([]string{"firewall"}, "mode", QuoteString(mode)), nil
}

// AddFirewallAllow appends host to [firewall] allow.<prof>; a host already
// there is an error.
func AddFirewallAllow(data, prof, host string) (string, error) {
	if !slices.Contains(FirewallProfiles, prof) {
		return "", fmt.Errorf("firewall profile %q is not one of %v", prof, FirewallProfiles)
	}
	d, s, err := parseFirewall(data, prof)
	if err != nil {
		return "", err
	}
	elem := QuoteString(host)
	if s == nil {
		return d.insertKey([]string{"firewall"}, "allow."+prof, "["+elem+"]"), nil
	}
	elems, err := d.arrayElems(s)
	if err != nil {
		return "", err
	}
	if slices.ContainsFunc(elems, func(e arrayElem) bool { return e.value == host }) {
		return "", fmt.Errorf("project.toml: %s is already in firewall.allow.%s", host, prof)
	}
	return d.appendToArray(s, elem)
}

// RemoveFirewallAllow removes host from [firewall] allow.<prof>; a host not
// there is an error.
func RemoveFirewallAllow(data, prof, host string) (string, error) {
	d, s, err := parseFirewall(data, prof)
	if err != nil {
		return "", err
	}
	if s == nil {
		return "", fmt.Errorf("project.toml: %s is not in firewall.allow.%s", host, prof)
	}
	return d.removeArrayElem(s, host)
}
