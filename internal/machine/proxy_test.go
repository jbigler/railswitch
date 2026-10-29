package machine_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/unithash"
)

var update = flag.Bool("update", false, "rewrite testdata/golden")

// twoProjects registers demo then api; api's subnet is moved below demo's so
// the unit must sort by subnet, not by registry order.
func twoProjects() *machine.Registry {
	return &machine.Registry{Schema: machine.SchemaVersion, Projects: []machine.ProjectEntry{
		{Prefix: "demo", Workspace: "/ws/demo", Subnet: 7},
		{Prefix: "api", Workspace: "/ws/api", Subnet: 3},
	}}
}

func TestProxyUnitsNoProjects(t *testing.T) {
	if units := machine.ProxyUnits(&machine.Registry{Schema: machine.SchemaVersion}); len(units) != 0 {
		t.Fatalf("zero projects rendered %v", units)
	}
}

func TestProxyUnitsLintClean(t *testing.T) {
	if fs := render.Lint(machine.ProxyUnits(twoProjects())); len(fs) != 0 {
		t.Fatalf("lint: %+v", fs)
	}
}

// TestProxyUnitHashLabel: Traefik's unit carries railswitch.unit-hash, the
// hash of the unit with that label line, its Network= lines and the
// [Quadlet] section NoNetworkWait appends excluded: turning the network
// wait off restarts no container.
func TestProxyUnitHashLabel(t *testing.T) {
	data := machine.ProxyUnits(twoProjects())[machine.ProxyUnitName]
	if !bytes.HasSuffix(data, []byte(machine.NoNetworkWaitSection)) {
		t.Fatalf("no [Quadlet] DefaultDependencies=false at the end:\n%s", data)
	}
	data = bytes.TrimSuffix(data, []byte(machine.NoNetworkWaitSection))
	key := unithash.Key("railswitch")
	h := unithash.Read(data, key)
	var rest []byte
	for line := range bytes.Lines(bytes.Replace(data, []byte("Label="+key+"="+h+"\n"), nil, 1)) {
		if !bytes.HasPrefix(line, []byte("Network=")) {
			rest = append(rest, line...)
		}
	}
	if h == "" || unithash.Sum(rest) != h {
		t.Errorf("unit-hash %q; want %q", h, unithash.Sum(rest))
	}
}

func TestRefreshProxyUnits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, machine.ProxyUnitName)
	both := twoProjects()
	write := func(data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func() string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	// Not on disk: left absent.
	if err := machine.RefreshProxyUnits(both, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("absent unit was written: %v", err)
	}

	// Names demo and api; api is unregistered: rewritten without it.
	write(machine.ProxyUnits(both)[machine.ProxyUnitName])
	demo := &machine.Registry{Schema: machine.SchemaVersion, Projects: both.Projects[:1]}
	if err := machine.RefreshProxyUnits(demo, dir); err != nil {
		t.Fatal(err)
	}
	if got, want := read(), string(machine.ProxyUnits(demo)[machine.ProxyUnitName]); got != want {
		t.Errorf("unit =\n%s\nwant\n%s", got, want)
	}

	// No project left: the railswitch-written unit goes.
	empty := &machine.Registry{Schema: machine.SchemaVersion}
	if err := machine.RefreshProxyUnits(empty, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("unit with no project left: %v; want removed", err)
	}

	// A file railswitch did not write is never removed.
	write([]byte("[Container]\n"))
	if err := machine.RefreshProxyUnits(empty, dir); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "[Container]\n" {
		t.Errorf("user file = %q", got)
	}
}

