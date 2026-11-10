package machine

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
	LAN     []LAN         `toml:"lan"`   // [[lan]] (D196)
}

// LAN is a machine-local exception to the firewall's floor (D196): the
// service Service, in the projects Projects (prefixes; empty = every one),
// may reach Host, "<name or IPv4 address>[:<port>]", on its LAN address and
// that TCP port (443 when none is given). LoadConfig adds the port.
type LAN struct {
	Service  string   `toml:"service"`
	Host     string   `toml:"host"`
	Projects []string `toml:"projects"`
}

// lanHostRe is a host name: labels of letters, digits and inner hyphens.
var lanHostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// lanPrivate are the floor's ranges a LAN exception may open; 0.0.0.0/8
// and 169.254.0.0/16 (cloud metadata) it never does.
var lanPrivate = []string{"10.0.0.0/8", "100.64.0.0/10", "172.16.0.0/12", "192.168.0.0/16"}

// check validates lan[i] and gives its host a port.
func (l *LAN) check(i int) error {
	field := func(f string) string { return fmt.Sprintf("lan[%d].%s", i, f) }
	if err := naming.ValidService(l.Service); err != nil {
		return fmt.Errorf("%s: %w", field("service"), err)
	}
	host, port := l.Host, "443"
	if h, p, ok := strings.Cut(l.Host, ":"); ok {
		host, port = h, p
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return fmt.Errorf("%s: %q: the port is not 1 to 65535", field("host"), l.Host)
	}
	if ip := net.ParseIP(host).To4(); ip != nil {
		if !slices.ContainsFunc(lanPrivate, func(c string) bool { _, n, _ := net.ParseCIDR(c); return n.Contains(ip) }) {
			return fmt.Errorf("%s: %q is not a LAN address (10/8, 100.64/10, 172.16/12, 192.168/16); a public one goes in an allow list", field("host"), host)
		}
	} else if len(host) > 253 || !lanHostRe.MatchString(host) {
		return fmt.Errorf("%s: %q is not a host name or IPv4 address", field("host"), host)
	}
	l.Host = host + ":" + port
	for j, p := range l.Projects {
		if err := naming.ValidPrefix(p); err != nil {
			return fmt.Errorf("%s[%d]: %w", field("projects"), j, err)
		}
	}
	return nil
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
	for i := range c.LAN {
		if err := c.LAN[i].check(i); err != nil {
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
