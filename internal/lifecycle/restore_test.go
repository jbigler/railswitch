package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
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
	if n := containing(e.calls(), "evil"); n != 0 {
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
	if n := containing(e.calls(), "pg_restore"); n != 0 {
		t.Errorf("restored after a refusal")
	}
	done, err := e.l.DBRestore(ctx, "feat", "", []string{"filial_queue"}, true)
	if err != nil || !slices.Equal(done, []string{"filial_queue"}) || containing(e.calls(), "-d filial_dev") != 0 {
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
	if containing(e.calls(), "sh filial_queue") != 0 {
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
	if n := containing(e.calls(), "pg_restore"); n != 0 {
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
	if n := containing(e.calls(), "dropdb"); n != 0 {
		t.Errorf("prepare ran before every file was opened")
	}
}

// A restore that fails leaves the app unstarted and a marker; the next up
// restores again (the volume exists by then), and a success clears it.
func TestUpRetriesUnfinishedRestore(t *testing.T) {
	e := setup(t)
	e.missing("podman", "image", "exists")
	e.missing("podman", "volume", "exists")
	if err := os.WriteFile(filepath.Join(e.ws, "db-dumps", "filial_dev.dump"), []byte("PGDMP dev"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.f.On([]string{"podman", "exec", "-i", "filial-feat-db", "pg_restore"}, sys.Result{ExitCode: 1}, errors.New("exit 1"))
	e.f.On([]string{"podman", "exec"}, sys.Result{}, nil)
	err := e.l.Up(ctx, "feat")
	marker := filepath.Join(e.ws, ".railswitch", "restore-pending", "feat")
	if err == nil || !strings.Contains(err.Error(), "the next up or railswitch db restore restores the dumps") || !strings.Contains(err.Error(), marker) {
		t.Fatalf("err = %v", err)
	}
	if count(e.calls(), "systemctl --user start filial-feat-rails.service") != 0 {
		t.Error("the app started on a database whose restore failed")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("no marker: %v", err)
	}

	f := setupAgain(t, e)
	f.missing("podman", "image", "exists")
	f.f.On([]string{"podman", "exec"}, sys.Result{}, nil)
	if err := f.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	if containing(f.calls(), "pg_restore") != 1 {
		t.Errorf("the next up didn't restore again:\n%s", dump(f.calls()))
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the marker survived a restore")
	}
}

// setupAgain is e with a fresh fake (the same workspace and registry): the
// next command after one whose scripted results must not carry over.
func setupAgain(t *testing.T, e *tenv) *tenv {
	t.Helper()
	f := &systest.Fake{}
	f.On([]string{"podman", "info", "--format", "{{.Store.GraphRoot}}"}, sys.Result{Stdout: []byte("/g\n")}, nil)
	f.On([]string{"systemctl", "--user", "show", "--timestamp=us+utc", "-p", "ActiveEnterTimestamp"}, sys.Result{Stdout: []byte("ActiveEnterTimestamp=Fri 2100-01-01 00:00:00.000000 UTC\n")}, nil)
	f.On([]string{"podman", "image", "inspect", "--format", "{{.Digest}}"}, sys.Result{Stdout: []byte("sha256:aa\n")}, nil)
	l := *e.l
	l.R = f
	return &tenv{ws: e.ws, f: f, out: e.out, l: &l}
}

// containing counts the calls whose joined argv holds sub anywhere (count
// matches a prefix only).
func containing(calls [][]string, sub string) int {
	n := 0
	for _, c := range calls {
		if strings.Contains(joined(c), sub) {
			n++
		}
	}
	return n
}

// A [dump] file that doesn't expand is an error, not "no dumps".
func TestDumpFilesExpansionError(t *testing.T) {
	e := setup(t)
	m, in, err := e.l.load(ctx, "feat")
	if err != nil {
		t.Fatal(err)
	}
	o := *m.Inputs.Stack.Option("database").Option
	d := *o.Dump
	d.File = "{nope}{dump.db}.dump"
	o.Dump = &d
	if _, err := dumpFiles(m, filepath.Join(e.ws, "db-dumps"), &o, in.Scope("db")); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v", err)
	}
}

// Any successful db restore clears a marker an earlier failure left: the
// next up never restores over what was restored by hand.
func TestDBRestoreClearsMarker(t *testing.T) {
	e := setup(t)
	e.dbRunning("feat")
	dir := writeDumps(t, e.ws)
	marker := filepath.Join(e.ws, ".railswitch", "restore-pending", "feat")
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e.f.On([]string{"podman", "exec"}, sys.Result{}, nil)
	if _, err := e.l.DBRestore(ctx, "feat", dir, []string{"filial_dev"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the marker survived a restore with --from and a name")
	}
}