// TestProxyUnitsMachine: the machine-wide units are Traefik, the machine
// network and Dozzle; Traefik joins every project's proxy network and the
// machine network, publishes only :80 and routes its API by host.
func TestProxyUnitsMachine(t *testing.T) {
	units := machine.ProxyUnits(twoProjects())
	names := []string{machine.ProxyUnitName, machine.MachineNetworkUnitName, machine.DozzleUnitName}
	if len(units) != len(names) {
		t.Fatalf("units = %d, want %v", len(units), names)
	}
	for _, name := range names {
		got, ok := units[name]
		if !ok {
			t.Fatalf("no %s", name)
		}
		golden := filepath.Join("testdata", "golden", name)
		if *update {
			if err := os.WriteFile(golden, got, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("%s differs from its golden (-update):\n--- got\n%s\n--- want\n%s", name, got, want)
		}
	}
	if fs := render.Lint(map[string][]byte{machine.DozzleUnitName: units[machine.DozzleUnitName]}); len(fs) != 0 {
		t.Errorf("dozzle lint: %+v", fs)
	}
	tr := string(units[machine.ProxyUnitName])
	if n := strings.Count(tr, "\nNetwork="); n != 3 {
		t.Errorf("traefik has %d Network= lines, want 3:\n%s", n, tr)
	}
	for _, bad := range []string{"Requires=", "After=", "--api.insecure", "127.0.0.1:8080"} {
		if strings.Contains(tr, bad) {
			t.Errorf("traefik unit has %q:\n%s", bad, tr)
		}
	}
	for _, want := range []string{
		"Network=railswitch-machine.network:ip=10.214.255.2\n",
		"Label=traefik.enable=true\n",
		"Label=\"traefik.http.routers.railswitch-api.rule=Host(`traefik.localhost`) && (" + machine.HostClientClause() + ")\"\n",
		"Label=traefik.http.routers.railswitch-api.service=api@internal\n",
	} {
		if !strings.Contains(tr, want) {
			t.Errorf("traefik unit lacks %q", want)
		}
	}
	dz := string(units[machine.DozzleUnitName])
	if want := "Label=\"traefik.http.routers.railswitch-logs.rule=Host(`logs.localhost`) && (" + machine.HostClientClause() + ")\"\n"; !strings.Contains(dz, want) {
		t.Errorf("dozzle unit lacks %q:\n%s", want, dz)
	}
}

// TestRefreshProxyUnitsMachine: every machine-wide unit is refreshed, and
// all go with the last project.
func TestRefreshProxyUnitsMachine(t *testing.T) {
	dir := t.TempDir()
	one := &machine.Registry{Schema: machine.SchemaVersion, Projects: twoProjects().Projects[:1]}
	for name, data := range machine.ProxyUnits(twoProjects()) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := machine.RefreshProxyUnits(one, dir); err != nil {
		t.Fatal(err)
	}
	for name, want := range machine.ProxyUnits(one) {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != string(want) {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	if err := machine.RefreshProxyUnits(&machine.Registry{Schema: machine.SchemaVersion}, dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{machine.ProxyUnitName, machine.MachineNetworkUnitName, machine.DozzleUnitName} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s with no project left: %v; want removed", name, err)
		}
	}
}

// TestTraefikHashIgnoresNetworks: adding a project changes Traefik's unit
// (a Network= line) but not its unit-hash, so Traefik isn't restarted.
func TestTraefikHashIgnoresNetworks(t *testing.T) {
	key := unithash.Key("railswitch")
	both := machine.ProxyUnits(twoProjects())[machine.ProxyUnitName]
	one := machine.ProxyUnits(&machine.Registry{Schema: machine.SchemaVersion, Projects: twoProjects().Projects[:1]})[machine.ProxyUnitName]
	if bytes.Equal(both, one) {
		t.Fatal("units equal")
	}
	if a, b := unithash.Read(both, key), unithash.Read(one, key); a == "" || a != b {
		t.Errorf("unit-hash %q with two projects, %q with one", a, b)
	}
}

// TestDozzleUnitHashReadOnly (final M6): Dozzle's unit carries a unit-hash
// label, so up restarts it when its unit changes, and pins actions and
// shell off.
func TestDozzleUnitHashReadOnly(t *testing.T) {
	data := machine.ProxyUnits(twoProjects())[machine.DozzleUnitName]
	key := unithash.Key("railswitch")
	h := unithash.Read(data, key)
	if rest := bytes.TrimSuffix(bytes.Replace(data, []byte("Label="+key+"="+h+"\n"), nil, 1), []byte(machine.NoNetworkWaitSection)); h == "" || unithash.Sum(rest) != h {
		t.Errorf("dozzle unit-hash %q; want %q", h, unithash.Sum(rest))
	}
	for _, want := range []string{"\nEnvironment=DOZZLE_ENABLE_ACTIONS=false\n", "\nEnvironment=DOZZLE_ENABLE_SHELL=false\n"} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("dozzle unit lacks %q:\n%s", want[1:], data)
		}
	}
}

// TestAddNoNetworkWait: a railswitch unit without the [Quadlet] section
// gets it once; one with it, another program's unit and a non-unit file
// are left alone.
func TestAddNoNetworkWait(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"old-proxy.network": machine.Marker + "x (prefix old).\n\n[Network]\nNetworkName=old_proxy\n",
		"new-proxy.network": string(machine.NoNetworkWait([]byte(machine.Marker + "x (prefix new).\n\n[Network]\n"))),
		"mine.container":    "[Container]\nImage=x\n",
		"notes.txt":         machine.Marker + "x\n",
	}
	for n, s := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range [][]string{{"old-proxy.network"}, nil} {
		got, err := machine.AddNoNetworkWait(dir)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("run %d: %q, %v; want %q", i, got, err, want)
		}
	}
	for n, s := range files {
		data, _ := os.ReadFile(filepath.Join(dir, n))
		want := s
		if n == "old-proxy.network" {
			want = s + machine.NoNetworkWaitSection
		}
		if string(data) != want {
			t.Errorf("%s = %q; want %q", n, data, want)
		}
	}
	if got, err := machine.AddNoNetworkWait(filepath.Join(dir, "none")); got != nil || err != nil {
		t.Errorf("missing dir: %q, %v", got, err)
	}
}
