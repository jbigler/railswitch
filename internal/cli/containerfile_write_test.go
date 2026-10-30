package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
)

type showOut struct {
	Name, File, Kind, Text, Hash, Diff string
	Default                            *struct{ Ref, Text string }
	Ejected                            *struct{ Ref, SHA256 string }
	DefaultChanged                     bool `json:"default_changed"`
}

func showJSON(t *testing.T, ws, name string) showOut {
	t.Helper()
	var d showOut
	out := mustRun(t, "-C", ws, "--json", "containerfile", "show", name)
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return d
}

func TestContainerfileShowJSON(t *testing.T) {
	ws, _ := lockedWS(t)
	file := filepath.Join(ws, "containerfiles", "Containerfile.pg-client")
	def := string(builtinDefault(t, postgresFiles, "Containerfile.pg-client"))

	d := showJSON(t, ws, "pg-client")
	if d.Name != "pg-client" || d.File != "containerfiles/Containerfile.pg-client" || d.Kind != "addon" || d.Text != def || d.Hash != workspace.Hash([]byte(def)) {
		t.Errorf("show pg-client: %+v", d)
	}
	if d.Default == nil || d.Default.Ref != "postgres@1" || d.Default.Text != def || d.Ejected != nil || d.Diff != "" || d.DefaultChanged {
		t.Errorf("an unedited file without a header: %+v", d)
	}

	mustRun(t, "-C", ws, "containerfile", "eject", "pg-client", "--force")
	writeFile(t, file, readFile(t, file)+"# mine\n")
	d = showJSON(t, ws, "pg-client")
	if d.Ejected == nil || d.Ejected.Ref != "postgres@1" || d.DefaultChanged {
		t.Errorf("ejected %+v changed %v", d.Ejected, d.DefaultChanged)
	}
	if !strings.HasPrefix(d.Diff, "--- default\n+++ containerfiles/Containerfile.pg-client\n") || !strings.Contains(d.Diff, "\n+# mine\n") || strings.Contains(d.Diff, "Ejected from") {
		t.Errorf("diff (the header dropped, the edit kept):\n%s", d.Diff)
	}

	text := readFile(t, file)
	writeFile(t, file, regexp.MustCompile(`sha256 [0-9a-f]{64}`).ReplaceAllString(text, "sha256 "+strings.Repeat("0", 64)))
	if d = showJSON(t, ws, "pg-client"); !d.DefaultChanged {
		t.Error("default_changed false after the header's sha changed")
	}
	// The build chain and the overview say so too.
	if out := mustRun(t, "-C", ws, "-w", "master", "--json", "containerfile", "list"); !strings.Contains(out, `"name":"addon pg-client","file":"containerfiles/Containerfile.pg-client"`) || !regexp.MustCompile(`"addon pg-client"[^}]*"default_changed":true`).MatchString(out) {
		t.Errorf("containerfile list:\n%s", out)
	}
	_, _, st, err := projectStack(ws)
	if err != nil {
		t.Fatal(err)
	}
	if got := staleEjects(ws, st); !slices.Equal(got, []string{"pg-client"}) {
		t.Errorf("staleEjects = %v", got)
	}

	for name, kind := range map[string]string{"base": "base", "nvim": "tool", "playwright": "standalone", "agent-base": "agent-base", "probe": "agent"} {
		if d := showJSON(t, ws, name); d.Kind != kind || d.Default == nil {
			t.Errorf("%s: kind %q default %v; want %s", name, d.Kind, d.Default, kind)
		}
	}

	mustRun(t, "-C", ws, "containerfile", "new", "vips")
	if d := showJSON(t, ws, "vips"); d.Kind != "user" || d.Default != nil || d.Ejected != nil || d.Diff != "" {
		t.Errorf("user addon: %+v", d)
	}
	out := mustRun(t, "-C", ws, "--json", "containerfile", "show", "vips")
	if !strings.Contains(out, `"default":null`) || !strings.Contains(out, `"ejected":null`) {
		t.Errorf("default and ejected not null: %s", out)
	}
	removeFile(t, file)
	if code, _, errs := runIn(t, "-C", ws, "--json", "containerfile", "show", "pg-client"); code != 1 || !strings.Contains(errs, "eject") {
		t.Errorf("missing file: exit %d: %s", code, errs)
	}
}

