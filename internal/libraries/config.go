// Package libraries holds the library repositories on a machine (D103):
// libraries.toml, the hardened fetch of each (D104) and its verified
// checkout under <data>/libraries/<alias>/.
package libraries

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"syscall"

	"github.com/BurntSushi/toml"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/tomlfile"
)

// File is libraries.toml's name in the config directory.
const File = "libraries.toml"

// Config is libraries.toml.
type Config struct {
	Schema    int        `toml:"schema"`
	Libraries []*Library `toml:"library,omitempty"`
}

// Library is one library repository. Branch "" is the remote's default;
// ID is the library id of the first verified index, "" until then.
type Library struct {
	Alias  string `toml:"alias"`
	URL    string `toml:"url"`
	Branch string `toml:"branch"`
	ID     string `toml:"id"`
}

var aliasRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)

// CheckAlias refuses an alias that can't prefix a ref.
func CheckAlias(alias string) error {
	if !aliasRe.MatchString(alias) || alias == "local" {
		return fmt.Errorf("alias %q must match %s and not be local", alias, aliasRe)
	}
	return nil
}

// Path is libraries.toml in d's config directory.
func Path(d machine.Dirs) string { return filepath.Join(d.Config, File) }

// Load reads libraries.toml; a missing file is no libraries.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{Schema: Schema}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := tomlfile.Decode(data, Schema, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var errs []error
	seen := map[string]bool{}
	for _, l := range c.Libraries {
		if err := l.check(); err != nil {
			errs = append(errs, err)
		}
		if seen[l.Alias] {
			errs = append(errs, fmt.Errorf("library %s is listed twice", l.Alias))
		}
		seen[l.Alias] = true
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func (l *Library) check() error {
	if err := CheckAlias(l.Alias); err != nil {
		return err
	}
	if err := sys.GitArg("url", l.URL); err != nil {
		return err
	}
	if l.Branch != "" {
		return sys.GitArg("branch", l.Branch)
	}
	return nil
}

// Save writes c to path atomically.
func (c *Config) Save(path string) error {
	c.Schema = Schema
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(c); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return tomlfile.WriteAtomic(path, b.Bytes(), 0o644)
}

// Library is alias's entry, or nil.
func (c *Config) Library(alias string) *Library {
	for _, l := range c.Libraries {
		if l.Alias == alias {
			return l
		}
	}
	return nil
}

// Add adds lib, whose alias must be new.
func (c *Config) Add(lib Library) error {
	if err := lib.check(); err != nil {
		return err
	}
	if c.Library(lib.Alias) != nil {
		return fmt.Errorf("library %s is already added", lib.Alias)
	}
	c.Libraries = append(c.Libraries, &lib)
	return nil
}

// Remove removes alias's entry.
func (c *Config) Remove(alias string) error {
	if c.Library(alias) == nil {
		return fmt.Errorf("no library %s", alias)
	}
	c.Libraries = slices.DeleteFunc(c.Libraries, func(l *Library) bool { return l.Alias == alias })
	return nil
}

// Update loads libraries.toml, lets fn change it and saves it when fn
// returns save, under an exclusive flock on its .lock file.
func Update(d machine.Dirs, fn func(*Config) (save bool, err error)) error {
	path := Path(d)
	unlock, err := flock(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	c, err := Load(path)
	if err != nil {
		return err
	}
	save, err := fn(c)
	if err != nil || !save {
		return err
	}
	return c.Save(path)
}

// Lock takes the alias's exclusive lock, which every change to its
// directory holds (fetch, promote, rm). Lock order: an alias, then
// signers.toml, then libraries.toml.
func Lock(d machine.Dirs, alias string) (unlock func(), err error) {
	return flock(filepath.Join(d.Data, "libraries", alias+".lock"))
}

func flock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() { f.Close() }, nil
}
