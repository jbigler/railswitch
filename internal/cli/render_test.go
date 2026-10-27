package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
	"github.com/jbigler/railswitch/internal/testfixture"
)

const filialFixture = "../../testdata/fixtures/filial"

// graphRoot is podman's graph root as the fake runner reports it.
const graphRoot = "/home/u/.local/share/containers/storage"

// fakeHost is a fake runner for this host: no host git config, podman's
// graph root at graphRoot.
func fakeHost() *systest.Fake {
	f := &systest.Fake{}
	f.On([]string{"podman", "info", "--format", "{{.Store.GraphRoot}}"}, sys.Result{Stdout: []byte(graphRoot + "\n")}, nil)
	return f
}

// runIn runs railswitch with a fakeHost runner.
func runIn(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return runWith(t, fakeHost(), args...)
}

func runWith(t *testing.T, f *systest.Fake, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := dispatch(context.Background(), f, args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

// filialWS is the Filial fixture workspace (testfixture.Workspace) with
// XDG_CONFIG_HOME and XDG_DATA_HOME pointing into a temp dir, and the project
// registered with its master (ID 1) and feat (ID 2) worktrees.
func filialWS(t *testing.T) (ws string, dirs machine.Dirs) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	ws = testfixture.Workspace(t, filialFixture)
	var err error

	dirs, err = machine.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	if _, err := reg.AddProject("filial", ws, nil); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"master", "feat"} {
		if _, err := reg.AddWorktree("filial", slug, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	return ws, dirs
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errs := runIn(t, args...)
	if code != 0 {
		t.Fatalf("railswitch %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, out, errs)
	}
	return out
}

func TestLockRenderCheck(t *testing.T) {
	ws, dirs := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	mustRun(t, "-C", ws, "render")
	for _, u := range []string{"filial-master-rails.container", "filial-feat-rails.container"} {
		if _, err := os.Stat(filepath.Join(dirs.Quadlet, u)); err != nil {
			t.Errorf("unit %s: %v", u, err)
		}
	}
	if out := mustRun(t, "-C", ws, "render", "--check"); out != "" {
		t.Errorf("render --check after render printed %q", out)
	}

	path := filepath.Join(ws, "project.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), `TZ = "Asia/Bangkok"`, `TZ = "Europe/Paris"`, 1)
	if edited == string(data) {
		t.Fatal("fixture has no TZ line")
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runIn(t, "-C", ws, "render", "--check")
	if code != 1 || !strings.Contains(out, "+++ b/.railswitch/env/master/rails.env") {
		t.Fatalf("render --check after edit: exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	if strings.Contains(out, "Europe/Paris") || strings.Contains(out, "s3cret") {
		t.Errorf("check diff shows a private env file's contents:\n%s", out)
	}
}

// TestRenderCheckNoRegistryWrite: port bases that differ from the
// registered ones are a change render --check reports, without writing
// registry.toml; render records them.
func TestRenderCheckNoRegistryWrite(t *testing.T) {
	ws, dirs := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	mustRun(t, "-C", ws, "render")
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := reg.Project("filial")
	if want.Ports["app"] != 13000 || want.Ports["db"] != 55432 {
		t.Fatalf("render recorded ports %v", want.Ports)
	}
	if err := reg.SetPorts("filial", map[string]int{"app": 13000}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	reg.Close()
	path := filepath.Join(dirs.Data, "registry.toml")
	before := readFile(t, path)

	code, out, errs := runIn(t, "-C", ws, "render", "--check")
	if code != 1 || !strings.Contains(out, "registry.toml") || !strings.Contains(out, "+ports filial") {
		t.Errorf("render --check: exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	if after := readFile(t, path); after != before {
		t.Errorf("render --check wrote registry.toml:\n%s\nwas:\n%s", after, before)
	}

	mustRun(t, "-C", ws, "render")
	if got, _ := registry(t, dirs).Project("filial"); !maps.Equal(got.Ports, want.Ports) {
		t.Errorf("ports after render = %v; want %v", got.Ports, want.Ports)
	}
	if out := mustRun(t, "-C", ws, "render", "--check"); out != "" {
		t.Errorf("render --check after render printed %q", out)
	}
}

func TestRenderNoLock(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	if err := os.Remove(filepath.Join(ws, "stack.lock")); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runIn(t, "-C", ws, "render")
	if code != 1 || !strings.Contains(errs, "railswitch lock") {
		t.Fatalf("exit %d, stderr %q", code, errs)
	}
}

func TestEnv(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	out := mustRun(t, "-C", filepath.Join(ws, "master"), "env", "rails")
	lines := strings.Split(out, "\n")
	has := func(prefix string) bool {
		for _, l := range lines {
			if strings.HasPrefix(l, prefix) {
				return true
			}
		}
		return false
	}
	for _, want := range []string{
		"DATABASE_URL=••••••••  # option: option postgres@1 [gives]",
		"PGPASSWORD=••••••••  # ",
		"TZ=Asia/Bangkok  # project: project.toml [env]",
		"TWILIO_AUTH_TOKEN  # secret: podman secret filial_twilio_auth_token",
		"WORKTREE_SLUG=master  # worktree: worktree",
	} {
		if !has(want) {
			t.Errorf("no line starting %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "s3cret") || strings.Contains(out, "TWILIO_AUTH_TOKEN=") {
		t.Errorf("env leaks a value:\n%s", out)
	}

	if code, _, errs := runIn(t, "-C", ws, "env", "rails"); code == 0 || !strings.Contains(errs, "-w") {
		t.Errorf("env at the workspace root: exit %d, stderr %q", code, errs)
	}
	if code, _, errs := runIn(t, "-C", ws, "-w", "master", "env", "nope"); code == 0 || !strings.Contains(errs, "nope") {
		t.Errorf("env nope: exit %d, stderr %q", code, errs)
	}
}

func TestEnvJSON(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	out := mustRun(t, "-C", ws, "-w", "master", "--json", "env", "rails")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	byName := map[string]map[string]any{}
	for _, r := range rows {
		if r["service"] != "rails" {
			t.Errorf("row of service %v", r["service"])
		}
		byName[r["name"].(string)] = r
	}
	tw, ok := byName["TWILIO_AUTH_TOKEN"]
	if !ok {
		t.Fatalf("no TWILIO_AUTH_TOKEN in %s", out)
	}
	if v, present := tw["value"]; !present || v != nil || tw["layer"] != "secret" || tw["source"] != "podman secret filial_twilio_auth_token" {
		t.Errorf("TWILIO_AUTH_TOKEN = %v", tw)
	}
	if v := byName["DATABASE_URL"]["value"]; v != "••••••••" {
		t.Errorf("DATABASE_URL value = %v", v)
	}
	if r := byName["TZ"]; r["value"] != "Asia/Bangkok" || r["layer"] != "project" || r["source"] != "project.toml [env]" {
		t.Errorf("TZ = %v", r)
	}
}

// TestRenderStaleLock: a lock whose hash differs from the built-in stops
// render, but not commands that only Load the workspace (env).
func TestRenderStaleLock(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	path := filepath.Join(ws, "stack.lock")
	l, err := config.ReadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	e := l.Options["redis"]
	e.SHA256 = strings.Repeat("0", 64)
	l.Options["redis"] = e
	if err := l.Write(path); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"render"}, {"render", "--check"}} {
		code, _, errs := runIn(t, append([]string{"-C", ws}, args...)...)
		if code != 1 || !strings.Contains(errs, "stack.lock is out of date for redis@1: run railswitch lock") {
			t.Errorf("%v: exit %d, stderr %q", args, code, errs)
		}
	}
	mustRun(t, "-C", ws, "-w", "master", "env", "rails")
}

// TestEnvMasksProjectGenerated: a project [env] value using a generated value
// no option declares is masked too.
func TestEnvMasksProjectGenerated(t *testing.T) {
	ws, _ := filialWS(t)
	appendFile(t, filepath.Join(ws, "mise.local.toml"), "MY_TOKEN = \"tok-123456\"\n")
	data, err := os.ReadFile(filepath.Join(ws, "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), "[env]\n", "[env]\nMY_URL = \"https://u:{generated.MY_TOKEN}@x\"\n", 1)
	if err := os.WriteFile(filepath.Join(ws, "project.toml"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "-C", ws, "lock")
	for _, args := range [][]string{{"env", "rails"}, {"--json", "env", "rails"}} {
		out := mustRun(t, append([]string{"-C", ws, "-w", "master"}, args...)...)
		if strings.Contains(out, "tok-123456") || !strings.Contains(out, "MY_URL") || !strings.Contains(out, masked) {
			t.Errorf("%v:\n%s", args, out)
		}
	}
}

// TestRenderThroughSymlink: a workspace reached through a symlink is the
// registered one, and renders the same bytes as through its real path.
func TestRenderThroughSymlink(t *testing.T) {
	ws, _ := filialWS(t)
	link := filepath.Join(t.TempDir(), "ws-link")
	if err := os.Symlink(ws, link); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "-C", link, "lock")
	mustRun(t, "-C", ws, "render")
	if out := mustRun(t, "-C", link, "render", "--check"); out != "" {
		t.Errorf("render --check via the symlink after a render via the real path:\n%s", out)
	}
	mustRun(t, "-C", link, "render")
	if out := mustRun(t, "-C", ws, "render", "--check"); out != "" {
		t.Errorf("render --check via the real path after a render via the symlink:\n%s", out)
	}
}

func appendFile(t *testing.T, path, data string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprint(f, data); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestRenderDashboardUnitWarning: a dashboard unit render can't write is
// a warning on stderr; render still succeeds.
func TestRenderDashboardUnitWarning(t *testing.T) {
	ws, dirs := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	if err := os.MkdirAll(filepath.Dir(dirs.SystemdUser), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dirs.SystemdUser, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runIn(t, "-C", ws, "render")
	if code != 0 || !strings.HasPrefix(errs, "warning: railswitch-dashboard.service: ") {
		t.Errorf("render: exit %d, stderr %q", code, errs)
	}
}
