package machine_test

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
)

func dashDirs(t *testing.T) machine.Dirs {
	t.Helper()
	d := t.TempDir()
	return machine.Dirs{Config: filepath.Join(d, "config/railswitch"), Data: filepath.Join(d, "data/railswitch"),
		Quadlet: filepath.Join(d, "config/containers/systemd"), SystemdUser: filepath.Join(d, "config/systemd/user")}
}

// TestDashboardUnit: the unit text, exactly as the spec's ini block.
func TestDashboardUnit(t *testing.T) {
	want := `# railswitch: machine unit, rewritten by railswitch
[Unit]
Description=RailSwitch dashboard

[Service]
ExecStart=/usr/bin/railswitch dashboard serve
Restart=always
RestartSec=2

[Install]
WantedBy=default.target
`
	if got := string(machine.DashboardUnit("/usr/bin/railswitch")); got != want {
		t.Errorf("DashboardUnit:\n%s\nwant:\n%s", got, want)
	}
}

// TestDashboardUnitEscapesPath: a path systemd would split or expand is
// quoted and escaped.
func TestDashboardUnitEscapesPath(t *testing.T) {
	for exe, want := range map[string]string{
		`/opt/my apps/railswitch`: `ExecStart="/opt/my apps/railswitch" dashboard serve`,
		`/opt/a%b$c/railswitch`:   `ExecStart=/opt/a%%b$$c/railswitch dashboard serve`,
		`/opt/a"b\c/railswitch`:   `ExecStart="/opt/a\"b\\c/railswitch" dashboard serve`,
	} {
		got := string(machine.DashboardUnit(exe))
		if !strings.Contains(got, "\n"+want+"\n") {
			t.Errorf("DashboardUnit(%q):\n%s\nwant line %s", exe, got, want)
		}
	}
}

// TestWriteDashboardUnit: the first write changes the file (0644 under
// SystemdUser), a second identical one doesn't, a new path does.
func TestWriteDashboardUnit(t *testing.T) {
	d := dashDirs(t)
	path := filepath.Join(d.SystemdUser, machine.DashboardService)
	for i, c := range []struct {
		exe     string
		changed bool
	}{{"/usr/bin/railswitch", true}, {"/usr/bin/railswitch", false}, {"/usr/local/bin/railswitch", true}} {
		changed, err := machine.WriteDashboardUnit(d, c.exe)
		if err != nil {
			t.Fatal(err)
		}
		if changed != c.changed {
			t.Errorf("write %d (%s): changed = %v; want %v", i, c.exe, changed, c.changed)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != string(machine.DashboardUnit(c.exe)) {
			t.Errorf("write %d: file\n%s", i, data)
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o644 {
			t.Errorf("write %d: mode %v; want 0644", i, fi.Mode().Perm())
		}
	}
}

// TestDashboardToken: created 0600 in a 0700 dir, 64 hex chars, stable.
func TestDashboardToken(t *testing.T) {
	d := dashDirs(t)
	tok, err := machine.DashboardToken(d)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := hex.DecodeString(tok); err != nil || len(tok) != 64 || len(b) != 32 {
		t.Fatalf("token %q: want 64 hex chars", tok)
	}
	path := filepath.Join(d.Data, "dashboard", "token")
	for p, want := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v; want %v", p, fi.Mode().Perm(), want)
		}
	}
	again, err := machine.DashboardToken(d)
	if err != nil || again != tok {
		t.Errorf("second call = %q, %v; want %q", again, err, tok)
	}
	if ents, err := os.ReadDir(filepath.Dir(path)); err != nil || len(ents) != 1 || ents[0].Name() != "token" {
		t.Errorf("token dir holds %v (%v); want only token", ents, err)
	}
	if err := os.WriteFile(path, []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := machine.DashboardToken(d); err == nil {
		t.Error("a malformed token file was accepted")
	}
}