type writeOut struct {
	Hash  string
	Steps []struct {
		Worktree, Name string
		OldTag         string `json:"old_tag"`
		NewTag         string `json:"new_tag"`
	}
	Restart []string
	Errors  []string
	Used    *bool
}

func parseWriteOut(t *testing.T, out string) writeOut {
	t.Helper()
	var d writeOut
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if d.Steps == nil || d.Restart == nil || d.Errors == nil {
		t.Fatalf("null steps, restart or errors: %s", out)
	}
	return d
}

// pgClient is ws's Containerfile.pg-client path and text.
func pgClient(t *testing.T, ws string) (string, string) {
	t.Helper()
	file := filepath.Join(ws, "containerfiles", "Containerfile.pg-client")
	return file, readFile(t, file)
}

func TestContainerfileWriteDryRun(t *testing.T) {
	ws, _ := lockedWS(t)
	file, before := pgClient(t, ws)
	oldTag := list(t, ws).tag(t, "addon pg-client")
	next := before + "# one more line\n"
	code, out, errs := runStdin(t, unitsFake(), next, "-C", ws, "--json", "containerfile", "write", "pg-client", "--if-hash", workspace.Hash([]byte(before)), "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	d := parseWriteOut(t, out)
	if d.Hash != workspace.Hash([]byte(next)) || len(d.Errors) != 0 {
		t.Errorf("hash %q errors %q", d.Hash, d.Errors)
	}
	var master []string
	for _, s := range d.Steps {
		if s.OldTag == s.NewTag {
			t.Errorf("unchanged step listed: %+v", s)
		}
		if s.Worktree == "master" {
			master = append(master, s.Name)
			if s.Name == "addon pg-client" && s.OldTag != oldTag {
				t.Errorf("old tag %s; want %s", s.OldTag, oldTag)
			}
		}
	}
	if !slices.Contains(master, "addon pg-client") || slices.Contains(master, "base") || slices.Contains(master, "playwright") {
		t.Errorf("master's changed steps %q; want the addon and what builds on it, not base or the standalone build", master)
	}
	if !slices.ContainsFunc(d.Steps, func(s struct {
		Worktree, Name string
		OldTag         string `json:"old_tag"`
		NewTag         string `json:"new_tag"`
	}) bool {
		return s.Worktree == "feat"
	}) {
		t.Error("feat's steps not listed")
	}
	if !slices.Equal(d.Restart, []string{"master"}) {
		t.Errorf("restart %q; want only the running master", d.Restart)
	}
	if readFile(t, file) != before {
		t.Error("--dry-run wrote the file")
	}
}

func TestContainerfileWriteContractError(t *testing.T) {
	ws, _ := lockedWS(t)
	file, before := pgClient(t, ws)
	hash := workspace.Hash([]byte(before))
	bad := "ARG BASE_IMAGE\nFROM docker.io/library/debian:trixie\nUSER appuser\n"
	code, out, errs := runStdin(t, unitsFake(), bad, "-C", ws, "--json", "containerfile", "write", "pg-client", "--if-hash", hash, "--dry-run")
	if code != 0 {
		t.Fatalf("dry run: exit %d: %s", code, errs)
	}
	if d := parseWriteOut(t, out); len(d.Errors) == 0 || !strings.Contains(strings.Join(d.Errors, "\n"), "FROM ${BASE_IMAGE}") || len(d.Steps) != 0 {
		t.Errorf("dry run: %+v", d)
	}
	code, _, errs = runStdin(t, unitsFake(), bad, "-C", ws, "containerfile", "write", "pg-client", "--if-hash", hash)
	if code != 1 || !strings.Contains(errs, "FROM ${BASE_IMAGE}") {
		t.Errorf("real run: exit %d: %s", code, errs)
	}
	if readFile(t, file) != before {
		t.Error("a refused write changed the file")
	}
}

// TestContainerfileWriteHashMismatch: the file changed on disk between the
// read the page showed (its hash) and the save: refused, nothing written.
func TestContainerfileWriteHashMismatch(t *testing.T) {
	ws, _ := lockedWS(t)
	file, before := pgClient(t, ws)
	hash := workspace.Hash([]byte(before))
	writeFile(t, file, before+"# by hand\n")
	for _, dry := range []bool{true, false} {
		args := []string{"-C", ws, "containerfile", "write", "pg-client", "--if-hash", hash}
		if dry {
			args = append(args, "--dry-run")
		}
		code, _, errs := runStdin(t, unitsFake(), before+"# mine\n", args...)
		if code != 1 || !strings.Contains(errs, "changed on disk since you opened it") {
			t.Errorf("dry %v: exit %d: %s", dry, code, errs)
		}
	}
	if readFile(t, file) != before+"# by hand\n" {
		t.Error("the file was overwritten")
	}
	if code, _, _ := runStdin(t, unitsFake(), before, "-C", ws, "containerfile", "write", "pg-client"); code != 2 {
		t.Errorf("no --if-hash: exit %d; want 2", code)
	}
}

func TestContainerfileWriteNotInChain(t *testing.T) {
	ws, _ := lockedWS(t)
	stray := filepath.Join(ws, "containerfiles", "Containerfile.stray")
	writeFile(t, stray, vipsSkeleton)
	for _, name := range []string{"stray", "nope"} {
		code, _, errs := runStdin(t, unitsFake(), vipsSkeleton+"# x\n", "-C", ws, "containerfile", "write", name, "--if-hash", workspace.Hash([]byte(vipsSkeleton)))
		if code != 1 || !strings.Contains(errs, "not built by this project") {
			t.Errorf("%s: exit %d: %s", name, code, errs)
		}
	}
	if readFile(t, stray) != vipsSkeleton {
		t.Error("stray file written")
	}
	if code, _, _ := runStdin(t, unitsFake(), "x", "-C", ws, "containerfile", "write", "../project.toml", "--if-hash", "sha256:0"); code != 2 {
		t.Errorf("a path as the name: exit %d; want 2", code)
	}
}

func TestContainerfileWriteTooBig(t *testing.T) {
	ws, _ := lockedWS(t)
	file, before := pgClient(t, ws)
	big := before + strings.Repeat("#", 1<<20)
	code, _, errs := runStdin(t, unitsFake(), big, "-C", ws, "containerfile", "write", "pg-client", "--if-hash", workspace.Hash([]byte(before)))
	if code != 1 || !strings.Contains(errs, "1 MiB") {
		t.Errorf("exit %d: %s", code, errs)
	}
	if readFile(t, file) != before {
		t.Error("the file changed")
	}
}

func TestContainerfileWrite(t *testing.T) {
	ws, dirs := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	file, before := pgClient(t, ws)
	oldTag := list(t, ws).tag(t, "addon pg-client")
	next := before + "# one more line\n"
	code, out, errs := runStdin(t, unitsFake(), next, "-C", ws, "containerfile", "write", "pg-client", "--if-hash", workspace.Hash([]byte(before)))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if readFile(t, file) != next {
		t.Fatal("file not replaced")
	}
	fi, err := os.Stat(file)
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %v (%v)", fi.Mode(), err)
	}
	newTag := list(t, ws).tag(t, "addon pg-client")
	if newTag == oldTag {
		t.Fatal("tag unchanged")
	}
	if !strings.Contains(out, "master addon pg-client "+newTag) || !strings.Contains(out, "wrote "+file) {
		t.Errorf("output:\n%s", out)
	}
	// The app runs the chain's last image: its unit names the new base tag.
	if unit := readFile(t, filepath.Join(dirs.Quadlet, "filial-master-rails.container")); strings.Contains(unit, oldTag) {
		t.Error("the app unit still names the old tag")
	}
	if out := mustRun(t, "-C", ws, "render", "--check"); out != "" {
		t.Errorf("render --check after write:\n%s", out)
	}
}

