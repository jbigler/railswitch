package render

import (
	"os"
	"os/exec"
	"path/filepath"
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
		`marker='/home/appuser'/.railswitch-setup-`,
		"(cd '/app' && exec sh -ec 'gh extension install github/gh-stack')",
		"exec '/usr/local/bin/entrypoint-claude.sh' \"$@\"",
		"failed; it runs again at the next start",
		"</dev/null || return 1",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
	if !slices.ContainsFunc(s.Mounts, func(mt Mount) bool { return mt.Target == agentSetupPath }) {
		t.Error("the setup script is not mounted")
	}
	// Another list is another marker: a changed setup runs again.
	other := string(agentSetupScript("claude", cmds[:1], "/app", "/home/appuser", "/e"))
	if marker := func(s string) string { return strings.SplitN(strings.SplitN(s, "marker=", 2)[1], "\n", 2)[0] }; marker(other) == marker(script) {
		t.Error("two lists share a marker")
	}
	// Without setup, nothing changes.
	m, _ = filial(t, nil)
	if s := m.Services[slices.IndexFunc(m.Services, func(s Service) bool { return s.ID == "agent-claude" })]; s.Entrypoint == agentSetupPath {
		t.Error("an agent without setup got the setup script")
	}
}

// TestGHToken (D133): a github.com secret an agent receives as a file
// also reaches it as GH_TOKEN, once; nvim (no gh) doesn't get it; a
// secret already delivered as GH_TOKEN, or the agent's own env GH_TOKEN,
// wins.
func TestGHToken(t *testing.T) {
	gh := func(more func(p *config.Project)) *Instance {
		_, in := filial(t, func(p *config.Project) {
			s := p.Secrets["github_token"]
			s.GitHost = "github.com"
			s.Services = []string{"nvim", "agent-claude"}
			p.Secrets["github_token"] = s
			if more != nil {
				more(p)
			}
		})
		return in
	}
	count := func(specs []string) int {
		n := 0
		for _, s := range specs {
			if strings.HasSuffix(s, "target=GH_TOKEN") {
				n++
			}
		}
		return n
	}
	in := gh(nil)
	if n := count(in.SecretSpecs("agent-claude")); n != 1 {
		t.Errorf("agent GH_TOKEN %d times; want 1: %q", n, in.SecretSpecs("agent-claude"))
	}
	if n := count(in.SecretSpecs("nvim")); n != 0 {
		t.Errorf("nvim got GH_TOKEN: %q", in.SecretSpecs("nvim"))
	}
	in = gh(func(p *config.Project) {
		p.Secrets["mine"] = config.Secret{Services: []string{"agent-claude"}, Env: "GH_TOKEN"}
	})
	if n := count(in.SecretSpecs("agent-claude")); n != 1 {
		t.Errorf("with the project's own GH_TOKEN secret: %d; want only it", n)
	}
	in = gh(func(p *config.Project) {
		a := p.Agents["claude"]
		a.Env = map[string]string{"GH_TOKEN": "plain"}
		p.Agents["claude"] = a
	})
	if n := count(in.SecretSpecs("agent-claude")); n != 0 {
		t.Errorf("the agent's env GH_TOKEN and a GH_TOKEN secret both: %q", in.SecretSpecs("agent-claude"))
	}
}

// TestAgentSetupScriptRuns runs the generated script with sh: a failing
// command stops the rest and writes no marker (the next start runs it
// again), the entrypoint runs either way with the arguments, and a full
// run is not repeated.
func TestAgentSetupScriptRuns(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "entry.sh")
	writeExec := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExec(entry, "#!/bin/sh\necho \"entry $*\"\n")
	run := func(cmds []string) (string, string) {
		t.Helper()
		script := filepath.Join(dir, "setup.sh")
		writeExec(script, string(agentSetupScript("probe", cmds, dir, home, entry)))
		cmd := exec.Command("sh", script, "a1", "a 2")
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("run: %v\n%s%s", err, out.String(), errb.String())
		}
		return out.String(), errb.String()
	}
	markers := func() int {
		m, _ := filepath.Glob(filepath.Join(home, ".railswitch-setup-*"))
		return len(m)
	}
	after := filepath.Join(dir, "after")
	out, errs := run([]string{"false", "touch " + after})
	if _, err := os.Stat(after); err == nil {
		t.Error("a command after a failing one ran")
	}
	if markers() != 0 || !strings.Contains(errs, "failed; it runs again") || !strings.Contains(out, "entry a1 a 2") {
		t.Errorf("failure: markers %d\nout %q\nerr %q", markers(), out, errs)
	}
	count := filepath.Join(dir, "count")
	ok := []string{"echo x >> " + count, "true"}
	for range 2 {
		if out, _ := run(ok); !strings.Contains(out, "entry a1 a 2") {
			t.Errorf("entrypoint not run: %q", out)
		}
	}
	if data, _ := os.ReadFile(count); string(data) != "x\n" || markers() != 1 {
		t.Errorf("a full run repeated: count %q, markers %d", data, markers())
	}
}

// An agent package with no entrypoint can't have a setup: its image's own
// would be lost behind the script.
func TestAgentSetupNeedsEntrypoint(t *testing.T) {
	_, err := Build(fixtureInputs(t, fixtureWS(t), func(p *config.Project) {
		a := p.Agents["probe"]
		a.Setup = []string{"true"}
		p.Agents["probe"] = a
	}))
	if err == nil || !strings.Contains(err.Error(), "[agents.probe] setup: the agent's package has no entrypoint") {
		t.Errorf("err = %v", err)
	}
}
