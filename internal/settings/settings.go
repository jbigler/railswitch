// Package settings stages edits of a project's project.toml (D84) and judges
// what each does to the project's data and running worktrees (D83) by
// rendering the current and the edited project and comparing them.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/jbigler/railswitch/internal/config"
)

// Edit is one staged change. Op says which fields it takes (ops).
type Edit struct {
	Op    string `json:"op"`
	Key   string `json:"key,omitempty"`
	Ref   string `json:"ref,omitempty"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
	Label string `json:"label,omitempty"`
	URL   string `json:"url,omitempty"`
}

// opSpec is what an op takes: required fields must be non-empty, optional
// ones may be, any other must be absent.
type opSpec struct{ required, optional []string }

var ops = map[string]opSpec{
	"option": {required: []string{"key", "ref"}},
	"pack":   {required: []string{"ref"}},
	// The firewall (D119): mode "" is the default; key is a profile of
	// config.FirewallProfiles, value a host.
	"firewall.mode":         {optional: []string{"value"}},
	"firewall.allow.add":    {required: []string{"key", "value"}},
	"firewall.allow.remove": {required: []string{"key", "value"}},
	"env.set":               {required: []string{"name"}, optional: []string{"value"}},
	"env.unset":             {required: []string{"name"}},
	"link.add":              {required: []string{"label", "url"}},
	"link.remove":           {required: []string{"label"}},
	"link.hide":             {required: []string{"label"}},
	"link.show":             {required: []string{"label"}},
}

func (e Edit) fields() map[string]string {
	return map[string]string{"key": e.Key, "ref": e.Ref, "name": e.Name, "value": e.Value, "label": e.Label, "url": e.URL}
}

// ParseEdits reads one JSON array of edits, strictly: an unknown field or
// op, a missing field or one the op does not take is an error.
func ParseEdits(r io.Reader) ([]Edit, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var edits []Edit
	if err := dec.Decode(&edits); err != nil {
		return nil, fmt.Errorf("edits: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("edits: want one JSON array")
	}
	for i, e := range edits {
		spec, ok := ops[e.Op]
		if !ok {
			return nil, fmt.Errorf("edit %d: unknown op %q", i, e.Op)
		}
		f := e.fields()
		for _, name := range []string{"key", "ref", "name", "value", "label", "url"} {
			switch {
			case slices.Contains(spec.required, name):
				if f[name] == "" {
					return nil, fmt.Errorf("edit %d: %s needs %s", i, e.Op, name)
				}
			case f[name] != "" && !slices.Contains(spec.optional, name):
				return nil, fmt.Errorf("edit %d: %s takes no %s", i, e.Op, name)
			}
		}
	}
	return edits, nil
}

// ApplyText is data with e applied by the line editor; the result is not
// validated.
func ApplyText(data string, e Edit) (string, error) {
	switch e.Op {
	case "option":
		return config.SetOption(data, e.Key, e.Ref, slices.Contains(config.Slots, e.Key))
	case "pack":
		return config.SetPack(data, e.Ref)
	case "firewall.mode":
		return config.SetFirewallMode(data, e.Value)
	case "firewall.allow.add":
		return config.AddFirewallAllow(data, e.Key, e.Value)
	case "firewall.allow.remove":
		return config.RemoveFirewallAllow(data, e.Key, e.Value)
	case "env.set":
		return config.SetEnv(data, e.Name, e.Value)
	case "env.unset":
		return config.UnsetEnv(data, e.Name)
	case "link.add":
		return config.AddLink(data, e.Label, e.URL)
	case "link.remove":
		return config.RemoveLink(data, e.Label)
	case "link.hide":
		return config.HideLink(data, e.Label)
	case "link.show":
		return config.ShowLink(data, e.Label)
	}
	return "", fmt.Errorf("unknown op %q", e.Op)
}
