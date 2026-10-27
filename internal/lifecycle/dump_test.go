package lifecycle

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
)

// dbRunning makes filial-<slug>-db report running.
func (e *tenv) dbRunning(slug string) {
	e.f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", "filial-" + slug + "-db"}, sys.Result{Stdout: []byte("true\n")}, nil)
}

// noTemps fails when a .tmp- file is left in dir.
func noTemps(t *testing.T, dir string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
}

func TestDBDump(t *testing.T) {
	e := setup(t)
	e.dbRunning("feat")
	e.f.On([]string{"podman", "exec", "filial-feat-db"}, sys.Result{Stdout: []byte("PGDMP\x00dump")}, nil)
	e.f.On([]string{"podman", "volume", "export"}, sys.Result{Stdout: []byte("tar bytes")}, nil)
	paths, err := e.l.DBDump(ctx, "feat")
	if err != nil {
		t.Fatal(err)
	}
	dumps := filepath.Join(e.ws, "db-dumps")
	dump, seed := filepath.Join(dumps, "filial_dev.dump"), filepath.Join(dumps, "rustfs_data.tar.gz")
	if len(paths) != 2 || paths[0] != dump || paths[1] != seed {
		t.Errorf("paths = %v", paths)
	}
	got, err := os.ReadFile(dump)
	if err != nil || string(got) != "PGDMP\x00dump" {
		t.Errorf("dump = %q, %v", got, err)
	}
	if fi, err := os.Stat(dump); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("dump mode: %v, %v", fi, err)
	}
	wantInOrder(t, e.calls(), "podman exec filial-feat-db pg_dump -Fc -U postgres filial_dev", "podman volume export filial-feat-")
	exp := find(t, e.calls(), "podman volume export")
	if len(exp) != 4 || !strings.Contains(exp[3], "rustfs") {
		t.Errorf("export call = %v", exp)
	}
	f, err := os.Open(seed)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := io.ReadAll(zr); err != nil || string(b) != "tar bytes" {
		t.Errorf("seed = %q, %v", b, err)
	}
	if fi, err := os.Stat(seed); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("seed mode: %v, %v", fi, err)
	}
	for _, c := range e.f.Cmds {
		if c.Name == "podman" && len(c.Args) > 0 && c.Args[0] == "exec" && c.Timeout != dumpTimeout {
			t.Errorf("pg_dump timeout = %v", c.Timeout)
		}
	}
	for _, p := range paths {
		if !strings.Contains(e.out.String(), p) {
			t.Errorf("output lacks %s:\n%s", p, e.out)
		}
	}
	noTemps(t, dumps)
}

func TestDBDumpFailureKeepsOld(t *testing.T) {
	e := setup(t)
	e.dbRunning("feat")
	dumps := filepath.Join(e.ws, "db-dumps")
	old := filepath.Join(dumps, "filial_dev.dump")
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.f.On([]string{"podman", "exec", "filial-feat-db"}, sys.Result{Stdout: []byte("partial"), ExitCode: 1}, fmt.Errorf("pg_dump: %w (exit 1)", sys.ErrExit))
	if _, err := e.l.DBDump(ctx, "feat"); !errors.Is(err, sys.ErrExit) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(old); !bytes.Equal(got, []byte("old")) {
		t.Errorf("old dump changed: %q", got)
	}
	noTemps(t, dumps)
	if count(e.calls(), "podman volume export") != 0 {
		t.Errorf("volume exported after a failed dump:\n%s", dump(e.calls()))
	}
}

func TestDBDumpNotRunning(t *testing.T) {
	e := setup(t)
	_, err := e.l.DBDump(ctx, "feat")
	if err == nil || !strings.Contains(err.Error(), "filial-feat-db is not running: railswitch up") {
		t.Fatalf("err = %v", err)
	}
}

func TestDBDumpNoOption(t *testing.T) {
	e := setup(t)
	p := filepath.Join(e.ws, "project.toml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.Replace(b, []byte(`database = "postgres@1"`), []byte(`database = "none"`), 1)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := workspace.Lock(ctx, e.ws); err != nil {
		t.Fatal(err)
	}
	if e.l.WS, err = workspace.Load(ctx, e.ws, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.l.DBDump(ctx, "feat"); err == nil || !strings.Contains(err.Error(), "database") {
		t.Fatalf("err = %v", err)
	}
}
