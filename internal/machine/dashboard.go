package machine

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jbigler/railswitch/internal/tomlfile"
)

// The dashboard: its plain systemd user unit (not Quadlet: it runs on the
// host, as the user), the address it listens on and the one users open.
const (
	DashboardService = "railswitch-dashboard.service"
	DashboardAddr    = "127.0.0.1:7700"
	DashboardURL     = "http://railswitch.localhost:7700/"
)

// dashboardMarker is line 1 of the dashboard unit.
const dashboardMarker = "# railswitch: machine unit, rewritten by railswitch"

// DashboardUnit is the dashboard's unit running exe (absolute, symlinks
// resolved) as `dashboard serve`. Every app unit Wants= it; `railswitch
// dashboard` enables it (WantedBy=default.target), so it runs at login
// with no project started, after a reboot too.
// No NoNewPrivileges=: it would break rootless podman's setuid newuidmap in
// the commands the dashboard runs.
func DashboardUnit(exe string) []byte {
	return []byte(dashboardMarker + "\n" +
		"[Unit]\n" +
		"Description=RailSwitch dashboard\n" +
		"\n" +
		"[Service]\n" +
		"ExecStart=" + execPath(exe) + " dashboard serve\n" +
		"Restart=always\n" +
		"RestartSec=2\n" +
		"\n" +
		"[Install]\n" +
		"WantedBy=default.target\n")
}

// execPath is p as the first word of ExecStart=: % and $ doubled so
// systemd expands neither, double-quoted with C escapes when it holds a
// character systemd would split or unquote at.
func execPath(p string) string {
	p = strings.NewReplacer("%", "%%", "$", "$$").Replace(p)
	if !strings.ContainsAny(p, " \t\n\"'\\") {
		return p
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(p) + `"`
}

// WriteDashboardUnit writes DashboardUnit(exe) to d.SystemdUser (0644,
// atomically) when it differs from what is there; changed says it did.
func WriteDashboardUnit(d Dirs, exe string) (changed bool, err error) {
	path := filepath.Join(d.SystemdUser, DashboardService)
	data := DashboardUnit(exe)
	old, err := os.ReadFile(path)
	if err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(d.SystemdUser, 0o755); err != nil {
		return false, err
	}
	if err := tomlfile.WriteAtomic(path, data, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// DashboardToken is the dashboard's login token, from
// <d.Data>/dashboard/token: 32 random bytes, hex. When the file is missing
// it is created (0600, its directory 0700); a concurrent creator's token
// wins over ours, so every caller sees the same one.
func DashboardToken(d Dirs) (string, error) {
	dir := filepath.Join(d.Data, "dashboard")
	path := filepath.Join(dir, "token")
	tok, err := readToken(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return tok, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".token.*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // the link to token, if made, stays
	b := make([]byte, 32)
	rand.Read(b) // never fails (crypto/rand)
	err = tmp.Chmod(0o600)
	if err == nil {
		_, err = tmp.WriteString(hex.EncodeToString(b) + "\n")
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	// Link, unlike rename, never replaces a token another process made.
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	return readToken(path)
}

// readToken reads and checks a token file.
func readToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(data))
	if b, err := hex.DecodeString(tok); err != nil || len(b) != 32 {
		return "", fmt.Errorf("%s is not 64 hex characters: delete it and run railswitch dashboard for a new one", path)
	}
	return tok, nil
}
