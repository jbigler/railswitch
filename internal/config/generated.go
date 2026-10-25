package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/BurntSushi/toml"
)

// ReadGenerated returns the string values of [env] in the workspace-root
// mise.local.toml, where railswitch new writes the generated service values
// (D17). Other tables and non-string values are ignored. A missing file gives
// an empty map: render reports each missing name.
func ReadGenerated(path string) (map[string]string, error) {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Env map[string]any `toml:"env"`
	}
	if _, err := toml.Decode(string(data), &doc); err != nil {
		// The file also holds user secret values: name the line, never echo content.
		var perr toml.ParseError
		if errors.As(err, &perr) {
			return nil, fmt.Errorf("%s: invalid TOML at line %d", path, perr.Position.Line)
		}
		return nil, fmt.Errorf("%s: invalid TOML", path)
	}
	for k, v := range doc.Env {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out, nil
}
