package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noAgentWS is lockedWS without agents: no [agents.*], no agent secrets
// and no agent Containerfiles; github_token (git_host github.com, as
// project new writes it) goes to nvim only.
func noAgentWS(t *testing.T) string {
	t.Helper()
	ws, _ := filialWS(t)
	path := filepath.Join(ws, "project.toml")
	data := readFile(t, path)
	for _, cut := range [][2]string{
		{"[secrets.probe_key]", "[tools.nvim]"},
		{"[agents.claude]", "[firewall]"},
	} {
		i, j := strings.Index(data, cut[0]), strings.Index(data, cut[1])
		if i < 0 || j < i {
			t.Fatalf("fixture lacks %s … %s", cut[0], cut[1])
		}
		data = data[:i] + data[j:]
	}
	data = strings.Replace(data, "services = [\"nvim\"]\n", "services = [\"nvim\"]\ngit_host = \"github.com\"\n", 1)
	writeFile(t, path, data)
	for _, n := range []string{"agent-base", "claude", "probe"} {
		if err := os.Remove(filepath.Join(ws, "containerfiles", "Containerfile."+n)); err != nil {
			t.Fatal(err)
		}
	}
	mustRun(t, "-C", ws, "lock")
	return ws
}

// snapshot is every file of the workspace that agent add may write.
func snapshot(t *testing.T, ws string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, rel := range []string{"project.toml", "stack.lock", "containerfiles/Containerfile.agent-base", "containerfiles/Containerfile.claude"} {
		data, err := os.ReadFile(filepath.Join(ws, rel))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		files[rel] = string(data)
	}
	return files
}

func sameFiles(t *testing.T, before, after map[string]string) {
	t.Helper()
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed:\n%s", k, after[k])
		}
	}
}

