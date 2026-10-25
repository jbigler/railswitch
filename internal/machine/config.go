package machine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jbigler/railswitch/internal/tomlfile"
)

// ConfigSchema is the config.toml schema this railswitch reads (D39).
const ConfigSchema = 1

// Config is the machine-wide <config dir>/config.toml (D37).
type Config struct {
	Schema  int           `toml:"schema"` // 1 (D39); a higher value is refused
	Secrets SecretsConfig `toml:"secrets"`
}

// SecretsConfig picks the podman secret driver for every value railswitch
// stores.
type SecretsConfig struct {
	Driver     string            `toml:"driver"` // "" = podman's default (file)
	DriverOpts map[string]string `toml:"driver_opts"`
}

// LoadConfig reads <d.Config>/config.toml strictly. A missing file is a zero
// Config.
func LoadConfig(d Dirs) (*Config, error) {
	path := filepath.Join(d.Config, "config.toml")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := tomlfile.Decode(data, ConfigSchema, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.Schema != ConfigSchema {
		return nil, fmt.Errorf("%s: schema: %d is not %d; config.toml starts with `schema = %d`", path, c.Schema, ConfigSchema, ConfigSchema)
	}
	for _, k := range pathOpts {
		v, ok := c.Secrets.DriverOpts[k]
		if !ok {
			continue
		}
		if rest, ok := strings.CutPrefix(v, "~/"); ok {
			home := os.Getenv("HOME")
			if !filepath.IsAbs(home) {
				return nil, fmt.Errorf("%s: secrets.driver_opts.%s: %q starts with ~/ but HOME is not an absolute path", path, k, v)
			}
			v = filepath.Join(home, rest)
		}
		if !filepath.IsAbs(v) {
			return nil, fmt.Errorf("%s: secrets.driver_opts.%s: %q is not an absolute path (or ~/…)", path, k, v)
		}
		c.Secrets.DriverOpts[k] = v
	}
	return &c, nil
}

// pathOpts are the driver_opts that hold a path: a leading ~/ is the
// user's home, and the result must be absolute.
var pathOpts = []string{"root", "gpghomedir"}
