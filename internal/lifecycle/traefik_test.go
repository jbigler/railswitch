package lifecycle

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
	"github.com/jbigler/railswitch/internal/workspace"
)

const traefikNetworksFormat = `{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}`

// traefikRunning makes railswitch-traefik run, attached to attached.
func traefikRunning(f *systest.Fake, attached string) {
	f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", traefikContainer}, sys.Result{Stdout: []byte("true\n")}, nil)
	f.On([]string{"podman", "container", "inspect", "--format", traefikNetworksFormat, traefikContainer}, sys.Result{Stdout: []byte(attached + "\n")}, nil)
}

func TestAttachTraefik(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	dirs, err := machine.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	subnet := map[string]int{}
	for _, p := range []string{"a", "b", "c"} {
		pe, err := reg.AddProject(p, "/ws/"+p, nil)
		if err != nil {
			t.Fatal(err)
		}
		subnet[p] = pe.Subnet
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	reg.Close()

	f := &systest.Fake{}
	traefikRunning(f, "a_proxy gone_proxy railswitch_machine ")
	f.On([]string{"podman", "network", "ls", "--format", "json"}, sys.Result{Stdout: []byte(`[
{"name":"a_proxy","subnets":[{"subnet":"` + machine.Subnet(subnet["a"]) + `"}]},
{"name":"b_proxy","subnets":[{"subnet":"` + machine.Subnet(subnet["b"]) + `"}]},
{"name":"gone_proxy","subnets":[{"subnet":"10.214.9.0/24"}]}]`)}, nil)
	l := &L{R: f, WS: &workspace.Workspace{Dirs: dirs}, Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
	if err := l.AttachTraefik(ctx); err != nil {
		t.Fatal(err)
	}
	var changes []string
	for _, c := range f.Calls {
		j := joined(c)
		if strings.HasPrefix(j, "podman network connect") || strings.HasPrefix(j, "podman network disconnect") {
			changes = append(changes, j)
		}
		if strings.Contains(j, "c_proxy") || strings.Contains(j, "restart") {
			t.Errorf("call %q", j)
		}
	}
	want := []string{
		"podman network connect --ip " + machine.TraefikIP(subnet["b"]) + " b_proxy railswitch-traefik",
		"podman network disconnect gone_proxy railswitch-traefik",
	}
	if !slices.Equal(changes, want) {
		t.Errorf("changes = %q\nwant %q", changes, want)
	}
}

// TestAttachTraefikNotRunning: with Traefik down nothing is attached; its
// next start joins every network its unit names.
func TestAttachTraefikNotRunning(t *testing.T) {
	e := setup(t)
	if err := e.l.AttachTraefik(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(e.calls(), "podman network"); n != 0 {
		t.Errorf("calls:\n%s", dump(e.calls()))
	}
}

// TestUpNoTraefikRestartOnNewProject (Review Focus 5): another project is
// registered while Traefik runs. Only Traefik's Network= lines change, so
// it is not restarted: it joins the network live, before the app starts.
func TestUpNoTraefikRestartOnNewProject(t *testing.T) {
	e := setup(t)
	upTwice(t, e)
	e.units(loadedClean)
	e.labels(t)
	reg, err := machine.Open(e.l.WS.Dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	other, err := reg.AddProject("other", "/ws/other", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	reg.Close()
	e.networks(`[{"name":"filial_proxy","subnets":[{"subnet":"10.214.0.0/24"}]},{"name":"other_proxy","subnets":[{"subnet":"` + machine.Subnet(other.Subnet) + `"}]},{"name":"railswitch_machine","subnets":[{"subnet":"10.214.255.0/24"}]}]`)
	traefikRunning(e.f, "filial_proxy railswitch_machine ")
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	calls := e.calls()
	if n := count(calls, "systemctl --user restart"); n != 0 {
		t.Fatalf("restarted:\n%s", dump(calls))
	}
	wantInOrder(t, calls,
		"systemctl --user daemon-reload",
		"systemctl --user start filial-proxy-network.service",
		"podman network connect --ip "+machine.TraefikIP(other.Subnet)+" other_proxy railswitch-traefik",
		"systemctl --user start --job-mode=ignore-requirements filial-feat-rails.service")
	if n := count(calls, "podman network disconnect"); n != 0 {
		t.Errorf("disconnected:\n%s", dump(calls))
	}
}

// TestUpRefusesMachineSubnetTaken: another network on 10.214.255.0/24 stops
// up before anything starts, naming it.
func TestUpRefusesMachineSubnetTaken(t *testing.T) {
	e := setup(t)
	e.networks(`[{"name":"other","subnets":[{"subnet":"10.214.255.0/24"}]}]`)
	err := e.l.Up(ctx, "feat")
	if err == nil || !strings.Contains(err.Error(), "other") || !strings.Contains(err.Error(), "10.214.255.0/24") {
		t.Fatalf("err = %v; want it to name network other", err)
	}
	if n := count(e.calls(), "systemctl --user start"); n != 0 {
		t.Errorf("started:\n%s", dump(e.calls()))
	}
}

// TestAttachTraefikAlreadyConnected (final M5): another `up` connecting
// Traefik first is no failure.
func TestAttachTraefikAlreadyConnected(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	dirs, err := machine.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	pe, err := reg.AddProject("a", "/ws/a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	reg.Close()
	f := &systest.Fake{}
	traefikRunning(f, "railswitch_machine ")
	f.On([]string{"podman", "network", "ls", "--format", "json"}, sys.Result{Stdout: []byte(`[{"name":"a_proxy","subnets":[{"subnet":"` + machine.Subnet(pe.Subnet) + `"}]}]`)}, nil)
	const stderr = "Error: network is already connected"
	f.On([]string{"podman", "network", "connect"}, sys.Result{ExitCode: 125, Stderr: []byte(stderr)}, fmt.Errorf("podman: %w (exit 125): %s", sys.ErrExit, stderr))
	l := &L{R: f, WS: &workspace.Workspace{Dirs: dirs}, Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
	if err := l.AttachTraefik(ctx); err != nil {
		t.Fatalf("AttachTraefik: %v", err)
	}
}
