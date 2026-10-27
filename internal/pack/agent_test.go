package pack

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/config"
)

const agentToml = `name = "a"
version = 1
allow = ["x.example"]
[image]
file = "Containerfile.a"
`

// withAgent is minimal() plus built-in agent package a@1.
func withAgent() fstest.MapFS {
	fsys := minimal()
	fsys["agents/a/agent.toml"] = &fstest.MapFile{Data: []byte(agentToml)}
	fsys["agents/a/Containerfile.a"] = &fstest.MapFile{Data: []byte("FROM base\n")}
	return fsys
}

func TestLoadAgents(t *testing.T) {
	c, err := Load(withAgent())
	if err != nil {
		t.Fatal(err)
	}
	a := c.Agents[Ref{"a", 1, false}]
	if a == nil || a.Image.File != "Containerfile.a" || !slices.Equal(a.Allow, []string{"x.example"}) {
		t.Fatalf("Agents = %+v", c.Agents)
	}

	fsys := withAgent()
	fsys["agents/a/agent.toml"] = &fstest.MapFile{Data: []byte(agentToml + "bogus = 1\n")}
	if _, err := Load(fsys); err == nil || !strings.Contains(err.Error(), "agents/a/agent.toml") || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("unknown key: %v", err)
	}
}

func TestDefaultAgent(t *testing.T) {
	fsys := withAgent()
	fsys["agents/a/agent.toml"] = &fstest.MapFile{Data: []byte("default = true\n" + agentToml)}
	c, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := c.DefaultAgent(); !ok || r != (Ref{"a", 1, false}) {
		t.Errorf("DefaultAgent = %v, %v", r, ok)
	}
	fsys["agents/b/agent.toml"] = &fstest.MapFile{Data: []byte("default = true\n" + strings.ReplaceAll(agentToml, `"a"`, `"b"`))}
	fsys["agents/b/Containerfile.a"] = &fstest.MapFile{Data: []byte("FROM base\n")}
	if _, err := Load(fsys); err == nil || !strings.Contains(err.Error(), "default") {
		t.Errorf("two defaults: %v", err)
	}
}

func writeLocalAgent(t *testing.T, root, dir, name string) {
	t.Helper()
	d := filepath.Join(root, "agents", dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := strings.ReplaceAll(agentToml, `"a"`, `"`+name+`"`)
	if err := os.WriteFile(filepath.Join(d, "agent.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "Containerfile.a"), []byte("FROM base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadLocalAgents(t *testing.T) {
	root := t.TempDir()
	writeLocalAgent(t, root, "probe", "probe")
	c := &Catalog{Agents: map[Ref]*Agent{}}
	if err := LoadLocalAgents(c, root); err != nil {
		t.Fatal(err)
	}
	if a := c.Agents[Ref{"probe", 1, true}]; a == nil || a.Name != "probe" {
		t.Fatalf("Agents = %+v", c.Agents)
	}

	bad := t.TempDir()
	writeLocalAgent(t, bad, "probe", "other")
	if err := LoadLocalAgents(&Catalog{Agents: map[Ref]*Agent{}}, bad); err == nil || !strings.Contains(err.Error(), "probe") {
		t.Errorf("name mismatch: %v", err)
	}

	c = &Catalog{Agents: map[Ref]*Agent{}}
	if err := LoadLocalAgents(c, t.TempDir()); err != nil || len(c.Agents) != 0 {
		t.Errorf("no agents/: %v, %v", err, c.Agents)
	}
}

func TestRefStringLocal(t *testing.T) {
	if got := (Ref{"probe", 1, true}).String(); got != "local:probe@1" {
		t.Errorf("String = %q", got)
	}
}

func agentProject(agents map[string]config.Agent) *config.Project {
	return &config.Project{
		Schema:  1,
		Project: config.Meta{Prefix: "pp", BaseBranch: "main"},
		Stack:   config.StackRef{Pack: "p@1", App: "web"},
		Agents:  agents,
	}
}

func TestResolveAgents(t *testing.T) {
	c, err := Load(withAgent())
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.Resolve(agentProject(map[string]config.Agent{
		"zed":   {Use: "a@1", Run: "service", Allow: []string{"y.example", "x.example"}},
		"alpha": {Use: "a@1"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var names, services, runs []string
	for _, a := range st.Agents {
		names = append(names, a.Name)
		services = append(services, a.Service)
		runs = append(runs, a.Run)
	}
	if !slices.Equal(names, []string{"alpha", "zed"}) || !slices.Equal(services, []string{"agent-alpha", "agent-zed"}) || !slices.Equal(runs, []string{"on-demand", "service"}) {
		t.Errorf("names %v services %v runs %v", names, services, runs)
	}
	if got := st.Agents[1].Allow; !slices.Equal(got, []string{"x.example", "y.example"}) {
		t.Errorf("zed allow = %v", got)
	}
	if st.Agents[0].Ref != (Ref{"a", 1, false}) {
		t.Errorf("alpha ref = %v", st.Agents[0].Ref)
	}

	if _, err := c.Resolve(agentProject(map[string]config.Agent{"x": {Use: "nope@1"}})); err == nil || !strings.Contains(err.Error(), "unknown agent package nope@1") {
		t.Errorf("unknown package: %v", err)
	}
	p := agentProject(nil)
	p.Options = map[string]string{"cache": "local:o@1"}
	if _, err := c.Resolve(p); err == nil || !strings.Contains(err.Error(), "agents only until Stage 5") {
		t.Errorf("local option: %v", err)
	}
}

func TestResolveAgentRunFromPackage(t *testing.T) {
	fsys := withAgent()
	fsys["agents/a/agent.toml"] = &fstest.MapFile{Data: []byte(`run = "service"` + "\n" + agentToml)}
	c, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.Resolve(agentProject(map[string]config.Agent{"x": {Use: "a@1"}, "y": {Use: "a@1", Run: "on-demand"}}))
	if err != nil {
		t.Fatal(err)
	}
	if st.Agents[0].Run != "service" || st.Agents[1].Run != "on-demand" {
		t.Errorf("runs = %s %s", st.Agents[0].Run, st.Agents[1].Run)
	}
}

func TestResolveContainerfileCollision(t *testing.T) {
	fsys := withAgent()
	fsys["agents/a/Containerfile.base"] = &fstest.MapFile{Data: []byte("FROM base\n")}
	fsys["agents/a/agent.toml"] = &fstest.MapFile{Data: []byte(strings.ReplaceAll(agentToml, "Containerfile.a", "Containerfile.base"))}
	c, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Resolve(agentProject(map[string]config.Agent{"x": {Use: "a@1"}}))
	if err == nil || !strings.Contains(err.Error(), "Containerfile.base") || !strings.Contains(err.Error(), "pack p") || !strings.Contains(err.Error(), "agents.x") {
		t.Errorf("collision: %v", err)
	}
}

func TestDefaultAgentUnique(t *testing.T) {
	fsys := withAgent()
	fsys["agents/a/agent.toml"] = &fstest.MapFile{Data: []byte("default = true\n" + agentToml)}
	fsys["agents/b/agent.toml"] = &fstest.MapFile{Data: []byte("default = true\n" + strings.ReplaceAll(agentToml, `"a"`, `"b"`))}
	fsys["agents/b/Containerfile.a"] = &fstest.MapFile{Data: []byte("FROM base\n")}
	_, err := Load(fsys)
	if err == nil || !strings.Contains(err.Error(), "a@1") || !strings.Contains(err.Error(), "b@1") {
		t.Errorf("two defaults: %v, want both named", err)
	}
}
