package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

func TestTaskLsJSON(t *testing.T) {
	ws, _ := lockedWS(t)
	path := filepath.Join(ws, "project.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, "\n[tasks]\nlint = \"bin/rubocop\"\n\"test:system\" = \"\"\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runIn(t, "-C", ws, "-w", "feat", "--json", "task", "ls")
	if code != 0 {
		t.Fatalf("task ls: exit %d: %s", code, errs)
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	want := []map[string]string{
		{"name": "console", "command": "bin/rails console", "source": "pack"},
		{"name": "lint", "command": "bin/rubocop", "source": "project"},
		{"name": "test", "command": "bin/rails test", "source": "pack"},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows %v; want %v", rows, want)
	}
	for i := range want {
		if len(rows[i]) != len(want[i]) {
			t.Errorf("row %d %v; want %v", i, rows[i], want[i])
		}
		for k, v := range want[i] {
			if rows[i][k] != v {
				t.Errorf("row %d %s = %q; want %q", i, k, rows[i][k], v)
			}
		}
	}
	code, out, _ = runIn(t, "-C", ws, "-w", "feat", "task", "ls")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if code != 0 || len(lines) != 4 || strings.Join(strings.Fields(lines[0]), " ") != "NAME SOURCE COMMAND" ||
		strings.Join(strings.Fields(lines[2]), " ") != "lint project bin/rubocop" {
		t.Errorf("text:\n%s", out)
	}
	if code, _, _ := runIn(t, "-C", ws, "-w", "feat", "task", "ls", "x"); code != 2 {
		t.Errorf("task ls x: exit %d; want 2", code)
	}
}

func TestTaskCLIArgv(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	f := fakeHost()
	f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", "filial-feat-rails"}, sys.Result{Stdout: []byte("true\n")}, nil)
	f.On([]string{"podman", "exec"}, sys.Result{ExitCode: 3}, sys.ErrExit)
	code, _, errs := runWith(t, f, "-C", ws, "-w", "feat", "task", "test", "-v", "a b")
	if code != 3 {
		t.Fatalf("exit %d: %s; want the task's 3", code, errs)
	}
	want := []string{"podman", "exec", "-i", "-w", "/app", "filial-feat-rails", "sh", "-c", `bin/rails test "$@"`, "test", "-v", "a b"}
	if got := f.Calls[len(f.Calls)-1]; !slices.Equal(got, want) {
		t.Errorf("argv %q; want %q", got, want)
	}
	// A -- after the name is dropped.
	if code, _, errs := runWith(t, f, "-C", ws, "-w", "feat", "task", "test", "--", "-x"); code != 3 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if got := f.Calls[len(f.Calls)-1]; !slices.Equal(got[len(got)-2:], []string{"test", "-x"}) {
		t.Errorf("argv %q; want it to end in test -x", got)
	}
}

func TestTaskUnknown(t *testing.T) {
	ws, _ := lockedWS(t)
	code, _, errs := runIn(t, "-C", ws, "-w", "feat", "task", "tset")
	if code != 2 || !strings.Contains(errs, "unknown task tset; railswitch task ls") {
		t.Errorf("exit %d stderr %q; want 2, unknown task", code, errs)
	}
	if code, _, _ := runIn(t, "-C", ws, "-w", "feat", "task"); code != 2 {
		t.Errorf("task: exit %d; want 2", code)
	}
}

func TestTaskNewTerm(t *testing.T) {
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
	code, _, errs := runIn(t, "-C", filepath.Join(ws, "feat"), "task", "--new-term", "test", "a")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	want := []string{"/bin/railswitch", "-C", ws, "-w", "feat", "task", "test", "a"}
	if !slices.Equal(gotArgv, want) {
		t.Errorf("argv %q; want %q", gotArgv, want)
	}
	if gotDir != filepath.Join(ws, "feat") || gotTitle != "feat test" {
		t.Errorf("title %q dir %q", gotTitle, gotDir)
	}

	// An unknown task fails here, before any terminal opens.
	gotArgv = nil
	if code, _, errs := runIn(t, "-C", filepath.Join(ws, "feat"), "task", "--new-term", "tset"); code != 2 || !strings.Contains(errs, "unknown task tset") || gotArgv != nil {
		t.Errorf("unknown task: exit %d stderr %q argv %q", code, errs, gotArgv)
	}
}

func TestCompleteTask(t *testing.T) {
	ws, _ := lockedWS(t)
	t.Chdir(ws)
	if got, want := complete(t, "task", ""), []string{"console", "ls", "test", "test:system"}; !slices.Equal(got, want) {
		t.Errorf("task: %q; want %q", got, want)
	}
	if got := complete(t, "task", "test", ""); len(got) != 0 {
		t.Errorf("task test: %q; want nothing", got)
	}
	if got, want := complete(t, "task", "--"), []string{"--new-term"}; !slices.Equal(got, want) {
		t.Errorf("task --: %q; want %q", got, want)
	}
	if got, want := complete(t, "pr", ""), []string{"ls"}; !slices.Equal(got, want) {
		t.Errorf("pr: %q; want %q", got, want)
	}
	if got, want := complete(t, "wt", "add", "--"), []string{"--name", "--up"}; !slices.Equal(got, want) {
		t.Errorf("wt add --: %q; want %q", got, want)
	}
}
