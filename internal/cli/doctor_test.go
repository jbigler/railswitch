package cli

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

func TestDoctorJSON(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	mustRun(t, "-C", ws, "render")
	// The fake podman reports no version, so doctor fails.
	code, out, errs := runIn(t, "-C", ws, "--json", "doctor")
	if code != 1 {
		t.Fatalf("doctor --json: exit %d, want 1\nstdout: %s\nstderr: %s", code, out, errs)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("doctor --json output is not a JSON array: %v\n%s", err, out)
	}
	byName := map[string]map[string]any{}
	for _, r := range rows {
		var keys []string
		for k := range r {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if !slices.Equal(keys, []string{"detail", "fix", "name", "ok"}) {
			t.Errorf("row keys = %q", keys)
		}
		byName[r["name"].(string)] = r
	}
	if r := byName["podman >= 5.0"]; r == nil || r["ok"] != false {
		t.Errorf("podman row = %v", r)
	}
	if r := byName["units current"]; r == nil || r["ok"] != true {
		t.Errorf("units current row = %v", r)
	}
}

func TestDoctorText(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	f := fakeHost()
	f.On([]string{"podman", "version", "--format", "{{.Client.Version}}"}, sys.Result{Stdout: []byte("4.9.3\n")}, nil)
	code, out, _ := runWith(t, f, "-C", ws, "doctor")
	if code != 1 {
		t.Fatalf("doctor: exit %d, want 1\n%s", code, out)
	}
	for _, want := range []string{"FAIL  podman >= 5.0", "podman 4.9.3", "fix: install podman 5.0 or newer", "FAIL  units current", "fix: railswitch render"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output lacks %q:\n%s", want, out)
		}
	}
}

func TestDoctorArgs(t *testing.T) {
	if code, _, _ := runIn(t, "doctor", "extra"); code != 2 {
		t.Errorf("doctor extra: exit %d, want 2", code)
	}
}

func sudoCalls(f *systest.Fake) (calls [][]string, stdins []string) {
	for i, c := range f.Calls {
		if c[0] == "sudo" {
			calls = append(calls, c)
			stdins = append(stdins, string(f.Stdins[i]))
		}
	}
	return calls, stdins
}

func TestAllowPortsYes(t *testing.T) {
	f := &systest.Fake{}
	code, out, errs := runStdin(t, f, "y\n", "allow-ports")
	if code != 0 {
		t.Fatalf("allow-ports: exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	for _, want := range []string{
		"sudo sysctl -w net.ipv4.ip_unprivileged_port_start=80",
		"sudo tee /etc/sysctl.d/90-railswitch.conf",
		"[y/N]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("allow-ports output lacks %q:\n%s", want, out)
		}
	}
	calls, stdins := sudoCalls(f)
	want := [][]string{
		{"sudo", "sysctl", "-w", "net.ipv4.ip_unprivileged_port_start=80"},
		{"sudo", "tee", "/etc/sysctl.d/90-railswitch.conf"},
	}
	if !slices.EqualFunc(calls, want, slices.Equal) {
		t.Fatalf("sudo calls = %q, want %q", calls, want)
	}
	if stdins[1] != "net.ipv4.ip_unprivileged_port_start=80\n" {
		t.Errorf("tee stdin = %q", stdins[1])
	}
	if len(f.Calls) != 2 {
		t.Errorf("calls = %q, want only the two sudo calls", f.Calls)
	}
}

func TestAllowPortsDeclined(t *testing.T) {
	for _, in := range []string{"", "\n", "n\n", "nope\n"} {
		f := &systest.Fake{}
		code, out, _ := runStdin(t, f, in, "allow-ports")
		if code != 1 || len(f.Calls) != 0 {
			t.Errorf("allow-ports with %q: exit %d, calls %q\n%s", in, code, f.Calls, out)
		}
	}
}

func TestAllowPortsSysctlFails(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"sudo", "sysctl"}, sys.Result{ExitCode: 1}, errors.New("sudo: non-zero exit (exit 1)"))
	code, _, errs := runStdin(t, f, "yes\n", "allow-ports")
	if code != 1 || !strings.Contains(errs, "sudo") {
		t.Errorf("allow-ports with a failing sysctl: exit %d, stderr %q", code, errs)
	}
	if calls, _ := sudoCalls(f); len(calls) != 1 {
		t.Errorf("sudo calls after a failed sysctl = %q, want no tee", calls)
	}
}

func TestAllowPortsNoTimeout(t *testing.T) {
	f := &systest.Fake{}
	if code, out, errs := runStdin(t, f, "y\n", "allow-ports"); code != 0 {
		t.Fatalf("allow-ports: exit %d\n%s%s", code, out, errs)
	}
	if len(f.Cmds) != 2 {
		t.Fatalf("calls = %q", f.Calls)
	}
	// sudo may prompt for a password on the tty: no deadline.
	for i, c := range f.Cmds {
		if c.Timeout != sys.NoTimeout {
			t.Errorf("%q: Timeout %v, want sys.NoTimeout", f.Calls[i], c.Timeout)
		}
	}
}

// doctorRows runs `railswitch --json doctor` with args before it and decodes
// the rows.
func doctorRows(t *testing.T, args ...string) (int, []map[string]any) {
	t.Helper()
	code, out, errs := runIn(t, append(args, "--json", "doctor")...)
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("doctor --json: %v\nstdout: %s\nstderr: %s", err, out, errs)
	}
	return code, rows
}

func rowNames(rows []map[string]any) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r["name"].(string))
	}
	return out
}

func TestDoctorOutsideWorkspace(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	code, rows := doctorRows(t, "-C", tmp)
	names := rowNames(rows)
	if code != 1 || len(names) == 0 || names[0] != "podman >= 5.0" {
		t.Fatalf("doctor outside a workspace: exit %d, checks %q", code, names)
	}
	for _, n := range []string{"workspace", "firewall descriptor", "units current"} {
		if slices.Contains(names, n) {
			t.Errorf("doctor outside a workspace ran %q", n)
		}
	}
}

func TestDoctorWorkspaceLoadFails(t *testing.T) {
	ws, _ := filialWS(t) // no stack.lock
	code, rows := doctorRows(t, "-C", ws)
	names := rowNames(rows)
	if code != 1 || len(rows) == 0 || names[0] != "workspace" {
		t.Fatalf("doctor in a workspace without a lock: exit %d, checks %q", code, names)
	}
	if rows[0]["ok"] != false || rows[0]["fix"] != "railswitch lock" || !strings.Contains(rows[0]["detail"].(string), "railswitch lock") {
		t.Errorf("workspace row = %v", rows[0])
	}
	if !slices.Contains(names, "podman >= 5.0") || slices.Contains(names, "units current") {
		t.Errorf("checks = %q, want the host checks and no project checks", names)
	}
}