func TestAgentAdd(t *testing.T) {
	ws := noAgentWS(t)
	code, out, errs := runIn(t, "-C", filepath.Join(ws, "containerfiles"), "agent", "add", "claude")
	if code != 0 {
		t.Fatalf("agent add: exit %d: %s", code, errs)
	}
	p := loadProject(t, ws)
	if a := p.Agents["claude"]; a.Use != "claude@1" || a.Run != "" {
		t.Errorf("[agents.claude] = %+v", a)
	}
	s, ok := p.Secrets["claude_token"]
	if !ok || strings.Join(s.Services, ",") != "agent-claude" || s.Env != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Errorf("claude_token = %+v (%v)", s, ok)
	}
	if g := p.Secrets["github_token"]; strings.Join(g.Services, ",") != "nvim,agent-claude" {
		t.Errorf("github_token services = %q", g.Services)
	}
	toml := readFile(t, filepath.Join(ws, "project.toml"))
	if !strings.Contains(toml, "# values live in podman secrets") || !strings.Contains(toml, "[agents.claude]\nuse = \"claude@1\"\n") {
		t.Errorf("project.toml:\n%s", toml)
	}
	if lock := readFile(t, filepath.Join(ws, "stack.lock")); !strings.Contains(lock, `agent."claude@1"`) {
		t.Errorf("stack.lock lacks agent.\"claude@1\":\n%s", lock)
	}
	for _, n := range []string{"agent-base", "claude"} {
		if _, err := os.Stat(filepath.Join(ws, "containerfiles", "Containerfile."+n)); err != nil {
			t.Error(err)
		}
	}
	for _, want := range []string{"railswitch secret set claude_token", "railswitch agent claude"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestAgentAddExisting(t *testing.T) {
	ws := noAgentWS(t)
	mustRun(t, "-C", ws, "agent", "add", "claude")
	before := snapshot(t, ws)
	if code, _, _ := runIn(t, "-C", ws, "agent", "add", "claude"); code == 0 {
		t.Fatal("second agent add claude succeeded")
	}
	sameFiles(t, before, snapshot(t, ws))
}

func TestAgentAddReserved(t *testing.T) {
	ws := noAgentWS(t)
	before := snapshot(t, ws)
	for _, n := range []string{"add", "ls"} {
		code, _, errs := runIn(t, "-C", ws, "agent", "add", n, "--use", "claude@1")
		if code == 0 || !strings.Contains(errs, "reserved") {
			t.Errorf("agent add %s: exit %d: %s", n, code, errs)
		}
	}
	sameFiles(t, before, snapshot(t, ws))
}

func TestAgentAddUse(t *testing.T) {
	ws := noAgentWS(t)
	cf := filepath.Join(ws, "containerfiles", "Containerfile.claude")
	writeFile(t, cf, "FROM mine\n")
	mustRun(t, "-C", ws, "agent", "add", "rev", "--use", "claude@1", "--run", "service")
	p := loadProject(t, ws)
	if a := p.Agents["rev"]; a.Use != "claude@1" || a.Run != "service" {
		t.Errorf("[agents.rev] = %+v", a)
	}
	if s := p.Secrets["rev_token"]; strings.Join(s.Services, ",") != "agent-rev" {
		t.Errorf("rev_token = %+v", s)
	}
	if got := readFile(t, cf); got != "FROM mine\n" {
		t.Errorf("Containerfile.claude overwritten:\n%s", got)
	}
}

func TestAgentAddLocal(t *testing.T) {
	ws := noAgentWS(t)
	mustRun(t, "-C", ws, "agent", "add", "probe", "--use", "local:probe@1")
	if a := loadProject(t, ws).Agents["probe"]; a.Use != "local:probe@1" {
		t.Errorf("[agents.probe] = %+v", a)
	}
	if _, err := os.Stat(filepath.Join(ws, "containerfiles", "Containerfile.probe")); err != nil {
		t.Error(err)
	}
}

func TestAgentAddUnknown(t *testing.T) {
	ws := noAgentWS(t)
	before := snapshot(t, ws)
	code, _, errs := runIn(t, "-C", ws, "agent", "add", "nope")
	if code != 2 || !strings.Contains(errs, "--use") || !strings.Contains(errs, "claude@1") {
		t.Errorf("agent add nope: exit %d: %s", code, errs)
	}
	sameFiles(t, before, snapshot(t, ws))
}

func TestAgentNoAgentsHint(t *testing.T) {
	ws := noAgentWS(t)
	code, _, errs := runIn(t, "-C", ws, "-w", "feat", "agent")
	if code == 0 || !strings.Contains(errs, "railswitch agent add claude") {
		t.Errorf("agent: exit %d: %s", code, errs)
	}
}

// dropGitHubToken removes [secrets.github_token] and sets repo.
func dropGitHubToken(t *testing.T, ws, repo string) {
	t.Helper()
	path := filepath.Join(ws, "project.toml")
	data := readFile(t, path)
	i, j := strings.Index(data, "[secrets.github_token]"), strings.Index(data, "[tools.nvim]")
	data = data[:i] + data[j:]
	data = strings.Replace(data, `"git@github.com:[ORG]/[REPO].git"`, `"`+repo+`"`, 1)
	writeFile(t, path, data)
}

func TestAgentAddDeclaresGitHubToken(t *testing.T) {
	ws := noAgentWS(t)
	dropGitHubToken(t, ws, "git@github.com:[ORG]/[REPO].git")
	out := mustRun(t, "-C", ws, "agent", "add", "claude")
	g, ok := loadProject(t, ws).Secrets["github_token"]
	if !ok || strings.Join(g.Services, ",") != "agent-claude" || g.GitHost != "github.com" {
		t.Errorf("github_token = %+v (%v)", g, ok)
	}
	if !strings.Contains(out, "railswitch secret set github_token") {
		t.Errorf("output:\n%s", out)
	}
}

func TestAgentAddNoGitHubToken(t *testing.T) {
	ws := noAgentWS(t)
	dropGitHubToken(t, ws, "git@gitlab.com:org/repo.git")
	mustRun(t, "-C", ws, "agent", "add", "claude")
	if _, ok := loadProject(t, ws).Secrets["github_token"]; ok {
		t.Error("github_token declared for a gitlab repo")
	}
}

// The derived secret name is validated before anything is written.
func TestAgentAddLongSecretName(t *testing.T) {
	ws := noAgentWS(t)
	toml := filepath.Join(ws, "agents", "probe", "agent.toml")
	writeFile(t, toml, readFile(t, toml)+"\n[[secrets]]\nname = \"a_rather_long_key\"\nenv = \"K\"\n")
	before := snapshot(t, ws)
	name := strings.Repeat("a", 25)
	code, _, errs := runIn(t, "-C", ws, "agent", "add", name, "--use", "local:probe@1")
	if code == 0 {
		t.Fatal("agent add succeeded")
	}
	if !strings.Contains(errs, name+"_a_rather_long_key") {
		t.Errorf("stderr %q", errs)
	}
	sameFiles(t, before, snapshot(t, ws))
}
