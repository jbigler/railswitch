package lifecycle

import (
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

// withMemory turns [memory] on in the fixture and renders it.
func (e *tenv) withMemory(t *testing.T) {
	t.Helper()
	e.editProject(t, func(s string) string { return s + "\n[memory]\n" })
	e.render(t)
	e.reset()
}

func TestMemoryUpStartsAndCreates(t *testing.T) {
	e := setup(t)
	e.withMemory(t)
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=inactive\n")
	e.f.On([]string{"podman", "exec", "filial_memory", "dolt", "sql"}, sys.Result{Stdout: []byte(`{"rows":[{"Database":"information_schema"},{"Database":"mysql"}]}`)}, nil)
	m, err := e.l.model(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.l.MemoryUp(ctx, m); err != nil {
		t.Fatal(err)
	}
	calls := e.calls()
	wantInOrder(t, calls,
		"systemctl --user daemon-reload", // the loaded unit has no hash yet
		"systemctl --user start filial-memory.service",
		"podman exec filial_memory dolt sql -r json -q SHOW DATABASES",
		"podman run --rm --interactive --network filial_memory")
	run := find(t, calls, "podman run --rm --interactive --network filial_memory")
	j := " " + joined(run) + " "
	for _, want := range []string{
		" --read-only ", " --cap-drop all ", " --security-opt no-new-privileges ",
		" --userns keep-id:uid=1000,gid=1000 ", " --entrypoint bd ",
		" --env BEADS_DIR=/tmp/.beads ", " --env BEADS_DOLT_SERVER_HOST=memory ", " --env BD_DISABLE_METRICS=1 ",
		" init --server --stealth --non-interactive --skip-hooks --skip-agents -p filial --database beads ",
	} {
		if !strings.Contains(j, want) {
			t.Errorf("bd init run lacks %q:\n%s", want, j)
		}
	}
	if strings.Contains(j, "--publish") || strings.Contains(j, "filial_proxy") || strings.Contains(j, "-dev ") {
		t.Errorf("bd init joins more than the memory network:\n%s", j)
	}
}

func TestMemoryUpKeepsDatabase(t *testing.T) {
	e := setup(t)
	e.withMemory(t)
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=inactive\n")
	e.f.On([]string{"podman", "exec", "filial_memory", "dolt", "sql"}, sys.Result{Stdout: []byte(`{"rows":[{"Database":"beads"},{"Database":"mysql"}]}`)}, nil)
	m, err := e.l.model(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.l.MemoryUp(ctx, m); err != nil {
		t.Fatal(err)
	}
	if n := count(e.calls(), "podman run"); n != 0 {
		t.Errorf("%d bd runs with the database there:\n%s", n, dump(e.calls()))
	}
}

// An agent of a [memory] project starts the server first; without
// [memory] nothing of it runs.
func TestAgentStartsMemory(t *testing.T) {
	e := setup(t)
	e.withMemory(t)
	e.f.On([]string{"podman", "exec", "filial_memory", "dolt", "sql"}, sys.Result{Stdout: []byte(`{"rows":[{"Database":"beads"}]}`)}, nil)
	if err := e.l.Agent(ctx, "feat", "claude", []string{"--version"}); err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, e.calls(), "systemctl --user start filial-memory.service", "podman --hooks-dir")
	run := find(t, e.calls(), "podman --hooks-dir")
	if !slices.Contains(run, "filial_memory") {
		t.Errorf("the agent's run doesn't join filial_memory:\n%s", joined(run))
	}

	e = setup(t)
	if err := e.l.Agent(ctx, "feat", "claude", []string{"--version"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range e.calls() {
		if j := joined(c); strings.Contains(j, "filial_memory") || strings.Contains(j, "filial-memory") {
			t.Errorf("no [memory]: %s", joined(c))
		}
	}
}

// The report never starts the server, nor runs bd while it is stopped.
func TestMemoryReportStopped(t *testing.T) {
	e := setup(t)
	e.withMemory(t)
	e.units("LoadState=loaded\nActiveState=inactive\n")
	r, err := e.l.Memory(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Server.State != "inactive" || r.Issues != nil || len(r.Worktrees) != 2 || r.Worktrees[0].Ready == nil {
		t.Errorf("report %+v", r)
	}
	for _, c := range e.calls() {
		if c[0] == "podman" || slices.Contains(c, "start") {
			t.Errorf("a stopped server's report ran %s", joined(c))
		}
	}
}

func TestMemoryNoMemory(t *testing.T) {
	e := setup(t)
	if _, err := e.l.Memory(ctx, 0); err == nil || !strings.Contains(err.Error(), "add [memory]") {
		t.Errorf("Memory without [memory]: %v", err)
	}
}

func TestMemoryFill(t *testing.T) {
	r := &MemoryReport{Worktrees: []MemoryWorktree{{Slug: "main"}, {Slug: "feat"}}, Cursor: 4}
	out := strings.Join([]string{
		`status {"schema_version":1,"data":{"summary":{"total_issues":3,"open_issues":2,"in_progress_issues":1,"blocked_issues":1,"ready_issues":1,"closed_issues":0}}}`,
		`memories {"schema_version":1,"data":{"podman":"use podman","no-phi":"never"}}`,
		`ready {"schema_version":1,"data":[{"id":"d-1","title":"A","status":"open","labels":["worktree:main"]},{"id":"d-9","title":"unlabelled","status":"open"}]}`,
		`claimed {"schema_version":1,"data":[{"id":"d-2","title":"B","status":"in_progress","assignee":"feat/shell","labels":["x","worktree:feat"]}]}`,
		`blocked {"schema_version":1,"data":[{"id":"d-3","title":"C","status":"open","labels":["worktree:main","worktree:feat"]}]}`,
		`activity [{"seq":5,"ts":"t","op":"create","issue_id":"d-1","actor":"main/shell"},{"seq":7,"op":"claim","issue_id":"d-2","actor":"railswitch"}]`,
	}, "\n") + "\n"
	if err := r.fill([]byte(out)); err != nil {
		t.Fatal(err)
	}
	if c := r.Issues; c == nil || c.Total != 3 || c.InProgress != 1 || r.Memories != 2 {
		t.Errorf("counts %+v memories %d", r.Issues, r.Memories)
	}
	ids := func(is []MemoryIssue) (out []string) {
		for _, i := range is {
			out = append(out, i.ID)
		}
		return out
	}
	main, feat := r.Worktrees[0], r.Worktrees[1]
	if !slices.Equal(ids(main.Ready), []string{"d-1"}) || !slices.Equal(ids(feat.Claimed), []string{"d-2"}) ||
		!slices.Equal(ids(main.Blocked), []string{"d-3"}) || !slices.Equal(ids(feat.Blocked), []string{"d-3"}) || len(feat.Ready) != 0 {
		t.Errorf("worktrees %+v", r.Worktrees)
	}
	if len(r.Activity) != 2 || r.Activity[0].Worktree != "main" || r.Activity[0].Agent != "shell" || r.Activity[1].Worktree != "" || r.Cursor != 7 {
		t.Errorf("activity %+v cursor %d", r.Activity, r.Cursor)
	}

	r = &MemoryReport{}
	err := r.fill([]byte("status null\nmemories {\"data\":{}}\nready not-json\n"))
	if err == nil || !strings.Contains(err.Error(), "status, ready, claimed, blocked, activity") {
		t.Errorf("fill of failures: %v", err)
	}
}

func TestSchemaVersion(t *testing.T) {
	for in, want := range map[string]int{`{"rows": [{"v":"66"}]}`: 66, `{"rows":[{"v":7}]}`: 7} {
		if got, err := schemaVersion([]byte(in)); err != nil || got != want {
			t.Errorf("schemaVersion(%s) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := schemaVersion([]byte(`{"rows":[]}`)); err == nil {
		t.Error("no rows: no error")
	}
}
