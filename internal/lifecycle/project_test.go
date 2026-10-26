package lifecycle

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
	"github.com/jbigler/railswitch/internal/testfixture"
	"github.com/jbigler/railswitch/internal/workspace"
)

// projects registers and renders, from the Filial fixture, one workspace
// per prefix, each with worktree master, and returns the machine dirs, the
// workspaces by prefix and a fake host.
func projects(t *testing.T, prefixes ...string) (machine.Dirs, map[string]string, *systest.Fake) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	dirs, err := machine.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	f := &systest.Fake{}
	f.On([]string{"podman", "info", "--format", "{{.Store.GraphRoot}}"}, sys.Result{Stdout: []byte("/g\n")}, nil)
	f.On([]string{"podman", "image", "inspect", "--format", "{{.Digest}}"}, sys.Result{Stdout: []byte("sha256:aa\n")}, nil)
	wss := map[string]string{}
	for _, pfx := range prefixes {
		src := testfixture.Workspace(t, "../../testdata/fixtures/filial")
		ws := filepath.Join(filepath.Dir(src), pfx)
		if err := os.Rename(src, ws); err != nil {
			t.Fatal(err)
		}
		toml := filepath.Join(ws, "project.toml")
		data, err := os.ReadFile(toml)
		if err != nil {
			t.Fatal(err)
		}
		data = bytes.Replace(data, []byte(`prefix      = "filial"`), []byte(`prefix      = "`+pfx+`"`), 1)
		if err := os.WriteFile(toml, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(ws, "master", ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		reg, err := machine.Open(dirs.Data)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reg.AddProject(pfx, ws, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := reg.AddWorktree(pfx, "master", nil); err != nil {
			t.Fatal(err)
		}
		if err := reg.Save(); err != nil {
			t.Fatal(err)
		}
		reg.Close()
		if err := workspace.Lock(ctx, ws); err != nil {
			t.Fatal(err)
		}
		w, err := workspace.Load(ctx, ws, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Render(ctx, f, false); err != nil {
			t.Fatal(err)
		}
		wss[pfx] = ws
	}
	f.Calls, f.Cmds, f.Stdins = nil, nil, nil
	return dirs, wss, f
}

// nonGit are f's calls but git's.
func nonGit(f *systest.Fake) [][]string {
	var out [][]string
	for _, c := range f.Calls {
		if c[0] != "git" {
			out = append(out, c)
		}
	}
	return out
}

func unitNames(t *testing.T, dir, prefix string) []string {
	t.Helper()
	us, err := render.MarkedUnits(dir, prefix)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, u := range us {
		out = append(out, filepath.Base(u.Path))
	}
	return out
}

// TestProjectRemoveExactNames (Review Focus 4): removing api never names an
// api2 object.
func TestProjectRemoveExactNames(t *testing.T) {
	dirs, _, f := projects(t, "api", "api2")
	before := unitNames(t, dirs.Quadlet, "api2")
	if len(before) == 0 || len(unitNames(t, dirs.Quadlet, "api")) == 0 {
		t.Fatal("fixture rendered no units")
	}
	if err := ProjectRemove(ctx, f, dirs, &bytes.Buffer{}, strings.NewReader(""), false, "api", true, true); err != nil {
		t.Fatal(err)
	}
	api := func(a string) bool { return strings.HasPrefix(a, "api-") || strings.HasPrefix(a, "api_") }
	for _, c := range nonGit(f) {
		if slices.ContainsFunc(c, func(a string) bool { return strings.HasPrefix(a, "api2-") || strings.HasPrefix(a, "api2_") }) {
			t.Errorf("names an api2 object: %q", c)
		}
		if slices.ContainsFunc(c, api) {
			continue
		}
		j := joined(c)
		if !(strings.HasPrefix(j, "podman container inspect ") && c[len(c)-1] == traefikContainer) && j != "systemctl --user daemon-reload" {
			t.Errorf("unexpected call: %q", c)
		}
	}
	if after := unitNames(t, dirs.Quadlet, "api2"); !slices.Equal(after, before) {
		t.Errorf("api2 units %v, want %v", after, before)
	}
	if left := unitNames(t, dirs.Quadlet, "api"); len(left) != 0 {
		t.Errorf("api units left: %v", left)
	}
	reg, err := machine.Read(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Projects) != 1 || reg.Projects[0].Prefix != "api2" || len(reg.Worktrees) != 1 || reg.Worktrees[0].Prefix != "api2" {
		t.Errorf("registry %+v", reg)
	}
	traefik, err := os.ReadFile(filepath.Join(dirs.Quadlet, machine.ProxyUnitName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(traefik), "api-proxy.network") || !strings.Contains(string(traefik), "api2-proxy.network") {
		t.Errorf("traefik unit:\n%s", traefik)
	}
}

func TestProjectRemoveOrder(t *testing.T) {
	dirs, _, f := projects(t, "api")
	traefikRunning(f, "api_proxy railswitch_machine ")
	if err := ProjectRemove(ctx, f, dirs, &bytes.Buffer{}, strings.NewReader(""), false, "api", true, false); err != nil {
		t.Fatal(err)
	}
	calls := nonGit(f)
	wantInOrder(t, calls,
		"systemctl --user stop api-master-rails.service",
		"podman network disconnect api_proxy railswitch-traefik",
		"podman network rm api-master-dev",
		"podman network rm api_proxy",
		"systemctl --user daemon-reload")
	for _, c := range calls {
		if j := joined(c); strings.HasPrefix(j, "podman network rm") && slices.Contains(c, "-f") {
			t.Errorf("forced network removal: %q", c)
		}
		if strings.HasPrefix(joined(c), "podman volume") && strings.Contains(joined(c), "api_") {
			t.Errorf("touches a shared volume without --purge: %q", c)
		}
	}
}

func TestProjectRemoveDefaultsNo(t *testing.T) {
	dirs, _, f := projects(t, "api")
	out := &bytes.Buffer{}
	if err := ProjectRemove(ctx, f, dirs, out, strings.NewReader("\n"), true, "api", false, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[y/N]") || !strings.Contains(out.String(), "aborted") {
		t.Errorf("output %q", out.String())
	}
	if len(nonGit(f)) != 0 {
		t.Errorf("ran:\n%s", dump(nonGit(f)))
	}
	if len(unitNames(t, dirs.Quadlet, "api")) == 0 {
		t.Error("units removed")
	}
	if err := ProjectRemove(ctx, f, dirs, out, strings.NewReader(""), false, "api", false, false); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("non-TTY: got %v", err)
	}
	if reg, err := machine.Read(dirs.Data); err != nil || len(reg.Projects) != 1 {
		t.Fatalf("registry %+v %v", reg, err)
	}
}

func TestProjectRemoveMissingWorkspace(t *testing.T) {
	dirs, wss, f := projects(t, "api", "api2")
	if err := os.RemoveAll(wss["api"]); err != nil {
		t.Fatal(err)
	}
	if err := ProjectRemove(ctx, f, dirs, &bytes.Buffer{}, strings.NewReader(""), false, "api", true, false); err != nil {
		t.Fatal(err)
	}
	calls := nonGit(f)
	wantInOrder(t, calls,
		"systemctl --user stop api-master-rails.service",
		"podman network rm api-master-dev",
		"podman network rm api_proxy",
		"systemctl --user daemon-reload")
	for _, c := range calls {
		if strings.Contains(joined(c), "api2") {
			t.Errorf("names api2: %q", c)
		}
	}
	if left := unitNames(t, dirs.Quadlet, "api"); len(left) != 0 {
		t.Errorf("api units left: %v", left)
	}
	if len(unitNames(t, dirs.Quadlet, "api2")) == 0 {
		t.Error("api2 units removed")
	}
}

func TestProjectRemovePurgeKeepsRepo(t *testing.T) {
	dirs, wss, f := projects(t, "api")
	ws := wss["api"]
	if _, err := os.Stat(filepath.Join(ws, ".railswitch")); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	if err := ProjectRemove(ctx, f, dirs, out, strings.NewReader(""), false, "api", true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".railswitch")); !os.IsNotExist(err) {
		t.Errorf(".railswitch: %v", err)
	}
	for _, p := range []string{ws, filepath.Join(ws, "master", ".git"), filepath.Join(ws, "project.toml")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if !strings.Contains(out.String(), ws) {
		t.Errorf("output lacks the workspace path: %q", out.String())
	}
	calls := nonGit(f)
	wantInOrder(t, calls, "podman volume exists api_gems_ruby3.3.6", "podman volume rm api_gems_ruby3.3.6")
	for _, c := range calls {
		if j := joined(c); strings.HasPrefix(j, "podman volume rm api_") && slices.Contains(c, "-f") {
			t.Errorf("forced: %q", c)
		}
		if slices.ContainsFunc(c, func(a string) bool { return strings.HasPrefix(a, "--filter") }) || (c[0] == "git" && slices.Contains(c, "worktree")) {
			t.Errorf("filters or removes a worktree: %q", c)
		}
	}
}