// TestContainerfileWriteRestart: both apps run; master's up fails at the
// pull (the fake has no images), so the restart stops there and feat is
// never stopped. The write stays.
func TestContainerfileWriteRestart(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	for _, slug := range []string{"master", "feat"} {
		if err := os.MkdirAll(filepath.Join(ws, slug, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	file, before := pgClient(t, ws)
	f := fakeHost()
	f.On([]string{"systemctl", "--user", "show"}, sys.Result{Stdout: []byte(
		"Id=filial-master-rails.service\nActiveState=active\n\nId=filial-feat-rails.service\nActiveState=active\n")}, nil)
	next := before + "# one more line\n"
	code, _, errs := runStdin(t, f, next, "-C", ws, "containerfile", "write", "pg-client", "--restart", "--if-hash", workspace.Hash([]byte(before)))
	if code != 3 || !strings.Contains(errs, "restart master:") || strings.Contains(errs, "restart feat") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if readFile(t, file) != next {
		t.Error("the write did not stay")
	}
	var stopped []string
	for _, c := range f.Calls {
		if len(c) > 3 && c[0] == "systemctl" && c[2] == "stop" {
			stopped = append(stopped, c[3:]...)
		}
	}
	if !slices.Contains(stopped, "filial-master-rails.service") || slices.Contains(stopped, "filial-feat-rails.service") {
		t.Errorf("stopped %q; want master's app, not feat's", stopped)
	}
}

// TestCommitRestoresNewLock: commit's restore covers every file it changed:
// a replaced one gets its bytes and mode back, a new one (and the directory
// made for it) is removed, and a stack.lock that did not exist before is
// removed.
func TestCommitRestoresNewLock(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	proj, lock := filepath.Join(ws, "project.toml"), filepath.Join(ws, "stack.lock")
	before := readFile(t, proj)
	removeFile(t, lock)
	if err := os.Chmod(proj, 0o600); err != nil {
		t.Fatal(err)
	}
	f := unitsFake()
	f.On([]string{"podman", "info", "--format", "{{.Store.GraphRoot}}"}, sys.Result{}, os.ErrPermission)
	var out, errb strings.Builder
	e := &env{stdout: &out, stderr: &errb, run: f, dir: ws}
	err := e.commit(context.Background(), ws, []fileChange{
		{Rel: "project.toml", Data: []byte(before + "# edited\n"), Mode: 0o644},
		{Rel: "notes/new.txt", Data: []byte("x"), Mode: 0o644},
	}, &workspace.Accept{})
	if err == nil || !strings.Contains(err.Error(), "podman info") {
		t.Fatalf("err = %v", err)
	}
	if readFile(t, proj) != before {
		t.Error("project.toml not restored")
	}
	if fi, err := os.Stat(proj); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("project.toml mode %v (%v); want 0600 kept", fi.Mode(), err)
	}
	for _, p := range []string{lock, filepath.Join(ws, "notes")} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left behind: %v", p, err)
		}
	}
}

