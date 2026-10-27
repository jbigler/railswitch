package render

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
)

// noAgents turns the fixture's agents off.
func noAgents(p *config.Project) {
	p.Agents = nil
	delete(p.Secrets, "probe_key")
}

func TestAgentServices(t *testing.T) {
	m, in := filial(t, nil)
	var ids []string
	for _, s := range m.Agents() {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"agent-claude", "agent-probe"}) {
		t.Errorf("Agents() = %v", ids)
	}

	c := service(t, m, "agent-claude")
	if c.Role != RoleAgent || !c.OnDemand || !slices.Equal(c.Networks, []string{"dev", "proxy"}) ||
		!slices.Equal(c.Firewall, []string{"agent-claude"}) || c.FirewallMode != "enforce" {
		t.Errorf("agent-claude role %s on-demand %v networks %v firewall %v mode %q", c.Role, c.OnDemand, c.Networks, c.Firewall, c.FirewallMode)
	}
	if !c.Worktree || c.Workdir != "/app" || c.Unit.Memory != "4g" || c.Unit.PidsLimit != 4096 || !c.Unit.TTY || !c.DropsAllCaps() {
		t.Errorf("agent-claude worktree %v workdir %q unit %+v", c.Worktree, c.Workdir, c.Unit)
	}
	if c.Entrypoint != "/usr/local/bin/entrypoint-claude.sh" {
		t.Errorf("agent-claude entrypoint %q", c.Entrypoint)
	}
	if p := service(t, m, "agent-probe"); p.OnDemand || p.Role != RoleAgent {
		t.Errorf("agent-probe role %s on-demand %v", p.Role, p.OnDemand)
	}

	ws := m.Inputs.Workspace
	vols, err := in.VolumeSpecs(c)
	if err != nil {
		t.Fatal(err)
	}
	master := filepath.Join(ws, "master")
	for _, want := range []string{
		master + ":/app:z",
		master + ":" + master + ":z",
		filepath.Join(ws, ".railswitch", "status", "master") + ":/run/railswitch/status:rw,z",
		filepath.Join(ws, ".railswitch", "assets", "claude", "CLAUDE.md") + ":/opt/railswitch/agent/CLAUDE.md:ro,z",
		filepath.Join(ws, ".railswitch", "assets", "claude", "entrypoints", "entrypoint-claude.sh") + ":/usr/local/bin/entrypoint-claude.sh:ro,z",
		"filial-master-agent-claude-home:/home/appuser:U",
		"filial-master-node-modules:/app/node_modules:U",
		"filial_gems_ruby3.3.6:/usr/local/bundle",
		"filial_claude_plugins_cache:/home/appuser/.claude/plugins/cache:U",
	} {
		if !slices.Contains(vols, want) {
			t.Errorf("agent-claude volumes lack %q:\n%s", want, strings.Join(vols, "\n"))
		}
	}

	_, units := renderUnits(t, fixtureDir, nil)
	var wants string
	for _, l := range unitLines(t, units, "filial-feat-rails.container") {
		if v, ok := strings.CutPrefix(l, "Wants="); ok {
			wants = v
		}
	}
	if w := strings.Fields(wants); !slices.Contains(w, "filial-feat-agent-probe.service") || slices.Contains(w, "filial-feat-agent-claude.service") {
		t.Errorf("rails Wants=%s", wants)
	}
	if _, ok := units["filial-feat-agent-claude.container"]; ok {
		t.Error("an on-demand agent has a unit")
	}
	hasLine(t, units, "filial-feat-agent-probe.container", "PodmanArgs=--cpus=4 --tty --interactive")
}

// C11: a service that mounts the base .git writable also mounts the worktree
// at its own host path.
func TestWorktreeHostPath(t *testing.T) {
	ws, units := renderUnits(t, fixtureDir, nil)
	feat := filepath.Join(ws, "feat")
	line := "Volume=" + feat + ":" + feat + ":z"
	hasLine(t, units, "filial-feat-nvim.container", line)
	hasLine(t, units, "filial-feat-agent-probe.container", line)
	if slices.Contains(unitLines(t, units, "filial-feat-rails.container"), line) {
		t.Errorf("rails mounts the worktree at its host path")
	}
}

