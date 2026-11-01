package lifecycle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
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
	if err := workspace.Lock(ctx, e.ws, workspace.Accept{All: true}); err != nil {
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
	if err := e.l.Agent(ctx, "feat", "claude", []string{"--version"}, false); err != nil {
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
		"--rm", "-i", "--init", "--preserve-fds 1",
		"--name filial-feat-agent-claude",
		"--entrypoint sh",
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
	tail := hangup([]string{"/usr/local/bin/entrypoint-claude.sh", "--version"})[1:]
	if !strings.HasPrefix(c[len(c)-len(tail)-1], "localhost/filial/agent-claude:") || !slices.Equal(c[len(c)-len(tail):], tail) {
		t.Errorf("argv does not end with the claude image, the entrypoint and --version: %q", c[len(c)-len(tail)-1:])
	}
	// What the container runs, with a stand-in entrypoint: it gets exactly
	// the agent's arguments, never hangup's sh -c.
	ep := filepath.Join(t.TempDir(), "entrypoint")
	if err := os.WriteFile(ep, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := append([]string{}, tail...)
	run[3] = ep
	cmd := exec.Command("sh", run...)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cmd.ExtraFiles = []*os.File{r}                        // fd 3, as --preserve-fds 1 passes it
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // the watcher hangs up its group
	out, err := cmd.Output()
	w.Close()
	if err != nil || string(out) != "--version\n" {
		t.Errorf("the entrypoint got %q (%v); want --version alone", out, err)
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
	const join = "tmux -L agent new-session -A -s agent -e COLORTERM=truecolor -- probe"
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=inactive\n", unit)
	if err := e.l.Agent(ctx, "feat", "probe", nil, false); err != nil {
		t.Fatal(err)
	}
	calls := e.calls()
	wantInOrder(t, calls, "systemctl --user start filial-feat-rails.service",
		"systemctl --user start "+unit, "podman exec filial-feat-agent-probe tmux -L agent list-sessions")
	last := joined(calls[len(calls)-1])
	if !strings.HasPrefix(last, "podman exec -it --detach-keys= -e TERM=xterm-256color --preserve-fds 1 filial-feat-agent-probe sh -c ") || !strings.HasSuffix(last, join) {
		t.Errorf("last call %q; want the session joined or created", last)
	}
	if to := e.f.Cmds[len(e.f.Cmds)-1].Timeout; to != sys.NoTimeout {
		t.Errorf("attach timeout %v; want NoTimeout", to)
	}

	e.reset()
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=active\n", unit)
	if err := e.l.Agent(ctx, "feat", "probe", nil, false); err != nil {
		t.Fatal(err)
	}
	calls = e.calls()
	if n := count(calls, "systemctl --user start "+unit); n != 0 {
		t.Errorf("started an active unit:\n%s", dump(calls))
	}
	if last := joined(calls[len(calls)-1]); !strings.HasSuffix(last, join) {
		t.Errorf("last call %q; want the session joined", last)
	}

	// Arguments start a session: refused while one runs, its command without one.
	if err := e.l.Agent(ctx, "feat", "probe", []string{"x"}, false); err == nil || !strings.Contains(err.Error(), "has a session") {
		t.Errorf("argv with a live session: %v", err)
	}
	e.reset()
	e.f.On([]string{"podman", "exec", "filial-feat-agent-probe", "tmux", "-L", "agent", "has-session"}, sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit))
	if err := e.l.Agent(ctx, "feat", "probe", []string{"x", "y"}, false); err != nil {
		t.Fatal(err)
	}
	calls = e.calls()
	if last := joined(calls[len(calls)-1]); !strings.HasSuffix(last, "new-session -A -s agent -e COLORTERM=truecolor -- x y") {
		t.Errorf("last call %q; want a session running the arguments", last)
	}
}

func TestAgentNameChoice(t *testing.T) {
	e := setup(t)
	m, err := e.l.model(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// No name: the agent whose package is the built-in default (D140).
	if n, err := e.l.agentName(m, ""); err != nil || n != "claude" {
		t.Fatalf("two agents: got %q, %v; want claude, the built-in default", n, err)
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

// default = true in project.toml wins over the built-in default; with no
// mark and two agents of the built-in package, a name is needed (D140).
func TestAgentNameDefault(t *testing.T) {
	e := setup(t)
	e.editProject(t, func(s string) string {
		return strings.Replace(s, "use = \"local:probe@1\"\n", "use = \"local:probe@1\"\ndefault = true\n", 1)
	})
	m, err := e.l.model(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := e.l.agentName(m, ""); err != nil || n != "probe" {
		t.Fatalf("marked default: got %q, %v; want probe", n, err)
	}
	e = setup(t)
	e.editProject(t, func(s string) string {
		return withoutProbe(s) + "\n[agents.second]\nuse = \"claude@1\"\nrun = \"on-demand\"\n"
	})
	if m, err = e.l.model(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.l.agentName(m, ""); err == nil || !strings.Contains(err.Error(), "default = true") {
		t.Fatalf("two built-in defaults: got %v; want a name asked for", err)
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
	if n := count(e.calls(), "systemctl --user show -p LoadState,NeedDaemonReload,ActiveState,ExecStart,After filial-feat-agent-claude.service"); n != 0 {
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
	err := e.l.Agent(ctx, "feat", "claude", nil, false)
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

// An agent home is a dir that holds its login, not a volume: down keeps it.
func TestDownKeepsAgentHome(t *testing.T) {
	e := setup(t)
	if err := e.l.Down(ctx, "feat", true); err != nil {
		t.Fatal(err)
	}
	for _, c := range e.calls() {
		if l := strings.Join(c, " "); strings.Contains(l, "volume rm") && strings.Contains(l, "-home") {
			t.Errorf("down removed an agent home: %s", l)
		}
	}
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
		// A new home holds the request: it takes the default at its first start.
		{Name: "claude", Package: "claude@1", Run: "on-demand", State: "done", Message: "Ship it?", TS: 7, Reply: true, Config: true, DefaultPending: true},
		{Name: "probe", Package: "local:probe@1", Run: "service", State: "off"},
	}
	if !slices.Equal(rows, want) {
		t.Fatalf("rows %+v; want %+v", rows, want)
	}
}

// TestAgentDefault: promote copies the [config] paths of one worktree's
// home (no symlink, nothing else) into the project's default; apply, or
// promote --all, asks the other homes to take it at their next start.
func TestAgentDefault(t *testing.T) {
	e := setup(t)
	e.render(t)
	homes := filepath.Join(e.ws, ".railswitch", "agent-home")
	feat, master := filepath.Join(homes, "feat", "claude"), filepath.Join(homes, "master", "claude")
	for _, h := range []string{feat, master} {
		if err := os.Remove(filepath.Join(h, pack.AgentDefaultRequest)); err != nil {
			t.Fatalf("a new home should ask for the default: %v", err)
		}
	}
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(feat, ".claude", "settings.json"), `{"theme":"dark"}`)
	write(filepath.Join(feat, ".claude", "skills", "s", "SKILL.md"), "skill")
	write(filepath.Join(feat, ".claude", "history.jsonl"), "private")
	if err := os.Symlink("/etc/passwd", filepath.Join(feat, ".claude", "skills", "s", "leak")); err != nil {
		t.Fatal(err)
	}

	if _, _, err := e.l.ApplyAgentDefault(ctx, "feat", "claude", false); err == nil {
		t.Error("apply without a default")
	}
	if _, err := e.l.PromoteAgentDefault(ctx, "feat", "probe", false); err == nil {
		t.Error("promote of an agent without [config]")
	}
	// No name: the default agent, claude (D140).
	r, err := e.l.PromoteAgentDefault(ctx, "feat", "", true)
	if err != nil {
		t.Fatal(err)
	}
	def := filepath.Join(e.ws, ".railswitch", "agent-default", "claude")
	if data, err := os.ReadFile(filepath.Join(def, ".claude", "settings.json")); err != nil || string(data) != `{"theme":"dark"}` {
		t.Errorf("settings.json: %q %v", data, err)
	}
	for _, p := range []string{".claude/history.jsonl", ".claude/skills/s/leak"} {
		if _, err := os.Lstat(filepath.Join(def, p)); !os.IsNotExist(err) {
			t.Errorf("%s promoted: %v", p, err)
		}
	}
	if !slices.Equal(r.Requested, []string{"master"}) || !slices.Equal(r.Skipped, []string{".claude/skills/s/leak"}) {
		t.Errorf("result %+v", r)
	}
	if _, err := os.Stat(filepath.Join(master, pack.AgentDefaultRequest)); err != nil {
		t.Errorf("master not asked: %v", err)
	}
	rows, _ := e.l.AgentList(ctx, "master")
	if rows[0].Default == nil || rows[0].Default.From != "feat" {
		t.Errorf("agent ls default = %+v", rows[0].Default)
	}
	if rows, _ := e.l.AgentList(ctx, "feat"); rows[0].DefaultPending {
		t.Error("feat pending before apply")
	}
	if _, slugs, err := e.l.ApplyAgentDefault(ctx, "feat", "claude", false); err != nil || !slices.Equal(slugs, []string{"feat"}) {
		t.Errorf("apply: %v %v", slugs, err)
	}
	if rows, _ := e.l.AgentList(ctx, "feat"); !rows[0].DefaultPending {
		t.Error("feat not pending after apply")
	}

	// A FIFO an agent planted at the request's name fails at once.
	req := filepath.Join(master, pack.AgentDefaultRequest)
	if err := os.Remove(req); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(req, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := e.l.ApplyAgentDefault(ctx, "master", "claude", false)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("apply over a FIFO succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("apply blocked on a FIFO")
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

// T5: an on-demand run takes the package's [unit] exec (when no argv is
// given), shm_size and run_init, as its unit would.
func TestAgentOnDemandUnitSettings(t *testing.T) {
	e := setup(t)
	pkg := filepath.Join(e.ws, "agents", "probe", "agent.toml")
	data, err := os.ReadFile(pkg)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("[unit]\nexec = [\"probe\"]\n"), nil, 1)
	if err := os.WriteFile(pkg, append(data, "\n[unit]\nexec = [\"probe-run\", \"--all\"]\nshm_size = \"256mb\"\nrun_init = true\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	e.editProject(t, func(s string) string {
		return strings.Replace(s, "use = \"local:probe@1\"\nrun = \"service\"", "use = \"local:probe@1\"\nrun = \"on-demand\"", 1)
	})
	run := func(argv []string) []string {
		t.Helper()
		if err := e.l.Agent(ctx, "feat", "probe", argv, false); err != nil {
			t.Fatal(err)
		}
		calls := e.calls()
		i := -1
		for j, c := range calls {
			if strings.HasPrefix(joined(c), "podman --hooks-dir") {
				i = j
			}
		}
		if i < 0 {
			t.Fatalf("no podman run:\n%s", dump(calls))
		}
		return calls[i]
	}
	c := run(nil)
	if got := flagValues(c, "--shm-size"); !slices.Equal(got, []string{"256mb"}) {
		t.Errorf("--shm-size %q", got)
	}
	if !slices.Contains(c, "--init") {
		t.Errorf("argv lacks --init: %q", c)
	}
	if got := c[len(c)-2:]; !slices.Equal(got, []string{"probe-run", "--all"}) {
		t.Errorf("argv ends %q; want the package exec", got)
	}
	if c := run([]string{"--version"}); c[len(c)-1] != "--version" || slices.Contains(c, "probe-run") {
		t.Errorf("user argv: %q; want it instead of the package exec", c[len(c)-2:])
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
	err := e.l.Agent(ctx, "feat", "claude", nil, false)
	if err == nil || err.Error() != "agent probe is running in feat; one agent per worktree: railswitch agent stop probe" {
		t.Fatalf("got %v; want the one-agent refusal naming probe", err)
	}
	if n := count(e.calls(), "podman --hooks-dir"); n != 0 {
		t.Errorf("ran an agent:\n%s", dump(e.calls()))
	}

	e = setup(t)
	e.agentRunning("filial-master-agent-probe")
	if err := e.l.Agent(ctx, "feat", "claude", nil, false); err != nil {
		t.Fatal(err)
	}
	if n := count(e.calls(), "podman --hooks-dir"); n != 1 {
		t.Errorf("%d podman runs:\n%s", n, dump(e.calls()))
	}

	e = setup(t)
	e.agentRunning("filial-feat-agent-claude")
	err = e.l.Agent(ctx, "feat", "claude", nil, false)
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
	if err := e.l.Agent(ctx, "feat", "probe", nil, false); err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, e.calls(), "systemctl --user start "+unit, "podman exec filial-feat-agent-probe tmux -L agent list-sessions")
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

// Two agents running at once (a launch race, D62 has no lock): each is
// seen, stopped by name, or all stopped without one.
func TestAgentTwoRunning(t *testing.T) {
	e := setup(t)
	e.render(t)
	e.agentRunning("filial-feat-agent-probe")
	e.agentRunning("filial-feat-agent-claude")
	rows, err := e.l.AgentList(ctx, "feat")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || !rows[0].Running || !rows[1].Running {
		t.Errorf("rows %+v; want both running", rows)
	}
	err = e.l.Agent(ctx, "feat", "claude", nil, false)
	if err == nil || !strings.Contains(err.Error(), "railswitch agent stop probe") {
		t.Fatalf("got %v; want the refusal naming probe", err)
	}
	e.reset()
	if err := e.l.AgentStop(ctx, "feat", "probe"); err != nil {
		t.Fatal(err)
	}
	find(t, e.calls(), "systemctl --user stop filial-feat-agent-probe.service")
	if n := count(e.calls(), "podman stop"); n != 0 {
		t.Errorf("stopped claude too:\n%s", dump(e.calls()))
	}
	e.reset()
	if err := e.l.AgentStop(ctx, "feat", ""); err != nil {
		t.Fatal(err)
	}
	find(t, e.calls(), "systemctl --user stop filial-feat-agent-probe.service")
	find(t, e.calls(), "podman stop filial-feat-agent-claude")
}

// The same service agent already running: attach only, no start.
func TestAgentServiceRunningAttaches(t *testing.T) {
	e := setup(t)
	const unit = "filial-feat-agent-probe.service"
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=active\n", unit)
	e.agentRunning("filial-feat-agent-probe")
	if err := e.l.Agent(ctx, "feat", "probe", nil, false); err != nil {
		t.Fatal(err)
	}
	calls := e.calls()
	if n := count(calls, "systemctl --user start "+unit); n != 0 {
		t.Errorf("started the running agent:\n%s", dump(calls))
	}
	if last := joined(calls[len(calls)-1]); !strings.Contains(last, "tmux -L agent new-session -A -s agent") {
		t.Errorf("last call %q; want the session joined", last)
	}
}

// agent update rebuilds only the named agent's image, without the cache;
// the images it builds on exist and are not rebuilt (D138).
func TestAgentUpdate(t *testing.T) {
	e := setup(t)
	e.f.On([]string{"podman", "image", "inspect"}, sys.Result{Stdout: []byte("sha256:same\n")}, nil)
	if err := e.l.AgentUpdate(ctx, "feat", "claude"); err != nil {
		t.Fatal(err)
	}
	var builds []string
	for _, c := range e.calls() {
		if j := strings.Join(c, " "); strings.HasPrefix(j, "podman build") {
			builds = append(builds, j)
		}
	}
	if len(builds) != 1 || !strings.Contains(builds[0], "Containerfile.claude") || !strings.Contains(builds[0], "--no-cache") {
		t.Errorf("builds %q; want only the claude image, --no-cache", builds)
	}
	if n := containing(e.calls(), "podman rmi"); n != 0 {
		t.Error("removed an image the build didn't replace")
	}
	if err := e.l.AgentUpdate(ctx, "feat", "nope"); err == nil || !strings.Contains(err.Error(), "no agent nope") {
		t.Errorf("unknown agent: %v", err)
	}
}

// --no-token: the secret that delivers the package's token (by its env
// var, whatever it is called) is neither required nor passed; the agent's
// other secrets still are (D139). A service agent refuses it.
func TestAgentNoToken(t *testing.T) {
	e := setup(t)
	e.editProject(t, func(s string) string {
		return s + "\n[secrets.my_token]\nservices = [\"agent-claude\"]\nenv = \"CLAUDE_CODE_OAUTH_TOKEN\"\n" +
			"\n[secrets.other]\nservices = [\"agent-claude\"]\nenv = \"OTHER\"\n"
	})
	e.missing("podman", "secret", "exists", "filial_my_token")
	if err := e.l.Agent(ctx, "feat", "claude", nil, true); err != nil {
		t.Fatalf("agent --no-token with the token unset: %v", err)
	}
	i := slices.IndexFunc(e.calls(), func(c []string) bool { return strings.HasPrefix(joined(c), "podman --hooks-dir") })
	if i < 0 {
		t.Fatal("the agent didn't run")
	}
	j := joined(e.calls()[i])
	if strings.Contains(j, "my_token") || !strings.Contains(j, "filial_other,type=env,target=OTHER") {
		t.Errorf("secrets in %s", j)
	}
	if err := e.l.Agent(ctx, "feat", "probe", nil, true); err == nil || !strings.Contains(err.Error(), "runs as a service") {
		t.Errorf("service agent --no-token: %v", err)
	}
}

// D142: a service agent without a session gives way to another agent; one
// with a session doesn't (TestAgentRefusesOtherRunning).
func TestAgentStopsIdleServiceAgent(t *testing.T) {
	e := setup(t)
	e.agentRunning("filial-feat-agent-probe")
	e.f.On([]string{"podman", "exec", "filial-feat-agent-probe", "tmux", "-L", "agent", "has-session"}, sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit))
	if err := e.l.Agent(ctx, "feat", "claude", nil, false); err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, e.calls(), "systemctl --user stop filial-feat-agent-probe.service", "podman --hooks-dir")
	if msg := e.l.Err.(*bytes.Buffer).String(); !strings.Contains(msg, "stopping agent probe in feat: it has no session") {
		t.Errorf("stderr %q; want the stop named", msg)
	}
}

// An idle agent stays when the requested one can't start (its secret unset).
func TestAgentKeepsIdleAgentOnRefusal(t *testing.T) {
	e := setup(t)
	e.editProject(t, func(s string) string {
		return s + "\n[secrets.claude_token]\nservices = [\"agent-claude\"]\nenv = \"CLAUDE_CODE_OAUTH_TOKEN\"\n"
	})
	e.missing("podman", "secret", "exists", "filial_claude_token")
	e.agentRunning("filial-feat-agent-probe")
	e.f.On([]string{"podman", "exec", "filial-feat-agent-probe", "tmux", "-L", "agent", "has-session"}, sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit))
	if err := e.l.Agent(ctx, "feat", "claude", nil, false); err == nil || !strings.Contains(err.Error(), "claude_token") {
		t.Fatalf("got %v; want claude_token unset", err)
	}
	if n := count(e.calls(), "systemctl --user stop filial-feat-agent-probe.service"); n != 0 {
		t.Errorf("stopped the idle agent for an agent that can't start:\n%s", dump(e.calls()))
	}
}

// A session check that fails (not tmux's "no session") never counts as idle.
func TestAgentKeepsAgentWhenSessionUnknown(t *testing.T) {
	e := setup(t)
	e.agentRunning("filial-feat-agent-probe")
	e.f.On([]string{"podman", "exec", "filial-feat-agent-probe", "tmux", "-L", "agent", "has-session"}, sys.Result{ExitCode: 127}, fmt.Errorf("podman: %w (exit 127)", sys.ErrExit))
	if err := e.l.Agent(ctx, "feat", "claude", nil, false); err == nil || !strings.Contains(err.Error(), "can't tell") {
		t.Fatalf("got %v; want the check's failure", err)
	}
	if n := count(e.calls(), "systemctl --user stop filial-feat-agent-probe.service"); n != 0 {
		t.Errorf("stopped an agent whose session is unknown:\n%s", dump(e.calls()))
	}
}

// D142: up starts the default agent's container when it runs as a service
// and no agent runs in the worktree; railswitch agent's own up doesn't.
func TestUpStartsDefaultServiceAgent(t *testing.T) {
	var e *tenv
	fresh := func() {
		e = setup(t)
		e.editProject(t, func(s string) string {
			return strings.Replace(s, "use = \"local:probe@1\"\nrun = \"service\"", "use = \"local:probe@1\"\nrun = \"service\"\ndefault = true", 1)
		})
	}
	fresh()
	const unit = "filial-feat-agent-probe.service"
	started := func() bool {
		for _, c := range e.calls() {
			if j := joined(c); strings.HasPrefix(j, "systemctl --user start") && strings.Contains(j, unit) {
				return true
			}
		}
		return false
	}
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	if !started() {
		t.Errorf("up did not start %s:\n%s", unit, dump(e.calls()))
	}

	fresh()
	e.agentRunning("filial-feat-agent-claude")
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	if started() {
		t.Errorf("up started %s beside a running agent", unit)
	}

	fresh()
	if err := e.l.Agent(ctx, "feat", "claude", nil, false); err != nil {
		t.Fatal(err)
	}
	if started() {
		t.Errorf("agent claude's up started %s", unit)
	}
}

// D142: up never restarts a service agent with a session for an older
// unit (a terminal joining must not end the agent it joins); without one
// it does.
func TestUpKeepsStaleAgentWithSession(t *testing.T) {
	const unit = "filial-feat-agent-probe.service"
	for _, live := range []bool{true, false} {
		e := setup(t)
		upTwice(t, e)
		e.units(loadedClean)
		e.labels(t, "filial-feat-agent-probe")
		if !live {
			e.f.On([]string{"podman", "exec", "filial-feat-agent-probe", "tmux", "-L", "agent", "has-session"}, sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit))
		}
		if err := e.l.Up(ctx, "feat"); err != nil {
			t.Fatal(err)
		}
		restarted := count(e.calls(), "systemctl --user restart "+unit) > 0
		if restarted == live {
			t.Errorf("session %v: restarted %v\n%s", live, restarted, dump(e.calls()))
		}
		if warned := strings.Contains(e.l.Err.(*bytes.Buffer).String(), "keeps it while its agent has a session"); warned != live {
			t.Errorf("session %v: warned %v", live, warned)
		}
	}
}

// A session that starts after the idle check is seen before the stop.
func TestStopIdleAgentsRechecks(t *testing.T) {
	e := setup(t)
	m, err := e.l.model(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.l.stopIdleAgents(ctx, m, "feat", []string{"probe"}); err == nil || !strings.Contains(err.Error(), "agent probe is running") {
		t.Fatalf("got %v; want the one-agent refusal", err)
	}
	if n := count(e.calls(), "systemctl --user stop filial-feat-agent-probe.service"); n != 0 {
		t.Errorf("stopped an agent with a session")
	}
}

// has-session can't tell "no server" from "no session": a server that
// doesn't answer is an error, never idle.
func TestAgentSessionNeedsServer(t *testing.T) {
	e := setup(t)
	e.f.On([]string{"podman", "exec", "filial-feat-agent-probe", "tmux", "-L", "agent", "list-sessions"}, sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit))
	e.f.On([]string{"podman", "exec", "filial-feat-agent-probe", "tmux", "-L", "agent", "has-session"}, sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit))
	if live, err := e.l.agentSession(ctx, "filial-feat-agent-probe"); err == nil {
		t.Fatalf("live %v, no error; want the server's silence reported", live)
	}
}
