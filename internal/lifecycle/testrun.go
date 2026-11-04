package lifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/tomlfile"
	"github.com/jbigler/railswitch/internal/workspace"
)

// TestTask is the task whose runs railswitch records (D158).
const TestTask = "test"

// TestRun is the last run of a worktree's test task without arguments, as
// railswitch saw its own child end (D158).
type TestRun struct {
	ExitCode  int       `json:"exit_code"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Commit    string    `json:"commit"` // HEAD at the start; "" when unreadable
}

// TestStatus is a worktree's tests in the overview: State never, passed or
// failed; Stale when HEAD is another commit than the run's.
type TestStatus struct {
	State string   `json:"state"`
	Stale bool     `json:"stale"`
	Last  *TestRun `json:"last"` // nil: never run
}

// testRunPath is where slug's last test run is kept: under the workspace's
// .railswitch, which no container mounts (D158).
func testRunPath(ws, slug string) string {
	return filepath.Join(ws, ".railswitch", "tests", slug, "last.json")
}

func writeTestRun(ws, slug string, run TestRun) error {
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	p := testRunPath(ws, slug)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return tomlfile.WriteAtomic(p, append(data, '\n'), 0o600)
}

// testStatus is worktree slug's TestStatus, its checkout at dir; a record
// that can't be read counts as none.
func testStatus(ws, slug, dir string) *TestStatus {
	st := &TestStatus{State: "never"}
	data, err := safefile.Read(testRunPath(ws, slug), safefile.Limit)
	if err != nil {
		return st
	}
	var run TestRun
	if json.Unmarshal(data, &run) != nil {
		return st
	}
	st.Last, st.State = &run, "passed"
	if run.ExitCode != 0 {
		st.State = "failed"
	}
	head := workspace.HeadCommit(dir)
	st.Stale = run.Commit != "" && head != "" && head != run.Commit
	return st
}
