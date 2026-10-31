package render

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
)

func withMemory(p *config.Project) {
	p.Memory = &config.SharedMemory{Guidance: "No patient data, ever."}
}

func TestMemoryUnits(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, withMemory)
	hasLine(t, units, "filial-memory.network", "NetworkName=filial_memory")
	hasLine(t, units, "filial-memory.network", "Internal=true")
	hasLine(t, units, "filial-memory.network", "Options=isolate=strict")
	for _, l := range []string{
		"Image=" + MemoryImage,
		"ContainerName=filial_memory",
		"Network=filial-memory.network:alias=memory",
		"Volume=filial_memory:/var/lib/dolt",
		"Environment=DOLT_ROOT_HOST=%%",
		"Entrypoint=tini",
		"Exec=-- dolt sql-server --host=0.0.0.0 --port=3306",
		"DropCapability=all",
		"NoNewPrivileges=true",
		"Notify=healthy",
		"LogDriver=journald",
	} {
		hasLine(t, units, "filial-memory.container", l)
	}
	noLinePrefix(t, units, "filial-memory.container", "PublishPort=")
	if first := unitLines(t, units, "filial-memory.container")[0]; !strings.Contains(first, "(prefix filial)") {
		t.Errorf("memory unit marker %q: want a project-wide marker", first)
	}

	// A service agent joins the memory network; the firewall hook allows
	// its own networks, so the annotation lists it. No other service does.
	hasLine(t, units, "filial-feat-agent-probe.container", "Network=filial-memory.network")
	if !slices.ContainsFunc(unitLines(t, units, "filial-feat-agent-probe.container"), func(l string) bool {
		return strings.HasPrefix(l, "Annotation=") && strings.Contains(l, "filial.firewall.networks=filial-feat-dev,filial_proxy,filial_memory")
	}) {
		t.Errorf("agent-probe firewall annotation lacks filial_memory:\n%s", units["filial-feat-agent-probe.container"])
	}
	for name, data := range units {
		if strings.HasSuffix(name, ".container") && name != "filial-memory.container" && name != "filial-feat-agent-probe.container" && name != "filial-master-agent-probe.container" &&
			strings.Contains(string(data), "Network=filial-memory.network") {
			t.Errorf("%s joins the memory network", name)
		}
	}

}

func TestMemoryOff(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, nil)
	for name, data := range units {
		if strings.Contains(name, "memory") || strings.Contains(string(data), "memory.network") {
			t.Errorf("no [memory]: %s rendered or names the memory network", name)
		}
	}
	m, in := filial(t, nil)
	if _, ok := envVar(in, "agent-claude", "BEADS_DIR"); ok {
		t.Error("no [memory]: agent-claude has BEADS_DIR")
	}
	if slices.ContainsFunc(m.Assets, func(a Asset) bool { return strings.HasPrefix(a.Rel, MemoryDir) }) {
		t.Error("no [memory]: memory assets")
	}
}

func TestMemoryAgent(t *testing.T) {
	m, in := filial(t, withMemory)
	c := service(t, m, "agent-claude")
	if !slices.Equal(c.Networks, []string{"dev", "proxy", "memory"}) {
		t.Errorf("agent-claude networks %v", c.Networks)
	}
	if got := in.Networks(c); !slices.Equal(got, []string{"filial-master-dev", "filial_proxy", "filial_memory"}) {
		t.Errorf("Networks = %v", got)
	}
	if r := service(t, m, "rails"); slices.Contains(r.Networks, "memory") {
		t.Errorf("rails networks %v", r.Networks)
	}
	for name, want := range map[string]string{
		"BEADS_DIR":                  MemoryBeadsDir,
		"BEADS_DOLT_SERVER_MODE":     "1",
		"BEADS_DOLT_SERVER_HOST":     "memory",
		"BEADS_DOLT_SERVER_PORT":     "3306",
		"BEADS_DOLT_SERVER_DATABASE": "beads",
		"BEADS_ACTOR":                "master/claude",
		"BD_DISABLE_METRICS":         "1",
		"BD_EVENTS_JOURNAL":          "1",
	} {
		if e, ok := envVar(in, "agent-claude", name); !ok || e.Value != want {
			t.Errorf("agent-claude %s = %q (set %v); want %q", name, e.Value, ok, want)
		}
	}
	if _, ok := envVar(in, "rails", "BEADS_DIR"); ok {
		t.Error("rails has BEADS_DIR")
	}
	// beads.role through env git config, after the secrets' git wiring.
	var keys []string
	for _, e := range in.Env["agent-claude"] {
		if strings.HasPrefix(e.Name, "GIT_CONFIG_KEY_") {
			keys = append(keys, e.Value)
		}
	}
	if !slices.Contains(keys, "beads.role") {
		t.Errorf("agent-claude git config keys %v lack beads.role", keys)
	}

	vols, err := in.VolumeSpecs(c)
	if err != nil {
		t.Fatal(err)
	}
	ws := m.Inputs.Workspace
	for _, want := range []string{
		filepath.Join(ws, ".railswitch", "memory", "beads") + ":" + MemoryBeadsDir + ":ro,z",
		filepath.Join(ws, ".railswitch", "memory", "railswitch-prime.sh") + ":" + MemoryPrime + ":ro,z",
	} {
		if !slices.Contains(vols, want) {
			t.Errorf("agent-claude volumes lack %q:\n%s", want, strings.Join(vols, "\n"))
		}
	}
	var prime string
	for _, a := range m.Assets {
		switch a.Rel {
		case ".railswitch/memory/beads/metadata.json", ".railswitch/memory/beads/config.yaml":
			if a.Mode != 0o600 {
				t.Errorf("%s mode %o; want 0600 (bd warns about a group-readable BEADS_DIR)", a.Rel, a.Mode)
			}
		case ".railswitch/memory/railswitch-prime.sh":
			prime = string(a.Data)
		}
	}
	for _, want := range []string{"'No patient data, ever.'", "worktree:%s", `exec bd prime "$@"`} {
		if !strings.Contains(prime, want) {
			t.Errorf("railswitch-prime.sh lacks %q:\n%s", want, prime)
		}
	}
}

// A guidance change restarts a service agent: the script is a stamp input.
func TestMemoryGuidanceStamp(t *testing.T) {
	_, a := renderUnits(t, fixtureDir, withMemory)
	_, b := renderUnits(t, fixtureDir, func(p *config.Project) { p.Memory = &config.SharedMemory{Guidance: "Other."} })
	if string(a["filial-feat-agent-probe.container"]) == string(b["filial-feat-agent-probe.container"]) {
		t.Error("a guidance change leaves the service agent's unit unchanged")
	}
}

// [memory] builds the agent base with MEMORY=1 (bd), under a new tag.
func TestMemoryAgentBaseArg(t *testing.T) {
	agentBase := func(in *Instance) BuildStep {
		for _, b := range in.Builds {
			if b.Name == "agent-base" {
				return b
			}
		}
		t.Fatal("no agent-base build")
		return BuildStep{}
	}
	_, off := filial(t, nil)
	_, on := filial(t, withMemory)
	a, b := agentBase(off), agentBase(on)
	if _, ok := a.Args["MEMORY"]; ok {
		t.Errorf("no [memory]: agent-base args %v", a.Args)
	}
	if b.Args["MEMORY"] != "1" || a.Tag == b.Tag {
		t.Errorf("[memory]: agent-base args %v tag %s (without: %s)", b.Args, b.Tag, a.Tag)
	}
	if on.Values["image.agent-claude"] == off.Values["image.agent-claude"] {
		t.Error("[memory]: the agent's image tag is unchanged")
	}
}
