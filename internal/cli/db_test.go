package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

func TestDBDumpCommand(t *testing.T) {
	ws, _ := lockedWS(t)
	f := fakeHost()
	f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", "filial-feat-db"}, sys.Result{Stdout: []byte("true\n")}, nil)
	f.On([]string{"podman", "exec", "filial-feat-db"}, sys.Result{Stdout: []byte("PGDMP")}, nil)
	f.On([]string{"podman", "exec", "filial-feat-db", "psql"}, sys.Result{Stdout: []byte("filial_dev\n")}, nil)
	code, out, errs := runWith(t, f, "-C", ws, "-w", "feat", "db", "dump")
	if code != 0 {
		t.Fatalf("db dump: exit %d: %s", code, errs)
	}
	dump := filepath.Join(ws, "db-dumps", "filial_dev.dump")
	if b, err := os.ReadFile(dump); err != nil || string(b) != "PGDMP" {
		t.Errorf("dump = %q, %v", b, err)
	}
	if !strings.Contains(out, dump) {
		t.Errorf("out %q lacks %s", out, dump)
	}
}

// db snapshot <name> dumps into the worktree's snapshot dir; --json db
// snapshot ls lists it; rm deletes it.
func TestDBSnapshotCommand(t *testing.T) {
	ws, _ := lockedWS(t)
	f := fakeHost()
	f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", "filial-feat-db"}, sys.Result{Stdout: []byte("true\n")}, nil)
	f.On([]string{"podman", "exec", "filial-feat-db"}, sys.Result{Stdout: []byte("PGDMP")}, nil)
	f.On([]string{"podman", "exec", "filial-feat-db", "psql"}, sys.Result{Stdout: []byte("filial_dev\n")}, nil)
	if code, _, errs := runWith(t, f, "-C", ws, "-w", "feat", "db", "snapshot", "s1"); code != 0 {
		t.Fatalf("db snapshot: exit %d: %s", code, errs)
	}
	if b, err := os.ReadFile(filepath.Join(ws, ".railswitch", "snapshots", "feat", "s1", "filial_dev.dump")); err != nil || string(b) != "PGDMP" {
		t.Errorf("snapshot dump = %q, %v", b, err)
	}
	code, out, errs := runWith(t, f, "--json", "-C", ws, "-w", "feat", "db", "snapshot", "ls")
	var snaps []struct {
		Name  string   `json:"name"`
		Bytes int64    `json:"bytes"`
		Files []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &snaps); code != 0 || err != nil || len(snaps) != 1 || snaps[0].Name != "s1" || snaps[0].Bytes <= 5 || len(snaps[0].Files) != 2 {
		t.Fatalf("ls: exit %d %q %v %s", code, out, err, errs)
	}
	if code, out, _ := runWith(t, f, "-C", ws, "-w", "feat", "db", "snapshot", "ls"); code != 0 || !strings.Contains(out, "NAME") || !strings.Contains(out, "s1 ") {
		t.Errorf("text ls: %d %q", code, out)
	}
	if code, _, errs := runWith(t, f, "-C", ws, "-w", "feat", "db", "snapshot", "rm", "s1"); code != 0 {
		t.Fatalf("rm: exit %d %s", code, errs)
	}
	if _, err := os.Stat(filepath.Join(ws, ".railswitch", "snapshots", "feat", "s1")); err == nil {
		t.Error("s1 left after rm")
	}
}

func TestDBUsage(t *testing.T) {
	ws, _ := lockedWS(t)
	for _, args := range [][]string{{"db"}, {"db", "load"}, {"db", "dump", "x"}, {"db", "snapshot", "a", "b"}, {"db", "snapshot", "ls", "x"}, {"db", "snapshot", "rm"}, {"db", "snapshot", "restore"}} {
		if code, _, _ := runIn(t, append([]string{"-C", ws, "-w", "feat"}, args...)...); code != 2 {
			t.Errorf("railswitch %s: exit %d; want 2", strings.Join(args, " "), code)
		}
	}
}
