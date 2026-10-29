package render

import (
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
)

// TestAgentSetup (D133): an agent's setup puts a script in front of its
// entrypoint that runs the commands once per home (a marker per list),
// then execs the package's entrypoint with whatever arguments it got.
func TestAgentSetup(t *testing.T) {
	cmds := []string{"gh extension install github/gh-stack", "gh skill install github/gh-stack --scope user"}
	m, _ := filial(t, func(p *config.Project) {
		a := p.Agents["claude"]
		a.Setup = cmds
		p.Agents["claude"] = a
	})
	i := slices.IndexFunc(m.Services, func(s Service) bool { return s.ID == "agent-claude" })
	s := m.Services[i]
	if s.Entrypoint != agentSetupPath {
		t.Fatalf("entrypoint = %q; want the setup script", s.Entrypoint)
	}
	var script string
	for _, a := range m.Assets {
		if a.Rel == ".railswitch/hooks/agent-claude-setup.sh" {
			script = string(a.Data)
		}
	}
	for _, want := range []string{
		`marker="$HOME/.railswitch-setup-`,
		"(cd '/app' && exec sh -ec 'gh extension install github/gh-stack')",
		"exec '/usr/local/bin/entrypoint-claude.sh' \"$@\"",
		"failed; it runs again at the next start",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
	if !slices.ContainsFunc(s.Mounts, func(mt Mount) bool { return mt.Target == agentSetupPath }) {
		t.Error("the setup script is not mounted")
	}
	// Another list is another marker: a changed setup runs again.
	other := string(agentSetupScript("claude", cmds[:1], "/app", ""))
	if marker := func(s string) string { return strings.SplitN(strings.SplitN(s, "marker=", 2)[1], "\n", 2)[0] }; marker(other) == marker(script) {
		t.Error("two lists share a marker")
	}
	// Without setup, nothing changes.
	m, _ = filial(t, nil)
	if s := m.Services[slices.IndexFunc(m.Services, func(s Service) bool { return s.ID == "agent-claude" })]; s.Entrypoint == agentSetupPath {
		t.Error("an agent without setup got the setup script")
	}
}

// TestGHToken (D133): a github.com secret delivered as a file also reaches
// gh as GH_TOKEN, once; a secret already delivered as GH_TOKEN wins.
func TestGHToken(t *testing.T) {
	_, in := filial(t, func(p *config.Project) {
		s := p.Secrets["github_token"]
		s.GitHost = "github.com"
		p.Secrets["github_token"] = s
	})
	specs := in.SecretSpecs("nvim")
	n := 0
	for _, s := range specs {
		if strings.HasSuffix(s, ",type=env,target=GH_TOKEN") {
			n++
		}
	}
	if n != 1 || !slices.ContainsFunc(specs, func(s string) bool { return strings.Contains(s, "target=/run/secrets/github_token") }) {
		t.Errorf("specs = %q; want the file and one GH_TOKEN", specs)
	}
	_, in = filial(t, func(p *config.Project) {
		s := p.Secrets["github_token"]
		s.GitHost = "github.com"
		p.Secrets["github_token"] = s
		p.Secrets["mine"] = config.Secret{Services: []string{"nvim"}, Env: "GH_TOKEN"}
	})
	n = 0
	for _, s := range in.SecretSpecs("nvim") {
		if strings.HasSuffix(s, "target=GH_TOKEN") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("GH_TOKEN delivered %d times; want only the project's own", n)
	}
}
