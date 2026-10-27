package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/term"
)

func TestAgentArgs(t *testing.T) {
	e := &env{stderr: &bytes.Buffer{}}
	for _, c := range []struct {
		args []string
		name string
		argv []string
	}{
		{[]string{"--", "--version"}, "", []string{"--version"}},
		{[]string{"claude", "--", "-p", "a b"}, "claude", []string{"-p", "a b"}},
		{[]string{"claude"}, "claude", nil},
		{nil, "", nil},
		{[]string{"ls"}, "ls", nil},
		{[]string{"claude", "--", "x", "--", "y"}, "claude", []string{"x", "--", "y"}},
	} {
		name, argv, _, code, ok := agentArgs(e, c.args)
		if !ok || code != 0 || name != c.name || !slices.Equal(argv, c.argv) {
			t.Errorf("%q: name %q argv %q code %d ok %v; want %q %q", c.args, name, argv, code, ok, c.name, c.argv)
		}
	}
	for _, args := range [][]string{{"a", "b"}, {"a", "b", "--", "x"}, {"--bogus"}} {
		if _, _, _, code, ok := agentArgs(e, args); ok || code != 2 {
			t.Errorf("%q: code %d ok %v; want a usage error", args, code, ok)
		}
	}
}

func TestAgentLsJSON(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	if err := os.WriteFile(filepath.Join(ws, ".railswitch", "status", "feat", "claude.json"), []byte(`{"v":1,"state":"working","message":"editing","ts":42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runIn(t, "-C", ws, "-w", "feat", "--json", "agent", "ls")
	if code != 0 {
		t.Fatalf("agent ls: exit %d: %s", code, errs)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(rows) != 2 {
		t.Fatalf("rows %v", rows)
	}
	want := map[string]any{"name": "claude", "package": "claude@1", "run": "on-demand", "state": "working", "message": "editing", "ts": float64(42), "reply": false, "running": false}
	for k, v := range want {
		if rows[0][k] != v {
			t.Errorf("claude %s = %v; want %v", k, rows[0][k], v)
		}
	}
	if len(rows[0]) != len(want) {
		t.Errorf("claude fields %v; want exactly %v", rows[0], want)
	}
	if rows[1]["name"] != "probe" || rows[1]["run"] != "service" || rows[1]["state"] != "off" {
		t.Errorf("probe row %v", rows[1])
	}

	code, out, errs = runIn(t, "-C", ws, "-w", "feat", "agent", "ls")
	if code != 0 {
		t.Fatalf("agent ls: exit %d: %s", code, errs)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 || strings.Join(strings.Fields(lines[0]), " ") != "NAME PACKAGE RUN RUNNING STATE MESSAGE" ||
		strings.Join(strings.Fields(lines[1]), " ") != "claude claude@1 on-demand - working editing" {
		t.Errorf("text:\n%s", out)
	}
	if code, _, _ := runIn(t, "-C", ws, "-w", "feat", "agent", "ls", "--", "x"); code != 2 {
		t.Errorf("agent ls -- x: exit %d; want 2", code)
	}
}

// D62: the running agent is decided by podman, not by a status file.
func TestAgentLsRunning(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	f := fakeHost()
	f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", "filial-feat-agent-probe"}, sys.Result{Stdout: []byte("true\n")}, nil)
	code, out, errs := runWith(t, f, "-C", ws, "-w", "feat", "--json", "agent", "ls")
	if code != 0 {
		t.Fatalf("agent ls: exit %d: %s", code, errs)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(rows) != 2 || rows[0]["name"] != "claude" || rows[0]["running"] != false || rows[1]["name"] != "probe" || rows[1]["running"] != true {
		t.Errorf("rows %v", rows)
	}
	code, out, _ = runWith(t, f, "-C", ws, "-w", "feat", "agent", "ls")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if code != 0 || len(lines) != 3 || strings.Join(strings.Fields(lines[2]), " ") != "probe local:probe@1 service * running -" {
		t.Errorf("text:\n%s", out)
	}
}

func TestAgentStopCLI(t *testing.T) {
	ws, _ := lockedWS(t)
	code, out, errs := runIn(t, "-C", ws, "-w", "feat", "agent", "stop")
	if code != 0 || !strings.Contains(out, "no agent is running in feat") {
		t.Errorf("agent stop: exit %d out %q err %q", code, out, errs)
	}
	if code, _, _ := runIn(t, "-C", ws, "-w", "feat", "agent", "stop", "a", "b"); code != 2 {
		t.Errorf("agent stop a b: exit %d; want 2", code)
	}
	if code, _, _ := runIn(t, "-C", ws, "-w", "feat", "agent", "stop", "--", "x"); code != 2 {
		t.Errorf("agent stop -- x: exit %d; want 2", code)
	}
}

func TestAgentServiceArgvUsage(t *testing.T) {
	ws, _ := lockedWS(t)
	code, _, errs := runIn(t, "-C", ws, "-w", "feat", "agent", "probe", "--", "x")
	if code != 2 || !strings.Contains(errs, "[agents.probe]") {
		t.Fatalf("exit %d, stderr %q; want 2 naming [agents.probe]", code, errs)
	}
}

func TestCompleteAgent(t *testing.T) {
	ws, _ := lockedWS(t)
	t.Chdir(ws)
	if got, want := complete(t, "agent", ""), []string{"add", "claude", "ls", "probe", "stop"}; !slices.Equal(got, want) {
		t.Errorf("agent: %q; want %q", got, want)
	}
	if got := complete(t, "agent", "claude", ""); len(got) != 0 {
		t.Errorf("agent claude: %q; want nothing", got)
	}
	if got, want := complete(t, "agent", "stop", ""), []string{"claude", "probe"}; !slices.Equal(got, want) {
		t.Errorf("agent stop: %q; want %q", got, want)
	}
	if got := complete(t, "agent", "ls", ""); len(got) != 0 {
		t.Errorf("agent ls: %q; want nothing", got)
	}
}

func TestAgentNewTerm(t *testing.T) {
	ws, _ := lockedWS(t)
	var gotTitle, gotDir string
	var gotArgv []string
	oldExe, oldLaunch := executable, launchTerm
	t.Cleanup(func() { executable, launchTerm = oldExe, oldLaunch })
	executable = func() (string, error) { return "/bin/railswitch", nil }
	launchTerm = func(_ context.Context, _ *env, title, dir string, argv []string) error {
		gotTitle, gotDir, gotArgv = title, dir, argv
		return nil
	}
	code, _, errs := runIn(t, "-C", filepath.Join(ws, "feat"), "agent", "--new-term", "claude", "--", "-p", "x")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	want := []string{"/bin/railswitch", "-C", ws, "-w", "feat", "agent", "claude", "--", "-p", "x"}
	if !slices.Equal(gotArgv, want) {
		t.Errorf("argv %q; want %q", gotArgv, want)
	}
	if gotDir != filepath.Join(ws, "feat") || gotTitle == "" {
		t.Errorf("title %q dir %q", gotTitle, gotDir)
	}
}

func TestAgentNewTermExitCode(t *testing.T) {
	ws, _ := lockedWS(t)
	old := launchTerm
	t.Cleanup(func() { launchTerm = old })
	launchTerm = func(context.Context, *env, string, string, []string) error { return &term.ExitError{Code: 3} }
	code, _, errs := runIn(t, "-C", filepath.Join(ws, "feat"), "agent", "--new-term", "claude")
	if code != 3 || errs != "" {
		t.Fatalf("exit %d stderr %q; want 3, silent", code, errs)
	}
}

// B-M2: a bad name, or arguments for a service agent, fail in this terminal
// before any new one opens.
func TestAgentNewTermChecksFirst(t *testing.T) {
	ws, _ := lockedWS(t)
	old := launchTerm
	t.Cleanup(func() { launchTerm = old })
	launchTerm = func(context.Context, *env, string, string, []string) error {
		t.Error("terminal launched")
		return nil
	}
	if code, _, errs := runIn(t, "-C", filepath.Join(ws, "feat"), "agent", "--new-term", "clade"); code == 0 || !strings.Contains(errs, "clade") {
		t.Errorf("unknown agent: exit %d stderr %q", code, errs)
	}
	if code, _, errs := runIn(t, "-C", filepath.Join(ws, "feat"), "agent", "--new-term", "probe", "--", "x"); code != 2 || !strings.Contains(errs, "[agents.probe]") {
		t.Errorf("service agent argv: exit %d stderr %q; want a usage error", code, errs)
	}
}
