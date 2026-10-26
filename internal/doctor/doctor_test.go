package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
	"github.com/jbigler/railswitch/internal/testfixture"
	"github.com/jbigler/railswitch/internal/workspace"
)

const graphRoot = "/home/u/.local/share/containers/storage"

var (
	podmanVersion = []string{"podman", "version", "--format", "{{.Client.Version}}"}
	socketActive  = []string{"systemctl", "--user", "is-active", "podman.socket"}
	networkLs     = []string{"podman", "network", "ls", "--format", "json"}
)

// fakeHost answers podman info (render's graph root) and nothing else.
func fakeHost() *systest.Fake {
	f := &systest.Fake{}
	f.On([]string{"podman", "info", "--format", "{{.Store.GraphRoot}}"}, sys.Result{Stdout: []byte(graphRoot + "\n")}, nil)
	return f
}

// host is a Host whose proc/sys files live in a temp dir: the port start
// file holds portStart, and the quadlet generator and cgroup.controllers
// exist when present. LookPath finds every binary in found.
func host(t *testing.T, portStart string, present bool, found ...string) Host {
	t.Helper()
	dir := t.TempDir()
	h := Host{
		PortStart: filepath.Join(dir, "ip_unprivileged_port_start"),
		Quadlet:   []string{filepath.Join(dir, "libexec-quadlet"), filepath.Join(dir, "lib-quadlet")},
		Cgroup:    filepath.Join(dir, "cgroup.controllers"),
		LookPath: func(name string) (string, error) {
			if slices.Contains(found, name) {
				return "/usr/bin/" + name, nil
			}
			return "", exec.ErrNotFound
		},
	}
	if err := os.WriteFile(h.PortStart, []byte(portStart+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if present {
		for _, p := range []string{h.Quadlet[0], h.Cgroup} {
			if err := os.WriteFile(p, []byte("cpu memory pids\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	return h
}

// workspaceAt is the Filial fixture, locked and registered, with the
// machine dirs in a temp dir; rendered when render is set.
func workspaceAt(t *testing.T, render bool) *workspace.Workspace {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	root := testfixture.Workspace(t, "../../testdata/fixtures/filial")
	dirs, err := machine.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.AddProject("filial", root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.AddWorktree("filial", "master", nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	reg.Close()
	ctx := context.Background()
	if err := workspace.Lock(ctx, root); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Load(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if render {
		if _, err := ws.Render(ctx, fakeHost(), false); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

// writeConfig writes the machine config.toml.
func writeConfig(t *testing.T, dirs machine.Dirs, body string) {
	t.Helper()
	if err := os.MkdirAll(dirs.Config, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.Config, "config.toml"), []byte("schema = 1\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func byName(cs []Check) map[string]Check {
	m := map[string]Check{}
	for _, c := range cs {
		m[c.Name] = c
	}
	return m
}

func names(cs []Check) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

var hostChecks = []string{
	"podman >= 5.0", "quadlet generator", "podman.socket active", "unprivileged port 80",
	"nft", "nsenter", "getent", "jq", "curl", "machine subnet free",
}

func TestAllOK(t *testing.T) {
	ws := workspaceAt(t, true)
	writeConfig(t, ws.Dirs, "[secrets]\ndriver = \"pass\"\ndriver_opts = { key = \"ABCD1234\" }\n")
	f := fakeHost()
	f.On(podmanVersion, sys.Result{Stdout: []byte("5.2.1\n")}, nil)
	f.On(socketActive, sys.Result{Stdout: []byte("active\n")}, nil)
	f.On([]string{"gpg", "--list-secret-keys", "ABCD1234"}, sys.Result{Stdout: []byte("sec rsa4096\n")}, nil)
	h := host(t, "80", true, "nft", "nsenter", "getent", "jq", "curl", "pass", "gpg")

	got := h.Run(context.Background(), f, ws)
	want := append(slices.Clone(hostChecks), "firewall descriptor", "units current", "proxy subnet", "cgroup v2", "pass", "gpg", "gpg secret key")
	if !slices.Equal(names(got), want) {
		t.Fatalf("checks = %q\nwant %q", names(got), want)
	}
	for _, c := range got {
		if !c.OK {
			t.Errorf("%s failed: %s (fix: %s)", c.Name, c.Detail, c.Fix)
		}
		if c.OK && c.Fix != "" {
			t.Errorf("%s passed with fix text %q", c.Name, c.Fix)
		}
	}
	if d := byName(got)["podman >= 5.0"].Detail; !strings.Contains(d, "5.2.1") {
		t.Errorf("podman detail = %q, want the version", d)
	}
}

func TestAllFail(t *testing.T) {
	ws := workspaceAt(t, false)
	writeConfig(t, ws.Dirs, "[secrets]\ndriver = \"pass\"\ndriver_opts = { key = \"ABCD1234\" }\n")
	f := fakeHost()
	f.On(podmanVersion, sys.Result{Stdout: []byte("4.9.3\n")}, nil)
	f.On(socketActive, sys.Result{ExitCode: 3, Stdout: []byte("inactive\n")}, errors.New("systemctl: non-zero exit (exit 3)"))
	f.On([]string{"gpg", "--list-secret-keys", "ABCD1234"}, sys.Result{ExitCode: 2}, errors.New("gpg: non-zero exit (exit 2)"))
	f.On(networkLs, sys.Result{Stdout: []byte(`[{"name":"old_proxy","subnets":[{"subnet":"10.214.0.0/24"}]},{"name":"other","subnets":[{"subnet":"10.214.255.0/24"}]}]`)}, nil)
	h := host(t, "1024", false)

	got := h.Run(context.Background(), f, ws)
	if len(got) != len(hostChecks)+7 {
		t.Fatalf("checks = %q", names(got))
	}
	for _, c := range got {
		if c.OK {
			t.Errorf("%s passed: %s", c.Name, c.Detail)
		}
		if c.Fix == "" {
			t.Errorf("%s: no fix text", c.Name)
		}
	}
	m := byName(got)
	if fix := m["unprivileged port 80"].Fix; fix != "railswitch allow-ports" {
		t.Errorf("port 80 fix = %q", fix)
	}
	if d := m["unprivileged port 80"].Detail; !strings.Contains(d, "1024") {
		t.Errorf("port 80 detail = %q, want the current value", d)
	}
	if d := m["podman >= 5.0"].Detail; !strings.Contains(d, "4.9.3") {
		t.Errorf("podman detail = %q", d)
	}
	if fix := m["gpg secret key"].Fix; !strings.Contains(fix, "driver_opts.key") {
		t.Errorf("gpg key fix = %q, want it to name driver_opts.key", fix)
	}
	if fix := m["units current"].Fix; fix != "railswitch render" {
		t.Errorf("units fix = %q", fix)
	}
}

func TestPodmanErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		out string
		err error
	}{
		"not installed": {"", exec.ErrNotFound},
		"garbage":       {"podman\n", nil},
		"empty":         {"", nil},
	} {
		t.Run(name, func(t *testing.T) {
			f := &systest.Fake{}
			f.On(podmanVersion, sys.Result{Stdout: []byte(tc.out)}, tc.err)
			c := host(t, "80", true).podman(context.Background(), f)
			if c.OK || c.Fix == "" {
				t.Errorf("podman check = %+v, want a failure with a fix", c)
			}
		})
	}
}

func TestPortStartBelow80(t *testing.T) {
	c := host(t, "0", true).port()
	if !c.OK {
		t.Errorf("port start 0: %+v", c)
	}
	h := host(t, "80", true)
	h.PortStart = filepath.Join(t.TempDir(), "missing")
	if c := h.port(); c.OK || c.Fix != "railswitch allow-ports" {
		t.Errorf("unreadable port start: %+v", c)
	}
}

func TestNoMachineConfigNoPassChecks(t *testing.T) {
	ws := workspaceAt(t, true)
	f := fakeHost()
	got := host(t, "80", true).Run(context.Background(), f, ws)
	for _, n := range []string{"pass", "gpg", "gpg secret key"} {
		if _, ok := byName(got)[n]; ok {
			t.Errorf("check %q ran without a machine config", n)
		}
	}
	for _, c := range f.Calls {
		if c[0] == "gpg" || c[0] == "pass" {
			t.Errorf("ran %q", c)
		}
	}
}

func TestFileDriverNoPassChecks(t *testing.T) {
	ws := workspaceAt(t, true)
	writeConfig(t, ws.Dirs, "[secrets]\ndriver = \"file\"\n")
	got := host(t, "80", true).Run(context.Background(), fakeHost(), ws)
	if _, ok := byName(got)["gpg secret key"]; ok {
		t.Errorf("gpg key check ran with the file driver: %q", names(got))
	}
}

func TestPassWithoutKey(t *testing.T) {
	ws := workspaceAt(t, true)
	writeConfig(t, ws.Dirs, "[secrets]\ndriver = \"pass\"\n")
	f := fakeHost()
	got := byName(host(t, "80", true, "pass", "gpg").Run(context.Background(), f, ws))
	c := got["gpg secret key"]
	if c.OK || !strings.Contains(c.Fix, "driver_opts.key") {
		t.Errorf("gpg key check without a key = %+v", c)
	}
	for _, call := range f.Calls {
		if call[0] == "gpg" {
			t.Errorf("ran %q without a key", call)
		}
	}
}

func TestPassGPGHomedir(t *testing.T) {
	ws := workspaceAt(t, true)
	writeConfig(t, ws.Dirs, "[secrets]\ndriver = \"pass\"\ndriver_opts = { key = \"ABCD1234\", gpghomedir = \"/srv/gnupg\" }\n")
	f := fakeHost()
	f.On([]string{"gpg", "--homedir", "/srv/gnupg", "--list-secret-keys", "ABCD1234"}, sys.Result{}, nil)
	f.On([]string{"gpg"}, sys.Result{ExitCode: 2}, errors.New("gpg: non-zero exit (exit 2)"))
	if c := byName(host(t, "80", true, "pass", "gpg").Run(context.Background(), f, ws))["gpg secret key"]; !c.OK {
		t.Errorf("gpg key check with gpghomedir = %+v", c)
	}
}

func TestBadMachineConfig(t *testing.T) {
	ws := workspaceAt(t, true)
	writeConfig(t, ws.Dirs, "schema = 2\n")
	c, ok := byName(host(t, "80", true).Run(context.Background(), fakeHost(), ws))["machine config"]
	if !ok || c.OK || !strings.Contains(c.Detail, "config.toml") {
		t.Errorf("machine config check = %+v (present %v)", c, ok)
	}
}

func TestNoWorkspace(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	got := host(t, "80", true).Run(context.Background(), &systest.Fake{}, nil)
	want := append(slices.Clone(hostChecks), "cgroup v2")
	if !slices.Equal(names(got), want) {
		t.Errorf("checks outside a workspace = %q\nwant %q", names(got), want)
	}
}

func TestUnitsStale(t *testing.T) {
	ws := workspaceAt(t, true)
	if err := os.Remove(filepath.Join(ws.Dirs.Quadlet, "filial-master-rails.container")); err != nil {
		t.Fatal(err)
	}
	m := byName(host(t, "80", true).Run(context.Background(), fakeHost(), ws))
	if c := m["units current"]; c.OK || !strings.Contains(c.Detail, "filial-master-rails.container") {
		t.Errorf("units current with a missing unit = %+v", c)
	}
	if c := m["firewall descriptor"]; !c.OK {
		t.Errorf("firewall descriptor = %+v", c)
	}
}

func TestQuadletArchPath(t *testing.T) {
	h := host(t, "80", false)
	if c := h.quadlet(); c.OK || !strings.Contains(c.Fix, h.Quadlet[0]) || !strings.Contains(c.Fix, h.Quadlet[1]) {
		t.Errorf("quadlet check with neither path = %+v, want a failure whose fix names both", c)
	}
	if err := os.WriteFile(h.Quadlet[1], nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if c := h.quadlet(); !c.OK || c.Detail != h.Quadlet[1] {
		t.Errorf("quadlet check with only the Arch path = %+v", c)
	}
}

func TestDefaultQuadletPaths(t *testing.T) {
	want := []string{"/usr/libexec/podman/quadlet", "/usr/lib/podman/quadlet"}
	if !slices.Equal(DefaultHost.Quadlet, want) {
		t.Errorf("DefaultHost.Quadlet = %q, want %q", DefaultHost.Quadlet, want)
	}
}

func TestUnitsStaleLockFix(t *testing.T) {
	ws := workspaceAt(t, true)
	ws.Lock.Options = nil
	c := byName(host(t, "80", true).Run(context.Background(), fakeHost(), ws))["units current"]
	if c.OK || c.Fix != "railswitch lock" || !strings.Contains(c.Detail, "stack.lock is out of date") {
		t.Errorf("units current with a stale lock = %+v", c)
	}
}

func TestWorkspaceCheck(t *testing.T) {
	if c := Workspace(fmt.Errorf("%w: run railswitch lock", config.ErrNoLock)); c.OK || c.Name != "workspace" || c.Fix != "railswitch lock" {
		t.Errorf("Workspace(no lock) = %+v", c)
	}
	if c := Workspace(errors.New("project.toml: bad")); c.OK || c.Fix == "" || c.Detail != "project.toml: bad" {
		t.Errorf("Workspace(bad project) = %+v", c)
	}
}

func TestProxySubnet(t *testing.T) {
	ws := workspaceAt(t, true)
	f := fakeHost()
	f.On(networkLs, sys.Result{Stdout: []byte(`[{"name":"filial_proxy","subnets":[{"subnet":"10.214.0.0/24"}]},{"name":"podman","subnets":[{"subnet":"10.88.0.0/16"}]}]`)}, nil)
	if c := proxySubnet(context.Background(), f, ws); !c.OK || !strings.Contains(c.Detail, "10.214.0.0/24") {
		t.Errorf("no clash: %+v", c)
	}
	f = fakeHost()
	f.On(networkLs, sys.Result{Stdout: []byte(`[{"name":"old_proxy","subnets":[{"subnet":"10.214.0.0/24"}]}]`)}, nil)
	c := proxySubnet(context.Background(), f, ws)
	if c.OK || !strings.Contains(c.Detail, "10.214.0.0/24") || !strings.Contains(c.Detail, "old_proxy") || !strings.Contains(c.Fix, "railswitch up") || !strings.Contains(c.Fix, "filial_proxy") {
		t.Errorf("clash: %+v", c)
	}
	f = fakeHost()
	f.On(networkLs, sys.Result{ExitCode: 125}, errors.New("podman: non-zero exit (exit 125)"))
	if c := proxySubnet(context.Background(), f, ws); c.OK || !strings.Contains(c.Detail, "exit 125") {
		t.Errorf("ls fails: %+v", c)
	}
}

func TestMachineSubnet(t *testing.T) {
	f := fakeHost()
	f.On(networkLs, sys.Result{Stdout: []byte(`[{"name":"railswitch_machine","subnets":[{"subnet":"10.214.255.0/24"}]}]`)}, nil)
	if c := machineSubnet(context.Background(), f); !c.OK || c.Name != "machine subnet free" {
		t.Errorf("own network: %+v", c)
	}
	f = fakeHost()
	f.On(networkLs, sys.Result{Stdout: []byte(`[{"name":"other","subnets":[{"subnet":"10.214.255.0/24"}]}]`)}, nil)
	if c := machineSubnet(context.Background(), f); c.OK || !strings.Contains(c.Detail, "other") || !strings.Contains(c.Detail, "10.214.255.0/24") {
		t.Errorf("taken: %+v", c)
	}
}
