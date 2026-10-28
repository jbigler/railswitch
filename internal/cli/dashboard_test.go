package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

// dashEnv points XDG_CONFIG_HOME and XDG_DATA_HOME into a temp dir.
func dashEnv(t *testing.T) machine.Dirs {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	d, err := machine.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestDashboardPrintsLogin: `railswitch dashboard` prints the login link
// with the token file's token, writes the dashboard's unit and, unless it
// is already enabled and running, enables and starts it.
func TestDashboardPrintsLogin(t *testing.T) {
	d := dashEnv(t)
	for _, state := range []string{"active enabled", "inactive disabled", "active disabled"} {
		active, file, _ := strings.Cut(state, " ")
		f := &systest.Fake{}
		f.On([]string{"systemctl", "--user", "show", "-p", "ActiveState,UnitFileState", "railswitch-dashboard.service"}, sys.Result{Stdout: []byte("ActiveState=" + active + "\nUnitFileState=" + file + "\n")}, nil)
		f.On([]string{"systemctl", "--user"}, sys.Result{}, nil)
		code, out, errs := runWith(t, f, "dashboard")
		if code != 0 {
			t.Fatalf("%s: exit %d\n%s%s", state, code, out, errs)
		}
		tok, err := os.ReadFile(filepath.Join(d.Data, "dashboard", "token"))
		if err != nil {
			t.Fatal(err)
		}
		want := "http://railswitch.localhost:7700/login?token=" + strings.TrimSpace(string(tok)) + "\n"
		enabled := slices.ContainsFunc(f.Calls, func(c []string) bool {
			return slices.Equal(c, []string{"systemctl", "--user", "enable", "--now", "railswitch-dashboard.service"})
		})
		if state != "active enabled" {
			want += "started the dashboard; it starts at every login from now on\n"
			if !enabled {
				t.Errorf("%s: not enabled: %q", state, f.Calls)
			}
		} else if enabled {
			t.Errorf("%s: enabled again", state)
		}
		if out != want {
			t.Errorf("%s: output %q; want %q", state, out, want)
		}
		unit, err := os.ReadFile(filepath.Join(d.SystemdUser, "railswitch-dashboard.service"))
		if err != nil || !strings.Contains(string(unit), "WantedBy=default.target") {
			t.Errorf("%s: unit %q %v", state, unit, err)
		}
	}
}

// TestDashboardLoginSystemctlFails: the link is printed even when
// systemctl fails; the failure goes to stderr.
func TestDashboardLoginSystemctlFails(t *testing.T) {
	dashEnv(t)
	f := &systest.Fake{}
	f.On([]string{"systemctl"}, sys.Result{ExitCode: 1}, fmt.Errorf("systemctl: %w (exit 1)", sys.ErrExit))
	code, out, errs := runWith(t, f, "dashboard")
	if code != 0 || !strings.HasPrefix(out, "http://railswitch.localhost:7700/login?token=") || strings.Contains(out, "started") || !strings.Contains(errs, "railswitch: dashboard") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errs)
	}
}

type colorsDoc struct {
	Light, Dark struct {
		Base      string            `json:"base"`
		Overrides map[string]string `json:"overrides"`
		Colors    map[string]string `json:"colors"`
	}
	Warnings struct {
		Light []string `json:"light"`
		Dark  []string `json:"dark"`
	} `json:"warnings"`
}

func colorsJSON(t *testing.T) colorsDoc {
	t.Helper()
	code, out, errs := run(t, "--json", "dashboard", "colors")
	if code != 0 {
		t.Fatalf("dashboard colors --json: exit %d\n%s%s", code, out, errs)
	}
	var doc colorsDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.Warnings.Light == nil || doc.Warnings.Dark == nil {
		t.Errorf("warnings not arrays:\n%s", out)
	}
	return doc
}

// TestColorsCLI: set, then --json shows the override and a warning for a
// low-contrast text; --dark --base changes the dark preset; a bad token is
// refused; reset drops the overrides and keeps the preset.
func TestColorsCLI(t *testing.T) {
	dashEnv(t)
	if doc := colorsJSON(t); doc.Light.Base != "flexoki" || len(doc.Light.Overrides) != 0 || doc.Light.Colors["bg"] != "#FFFCF0" || len(doc.Warnings.Light) != 0 {
		t.Fatalf("default: %+v", doc)
	}
	code, out, errs := run(t, "dashboard", "colors", "set", "text=#eeeeee", "accent=#BC5215")
	if code != 0 || out != "" || !strings.HasPrefix(errs, "railswitch: warning: light text: contrast ") || strings.Count(errs, "\n") != 1 {
		t.Errorf("set light: exit %d, stdout %q, stderr %q; want one light text warning", code, out, errs)
	}
	if code, _, errs := run(t, "dashboard", "colors", "set", "--dark", "--base", "paper"); code != 0 || errs != "" {
		t.Errorf("set dark: exit %d, stderr %q; want no warning", code, errs)
	}
	doc := colorsJSON(t)
	if doc.Light.Overrides["text"] != "#EEEEEE" || doc.Light.Colors["text"] != "#EEEEEE" || len(doc.Light.Overrides) != 1 {
		t.Errorf("light after set: %+v", doc.Light)
	}
	if len(doc.Warnings.Light) != 1 || !strings.HasPrefix(doc.Warnings.Light[0], "text: contrast") {
		t.Errorf("light warnings %q; want one for text", doc.Warnings.Light)
	}
	if doc.Dark.Base != "paper" || doc.Dark.Colors["bg"] != "#18181B" || len(doc.Warnings.Dark) != 0 {
		t.Errorf("dark after set: %+v %q", doc.Dark, doc.Warnings.Dark)
	}
	code, out, errs = run(t, "dashboard", "colors", "set", "sel=#000000", "nope=#000000")
	if code == 0 || !strings.Contains(errs, "sel is fixed") || !strings.Contains(errs, `unknown token "nope"`) {
		t.Errorf("bad tokens: exit %d\n%s%s", code, out, errs)
	}
	if code, _, errs := run(t, "dashboard", "colors", "set"); code != 2 {
		t.Errorf("set with nothing: exit %d %s", code, errs)
	}
	code, out, _ = run(t, "dashboard", "colors")
	if code != 0 || !strings.Contains(out, "warning: light text: contrast") || !slices.ContainsFunc(strings.Split(out, "\n"), func(l string) bool {
		return strings.HasPrefix(l, "light") && strings.Contains(l, "text") && strings.Contains(l, "#EEEEEE") && strings.HasSuffix(l, "set")
	}) {
		t.Errorf("text output: exit %d\n%s", code, out)
	}
	mustRun(t, "dashboard", "colors", "reset")
	mustRun(t, "dashboard", "colors", "reset", "--dark")
	doc = colorsJSON(t)
	if doc.Light.Base != "flexoki" || len(doc.Light.Overrides) != 0 || doc.Dark.Base != "paper" || len(doc.Warnings.Light) != 0 {
		t.Errorf("after reset: %+v", doc)
	}
}
