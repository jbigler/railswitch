// Package machine holds the machine-wide state shared by every project: its
// directories and the registry of projects, subnets and worktree IDs.
package machine

import (
	"errors"
	"os"
	"path/filepath"
)

// Dirs are railswitch's machine directories.
type Dirs struct {
	Config  string // ~/.config/railswitch
	Data    string // ~/.local/share/railswitch
	Quadlet string // ~/.config/containers/systemd
}

// DefaultDirs reads XDG_CONFIG_HOME and XDG_DATA_HOME, falling back to $HOME.
// A relative XDG value is ignored, as the XDG spec requires.
func DefaultDirs() (Dirs, error) {
	home := os.Getenv("HOME")
	xdg := func(key, fallback string) (string, error) {
		if v := os.Getenv(key); filepath.IsAbs(v) {
			return v, nil
		}
		if !filepath.IsAbs(home) {
			return "", errors.New("HOME is not set to an absolute path and " + key + " is unset")
		}
		return filepath.Join(home, fallback), nil
	}
	cfg, err := xdg("XDG_CONFIG_HOME", ".config")
	if err != nil {
		return Dirs{}, err
	}
	data, err := xdg("XDG_DATA_HOME", ".local/share")
	if err != nil {
		return Dirs{}, err
	}
	return Dirs{
		Config:  filepath.Join(cfg, "railswitch"),
		Data:    filepath.Join(data, "railswitch"),
		Quadlet: filepath.Join(cfg, "containers", "systemd"),
	}, nil
}
