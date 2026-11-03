package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

// A snapshot is db dump into its own 0700 dir of 0600 files; ls lists it,
// restore puts it back, rm deletes it.
func TestDBSnapshotRoundTrip(t *testing.T) {
	e := setup(t)
	e.dbRunning("feat")
	e.f.On([]string{"podman", "exec", "filial-feat-db", "psql"}, sys.Result{Stdout: []byte("filial_dev\n")}, nil)
	e.f.On([]string{"podman", "exec", "filial-feat-db", "pg_dump"}, sys.Result{Stdout: []byte("PGDMP snap")}, nil)
	e.f.On([]string{"podman", "volume", "export"}, sys.Result{Stdout: []byte("tar bytes")}, nil)
	dir, err := e.l.DBSnapshot(ctx, "feat", "before-migrate")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(e.ws, ".railswitch", "snapshots", "feat", "before-migrate"); dir != want {
		t.Errorf("dir %s, want %s", dir, want)
	}
	for p, mode := range map[string]os.FileMode{dir: 0o700, filepath.Dir(dir): 0o700, filepath.Join(dir, "filial_dev.dump"): 0o600, filepath.Join(dir, "rustfs_data.tar.gz"): 0o600} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != mode {
			t.Errorf("%s: %v, %v; want %v", p, fi, err, mode)
		}
	}
	if !strings.Contains(e.out.String(), filepath.Join(dir, "filial_dev.dump")) || strings.Contains(e.out.String(), ".tmp-") {
		t.Errorf("output %q", e.out)
	}
	noTemps(t, filepath.Dir(dir))
	if _, err := e.l.DBSnapshot(ctx, "feat", "before-migrate"); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("second snapshot of one name: %v", err)
	}

	snaps, err := e.l.Snapshots("feat")
	if err != nil || len(snaps) != 1 || snaps[0].Name != "before-migrate" || !slices.Equal(snaps[0].Files, []string{"filial_dev.dump", "rustfs_data.tar.gz"}) || snaps[0].Bytes == 0 {
		t.Fatalf("ls = %+v, %v", snaps, err)
	}

	e.f.On([]string{"podman", "exec"}, sys.Result{}, nil)
	done, err := e.l.DBSnapshotRestore(ctx, "feat", "before-migrate", true)
	if err != nil || !slices.Equal(done, []string{"filial_dev", "the storage volume filial-feat-rustfs-data"}) {
		t.Fatalf("restore: %v, %v", done, err)
	}
	for i, c := range e.f.Calls {
		if slices.Contains(c, "pg_restore") && string(e.f.Stdins[i]) != "PGDMP snap" {
			t.Errorf("restored %q", e.f.Stdins[i])
		}
	}

	if err := e.l.DBSnapshotRemove("feat", "before-migrate"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("snapshot left: %v", err)
	}
	if snaps, err := e.l.Snapshots("feat"); err != nil || len(snaps) != 0 {
		t.Errorf("ls after rm = %+v, %v", snaps, err)
	}
}

// A failed dump leaves no snapshot and no temp dir.
func TestDBSnapshotFailure(t *testing.T) {
	e := setup(t)
	e.dbRunning("feat")
	e.f.On([]string{"podman", "exec", "filial-feat-db"}, sys.Result{ExitCode: 1}, fmt.Errorf("pg_dump: %w (exit 1)", sys.ErrExit))
	if _, err := e.l.DBSnapshot(ctx, "feat", "x"); !errors.Is(err, sys.ErrExit) {
		t.Fatalf("err = %v", err)
	}
	root := SnapshotsDir(e.ws, "feat")
	noTemps(t, root)
	if snaps, _ := e.l.Snapshots("feat"); len(snaps) != 0 {
		t.Errorf("ls = %+v", snaps)
	}
}

// Names are bare file names; no path, flag, dot file or subcommand word
// reaches the file system.
func TestDBSnapshotNames(t *testing.T) {
	e := setup(t)
	for _, n := range []string{"", "ls", "rm", "restore", "../x", "a/b", ".hidden", "-x", "a b", strings.Repeat("a", 65)} {
		if err := CheckSnapshotName(n); err == nil {
			t.Errorf("%q accepted", n)
		}
	}
	for _, n := range []string{"20261104-153012", "before_migrate.v2", "A"} {
		if err := CheckSnapshotName(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}
	if err := e.l.DBSnapshotRemove("feat", "../../db-dumps"); err == nil {
		t.Error("rm took a path")
	}
	if err := e.l.DBSnapshotRemove("feat", "nope"); err == nil || !strings.Contains(err.Error(), "no snapshot nope") {
		t.Errorf("rm unknown: %v", err)
	}
	// A symlink in the snapshots dir is no snapshot.
	root := SnapshotsDir(e.ws, "feat")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(e.ws, "db-dumps"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.l.DBSnapshotRestore(ctx, "feat", "link", true); err == nil {
		t.Error("restored through a symlink")
	}
	if snaps, _ := e.l.Snapshots("feat"); len(snaps) != 0 {
		t.Errorf("ls = %+v", snaps)
	}
}
