package cli

import (
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

func TestDBUsage(t *testing.T) {
	ws, _ := lockedWS(t)
	for _, args := range [][]string{{"db"}, {"db", "load"}, {"db", "dump", "x"}} {
		if code, _, _ := runIn(t, append([]string{"-C", ws, "-w", "feat"}, args...)...); code != 2 {
			t.Errorf("railswitch %s: exit %d; want 2", strings.Join(args, " "), code)
		}
	}
}
