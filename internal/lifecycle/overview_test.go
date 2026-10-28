package lifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/unithash"
)

func TestStackState(t *testing.T) {
	row := func(role, state string) OverviewService {
		return OverviewService{Service: role, Role: role, State: state}
	}
	for _, c := range []struct {
		name string
		rows []OverviewService
		want string
	}{
		{"all active", []OverviewService{row("app", "active/running"), row("worker", "active/running"), row("data", "active/running")}, "up"},
		{"one activating", []OverviewService{row("app", "activating/start"), row("worker", "active/running"), row("data", "active/running")}, "partial"},
		{"one inactive", []OverviewService{row("app", "inactive/dead"), row("data", "active/running")}, "partial"},
		{"none active", []OverviewService{row("app", "inactive/dead"), row("worker", "inactive/dead"), row("data", "inactive/dead")}, "stopped"},
		{"only activating", []OverviewService{row("app", "activating/start"), row("data", "inactive/dead")}, "partial"},
		{"any failed", []OverviewService{row("app", "active/running"), row("worker", "failed/failed"), row("data", "active/running")}, "failed"},
		{"other roles ignored", []OverviewService{
			row("app", "active/running"), row("deps", "failed/failed"), row("tool", "inactive/dead"),
			row("agent", "failed/failed"), row("agent", "on-demand"),
		}, "up"},
		{"no stack rows", []OverviewService{row("deps", "active/exited"), row("tool", "active/running")}, "stopped"},
		{"no rows", nil, "stopped"},
	} {
		if got := StackState(c.rows); got != c.want {
			t.Errorf("%s: StackState = %q; want %q", c.name, got, c.want)
		}
	}
}

// psJSON is `podman ps -a --format json` with each name running and no
// labels.
func psJSON(names ...string) string {
	var rows []string
	for _, n := range names {
		rows = append(rows, `{"Names":["`+n+`"],"State":"running","Labels":{}}`)
	}
	return "[" + strings.Join(rows, ",") + "]"
}

