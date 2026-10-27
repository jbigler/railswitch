package machine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/tomlfile"
)

// ConfigSchema is the config.toml schema this railswitch reads (D39).
const ConfigSchema = 1

// Config is the machine-wide <config dir>/config.toml (D37).
type Config struct {
	Schema  int           `toml:"schema"` // 1 (D39); a higher value is refused
	Secrets SecretsConfig `toml:"secrets"`
	Mounts  []Mount       `toml:"mount"` // [[mount]] (D57)
}

// Mount is a machine-local bind mount (D57): Source (absolute, ~/ expanded
// by LoadConfig) at Target in every unit and transient container of the
// service Service, in the projects Projects (prefixes; empty = every one).
type Mount struct {
	Service  string   `toml:"service"`
	Source   string   `toml:"source"`
	Target   string   `toml:"target"`
	RO       *bool    `toml:"ro"`       // nil = true
	Relabel  *bool    `toml:"relabel"`  // nil = true: SELinux relabel (z); false for a root-owned source
	Optional bool     `toml:"optional"` // a missing source skips the mount
	Projects []string `toml:"projects"`
}

// ReadOnly is ro, true when unset.
func (m Mount) ReadOnly() bool { return m.RO == nil || *m.RO }

// Relabels is relabel, true when unset.
func (m Mount) Relabels() bool { return m.Relabel == nil || *m.Relabel }

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
	for i := range c.Mounts {
		if err := c.Mounts[i].check(i); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return &c, nil
}

// check validates mount[i] and expands its source's leading ~/.
func (m *Mount) check(i int) error {
	field := func(f string) string { return fmt.Sprintf("mount[%d].%s", i, f) }
	if err := naming.ValidService(m.Service); err != nil {
		return fmt.Errorf("%s: %w", field("service"), err)
	}
	if rest, ok := strings.CutPrefix(m.Source, "~/"); ok {
		home := os.Getenv("HOME")
		if !filepath.IsAbs(home) {
			return fmt.Errorf("%s: %q starts with ~/ but HOME is not an absolute path", field("source"), m.Source)
		}
		m.Source = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(m.Source) {
		return fmt.Errorf("%s: %q is not an absolute path (or ~/…)", field("source"), m.Source)
	}
	if !filepath.IsAbs(m.Target) {
		return fmt.Errorf("%s: %q is not an absolute path", field("target"), m.Target)
	}
	if filepath.Clean(m.Target) == "/app" {
		return fmt.Errorf("%s: %q is the worktree's mount point", field("target"), m.Target)
	}
	for j, p := range m.Projects {
		if err := naming.ValidPrefix(p); err != nil {
			return fmt.Errorf("%s[%d]: %w", field("projects"), j, err)
		}
	}
	return nil
}

// pathOpts are the driver_opts that hold a path: a leading ~/ is the
// user's home, and the result must be absolute.
var pathOpts = []string{"root", "gpghomedir"}
