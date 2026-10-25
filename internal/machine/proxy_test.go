package machine_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/render"
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

func TestProxyUnitsGolden(t *testing.T) {
	units := machine.ProxyUnits(twoProjects())
	if len(units) != 1 {
		t.Fatalf("units = %v, want only %s", len(units), machine.ProxyUnitName)
	}
	got, ok := units[machine.ProxyUnitName]
	if !ok {
		t.Fatalf("no %s in %v", machine.ProxyUnitName, units)
	}
	golden := filepath.Join("testdata", "golden", machine.ProxyUnitName)
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("%s differs from its golden (go test ./internal/machine -run TestProxyUnitsGolden -update):\n--- got\n%s\n--- want\n%s", machine.ProxyUnitName, got, want)
	}
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

func TestMarkerShared(t *testing.T) {
	if render.Marker != machine.Marker {
		t.Fatalf("render.Marker %q != machine.Marker %q", render.Marker, machine.Marker)
	}
}
