package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
	"github.com/jbigler/railswitch/internal/workspace"
)

const cacheNone = `[{"op":"option","key":"cache","ref":"none"}]`

// unitsFake answers the app-unit state read: master active, feat not.
func unitsFake() *systest.Fake {
	f := fakeHost()
	f.On([]string{"systemctl", "--user", "show"}, sys.Result{Stdout: []byte(
		"Id=filial-master-rails.service\nActiveState=active\n\nId=filial-feat-rails.service\nActiveState=inactive\n")}, nil)
	return f
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

type previewDoc struct {
	Hash  string
	Diff  string
	Edits []struct {
		Index   int
		Impact  string
		Reasons []string
		Error   string
	}
	Restart []string
}

func TestSettingsPreviewJSON(t *testing.T) {
	ws, _ := lockedWS(t)
	before := readFile(t, filepath.Join(ws, "project.toml"))
	f := unitsFake()
	code, out, errs := runStdin(t, f, cacheNone, "-C", ws, "--json", "settings", "preview")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var p previewDoc
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if p.Hash != workspace.Hash([]byte(before)) || !strings.Contains(p.Diff, `-cache    = "redis@1"`) {
		t.Errorf("hash %q diff %q", p.Hash, p.Diff)
	}
	if len(p.Edits) != 1 || p.Edits[0].Impact != "check" || !slices.Contains(p.Edits[0].Reasons, "REDIS_URL is no longer given to rails") {
		t.Errorf("edits %+v", p.Edits)
	}
	if !slices.Equal(p.Restart, []string{"master"}) {
		t.Errorf("restart %v; want [master]", p.Restart)
	}
	shows := 0
	for _, c := range f.Calls {
		if len(c) > 2 && c[0] == "systemctl" && c[2] == "show" {
			shows++
		}
	}
	if shows != 1 {
		t.Errorf("%d systemctl show calls; want one batched read", shows)
	}
	if readFile(t, filepath.Join(ws, "project.toml")) != before {
		t.Error("preview wrote project.toml")
	}
}

func TestSettingsApplyWritesLocksRenders(t *testing.T) {
	ws, dirs := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	unit := filepath.Join(dirs.Quadlet, "filial-master-cache.container")
	if !exists(unit) {
		t.Fatalf("no %s before apply", unit)
	}
	path := filepath.Join(ws, "project.toml")
	before := readFile(t, path)
	lockBefore := readFile(t, filepath.Join(ws, "stack.lock"))
	hash := workspace.Hash([]byte(before))

	code, out, errs := runStdin(t, unitsFake(), cacheNone, "-C", ws, "settings", "apply", "--if-hash", hash)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	after := readFile(t, path)
	if !strings.Contains(after, `cache    = "none"`) || strings.Count(after, "\n") != strings.Count(before, "\n") {
		t.Errorf("project.toml:\n%s", after)
	}
	if readFile(t, filepath.Join(ws, "stack.lock")) == lockBefore {
		t.Error("stack.lock not rewritten")
	}
	if exists(unit) {
		t.Error("the removed service's unit file is still there")
	}
	for _, want := range []string{"wrote " + path, "removed filial-master-cache.container"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestSettingsApplyRestoresOnRenderFailure(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	path := filepath.Join(ws, "project.toml")
	before, lockBefore := readFile(t, path), readFile(t, filepath.Join(ws, "stack.lock"))
	f := unitsFake()
	f.On([]string{"podman", "info", "--format", "{{.Store.GraphRoot}}"}, sys.Result{}, os.ErrPermission)
	code, _, errs := runStdin(t, f, cacheNone, "-C", ws, "settings", "apply", "--if-hash", workspace.Hash([]byte(before)))
	if code != 1 || !strings.Contains(errs, "podman info") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if readFile(t, path) != before || readFile(t, filepath.Join(ws, "stack.lock")) != lockBefore {
		t.Error("project.toml or stack.lock not restored")
	}
}

func TestSettingsApplyIfHash(t *testing.T) {
	ws, _ := lockedWS(t)
	path := filepath.Join(ws, "project.toml")
	before := readFile(t, path)
	hash := workspace.Hash([]byte(before))
	if err := os.WriteFile(path, []byte(before+"\n# by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runStdin(t, unitsFake(), cacheNone, "-C", ws, "settings", "apply", "--if-hash", hash)
	if code != 1 || !strings.Contains(errs, "project.toml changed since the preview") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if readFile(t, path) != before+"\n# by hand\n" {
		t.Error("project.toml touched")
	}
	if code, _, _ := runStdin(t, unitsFake(), cacheNone, "-C", ws, "settings", "apply"); code != 2 {
		t.Errorf("apply without --if-hash: exit %d", code)
	}
}

func TestSettingsApplyBlocked(t *testing.T) {
	ws, _ := lockedWS(t)
	path := filepath.Join(ws, "project.toml")
	before := readFile(t, path)
	lockBefore := readFile(t, filepath.Join(ws, "stack.lock"))
	edits := `[{"op":"env.set","name":"A","value":"1"},{"op":"option","key":"cache","ref":"none"},{"op":"option","key":"jobs","ref":"sidekiq@1"}]`
	code, _, errs := runStdin(t, unitsFake(), edits, "-C", ws, "settings", "apply", "--if-hash", workspace.Hash([]byte(before)))
	if code != 1 || !strings.Contains(errs, "edit 2 is blocked") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if readFile(t, path) != before || readFile(t, filepath.Join(ws, "stack.lock")) != lockBefore {
		t.Error("workspace touched")
	}
}

// TestSettingsApplyRestart: only master's app runs, so only master is
// stopped and brought up again. The fake has no images, so its up fails at
// the pull: the restart stops there, and the settings stay applied.
func TestSettingsApplyRestart(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	path := filepath.Join(ws, "project.toml")
	before := readFile(t, path)
	for _, slug := range []string{"master", "feat"} {
		if err := os.MkdirAll(filepath.Join(ws, slug, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := unitsFake()
	code, _, errs := runStdin(t, f, cacheNone, "-C", ws, "settings", "apply", "--restart", "--if-hash", workspace.Hash([]byte(before)))
	if code != 1 || !strings.Contains(errs, "restart master:") || strings.Contains(errs, "restart feat") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(readFile(t, path), `cache    = "none"`) {
		t.Error("settings not applied")
	}
	var stopped []string
	for _, c := range f.Calls {
		if len(c) > 2 && c[0] == "systemctl" && c[2] == "stop" {
			stopped = append(stopped, c[3:]...)
		}
	}
	if !slices.Contains(stopped, "filial-master-rails.service") {
		t.Errorf("master's app unit not stopped: %v", stopped)
	}
	for _, u := range stopped {
		if u == "filial-feat-rails.service" {
			t.Errorf("stopped %s", u)
		}
	}
}

type projectShowDoc struct {
	Prefix, Name, Repo, Hash, File string
	BaseBranch                     string   `json:"base_branch"`
	AddChoices                     []string `json:"add_choices"`
	Options                        []struct {
		Key     string
		Slot    bool
		Ref     string
		Choices []string
	}
	Env   []struct{ Name, Value string }
	Links []struct {
		Label, URL, From string
		Hidden           bool
	}
}

func TestProjectShowJSON(t *testing.T) {
	ws, _ := lockedWS(t)
	if err := os.MkdirAll(filepath.Join(ws, "options", "mariadb"), 0o755); err != nil {
		t.Fatal(err)
	}
	opt := `name = "mariadb"
version = 1
slot = "database"
service = "db"
`
	if err := os.WriteFile(filepath.Join(ws, "options", "mariadb", "option.toml"), []byte(opt), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(ws, "project.toml")
	b := readFile(t, p)
	if err := os.WriteFile(p, []byte(strings.Replace(b, "schema = 1\n", "schema = 1\nhide_links = [\"Mailer previews\"]\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out := mustRun(t, "-C", ws, "--json", "project", "show")
	var d projectShowDoc
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if d.Prefix != "filial" || d.BaseBranch != "master" || d.Hash != workspace.Hash([]byte(readFile(t, p))) || d.File != readFile(t, p) {
		t.Errorf("doc %+v", d)
	}
	if len(d.Options) < 6 || d.Options[0].Key != "database" || !d.Options[0].Slot || d.Options[0].Ref != "postgres@1" {
		t.Fatalf("options %+v", d.Options)
	}
	if !slices.Contains(d.AddChoices, "postgres@1") || !slices.Contains(d.AddChoices, "local:mariadb@1") || slices.Contains(d.AddChoices, "none") {
		t.Errorf("add_choices %v; want every option, no none", d.AddChoices)
	}
	db := d.Options[0].Choices
	if !slices.Contains(db, "postgres@1") || !slices.Contains(db, "local:mariadb@1") || db[len(db)-1] != "none" || slices.Contains(db, "redis@1") {
		t.Errorf("database choices %v", db)
	}
	if len(d.Env) != 1 || d.Env[0].Name != "TZ" || d.Env[0].Value != "Asia/Bangkok" {
		t.Errorf("env %+v", d.Env)
	}
	if len(d.Links) == 0 || d.Links[0].Label != "Admin" || d.Links[0].From != "project" || d.Links[0].Hidden {
		t.Fatalf("links %+v", d.Links)
	}
	stack := 0
	for _, l := range d.Links[1:] {
		if l.From == "project" || !strings.Contains(l.From, "@") && !strings.HasPrefix(l.From, "tool ") {
			t.Errorf("link %+v", l)
		}
		if l.Label == "Mailer previews" && !l.Hidden {
			t.Errorf("hide_links ignored: %+v", l)
		}
		stack++
	}
	if stack == 0 {
		t.Error("no stack links")
	}
}

func TestProjectWrite(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	path := filepath.Join(ws, "project.toml")
	before := readFile(t, path)
	hash := workspace.Hash([]byte(before))
	next := before + "\n[tasks]\nhello = \"echo hi\"\n"

	if code, _, errs := runStdin(t, fakeHost(), "not = [toml", "-C", ws, "project", "write", "--if-hash", hash); code != 1 {
		t.Fatalf("invalid toml: exit %d: %s", code, errs)
	}
	if code, _, errs := runStdin(t, fakeHost(), next, "-C", ws, "project", "write", "--if-hash", "sha256:0"); code != 1 || !strings.Contains(errs, "changed since the preview") {
		t.Fatalf("stale hash: exit %d: %s", code, errs)
	}
	bad := strings.Replace(before, `pack = "rails@1"`, `pack = "nope@1"`, 1)
	if code, _, _ := runStdin(t, fakeHost(), bad, "-C", ws, "project", "write", "--if-hash", hash); code != 1 {
		t.Fatalf("unresolvable pack: exit %d", code)
	}
	if readFile(t, path) != before {
		t.Fatal("a refused write touched project.toml")
	}

	code, out, errs := runStdin(t, fakeHost(), next, "-C", ws, "project", "write", "--if-hash", hash)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if readFile(t, path) != next || !strings.Contains(out, "wrote "+path) {
		t.Errorf("project.toml:\n%s\nout: %s", readFile(t, path), out)
	}
}

// With no stack.lock before, a failed project write leaves none: a lock of
// the refused file would describe a project that was never written.
func TestProjectWriteNoLockLeftOnFailure(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	path, lock := filepath.Join(ws, "project.toml"), filepath.Join(ws, "stack.lock")
	before := readFile(t, path)
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	f := unitsFake()
	f.On([]string{"podman", "info", "--format", "{{.Store.GraphRoot}}"}, sys.Result{}, os.ErrPermission)
	code, _, errs := runStdin(t, f, before+"# edited\n", "-C", ws, "project", "write", "--if-hash", workspace.Hash([]byte(before)))
	if code != 1 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if readFile(t, path) != before {
		t.Error("project.toml not restored")
	}
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stack.lock left behind: %v", err)
	}
}
