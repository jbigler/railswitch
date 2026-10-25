package machine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

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
	return &c, nil
}
