package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

// writeDumps puts two database dumps, a seed and a symlinked dump into the
// workspace's db-dumps/.
func writeDumps(t *testing.T, ws string) string {
	t.Helper()
	dir := filepath.Join(ws, "db-dumps")
	for name, data := range map[string]string{"filial_dev.dump": "PGDMP dev", "filial_queue.dump": "PGDMP queue", "rustfs_data.tar.gz": "tar"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "evil.dump")); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Each dump replaces its database: prepare, then restore with the file on
// stdin, in the running database container; a symlink is no dump.
func TestDBRestore(t *testing.T) {
	e := setup(t)
	e.dbRunning("feat")
	writeDumps(t, e.ws)
	e.f.On([]string{"podman", "exec"}, sys.Result{}, nil)
	done, err := e.l.DBRestore(ctx, "feat", "", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(done, []string{"filial_dev", "filial_queue"}) {
		t.Errorf("restored %v", done)
	}
	wantInOrder(t, e.calls(),
		"podman exec filial-feat-db sh -c dropdb --if-exists --force -U postgres \"$1\" && createdb -U postgres \"$1\" sh filial_dev",
		"podman exec -i filial-feat-db pg_restore -U postgres --no-owner --no-acl -d filial_dev",
		"podman exec filial-feat-db sh -c dropdb --if-exists --force -U postgres \"$1\" && createdb -U postgres \"$1\" sh filial_queue",
		"podman exec -i filial-feat-db pg_restore -U postgres --no-owner --no-acl -d filial_queue")
	var stdins []string
	for i, c := range e.f.Calls {
		if slices.Contains(c, "pg_restore") {
			stdins = append(stdins, string(e.f.Stdins[i]))
		}
	}
	if !slices.Equal(stdins, []string{"PGDMP dev", "PGDMP queue"}) {
		t.Errorf("stdins %q", stdins)
	}
	if n := count(e.calls(), "evil"); n != 0 {
		t.Error("a symlinked dump was restored")
	}
}

// Named databases limit it; a name with no dump is refused before anything
// runs; with no terminal and no --yes it refuses, naming the databases.
func TestDBRestoreRefusals(t *testing.T) {
	e := setup(t)
	e.dbRunning("feat")
	writeDumps(t, e.ws)
	e.f.On([]string{"podman", "exec"}, sys.Result{}, nil)
	if _, err := e.l.DBRestore(ctx, "feat", "", []string{"nope"}, true); err == nil || !strings.Contains(err.Error(), "no dump of nope") {
		t.Errorf("unknown db: %v", err)
	}
	if _, err := e.l.DBRestore(ctx, "feat", "", nil, false); err == nil || !strings.Contains(err.Error(), "pass --yes") || !strings.Contains(err.Error(), "filial_dev, filial_queue") {
		t.Errorf("no terminal: %v", err)
	}
	if n := count(e.calls(), "pg_restore"); n != 0 {
		t.Errorf("restored after a refusal")
	}
	done, err := e.l.DBRestore(ctx, "feat", "", []string{"filial_queue"}, true)
	if err != nil || !slices.Equal(done, []string{"filial_queue"}) || count(e.calls(), "-d filial_dev") != 0 {
		t.Errorf("only filial_queue: %v %v", done, err)
	}
}

func TestDBRestoreNotRunning(t *testing.T) {
	e := setup(t)
	e.f.On([]string{"podman", "container", "inspect"}, sys.Result{Stdout: []byte("false\n")}, nil)
	writeDumps(t, e.ws)
	if _, err := e.l.DBRestore(ctx, "feat", "", nil, true); err == nil || !strings.Contains(err.Error(), "is not running") {
		t.Errorf("err = %v", err)
	}
}

// When one database fails, the error says which were restored, which may
// be empty and which weren't touched; nothing runs for the rest.
func TestDBRestorePartialFailure(t *testing.T) {
	e := setup(t)
	e.dbRunning("feat")
	writeDumps(t, e.ws)
	e.f.On([]string{"podman", "exec", "-i", "filial-feat-db", "pg_restore", "-U", "postgres", "--no-owner", "--no-acl", "-d", "filial_dev"}, sys.Result{ExitCode: 1}, errors.New("exit 1"))
	e.f.On([]string{"podman", "exec"}, sys.Result{}, nil)
	_, err := e.l.DBRestore(ctx, "feat", "", nil, true)
	if err == nil || !strings.Contains(err.Error(), "filial_dev may be empty or partly restored") || !strings.Contains(err.Error(), "not touched: filial_queue") {
		t.Errorf("err = %v", err)
	}
	if count(e.calls(), "sh filial_queue") != 0 {
		t.Error("ran on after the failure")
	}
}

// A dump replaced by a symlink after the listing is refused when opened.
func TestOpenRegularRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "a.dump")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	if f, err := openRegular(link); err == nil {
		f.Close()
		t.Error("opened a symlink")
	}
}

// A new database with a dump waiting is filled before the app starts
// (D122): the database alone, its option's prepare and restore, then the
// app, whose own setup then runs on the restored data.
func TestUpRestoresNewDatabase(t *testing.T) {
	e := setup(t)
	e.missing("podman", "image", "exists")
	e.missing("podman", "volume", "exists")
	if err := os.WriteFile(filepath.Join(e.ws, "db-dumps", "filial_dev.dump"), []byte("PGDMP dev"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.f.On([]string{"podman", "exec"}, sys.Result{}, nil)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, e.calls(),
		"podman volume exists filial-feat-db-data-pg",
		"systemctl --user start filial-feat-db.service",
		"podman exec filial-feat-db sh -c dropdb",
		"podman exec -i filial-feat-db pg_restore -U postgres --no-owner --no-acl -d filial_dev",
		"systemctl --user start filial-feat-rails.service")
	if !strings.Contains(e.out.String(), "restoring filial_dev into the new database") {
		t.Errorf("output:\n%s", e.out)
	}
}

// An existing database is never restored into by up.
func TestUpKeepsExistingDatabase(t *testing.T) {
	e := setup(t)
	e.missing("podman", "image", "exists")
	if err := os.WriteFile(filepath.Join(e.ws, "db-dumps", "filial_dev.dump"), []byte("PGDMP dev"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.f.On([]string{"podman", "exec"}, sys.Result{}, nil)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	if n := count(e.calls(), "pg_restore"); n != 0 {
		t.Errorf("restored into an existing database")
	}
}

// Nothing runs when a dump can't be opened at the last moment: every file
// is opened before the first prepare.
func TestDBRestoreOpensBeforePrepare(t *testing.T) {
	e := setup(t)
	e.dbRunning("feat")
	dir := writeDumps(t, e.ws)
	e.f.On([]string{"podman", "exec"}, sys.Result{}, nil)
	if err := os.Chmod(filepath.Join(dir, "filial_queue.dump"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.l.DBRestore(ctx, "feat", "", nil, true); err == nil {
		t.Fatal("restored with an unreadable dump")
	}
	if n := count(e.calls(), "dropdb"); n != 0 {
		t.Errorf("prepare ran before every file was opened")
	}
}
