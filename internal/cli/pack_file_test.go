package cli

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
	"github.com/jbigler/railswitch/internal/workspace"
)

// localPG is a locked workspace with postgres@1 ejected as local:pg@1,
// which the project does not use.
func localPG(t *testing.T) (ws, dir string) {
	t.Helper()
	ws, _ = lockedWS(t)
	mustRun(t, "-C", ws, "pack", "eject", "postgres@1", "--as", "pg")
	return ws, filepath.Join(ws, "options", "pg")
}

// localRails is a rendered workspace whose stack uses local:rails@1, an
// ejected copy of rails@1.
func localRails(t *testing.T) (ws, dir string) {
	t.Helper()
	ws, _ = filialWS(t)
	mustRun(t, "-C", ws, "pack", "eject", "rails@1")
	editProject(t, ws, `pack = "rails@1"`, `pack = "local:rails@1"`)
	mustRun(t, "-C", ws, "lock")
	mustRun(t, "-C", ws, "render")
	return ws, filepath.Join(ws, "packs", "rails")
}

// hashOf is workspace.Hash of the file at path.
func hashOf(t *testing.T, path string) string {
	t.Helper()
	return workspace.Hash([]byte(readFile(t, path)))
}

func TestPackShowFiles(t *testing.T) {
	ws, _ := localPG(t)
	var d struct {
		Files []struct {
			Path       string
			Size       int64
			Executable bool
		}
	}
	out := mustRun(t, "-C", ws, "--json", "pack", "show", "local:pg@1")
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range d.Files {
		paths = append(paths, f.Path)
		fi, err := os.Stat(filepath.Join(ws, "options", "pg", f.Path))
		if err != nil || fi.Size() != f.Size || f.Executable != (fi.Mode()&0o111 != 0) {
			t.Errorf("%+v vs %v (%v)", f, fi, err)
		}
	}
	if want := []string{"Containerfile.pg-client", "initdb/restore-dump.sh", "option.toml"}; !slices.Equal(paths, want) {
		t.Errorf("files %q; want %q", paths, want)
	}
	// A built-in's files are listed too, so the page can open them read-only.
	out = mustRun(t, "-C", ws, "--json", "pack", "show", "postgres@1")
	d.Files = nil
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	paths = nil
	for _, f := range d.Files {
		paths = append(paths, f.Path)
		if f.Path == "initdb/restore-dump.sh" && !f.Executable {
			t.Errorf("%s: not executable", f.Path)
		}
	}
	if want := []string{"Containerfile.pg-client", "initdb/restore-dump.sh", "option.toml"}; !slices.Equal(paths, want) {
		t.Errorf("built-in files %q; want %q", paths, want)
	}
}

func TestPackFileShow(t *testing.T) {
	ws, dir := localPG(t)
	var d struct{ Path, Text, Hash string }
	out := mustRun(t, "-C", ws, "--json", "pack", "file", "show", "local:pg@1", "option.toml")
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, filepath.Join(dir, "option.toml"))
	if d.Path != "option.toml" || d.Text != text || d.Hash != workspace.Hash([]byte(text)) {
		t.Errorf("show: %+v", d)
	}
	if out := mustRun(t, "-C", ws, "pack", "file", "show", "local:pg@1", "option.toml"); out != text {
		t.Errorf("text show:\n%s", out)
	}
	def := string(builtinDefault(t, postgresFiles, "Containerfile.pg-client"))
	if out := mustRun(t, "pack", "file", "show", "postgres@1", "Containerfile.pg-client"); out != def {
		t.Errorf("built-in show:\n%s", out)
	}
	for _, args := range [][]string{
		{"local:pg@1", "nope.sh"},
		{"local:pg@1", "../../project.toml"},
		{"postgres@1", "../rails/pack.toml"},
		{"nope@1", "option.toml"},
	} {
		if code, _, _ := runIn(t, append([]string{"-C", ws, "pack", "file", "show"}, args...)...); code == 0 {
			t.Errorf("show %q: exit 0", args)
		}
	}
}

