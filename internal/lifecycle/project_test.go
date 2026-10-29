package lifecycle

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
		if err := workspace.Lock(ctx, ws, workspace.Accept{All: true}); err != nil {
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
	traefikRunning(f, "api_proxy api2_proxy railswitch_machine ")
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
		if !(strings.HasPrefix(j, "podman container inspect ") && c[len(c)-1] == traefikContainer) && j != "systemctl --user daemon-reload" && j != "podman network ls --format json" && j != "podman images --format json" {
			t.Errorf("unexpected call: %q", c)
		}
	}
	if n := count(nonGit(f), "podman network disconnect"); n != 1 {
		t.Errorf("%d disconnects:\n%s", n, dump(nonGit(f)))
	}
	find(t, nonGit(f), "podman network disconnect api_proxy railswitch-traefik")
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

// TestProjectRemoveImages: project rm removes every name railswitch built
// for the prefix, never another prefix's or a pulled image; a refused rmi
// is printed and the removal finishes.
func TestProjectRemoveImages(t *testing.T) {
	dirs, _, f := projects(t, "api", "api2")
	traefikRunning(f, "api_proxy api2_proxy railswitch_machine ")
	f.On([]string{"podman", "images", "--format", "json"}, sys.Result{Stdout: []byte(`[
		{"Id":"a","Names":["localhost/api/app:3.3-0123abcd","localhost/api/app:3.3-89abcdef"]},
		{"Id":"b","Names":["localhost/api/postgres:17-00000000"]},
		{"Id":"c","Names":["localhost/api2/app:3.3-0123abcd"]},
		{"Id":"d","Names":["docker.io/library/postgres:17","localhost/api/notours:latest"]}]`)}, nil)
	f.On([]string{"podman", "rmi", "localhost/api/postgres:17-00000000"}, sys.Result{ExitCode: 2}, fmt.Errorf("image in use"))
	var out bytes.Buffer
	if err := ProjectRemove(ctx, f, dirs, &out, strings.NewReader(""), false, "api", true, false); err != nil {
		t.Fatal(err)
	}
	var rmi []string
	for _, c := range f.Calls {
		if len(c) == 3 && c[0] == "podman" && c[1] == "rmi" {
			rmi = append(rmi, c[2])
		}
	}
	want := []string{"localhost/api/app:3.3-0123abcd", "localhost/api/app:3.3-89abcdef", "localhost/api/postgres:17-00000000"}
	if !slices.Equal(rmi, want) {
		t.Errorf("rmi %q, want %q", rmi, want)
	}
	if !strings.Contains(out.String(), "kept image localhost/api/postgres:17-00000000") || !strings.Contains(out.String(), "removed 2 images of api") || !strings.Contains(out.String(), "removed project api") {
		t.Errorf("output:\n%s", out.String())
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
		"systemctl --user daemon-reload",
		"systemctl --user stop api-proxy-network.service")
	wantInOrder(t, calls, "systemctl --user daemon-reload", "systemctl --user stop api-master-dev-network.service")
	for _, c := range calls {
		if j := joined(c); strings.Contains(j, "railswitch-traefik.service") || strings.HasPrefix(j, "systemctl --user restart") {
			t.Errorf("touches Traefik's unit: %q", c)
		}
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
	shared(t, dirs, "api", 13001, 19001)
	// The recorded ports, and its db port served to itself by hand.
	serving(f, 13001, 19001, 55433)
	if err := ProjectRemove(ctx, f, dirs, &bytes.Buffer{}, strings.NewReader(""), false, "api", true, true); err != nil {
		t.Fatal(err)
	}
	calls := nonGit(f)
	wantInOrder(t, calls,
		"tailscale serve --https=13001 off",
		"tailscale serve --https=19001 off",
		"systemctl --user stop api-master-rails.service",
		"podman volume rm -f api-master-node-modules",
		"podman network rm api-master-dev",
		"podman network rm api_proxy",
		"systemctl --user daemon-reload",
		"systemctl --user stop api-master-dev-network.service",
		"systemctl --user stop api-proxy-network.service")
	if n := count(calls, "tailscale serve --https="); n != 2 {
		t.Errorf("%d serve offs; only the share ports:\n%s", n, dump(calls))
	}
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

// shared records worktree master of prefix as shared on ports.
func shared(t *testing.T, dirs machine.Dirs, prefix string, ports ...int) {
	t.Helper()
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	if err := reg.SetShare(prefix, "master", &machine.ShareEntry{Host: "box.tail1234.ts.net", IP: "100.64.0.7", Ports: ports}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
}

// serving scripts tailscale serving each port to its own loopback port.
func serving(f *systest.Fake, ports ...int) {
	var tcp, web []string
	for _, p := range ports {
		ps := strconv.Itoa(p)
		tcp = append(tcp, `"`+ps+`": {"HTTPS": true}`)
		web = append(web, `"box.tail1234.ts.net:`+ps+`": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:`+ps+`"}}}`)
	}
	f.On([]string{"tailscale", "serve", "status", "--json"}, sys.Result{Stdout: []byte(`{"TCP": {` + strings.Join(tcp, ",") + `}, "Web": {` + strings.Join(web, ",") + `}}`)}, nil)
}

// TestProjectRemoveSharedLoaded: a shared worktree's own share ports are
// turned off (DropShare) before it is stopped; a port it doesn't share stays.
func TestProjectRemoveSharedLoaded(t *testing.T) {
	dirs, _, f := projects(t, "api")
	shared(t, dirs, "api", 13001, 19001)
	serving(f, 13001, 19001, 55433)
	if err := ProjectRemove(ctx, f, dirs, &bytes.Buffer{}, strings.NewReader(""), false, "api", true, false); err != nil {
		t.Fatal(err)
	}
	calls := nonGit(f)
	wantInOrder(t, calls, "tailscale serve --https=13001 off", "tailscale serve --https=19001 off", "systemctl --user stop api-master-rails.service")
	if n := count(calls, "tailscale serve --https="); n != 2 {
		t.Errorf("%d serve offs:\n%s", n, dump(calls))
	}
}

// TestProjectRemoveSharedRecordedPort (final I1): a recorded share port the
// model no longer has (14001) is turned off too.
func TestProjectRemoveSharedRecordedPort(t *testing.T) {
	dirs, _, f := projects(t, "api")
	shared(t, dirs, "api", 13001, 14001)
	serving(f, 13001, 14001, 19001)
	if err := ProjectRemove(ctx, f, dirs, &bytes.Buffer{}, strings.NewReader(""), false, "api", true, false); err != nil {
		t.Fatal(err)
	}
	calls := nonGit(f)
	wantInOrder(t, calls, "tailscale serve --https=13001 off", "tailscale serve --https=14001 off", "tailscale serve --https=19001 off", "systemctl --user stop api-master-rails.service")
}

// TestProjectRemoveServeStatusFails (final M4): with tailscaled down,
// project rm of a shared project warns and carries on, loaded or not.
func TestProjectRemoveServeStatusFails(t *testing.T) {
	for _, loaded := range []bool{true, false} {
		t.Run(fmt.Sprint("loaded=", loaded), func(t *testing.T) {
			dirs, wss, f := projects(t, "api")
			if !loaded {
				if err := os.RemoveAll(wss["api"]); err != nil {
					t.Fatal(err)
				}
			}
			shared(t, dirs, "api", 13001)
			f.On([]string{"tailscale", "serve", "status"}, sys.Result{ExitCode: 1}, fmt.Errorf("tailscale: %w (exit 1)", sys.ErrExit))
			out := &bytes.Buffer{}
			if err := ProjectRemove(ctx, f, dirs, out, strings.NewReader(""), false, "api", true, false); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "warning: worktree master is shared") {
				t.Errorf("output %q lacks the warning", out.String())
			}
			if n := count(nonGit(f), "systemctl --user stop"); n == 0 {
				t.Errorf("stopped nothing:\n%s", dump(nonGit(f)))
			}
		})
	}
}

// TestProjectRemovePurgeOtherPrefix: a workspace that now loads as another
// prefix keeps its .railswitch/.
func TestProjectRemovePurgeOtherPrefix(t *testing.T) {
	dirs, wss, f := projects(t, "api")
	toml := filepath.Join(wss["api"], "project.toml")
	data, err := os.ReadFile(toml)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(toml, bytes.Replace(data, []byte(`prefix      = "api"`), []byte(`prefix      = "api3"`), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	if err := ProjectRemove(ctx, f, dirs, out, strings.NewReader(""), false, "api", true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wss["api"], ".railswitch")); err != nil {
		t.Errorf(".railswitch removed: %v", err)
	}
	if !strings.Contains(out.String(), "kept ") {
		t.Errorf("output %q", out.String())
	}
}

// TestProjectRemoveSharedUnrecorded: a share entry from before the ports
// were recorded, with no workspace, turns nothing off and says how to check.
func TestProjectRemoveSharedUnrecorded(t *testing.T) {
	dirs, wss, f := projects(t, "api")
	if err := os.RemoveAll(wss["api"]); err != nil {
		t.Fatal(err)
	}
	shared(t, dirs, "api")
	serving(f, 13001, 19001)
	out := &bytes.Buffer{}
	if err := ProjectRemove(ctx, f, dirs, out, strings.NewReader(""), false, "api", true, false); err != nil {
		t.Fatal(err)
	}
	if n := count(nonGit(f), "tailscale"); n != 0 {
		t.Errorf("ran tailscale:\n%s", dump(nonGit(f)))
	}
	if !strings.Contains(out.String(), "tailscale serve status") {
		t.Errorf("output %q", out.String())
	}
}