func TestAgentEnv(t *testing.T) {
	_, in := filial(t, nil)
	if e, _ := envVar(in, "agent-claude", "RAILSWITCH_STATUS_FILE"); e.Value != "/run/railswitch/status/claude.json" {
		t.Errorf("agent-claude RAILSWITCH_STATUS_FILE = %+v", e)
	}
	if e, _ := envVar(in, "agent-probe", "RAILSWITCH_AGENT"); e.Value != "probe" {
		t.Errorf("agent-probe RAILSWITCH_AGENT = %+v", e)
	}
	if e, _ := envVar(in, "agent-claude", "DISABLE_AUTOUPDATER"); e.Value != "1" {
		t.Errorf("agent-claude DISABLE_AUTOUPDATER (package [env]) = %+v", e)
	}
	for _, svc := range []string{"agent-claude", "agent-probe"} {
		if e, _ := envVar(in, svc, "BROWSER_CDP_URL"); e.Value != "http://browser:9223" || e.Layer != LayerOption {
			t.Errorf("%s BROWSER_CDP_URL = %+v", svc, e)
		}
		if _, ok := envVar(in, svc, "DATABASE_URL"); !ok {
			t.Errorf("%s has no DATABASE_URL", svc)
		}
	}
	for _, svc := range []string{"rails", "nvim", "deps"} {
		if e, ok := envVar(in, svc, "BROWSER_CDP_URL"); ok {
			t.Errorf("%s gets BROWSER_CDP_URL = %+v", svc, e)
		}
	}

	// [agents.<n>] env is layer project: it beats the package's [env].
	_, in = filial(t, func(p *config.Project) {
		a := p.Agents["claude"]
		a.Env = map[string]string{"DISABLE_AUTOUPDATER": "0"}
		p.Agents["claude"] = a
	})
	if e, _ := envVar(in, "agent-claude", "DISABLE_AUTOUPDATER"); e.Value != "0" || e.Layer != LayerProject {
		t.Errorf("agent-claude DISABLE_AUTOUPDATER with [agents.claude] env = %+v", e)
	}
}

func TestAgentSecretsScoped(t *testing.T) {
	_, in := filial(t, nil)
	if a := strings.Join(in.SecretArgs("agent-probe"), " "); !strings.Contains(a, "filial_probe_key") {
		t.Errorf("agent-probe secrets = %q", a)
	}
	if a := strings.Join(in.SecretArgs("agent-claude"), " "); strings.Contains(a, "filial_probe_key") {
		t.Errorf("agent-claude secrets = %q", a)
	}
}

func TestAgentNames(t *testing.T) {
	m, _ := filial(t, nil)
	n, err := m.NamesFor("feat")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(n.Units, "filial-feat-agent-probe.service") || slices.Contains(n.Units, "filial-feat-agent-claude.service") {
		t.Errorf("Units = %v", n.Units)
	}
	for _, c := range []string{"filial-feat-agent-claude", "filial-feat-agent-probe"} {
		if !slices.Contains(n.Containers, c) {
			t.Errorf("Containers lack %s: %v", c, n.Containers)
		}
	}
	for _, v := range []string{"filial-feat-agent-claude-home", "filial-feat-agent-probe-home"} {
		if !slices.Contains(n.Volumes, v) {
			t.Errorf("Volumes lack %s: %v", v, n.Volumes)
		}
	}
}

func TestPlanStatusDirs(t *testing.T) {
	p, ws, qdir := filialPlan(t)
	want := []string{".railswitch/status/master", ".railswitch/status/feat"}
	if !slices.Equal(p.StatusDirs, want) {
		t.Errorf("StatusDirs = %v; want %v", p.StatusDirs, want)
	}
	mustApply(t, p, ws, qdir, false)
	for _, rel := range want {
		fi, err := os.Stat(filepath.Join(ws, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: mode %v; want a 0700 dir", rel, fi.Mode())
		}
	}

	p, err := mustBuild(t, fixtureInputs(t, fixtureWS(t), noAgents)).Plan()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.StatusDirs) != 0 {
		t.Errorf("StatusDirs without agents = %v", p.StatusDirs)
	}
}