// Reset to default (eject --force) in a rendered workspace renders too, so
// the worktrees built from the edited file show as behind (stale) at once,
// as after a write.
func TestEjectForceRenders(t *testing.T) {
	ws, _ := lockedWS(t)
	file, before := pgClient(t, ws)
	if err := os.WriteFile(file, []byte(before+"# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "-C", ws, "render")
	mustRun(t, "-C", ws, "containerfile", "eject", "pg-client", "--force")
	if out := mustRun(t, "-C", ws, "render", "--check"); out != "" {
		t.Errorf("units not rendered after the reset:\n%s", out)
	}
}

// containerfile new in a rendered workspace renders: the new addon is in
// the units at once.
func TestContainerfileNewRenders(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	mustRun(t, "-C", ws, "containerfile", "new", "extras")
	if out := mustRun(t, "-C", ws, "render", "--check"); out != "" {
		t.Errorf("units not rendered after containerfile new:\n%s", out)
	}
}

// eject --force writes the default even when the render that follows
// can't run (a stale lock): it warns and exits 0, since the reset happened.
func TestEjectForceRenderFailsWarns(t *testing.T) {
	ws, dir := localRails(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dropStore(t)
	file, before := pgClient(t, ws)
	if err := os.WriteFile(file, []byte(before+"# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runIn(t, "-C", ws, "containerfile", "eject", "pg-client", "--force")
	if code != 0 || !strings.Contains(errs, "warning: ") || !strings.Contains(errs, "not rendered") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if strings.Contains(readFile(t, file), "# mine") {
		t.Error("not reset")
	}
}