func TestPackFilePath(t *testing.T) {
	for _, ok := range []string{"pack.toml", "Containerfile.base", "deps.allow", "README.md", "init-firewall-x.sh", "entrypoints/entrypoint-app.sh", "initdb/02-seed.sql"} {
		if err := pack.CheckFilePath(ok, "pack.toml"); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"option.toml", "Containerfile.", "notes.txt", ".sh", "entrypoints/sub/x.sh", "initdb/.x", "other/x", "", "/etc/passwd", "../x", "entrypoints/../../project.toml", "foo/bar", ".hidden", "entrypoints/", "README.md/x"} {
		if err := pack.CheckFilePath(bad, "pack.toml"); err == nil {
			t.Errorf("%q allowed", bad)
		}
	}
}

func TestPackFilePathRefused(t *testing.T) {
	ws, dir := localPG(t)
	before := readFile(t, filepath.Join(ws, "project.toml"))
	for _, p := range []string{"../x", "entrypoints/../../project.toml", "foo/bar", ".hidden", "../../project.toml"} {
		if code, _, errs := runStdin(t, fakeHost(), "x\n", "-C", ws, "pack", "file", "write", "local:pg@1", p, "--if-hash", ""); code == 0 {
			t.Errorf("write %q: exit 0 (%s)", p, errs)
		}
	}
	if readFile(t, filepath.Join(ws, "project.toml")) != before {
		t.Error("project.toml written")
	}

	// initdb/ a symlink to a dir outside the workspace: refused, the target untouched.
	outside := t.TempDir()
	if err := os.Rename(filepath.Join(dir, "initdb"), filepath.Join(outside, "initdb")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "initdb"), filepath.Join(dir, "initdb")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "initdb", "restore-dump.sh")
	script := readFile(t, target)
	for _, p := range []string{"initdb/restore-dump.sh", "initdb/new.sql"} {
		hash := ""
		if p == "initdb/restore-dump.sh" {
			hash = workspace.Hash([]byte(script))
		}
		code, _, errs := runStdin(t, fakeHost(), "# evil\n", "-C", ws, "pack", "file", "write", "local:pg@1", p, "--if-hash", hash)
		if code != 1 || !strings.Contains(errs, "symlink") {
			t.Errorf("write %s through a symlinked initdb/: exit %d: %s", p, code, errs)
		}
	}
	if readFile(t, target) != script {
		t.Error("the symlink's target was written")
	}
	if _, err := os.Stat(filepath.Join(outside, "initdb", "new.sql")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a file was created through the symlink: %v", err)
	}

	// A symlinked file: refused, its target untouched.
	victim := filepath.Join(outside, "victim")
	writeFile(t, victim, "keep\n")
	if err := os.Symlink(victim, filepath.Join(dir, "README.md")); err != nil {
		t.Fatal(err)
	}
	for _, hash := range []string{"", workspace.Hash([]byte("keep\n"))} {
		if code, _, errs := runStdin(t, fakeHost(), "# evil\n", "-C", ws, "pack", "file", "write", "local:pg@1", "README.md", "--if-hash", hash); code != 1 {
			t.Errorf("write a symlinked README.md: exit %d: %s", code, errs)
		}
	}
	if readFile(t, victim) != "keep\n" {
		t.Error("the symlinked file's target was written")
	}

	// The package dir itself a symlink: refused.
	ws2, dir2 := localPG(t)
	moved := filepath.Join(t.TempDir(), "pg")
	if err := os.Rename(dir2, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, dir2); err != nil {
		t.Fatal(err)
	}
	opt := readFile(t, filepath.Join(moved, "option.toml"))
	if code, _, errs := runStdin(t, fakeHost(), opt+"# x\n", "-C", ws2, "pack", "file", "write", "local:pg@1", "option.toml", "--if-hash", workspace.Hash([]byte(opt))); code != 1 || !strings.Contains(errs, "symlink") {
		t.Errorf("write through a symlinked package dir: exit %d: %s", code, errs)
	}
	if code, _, _ := runStdin(t, fakeHost(), "x", "-C", ws, "pack", "file", "write", "postgres@1", "option.toml", "--if-hash", ""); code != 2 {
		t.Errorf("a built-in ref: exit %d; want 2", code)
	}
}