// overviewEnv is setup with each worktree's .git/HEAD on branch
// "jb/<slug>", every agent's status dir, and systemctl answering for every
// unit of the project: state(slug, service) is its "<Active>/<Sub>".
func overviewEnv(t *testing.T, state func(slug, svc string) string) (*tenv, *render.Model) {
	t.Helper()
	e := setup(t)
	for _, s := range []string{"master", "feat"} {
		if err := os.MkdirAll(filepath.Join(e.ws, s, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(e.ws, s, ".git", "HEAD"), []byte("ref: refs/heads/jb/"+s+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(e.ws, ".railswitch", "status", s), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	m, err := e.l.model(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, s := range []string{"master", "feat"} {
		for _, svc := range m.Services {
			if svc.OnDemand {
				continue
			}
			active, sub, _ := strings.Cut(state(s, svc.ID), "/")
			out.WriteString("Id=" + unitOf(m, s, svc.ID) + "\nActiveState=" + active + "\nSubState=" + sub + "\n\n")
		}
	}
	e.f.On([]string{"systemctl", "--user", "show"}, sys.Result{Stdout: []byte(out.String())}, nil)
	e.f.On([]string{"podman", "ps"}, sys.Result{Stdout: []byte(psJSON())}, nil)
	e.reset()
	return e, m
}

func (e *tenv) status(t *testing.T, slug, agent, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.ws, ".railswitch", "status", slug, agent+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOverview(t *testing.T) {
	e, m := overviewEnv(t, func(slug, svc string) string {
		if slug == "feat" && svc == "db" {
			return "failed/failed"
		}
		return "active/running"
	})
	e.status(t, "feat", "probe", `{"v":1,"state":"permission","message":"Bash: rm -rf tmp","ts":9}`)
	ov, err := e.l.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Prefix != "filial" || ov.Workspace != e.l.WS.Root {
		t.Errorf("prefix %q workspace %q", ov.Prefix, ov.Workspace)
	}
	if len(ov.Worktrees) != 2 {
		t.Fatalf("worktrees %+v", ov.Worktrees)
	}
	a, b := ov.Worktrees[0], ov.Worktrees[1]
	if a.Slug != "master" || a.ID != 1 || !a.Base || a.Branch != "jb/master" || a.Stack != "up" || a.Agent != nil {
		t.Errorf("master %+v", a)
	}
	if b.Slug != "feat" || b.ID != 2 || b.Base || b.Branch != "jb/feat" || b.Stack != "failed" {
		t.Errorf("feat %+v", b)
	}
	if b.Agent == nil || b.Agent.Name != "probe" || b.Agent.State != "permission" || b.Agent.Message != "Bash: rm -rf tmp" || b.Agent.Running {
		t.Errorf("feat agent %+v", b.Agent)
	}
	var services []string
	for _, s := range b.Services {
		services = append(services, s.Service+":"+s.Role+":"+s.State)
	}
	var want []string
	for _, s := range m.Services {
		st := "active/running"
		switch {
		case s.OnDemand:
			st = "on-demand"
		case s.ID == "db":
			st = "failed/failed"
		}
		want = append(want, s.ID+":"+s.Role+":"+st)
	}
	if !reflect.DeepEqual(services, want) {
		t.Errorf("feat services %q; want %q", services, want)
	}
	for i, w := range ov.Worktrees {
		in, err := m.Instance(m.Inputs.Worktrees[i])
		if err != nil {
			t.Fatal(err)
		}
		links, err := m.Links(in)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(w.Links, links) {
			t.Errorf("%s links %+v; want %+v", w.Slug, w.Links, links)
		}
	}
	if n := count(e.calls(), "systemctl"); n != 1 {
		t.Errorf("%d systemctl calls; want 1:\n%s", n, dump(e.calls()))
	}
	if n := count(e.calls(), "podman"); n != 1 {
		t.Errorf("%d podman calls; want 1:\n%s", n, dump(e.calls()))
	}
	find(t, e.calls(), "podman ps -a --format json")
	for _, c := range e.f.Calls {
		if c[0] == "git" {
			t.Errorf("git call %q", c)
		}
	}

	// --json is the documented shape; the text form a table plus links.
	if err := e.l.PrintOverview(ctx, true); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(e.out.Bytes(), &doc); err != nil {
		t.Fatalf("%v: %s", err, e.out)
	}
	wt := doc["worktrees"].([]any)[1].(map[string]any)
	for _, k := range []string{"slug", "id", "branch", "base", "stack", "services", "agent", "links", "stale"} {
		if _, ok := wt[k]; !ok {
			t.Errorf("worktree JSON lacks %q: %v", k, wt)
		}
	}
	if svc := wt["services"].([]any)[0].(map[string]any); len(svc) != 3 {
		t.Errorf("service JSON %v; want service, role, state", svc)
	}
	if doc["worktrees"].([]any)[0].(map[string]any)["agent"] != nil {
		t.Errorf("master agent JSON not null")
	}
	e.out.Reset()
	if err := e.l.PrintOverview(ctx, false); err != nil {
		t.Fatal(err)
	}
	text := e.out.String()
	for _, w := range []string{"WORKTREE", "BRANCH", "STACK", "AGENT", "master (base)", "jb/feat", "failed", "probe: permission", "\n  App ", "http://feat."} {
		if !strings.Contains(text, w) {
			t.Errorf("text lacks %q:\n%s", w, text)
		}
	}
}

func TestOverviewMissingDir(t *testing.T) {
	e, _ := overviewEnv(t, func(string, string) string { return "inactive/dead" })
	if err := os.RemoveAll(filepath.Join(e.ws, "feat")); err != nil {
		t.Fatal(err)
	}
	ov, err := e.l.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Worktrees) != 2 {
		t.Fatalf("worktrees %+v", ov.Worktrees)
	}
	b := ov.Worktrees[1]
	if b.Slug != "feat" || b.Branch != "" || b.Stack != "stopped" || len(b.Links) == 0 {
		t.Errorf("feat %+v", b)
	}
}

func TestOverviewAgentChoice(t *testing.T) {
	for _, c := range []struct {
		name        string
		running     []string // running containers
		statuses    map[string]string
		want        string // agent name; "" = nil
		state       string
		wantRunning bool
	}{
		{"running wins", []string{"filial-feat-agent-probe", "filial-master-agent-claude"},
			map[string]string{"claude": `{"v":1,"state":"question","message":"m","ts":1}`}, "probe", "off", true},
		{"first non-off", []string{"filial-master-agent-claude"},
			map[string]string{"claude": `{"v":1,"state":"off","message":"","ts":1}`, "probe": `{"v":1,"state":"working","message":"w","ts":2}`}, "probe", "working", false},
		{"first configured non-off", nil,
			map[string]string{"claude": `{"v":1,"state":"done","message":"ok","ts":1}`, "probe": `{"v":1,"state":"working","message":"w","ts":2}`}, "claude", "done", false},
		{"none", []string{"filial-feat-agent-claude-x"},
			map[string]string{"claude": `{"v":2,"state":"working","message":"","ts":1}`}, "", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, _ := overviewEnv(t, func(string, string) string { return "active/running" })
			e.f.On([]string{"podman", "ps"}, sys.Result{Stdout: []byte(psJSON(c.running...))}, nil)
			for name, body := range c.statuses {
				e.status(t, "feat", name, body)
			}
			ov, err := e.l.Overview(ctx)
			if err != nil {
				t.Fatal(err)
			}
			got := ov.Worktrees[1].Agent
			switch {
			case c.want == "" && got != nil:
				t.Errorf("agent %+v; want nil", got)
			case c.want != "" && (got == nil || got.Name != c.want || got.State != c.state || got.Running != c.wantRunning):
				t.Errorf("agent %+v; want %s %s running=%v", got, c.want, c.state, c.wantRunning)
			}
		})
	}
}

func TestOverviewOnDemandRunning(t *testing.T) {
	e, _ := overviewEnv(t, func(string, string) string { return "active/running" })
	e.f.On([]string{"podman", "ps"}, sys.Result{Stdout: []byte(psJSON("filial-feat-agent-claude"))}, nil)
	ov, err := e.l.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"on-demand", "running"} {
		for _, s := range ov.Worktrees[i].Services {
			if s.Service == "agent-claude" && s.State != want {
				t.Errorf("%s agent-claude state %q; want %q", ov.Worktrees[i].Slug, s.State, want)
			}
		}
	}
	if a := ov.Worktrees[1].Agent; a == nil || a.Name != "claude" || !a.Running {
		t.Errorf("feat agent %+v; want claude running", a)
	}
}

// TestOverviewTextPrintable: a branch a container wrote reaches the text
// table without its control characters; --json keeps it as it is.
func TestOverviewTextPrintable(t *testing.T) {
	e, _ := overviewEnv(t, func(string, string) string { return "active/running" })
	if err := os.WriteFile(filepath.Join(e.ws, "feat", ".git", "HEAD"), []byte("ref: refs/heads/jb/\x1b[31mred\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.l.PrintOverview(ctx, false); err != nil {
		t.Fatal(err)
	}
	if text := e.out.String(); strings.Contains(text, "\x1b") || !strings.Contains(text, "jb/ [31mred") {
		t.Errorf("text:\n%q", text)
	}
	e.out.Reset()
	if err := e.l.PrintOverview(ctx, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.out.String(), `jb/\u001b[31mred`) {
		t.Errorf("JSON lost the raw branch: %s", e.out)
	}
}

func TestOverviewStale(t *testing.T) {
	e, m := overviewEnv(t, func(string, string) string { return "active/running" })
	e.render(t)
	key := unithash.Key(m.Prefix)
	app := m.Services[0].ID
	if m.Services[0].OnDemand {
		t.Fatal("first service is on-demand")
	}
	ctr := func(slug string) string { return containerOf(m, slug, app) }
	data, err := os.ReadFile(filepath.Join(e.l.WS.Dirs.Quadlet, ctr("feat")+".container"))
	if err != nil {
		t.Fatal(err)
	}
	cur := unithash.Read(data, key)
	if cur == "" {
		t.Fatal("rendered unit has no hash label")
	}
	row := func(name, state, hash string) string {
		return `{"Names":["` + name + `"],"State":"` + state + `","Labels":{"` + key + `":"` + hash + `"}}`
	}
	for _, c := range []struct {
		name         string
		ps           string
		master, feat bool
	}{
		{"old hash", "[" + row(ctr("feat"), "running", "old") + "]", false, true},
		{"no label", "[" + row(ctr("master"), "running", "") + "]", true, false},
		{"matching hash", "[" + row(ctr("feat"), "running", cur) + "]", false, false},
		{"old hash but not running", "[" + row(ctr("feat"), "exited", "old") + "]", false, false},
		{"not listed", "[]", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e.reset()
			e.f.On([]string{"podman", "ps"}, sys.Result{Stdout: []byte(c.ps)}, nil)
			ov, err := e.l.Overview(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if a, b := ov.Worktrees[0].Stale, ov.Worktrees[1].Stale; a != c.master || b != c.feat {
				t.Errorf("stale master=%v feat=%v; want %v %v", a, b, c.master, c.feat)
			}
			if n := count(e.calls(), "podman"); n != 1 {
				t.Errorf("%d podman calls; want 1:\n%s", n, dump(e.calls()))
			}
		})
	}
}

// review_pending says whether a package update waits for review.
func TestOverviewReviewPending(t *testing.T) {
	e, _ := overviewEnv(t, func(string, string) string { return "inactive/dead" })
	if err := e.l.PrintOverview(ctx, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.out.String(), `"review_pending":false`) {
		t.Errorf("accepted lock: %s", e.out)
	}
	e.out.Reset()
	e.l.WS.Lock.Options["redis"] = config.LockEntry{Version: "1", Source: "built in", SHA256: strings.Repeat("0", 64)}
	if err := e.l.PrintOverview(ctx, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.out.String(), `"review_pending":true`) {
		t.Errorf("waiting update: %s", e.out)
	}
}
