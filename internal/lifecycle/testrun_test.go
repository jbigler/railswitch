package lifecycle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

// TestTaskRecordsTests: a whole-suite run of the test task records its
// own exit code and the HEAD it started on; a run with arguments, another
// task or a cancelled run records nothing (D158).
func TestTaskRecordsTests(t *testing.T) {
	e := setup(t)
	ctr := "filial-feat-rails"
	e.f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", ctr}, sys.Result{Stdout: []byte("true\n")}, nil)
	head := strings.Repeat("c", 40)
	if err := os.MkdirAll(filepath.Join(e.ws, "worktrees", "feat", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.ws, "worktrees", "feat", ".git", "HEAD"), []byte(head+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.ws, "worktrees", "feat")
	rec := testRunPath(e.ws, "feat")

	if err := e.l.Task(ctx, "feat", "test", []string{"test/one_test.rb"}); err != nil {
		t.Fatal(err)
	}
	if err := e.l.Task(ctx, "feat", "console", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rec); !os.IsNotExist(err) {
		t.Fatalf("a partial run or another task recorded: %v", err)
	}
	if st := testStatus(e.ws, "feat", dir); st.State != "never" || st.Last != nil {
		t.Errorf("never run: %+v", st)
	}

	before := time.Now().UTC()
	if err := e.l.Task(ctx, "feat", "test", nil); err != nil {
		t.Fatal(err)
	}
	st := testStatus(e.ws, "feat", dir)
	if st.State != "passed" || st.Stale || st.Last.ExitCode != 0 || st.Last.Commit != head || st.Last.StartedAt.Before(before.Truncate(time.Second)) || st.Last.EndedAt.Before(st.Last.StartedAt) {
		t.Errorf("passed run: %+v %+v", st, st.Last)
	}
	if fi, err := os.Stat(rec); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("record %v %v; want mode 0600", fi, err)
	}

	e.f.On([]string{"podman", "exec"}, sys.Result{ExitCode: 3}, fmt.Errorf("podman: %w (exit 3)", sys.ErrExit))
	if err := e.l.Task(ctx, "feat", "test", nil); err == nil {
		t.Fatal("failed run: no error")
	}
	if st := testStatus(e.ws, "feat", dir); st.State != "failed" || st.Last.ExitCode != 3 {
		t.Errorf("failed run: %+v", st)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte(strings.Repeat("d", 40)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := testStatus(e.ws, "feat", dir); !st.Stale {
		t.Errorf("HEAD moved: not stale")
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_ = e.l.Task(cancelled, "feat", "test", nil)
	if st := testStatus(e.ws, "feat", dir); st.Last.ExitCode != 3 {
		t.Errorf("a cancelled run recorded: %+v", st.Last)
	}
}

func TestTestsCell(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	run := func(code int, ago time.Duration) *TestRun { return &TestRun{ExitCode: code, EndedAt: now.Add(-ago)} }
	for _, c := range []struct {
		st   *TestStatus
		want string
	}{
		{nil, "-"},
		{&TestStatus{State: "never"}, "never"},
		{&TestStatus{State: "passed", Last: run(0, 5*time.Minute)}, "passed 5m ago"},
		{&TestStatus{State: "failed", Last: run(1, 3*time.Hour), Stale: true}, "failed (exit 1) 3h ago (stale)"},
		{&TestStatus{State: "passed", Last: run(0, 10*time.Second)}, "passed just now"},
		{&TestStatus{State: "passed", Last: run(0, 72*time.Hour)}, "passed 3d ago"},
	} {
		if got := testsCell(c.st, now); got != c.want {
			t.Errorf("testsCell(%+v) = %q; want %q", c.st, got, c.want)
		}
	}
}