func TestPackFileWriteBreaksLoad(t *testing.T) {
	ws, dir := localPG(t)
	file := filepath.Join(dir, "option.toml")
	before := readFile(t, file)
	bad := strings.Replace(before, `slot = "database"`, `slot = "nope"`, 1)
	if bad == before {
		t.Fatal("no slot line")
	}
	hash := workspace.Hash([]byte(before))
	code, out, errs := runStdin(t, fakeHost(), bad, "-C", ws, "--json", "pack", "file", "write", "local:pg@1", "option.toml", "--if-hash", hash, "--dry-run")
	if code != 0 {
		t.Fatalf("dry run: exit %d: %s", code, errs)
	}
	if d := parseWriteOut(t, out); len(d.Errors) == 0 || !strings.Contains(d.Errors[0], "slot") {
		t.Errorf("dry run errors %q", d.Errors)
	}
	if code, _, errs := runStdin(t, fakeHost(), bad, "-C", ws, "pack", "file", "write", "local:pg@1", "option.toml", "--if-hash", hash); code != 1 || !strings.Contains(errs, "slot") {
		t.Errorf("real run: exit %d: %s", code, errs)
	}
	if readFile(t, file) != before {
		t.Error("the file changed")
	}
}

func TestPackFileWriteUnused(t *testing.T) {
	ws, dir := localPG(t)
	lock := readFile(t, filepath.Join(ws, "stack.lock"))
	code, out, errs := runStdin(t, fakeHost(), "# pg\n", "-C", ws, "--json", "pack", "file", "write", "local:pg@1", "README.md", "--if-hash", "", "--dry-run")
	if code != 0 {
		t.Fatalf("dry run: exit %d: %s", code, errs)
	}
	if d := parseWriteOut(t, out); d.Used == nil || *d.Used || len(d.Steps) != 0 || len(d.Restart) != 0 || d.Hash != workspace.Hash([]byte("# pg\n")) {
		t.Errorf("dry run: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("--dry-run wrote the file")
	}
	mustRunStdin(t, fakeHost(), "# pg\n", "-C", ws, "pack", "file", "write", "local:pg@1", "README.md", "--if-hash", "")
	if readFile(t, filepath.Join(dir, "README.md")) != "# pg\n" {
		t.Error("not written")
	}
	if readFile(t, filepath.Join(ws, "stack.lock")) != lock {
		t.Error("stack.lock changed for a package the project does not use")
	}
}

func TestPackFileWriteUsed(t *testing.T) {
	ws, dir := localRails(t)
	file := filepath.Join(dir, "entrypoints", "entrypoint-app.sh")
	before := readFile(t, file)
	lockBefore, err := config.ReadLock(filepath.Join(ws, "stack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	code, out, errs := runStdin(t, unitsFake(), before+"# mine\n", "-C", ws, "pack", "file", "write", "local:rails@1", "entrypoints/entrypoint-app.sh", "--if-hash", workspace.Hash([]byte(before)))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if readFile(t, file) != before+"# mine\n" || !strings.Contains(out, "wrote "+filepath.Join(ws, "stack.lock")) {
		t.Errorf("file or output:\n%s", out)
	}
	lockAfter, err := config.ReadLock(filepath.Join(ws, "stack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if lockAfter.Packs["rails"].SHA256 == lockBefore.Packs["rails"].SHA256 {
		t.Error("the lock's pack sha did not change")
	}
	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("entrypoint mode %v (%v)", fi.Mode(), err)
	}
	if asset := readFile(t, filepath.Join(ws, ".railswitch", "assets", "rails", "entrypoints", "entrypoint-app.sh")); asset != before+"# mine\n" {
		t.Errorf("the rendered asset is not the new file:\n%s", asset)
	}
	if out := mustRun(t, "-C", ws, "render", "--check"); out != "" {
		t.Errorf("render --check after the write:\n%s", out)
	}
}

// TestPackFileWriteAcceptsOnlyItsFile: a used package with another file
// edited in a text editor since the lock is not accepted by a write of
// one file; an earlier editor edit of the written file itself is.
func TestPackFileWriteAcceptsOnlyItsFile(t *testing.T) {
	ws, dir := localRails(t)
	lockPath := filepath.Join(ws, "stack.lock")
	file := filepath.Join(dir, "entrypoints", "entrypoint-app.sh")
	before := readFile(t, file)
	if err := os.WriteFile(file, []byte(before+"# from an editor\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	edited := readFile(t, file)
	code, _, errs := runStdin(t, unitsFake(), before+"# mine\n", "-C", ws, "pack", "file", "write", "local:rails@1", "entrypoints/entrypoint-app.sh", "--if-hash", workspace.Hash([]byte(edited)))
	if code != 0 {
		t.Fatalf("the written file's own editor edit: exit %d: %s", code, errs)
	}

	allow := filepath.Join(dir, "deps.allow")
	if err := os.WriteFile(allow, []byte(readFile(t, allow)+"evil.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lockBefore, cur := readFile(t, lockPath), readFile(t, file)
	code, _, errs = runStdin(t, unitsFake(), cur+"# again\n", "-C", ws, "pack", "file", "write", "local:rails@1", "entrypoints/entrypoint-app.sh", "--if-hash", workspace.Hash([]byte(cur)))
	if code != 1 || !strings.Contains(errs, "other files of local:rails@1 changed since it was accepted: review them first (railswitch lock --review)") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if readFile(t, lockPath) != lockBefore || readFile(t, file) != cur {
		t.Error("the refused write left stack.lock or the file changed")
	}
}

// TestPackFileWriteRestores: the render fails after stack.lock was
// rewritten; the package file, project.toml and stack.lock are as before.
func TestPackFileWriteRestores(t *testing.T) {
	ws, dir := localRails(t)
	file := filepath.Join(dir, "entrypoints", "entrypoint-app.sh")
	paths := []string{file, filepath.Join(ws, "project.toml"), filepath.Join(ws, "stack.lock")}
	var before []string
	for _, p := range paths {
		before = append(before, readFile(t, p))
	}
	f := unitsFake()
	f.On([]string{"podman", "info", "--format", "{{.Store.GraphRoot}}"}, sys.Result{}, os.ErrPermission)
	code, _, errs := runStdin(t, f, before[0]+"# mine\n", "-C", ws, "pack", "file", "write", "local:rails@1", "entrypoints/entrypoint-app.sh", "--if-hash", workspace.Hash([]byte(before[0])))
	if code != 1 || !strings.Contains(errs, "podman info") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for i, p := range paths {
		if readFile(t, p) != before[i] {
			t.Errorf("%s not restored", p)
		}
	}
	// A new file in a new directory: both gone after the failed write.
	code, _, _ = runStdin(t, f, "SELECT 1;\n", "-C", ws, "pack", "file", "write", "local:rails@1", "initdb/01.sql", "--if-hash", "")
	if code != 1 {
		t.Fatalf("new file: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "initdb")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("initdb/ left behind: %v", err)
	}
	if readFile(t, paths[2]) != before[2] {
		t.Error("stack.lock not restored after the new file")
	}
}

// TestPackFileRestartOnAsset: an entrypoint edit changes no unit, only a
// mounted asset; the running worktree still needs a restart to see it.
func TestPackFileRestartOnAsset(t *testing.T) {
	ws, dir := localRails(t)
	file := filepath.Join(dir, "entrypoints", "entrypoint-app.sh")
	before := readFile(t, file)
	code, out, errs := runStdin(t, unitsFake(), before+"# mine\n", "-C", ws, "--json", "pack", "file", "write", "local:rails@1", "entrypoints/entrypoint-app.sh", "--if-hash", workspace.Hash([]byte(before)), "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	d := parseWriteOut(t, out)
	if d.Used == nil || !*d.Used || !slices.Equal(d.Restart, []string{"master"}) || len(d.Errors) != 0 {
		t.Errorf("dry run: %s", out)
	}
	if readFile(t, file) != before {
		t.Error("--dry-run wrote the file")
	}
}

func TestPackFileShMode(t *testing.T) {
	ws, dir := localPG(t)
	mustRunStdin(t, fakeHost(), "#!/bin/sh\n", "-C", ws, "pack", "file", "write", "local:pg@1", "init-extra.sh", "--if-hash", "")
	mustRunStdin(t, fakeHost(), "# pg\n", "-C", ws, "pack", "file", "write", "local:pg@1", "README.md", "--if-hash", "")
	for name, want := range map[string]os.FileMode{"init-extra.sh": 0o755, "README.md": 0o644} {
		if fi, err := os.Stat(filepath.Join(dir, name)); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v (%v); want %v", name, fi.Mode(), err, want)
		}
	}
}

func TestPackFileAdd(t *testing.T) {
	ws, dir := localPG(t)
	mustRunStdin(t, fakeHost(), "SELECT 1;\n", "-C", ws, "pack", "file", "write", "local:pg@1", "initdb/02-seed.sql", "--if-hash", "")
	if readFile(t, filepath.Join(dir, "initdb", "02-seed.sql")) != "SELECT 1;\n" {
		t.Fatal("not added")
	}
	code, _, errs := runStdin(t, fakeHost(), "SELECT 2;\n", "-C", ws, "pack", "file", "write", "local:pg@1", "initdb/02-seed.sql", "--if-hash", "")
	if code != 1 || !strings.Contains(errs, "exists") {
		t.Errorf("add over an existing file: exit %d: %s", code, errs)
	}
	if readFile(t, filepath.Join(dir, "initdb", "02-seed.sql")) != "SELECT 1;\n" {
		t.Error("overwritten")
	}
	if code, _, _ := runStdin(t, fakeHost(), "x", "-C", ws, "pack", "file", "write", "local:pg@1", "README.md"); code != 2 {
		t.Errorf("no --if-hash: exit %d; want 2", code)
	}
	if code, _, errs := runStdin(t, fakeHost(), "x", "-C", ws, "pack", "file", "write", "local:pg@1", "README.md", "--if-hash", "sha256:0"); code != 1 || !strings.Contains(errs, "changed on disk since you opened it") {
		t.Errorf("a hash for a missing file: exit %d: %s", code, errs)
	}
}

func TestPackFileRm(t *testing.T) {
	ws, dir := localPG(t)
	manifest := filepath.Join(dir, "option.toml")
	if code, _, errs := runIn(t, "-C", ws, "pack", "file", "rm", "local:pg@1", "option.toml", "--if-hash", hashOf(t, manifest)); code != 1 || !strings.Contains(errs, "manifest") {
		t.Errorf("rm the manifest: exit %d: %s", code, errs)
	}
	// option.toml mounts initdb/restore-dump.sh: removing it breaks the load.
	script := filepath.Join(dir, "initdb", "restore-dump.sh")
	if code, _, errs := runIn(t, "-C", ws, "pack", "file", "rm", "local:pg@1", "initdb/restore-dump.sh", "--if-hash", hashOf(t, script)); code != 1 || !strings.Contains(errs, "restore-dump.sh") {
		t.Errorf("rm a mounted file: exit %d: %s", code, errs)
	}
	if !exists(script) {
		t.Fatal("a refused rm removed the file")
	}

	ws, dir = localRails(t)
	extra := filepath.Join(dir, "entrypoints", "extra.sh")
	mustRunStdin(t, unitsFake(), "#!/bin/sh\n", "-C", ws, "pack", "file", "write", "local:rails@1", "entrypoints/extra.sh", "--if-hash", "")
	lock := readFile(t, filepath.Join(ws, "stack.lock"))
	if code, _, _ := runWith(t, unitsFake(), "-C", ws, "pack", "file", "rm", "local:rails@1", "entrypoints/extra.sh", "--if-hash", "sha256:0"); code != 1 || !exists(extra) {
		t.Fatalf("rm with a stale hash: exit %d", code)
	}
	if code, out, errs := runWith(t, unitsFake(), "-C", ws, "pack", "file", "rm", "local:rails@1", "entrypoints/extra.sh", "--if-hash", hashOf(t, extra)); code != 0 || !strings.Contains(out, "removed "+extra) {
		t.Fatalf("rm: exit %d: %s%s", code, out, errs)
	}
	if exists(extra) {
		t.Error("not removed")
	}
	if readFile(t, filepath.Join(ws, "stack.lock")) == lock {
		t.Error("stack.lock not rewritten for a used package")
	}
}

func TestPackNewOption(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "pack", "new", "--option", "cache", "kv")
	for _, f := range []string{"option.toml", "README.md"} {
		if !exists(filepath.Join(ws, "options", "kv", f)) {
			t.Errorf("no options/kv/%s", f)
		}
	}
	if out := mustRun(t, "-C", ws, "--json", "pack", "show", "local:kv@1"); !strings.Contains(out, `"slot":"cache"`) || !strings.Contains(out, `"kind":"option"`) {
		t.Errorf("show: %s", out)
	}
	var ls packLsDoc
	if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "--json", "pack", "ls")), &ls); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(ls.Options, func(o packLsOption) bool { return o.Ref == "local:kv@1" && o.Slot == "cache" }) {
		t.Errorf("pack ls options %+v lack local:kv@1 (cache)", ls.Options)
	}
	// It is usable as the slot's option: the stack resolves and renders.
	editProject(t, ws, `cache    = "redis@1"`, `cache    = "local:kv@1"`)
	acceptLock(t, ws)
	mustRun(t, "-C", ws, "render")
	if code, _, _ := runIn(t, "-C", ws, "pack", "new", "--option", "nope", "kv2"); code != 2 {
		t.Errorf("unknown slot: exit %d; want 2", code)
	}
	if code, _, _ := runIn(t, "-C", ws, "pack", "new", "--option", "cache", "kv"); code != 1 {
		t.Errorf("existing: exit %d; want 1", code)
	}
}

func mustRunStdin(t *testing.T, f *systest.Fake, stdin string, args ...string) string {
	t.Helper()
	code, out, errs := runStdin(t, f, stdin, args...)
	if code != 0 {
		t.Fatalf("railswitch %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, out, errs)
	}
	return out
}

// A ref whose version is not the package's is refused before any write:
// local:rails@2 would otherwise skip the lock of local:rails@1.
func TestPackFileWrongVersion(t *testing.T) {
	ws, dir := localRails(t)
	file := filepath.Join(dir, "pack.toml")
	before := readFile(t, file)
	next := strings.Replace(before, "version = 1", "version = 2", 1)
	code, _, errs := runStdin(t, unitsFake(), next, "-C", ws, "pack", "file", "write", "local:rails@2", "pack.toml", "--if-hash", workspace.Hash([]byte(before)))
	if code != 1 || !strings.Contains(errs, "local:rails@1") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if readFile(t, file) != before {
		t.Error("written")
	}
	if code, _, errs := runIn(t, "-C", ws, "pack", "file", "show", "local:rails@2", "pack.toml"); code != 1 || !strings.Contains(errs, "local:rails@1") {
		t.Errorf("show: exit %d: %s", code, errs)
	}
}

// A used package broken on disk is repaired by a write: the current
// project not resolving doesn't stop the judgement of the fixed text.
func TestPackFileRepairBroken(t *testing.T) {
	ws, dir := localRails(t)
	file := filepath.Join(dir, "pack.toml")
	good := readFile(t, file)
	broken := good + "\nbogus_key = 1\n"
	if err := os.WriteFile(file, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	h := workspace.Hash([]byte(broken))
	code, out, errs := runStdin(t, unitsFake(), good, "--json", "-C", ws, "pack", "file", "write", "local:rails@1", "pack.toml", "--if-hash", h, "--dry-run")
	if code != 0 {
		t.Fatalf("dry run exit %d: %s", code, errs)
	}
	var d struct {
		Used   bool
		Errors []string
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil || !d.Used || len(d.Errors) != 0 {
		t.Fatalf("dry run %+v (%v): %s", d, err, out)
	}
	if code, _, errs := runStdin(t, unitsFake(), good, "-C", ws, "pack", "file", "write", "local:rails@1", "pack.toml", "--if-hash", h); code != 0 {
		t.Fatalf("write exit %d: %s", code, errs)
	}
	if out := mustRun(t, "-C", ws, "render", "--check"); out != "" {
		t.Errorf("render --check after the repair:\n%s", out)
	}
}

// Agent packages are in the Library too: a built-in's show and files, and
// a local one's files edited like a pack's.
func TestPackFileAgent(t *testing.T) {
	ws, _ := lockedWS(t)
	var d struct {
		Kind  string
		Files []struct{ Path string }
	}
	out := mustRun(t, "-C", ws, "--json", "pack", "show", "claude@1")
	if err := json.Unmarshal([]byte(out), &d); err != nil || d.Kind != "agent" || !slices.ContainsFunc(d.Files, func(f struct{ Path string }) bool { return f.Path == "agent.toml" }) {
		t.Fatalf("show %+v (%v): %s", d, err, out)
	}
	if out := mustRun(t, "-C", ws, "pack", "file", "show", "claude@1", "agent.toml"); !strings.Contains(out, "name = ") {
		t.Errorf("built-in agent file:\n%s", out)
	}
	mustRun(t, "-C", ws, "pack", "eject", "claude@1", "--as", "bot")
	dir := filepath.Join(ws, "agents", "bot")
	md := filepath.Join(dir, "CLAUDE.md")
	before := readFile(t, md)
	if code, _, errs := runStdin(t, unitsFake(), before+"more\n", "-C", ws, "pack", "file", "write", "local:bot@1", "CLAUDE.md", "--if-hash", workspace.Hash([]byte(before))); code != 0 {
		t.Fatalf("write exit %d: %s", code, errs)
	}
	if readFile(t, md) != before+"more\n" {
		t.Error("not written")
	}
	if code, _, errs := runStdin(t, unitsFake(), "x", "-C", ws, "pack", "file", "write", "local:bot@1", "initdb/x.sh", "--if-hash", ""); code != 1 || !strings.Contains(errs, "not a package file") {
		t.Errorf("initdb/ in an agent: exit %d: %s", code, errs)
	}
}

// pack show and pack file agree on which directory a local ref names when
// two of packs/, options/ and agents/ hold the name.
func TestPackLocalDirOrder(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "pack", "eject", "claude@1", "--as", "x")
	mustRun(t, "-C", ws, "pack", "eject", "redis@1", "--as", "x")
	var d struct{ Kind, Path string }
	out := mustRun(t, "-C", ws, "--json", "pack", "show", "local:x@1")
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runIn(t, "-C", ws, "pack", "file", "show", "local:x@1", map[string]string{"agent": "agent.toml", "option": "option.toml"}[d.Kind])
	if code != 0 {
		t.Errorf("show says %s at %s; pack file disagrees: %s", d.Kind, d.Path, errs)
	}
}
