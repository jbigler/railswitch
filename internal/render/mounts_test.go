package render

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMachineMountsRender(t *testing.T) {
	src := t.TempDir()
	gone := filepath.Join(src, "gone")
	in := fixtureInputs(t, fixtureWS(t), nil)
	in.Mounts = []MachineMount{
		{Service: "nvim", Source: src, Target: "/kitty-sock", Index: 0},
		{Service: "nvim", Source: gone, Target: "/gone", Optional: true, RO: true, Index: 1},
		{Service: "agent-claude", Source: src, Target: "/k", RO: true, Index: 2},
	}
	in.Host.Missing = map[string]bool{gone: true}
	m := mustBuild(t, in)
	units, err := Quadlet(m)
	if err != nil {
		t.Fatal(err)
	}
	line := "Volume=" + src + ":/kitty-sock:z\n"
	if u := string(units["filial-master-nvim.container"]); !strings.Contains(u, line) || strings.Contains(u, "/gone") {
		t.Errorf("nvim unit lacks %q or has the missing optional mount:\n%s", line, u)
	}
	if u := string(units["filial-master-rails.container"]); strings.Contains(u, src) {
		t.Errorf("rails unit has the nvim mount:\n%s", u)
	}
	s := service(t, m, "nvim")
	var origin string
	for _, mt := range s.Mounts {
		if mt.Target == "/kitty-sock" {
			origin = mt.Origin
		}
	}
	if origin != `[[mount]] #0 service = "nvim"` {
		t.Errorf("origin = %q", origin)
	}
	vols, err := mustInstance(t, m, "master").VolumeSpecs(service(t, m, "agent-claude"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(vols, "\n"), src+":/k:ro,z") {
		t.Errorf("agent-claude (on-demand transient) vols lack the mount: %v", vols)
	}
}
