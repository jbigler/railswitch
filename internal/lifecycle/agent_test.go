package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
)

// editProject rewrites the workspace's project.toml with edit, then locks
// and reloads it.
func (e *tenv) editProject(t *testing.T, edit func(string) string) {
	t.Helper()
	path := filepath.Join(e.ws, "project.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(edit(string(data))), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := workspace.Lock(ctx, e.ws); err != nil {
		t.Fatal(err)
	}
	w, err := workspace.Load(ctx, e.ws, "")
	if err != nil {
		t.Fatal(err)
	}
	e.l.WS = w
}

// withoutProbe drops the probe agent and its secret from project.toml.
func withoutProbe(s string) string {
	for _, cut := range []string{
		"[secrets.probe_key]\nservices = [\"agent-probe\"]\n",
		"[agents.probe]                             # test-only workspace package: agents/probe/\nuse = \"local:probe@1\"\nrun = \"service\"\n",
	} {
		s = strings.Replace(s, cut, "", 1)
	}
	return s
}

func TestAgentOnDemandArgv(t *testing.T) {
	e := setup(t)
	if err := e.l.Agent(ctx, "feat", "claude", []string{"--version"}); err != nil {
		t.Fatal(err)
	}
	calls := e.calls()
	wantInOrder(t, calls, "systemctl --user start filial-feat-rails.service", "podman --hooks-dir")
	if n := count(calls, "podman --hooks-dir"); n != 1 {
		t.Fatalf("%d podman runs:\n%s", n, dump(calls))
	}
	i := slices.IndexFunc(calls, func(c []string) bool { return strings.HasPrefix(joined(c), "podman --hooks-dir") })
	c := calls[i]
	if c[3] != "run" {
		t.Fatalf("not a podman run: %q", c)
	}
	j := " " + joined(c) + " "
	ip := machine.TraefikIP(0)
	for _, want := range []string{
		"--rm", "-i",
		"--name filial-feat-agent-claude",
		"--entrypoint /usr/local/bin/entrypoint-claude.sh",
		"--network filial-feat-dev",
		"--network filial_proxy",
		"--add-host s3.feat.filial.localhost:" + ip,
		"--annotation filial.firewall=agent-claude",
		"--annotation filial.firewall.mode=enforce",
		"-w /app",
	} {
		if !strings.Contains(j, " "+want+" ") {
			t.Errorf("argv lacks %q:\n%s", want, j)
		}
	}
	if !strings.HasPrefix(c[len(c)-2], "localhost/filial/agent-claude:") || c[len(c)-1] != "--version" {
		t.Errorf("argv does not end with the claude image and --version: %q", c[len(c)-2:])
	}
	// An agent's cap is its role's: the service agent's unit shows it.
	probe, err := os.ReadFile(filepath.Join(e.l.WS.Dirs.Quadlet, "filial-feat-agent-probe.container"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := flagValues(c, "--cpus"), unitCPUs(t, string(probe)); len(got) != 1 || got[0] != want {
		t.Errorf("--cpus %q; want %s (the agent role's cap)", got, want)
	}
	if to := e.f.Cmds[len(e.f.Cmds)-1].Timeout; to != sys.NoTimeout {
		t.Errorf("timeout %v; want NoTimeout", to)
	}
}

func TestExecNoName(t *testing.T) {
	e := setup(t)
	e.render(t)
	if err := e.l.Exec(ctx, "feat", true, []string{"ls"}); err != nil {
		t.Fatal(err)
	}
	c := find(t, e.calls(), "podman --hooks-dir")
	if slices.Contains(c, "--name") || slices.Contains(c, "--entrypoint") {
		t.Errorf("exec --fresh argv has --name or --entrypoint: %q", c)
	}
	if nets := flagValues(c, "--network"); !slices.Equal(nets, []string{"filial-feat-dev"}) {
		t.Errorf("exec --fresh networks %q", nets)
	}
}

func TestAgentServiceAttach(t *testing.T) {
	e := setup(t)
	const unit = "filial-feat-agent-probe.service"
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=inactive\n", unit)
	if err := e.l.Agent(ctx, "feat", "probe", nil); err != nil {
		t.Fatal(err)
	}
	calls := e.calls()
	wantInOrder(t, calls, "systemctl --user start filial-feat-rails.service",
		"systemctl --user start "+unit, "podman attach filial-feat-agent-probe")
	if to := e.f.Cmds[len(e.f.Cmds)-1].Timeout; to != sys.NoTimeout {
		t.Errorf("attach timeout %v; want NoTimeout", to)
	}

	e.reset()
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=active\n", unit)
	if err := e.l.Agent(ctx, "feat", "probe", nil); err != nil {
		t.Fatal(err)
	}
	calls = e.calls()
	if n := count(calls, "systemctl --user start "+unit); n != 0 {
		t.Errorf("started an active unit:\n%s", dump(calls))
	}
	if last := joined(calls[len(calls)-1]); last != "podman attach filial-feat-agent-probe" {
		t.Errorf("last call %q; want the attach", last)
	}

	err := e.l.Agent(ctx, "feat", "probe", []string{"x"})
	var u *UsageError
	if !errors.As(err, &u) || !strings.Contains(err.Error(), "[agents.probe]") {
		t.Fatalf("extra argv: got %v; want a usage error naming [agents.probe]", err)
	}
}

func TestAgentNameChoice(t *testing.T) {
	e := setup(t)
	m, err := e.l.model(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.l.agentName(m, ""); err == nil || !strings.Contains(err.Error(), "claude") || !strings.Contains(err.Error(), "probe") {
		t.Fatalf("two agents: got %v; want an error listing claude and probe", err)
	}
	if n, err := e.l.agentName(m, "probe"); err != nil || n != "probe" {
		t.Fatalf("probe: got %q, %v", n, err)
	}
	if _, err := e.l.agentName(m, "nope"); err == nil || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("unknown: got %v", err)
	}
	e.editProject(t, withoutProbe)
	if m, err = e.l.model(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := e.l.agentName(m, ""); err != nil || n != "claude" {
		t.Fatalf("one agent: got %q, %v; want claude", n, err)
	}
}

func TestUpNoReloadForOnDemand(t *testing.T) {
	e := setup(t)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	e.reset()
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=inactive\n")
	e.units("LoadState=not-found\nNeedDaemonReload=no\nActiveState=inactive\n", "filial-feat-agent-claude.service")
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	if n := count(e.calls(), "systemctl --user daemon-reload"); n != 0 {
		t.Errorf("daemon-reload with every unit loaded:\n%s", dump(e.calls()))
	}
	if n := count(e.calls(), "systemctl --user show -p LoadState,NeedDaemonReload,ActiveState filial-feat-agent-claude.service"); n != 0 {
		t.Errorf("asked systemd about the on-demand agent's unit:\n%s", dump(e.calls()))
	}
}

func TestUpIgnoresOnDemandSecret(t *testing.T) {
	e := setup(t)
	e.editProject(t, func(s string) string {
		return s + "\n[secrets.claude_token]\nservices = [\"agent-claude\"]\nenv = \"CLAUDE_CODE_OAUTH_TOKEN\"\n"
	})
	e.missing("podman", "secret", "exists", "filial_claude_token")
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatalf("up with an on-demand agent's secret unset: %v", err)
	}
	e.reset()
	err := e.l.Agent(ctx, "feat", "claude", nil)
	if err == nil || !strings.Contains(err.Error(), "claude_token") {
		t.Fatalf("agent claude: got %v; want claude_token unset", err)
	}
	if n := count(e.calls(), "podman --hooks-dir"); n != 0 {
		t.Errorf("ran the agent with its secret unset:\n%s", dump(e.calls()))
	}
}

func TestStatusOnDemand(t *testing.T) {
	for _, running := range []bool{false, true} {
		e := setup(t)
		e.f.On([]string{"systemctl", "--user", "show"}, sys.Result{Stdout: []byte("ActiveState=active\nSubState=running\n")}, nil)
		e.f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", "filial-feat-agent-claude"}, sys.Result{Stdout: []byte(fmt.Sprintf("%v\n", running))}, nil)
		if err := e.l.Status(ctx, "feat", true); err != nil {
			t.Fatal(err)
		}
		var rows []StatusRow
		if err := json.Unmarshal(e.out.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		want := "on-demand"
		if running {
			want = "running"
		}
		i := slices.IndexFunc(rows, func(r StatusRow) bool { return r.Service == "agent-claude" })
		if i < 0 || rows[i].State != want || rows[i].Role != "agent" {
			t.Errorf("running %v: rows %+v; want agent-claude agent %s", running, rows, want)
		}
		if n := count(e.calls(), "systemctl --user show -p ActiveState,SubState filial-feat-agent-claude.service"); n != 0 {
			t.Errorf("asked systemd about the on-demand agent")
		}
	}
}

func TestDownRemovesAgentHome(t *testing.T) {
	e := setup(t)
	if err := e.l.Down(ctx, "feat", true); err != nil {
		t.Fatal(err)
	}
	find(t, e.calls(), "podman volume rm -f filial-feat-agent-claude-home")
	find(t, e.calls(), "podman rm -f --ignore filial-feat-agent-claude")
}

func TestProjectRmPurgesOnDemandShared(t *testing.T) {
	// projects renames the fixture after its prefix: "filial" would clash
	// with the copy's own directory name.
	dirs, _, f := projects(t, "api")
	if err := ProjectRemove(ctx, f, dirs, &bytes.Buffer{}, strings.NewReader(""), false, "api", true, true); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"api_claude_plugins_cache", "api_claude_plugins_marketplaces"} {
		if !slices.ContainsFunc(nonGit(f), func(c []string) bool {
			return strings.HasPrefix(joined(c), "podman volume rm") && c[len(c)-1] == v
		}) {
			t.Errorf("did not remove %s:\n%s", v, dump(nonGit(f)))
		}
	}
}

func TestPreflightCreatesStatusDir(t *testing.T) {
	e := setup(t)
	e.render(t)
	dir := filepath.Join(e.ws, ".railswitch", "status", "feat")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	m, err := e.l.model(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.l.Preflight(ctx, m, "feat"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("status dir: %v %v", fi, err)
	}
}

func TestAgentList(t *testing.T) {
	e := setup(t)
	e.render(t)
	dir := filepath.Join(e.ws, ".railswitch", "status", "feat")
	if err := os.WriteFile(filepath.Join(dir, "claude.json"), []byte(`{"v":1,"state":"done","message":"Ship it?","ts":7}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := e.l.AgentList(ctx, "feat")
	if err != nil {
		t.Fatal(err)
	}
	want := []AgentRow{
		{Name: "claude", Package: "claude@1", Run: "on-demand", State: "done", Message: "Ship it?", TS: 7, Reply: true},
		{Name: "probe", Package: "local:probe@1", Run: "service", State: "off"},
	}
	if !slices.Equal(rows, want) {
		t.Fatalf("rows %+v; want %+v", rows, want)
	}
}

// TestAgentTransientMatchesUnit: a full transient run of an agent joins the
// networks, resolves the hosts and carries the annotations its unit has.
func TestAgentTransientMatchesUnit(t *testing.T) {
	e := setup(t)
	e.render(t)
	m, in, err := e.l.load(ctx, "feat")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service(m, "agent-probe")
	if err != nil {
		t.Fatal(err)
	}
	argv, err := e.l.transient(m, in, svc, "/env", runOpts{Full: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(e.l.WS.Dirs.Quadlet, "filial-feat-agent-probe.container"))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(b)
	if got := flagValues(argv, "--network"); !slices.Equal(got, []string{"filial-feat-dev", "filial_proxy"}) {
		t.Errorf("networks %q", got)
	}
	if got, want := flagValues(argv, "--add-host"), unitValues(unit, "AddHost"); len(want) == 0 || !slices.Equal(got, want) {
		t.Errorf("add-host %q, unit %q", got, want)
	}
	if got, want := flagValues(argv, "--annotation"), unitValues(unit, "Annotation"); !slices.Equal(got, want) {
		t.Errorf("annotations %q, unit %q", got, want)
	}
	if got, want := flagValues(argv, "--cpus"), unitCPUs(t, unit); len(got) != 1 || got[0] != want {
		t.Errorf("cpus %q, unit %s", got, want)
	}
	if got, want := flagValues(argv, "-v"), unitValues(unit, "Volume"); !slices.Equal(got, want) {
		t.Errorf("mounts %q, unit %q", got, want)
	}
}

// unitCPUs is the --cpus= value of a unit's PodmanArgs.
func unitCPUs(t *testing.T, unit string) string {
	t.Helper()
	for _, a := range unitValues(unit, "PodmanArgs") {
		for _, w := range strings.Fields(a) {
			if v, ok := strings.CutPrefix(w, "--cpus="); ok {
				return v
			}
		}
	}
	t.Fatalf("unit has no --cpus:\n%s", unit)
	return ""
}

// agentRunning makes container ctr report running.
func (e *tenv) agentRunning(ctr string) {
	e.f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", ctr}, sys.Result{Stdout: []byte("true\n")}, nil)
}

// D62: one running agent per worktree; other worktrees are independent.
func TestAgentRefusesOtherRunning(t *testing.T) {
	e := setup(t)
	e.agentRunning("filial-feat-agent-probe")
	err := e.l.Agent(ctx, "feat", "claude", nil)
	if err == nil || err.Error() != "agent probe is running in feat; one agent per worktree: railswitch agent stop probe" {
		t.Fatalf("got %v; want the one-agent refusal naming probe", err)
	}
	if n := count(e.calls(), "podman --hooks-dir"); n != 0 {
		t.Errorf("ran an agent:\n%s", dump(e.calls()))
	}

	e = setup(t)
	e.agentRunning("filial-master-agent-probe")
	if err := e.l.Agent(ctx, "feat", "claude", nil); err != nil {
		t.Fatal(err)
	}
	if n := count(e.calls(), "podman --hooks-dir"); n != 1 {
		t.Errorf("%d podman runs:\n%s", n, dump(e.calls()))
	}

	e = setup(t)
	e.agentRunning("filial-feat-agent-claude")
	err = e.l.Agent(ctx, "feat", "claude", nil)
	if err == nil || err.Error() != "agent claude is already running in feat" {
		t.Fatalf("got %v; want already running", err)
	}
}

func TestAgentServiceStartsOnRequest(t *testing.T) {
	e := setup(t)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	for _, c := range e.calls() {
		if j := joined(c); strings.HasPrefix(j, "systemctl --user start") && strings.Contains(j, "-agent-") {
			t.Errorf("up started an agent: %s", j)
		}
	}
	e.reset()
	const unit = "filial-feat-agent-probe.service"
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=inactive\n", unit)
	if err := e.l.Agent(ctx, "feat", "probe", nil); err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, e.calls(), "systemctl --user start "+unit, "podman attach filial-feat-agent-probe")
}

func TestAgentStop(t *testing.T) {
	e := setup(t)
	e.agentRunning("filial-feat-agent-probe")
	if err := e.l.AgentStop(ctx, "feat", ""); err != nil {
		t.Fatal(err)
	}
	find(t, e.calls(), "systemctl --user stop filial-feat-agent-probe.service")
	err := e.l.AgentStop(ctx, "feat", "claude")
	if err == nil || !strings.Contains(err.Error(), "probe") {
		t.Fatalf("stop claude while probe runs: got %v; want an error naming probe", err)
	}

	e = setup(t)
	e.agentRunning("filial-feat-agent-claude")
	if err := e.l.AgentStop(ctx, "feat", "claude"); err != nil {
		t.Fatal(err)
	}
	find(t, e.calls(), "podman stop filial-feat-agent-claude")

	e = setup(t)
	if err := e.l.AgentStop(ctx, "feat", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.out.String(), "no agent is running in feat") {
		t.Errorf("output %q", e.out.String())
	}
	for _, c := range e.calls() {
		if j := joined(c); strings.Contains(j, " stop ") {
			t.Errorf("stopped something: %s", j)
		}
	}
}
