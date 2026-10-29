package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/testfixture"
	"github.com/jbigler/railswitch/internal/unithash"
)

// TestHookUnits (D126): start hooks go in front of the app's server, deps
// hooks wrap the pack's deps entrypoint; each script is mounted read-only.
func TestHookUnits(t *testing.T) {
	ws, units := renderUnits(t, fixtureDir, func(p *config.Project) {
		p.Hooks = config.Hooks{Start: []string{"bin/rails db:test:prepare"}, Deps: []string{"gem install retest"}}
	})
	const app, deps = "filial-master-rails.container", "filial-master-deps.container"
	hasLine(t, units, app, "Entrypoint=/usr/local/bin/entrypoint-app.sh")
	hasLine(t, units, app, "Exec="+StartHookPath+" bin/dev")
	hasLine(t, units, app, "Volume="+ws+"/.railswitch/hooks/start.sh:"+StartHookPath+":ro,z")
	hasLine(t, units, deps, "Entrypoint="+DepsHookPath)
	hasLine(t, units, deps, "Exec=/usr/local/bin/entrypoint-deps.sh")
	hasLine(t, units, deps, "Volume="+ws+"/.railswitch/hooks/deps.sh:"+DepsHookPath+":ro,z")
	hasLine(t, units, deps, "Volume="+ws+"/.railswitch/assets/rails/entrypoints/entrypoint-deps.sh:/usr/local/bin/entrypoint-deps.sh:ro,z")
	noLinePrefix(t, units, app, "Volume="+ws+"/.railswitch/hooks/deps.sh")
}

// TestNoHooksNoChange: without [hooks] the units name no hook script.
func TestNoHooksNoChange(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, nil)
	for _, f := range []string{"filial-master-rails.container", "filial-master-deps.container"} {
		if u := string(units[f]); strings.Contains(u, StartHookPath) || strings.Contains(u, DepsHookPath) || strings.Contains(u, "/.railswitch/hooks/") {
			t.Errorf("%s names a hook:\n%s", f, units[f])
		}
	}
	hasLine(t, units, "filial-master-rails.container", "Exec=bin/dev")
}

// TestHookScriptsRun runs the generated scripts with sh: order, each hook
// in its own shell (its exit 0 or cd ends only itself), a failure stops
// the rest and the command, quotes survive.
func TestHookScriptsRun(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	run := func(script []byte, args ...string) (string, error) {
		p := filepath.Join(dir, "hook.sh")
		if err := os.WriteFile(p, script, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", append([]string{p}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := run(hookScript("start", []string{"echo one; exit 0; echo never", "cd /; echo \"it's $PWD\"", `printf '%s\n' 'two\n'`, `echo "in $PWD"`}, true, sub), "sh", "-c", `echo "server in $PWD"`)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := "hook start: echo one; exit 0; echo never\none\nhook start: cd /; echo \"it's $PWD\"\nit's /\nhook start: printf '%s\\n' 'two\\n'\ntwo\\n\nhook start: echo \"in $PWD\"\nin " + sub + "\nserver in " + dir + "\n"
	if out != want {
		t.Errorf("start output:\n%q\nwant\n%q", out, want)
	}
	out, err = run(hookScript("start", []string{"false", "echo after"}, true, dir), "echo", "server")
	if err == nil || strings.Contains(out, "after") || strings.Contains(out, "server") {
		t.Errorf("a failing hook went on: %v\n%s", err, out)
	}
	out, err = run(hookScript("deps", []string{"echo hook"}, false, dir), "echo", "installs")
	if err != nil || out != "installs\nhook deps: echo hook\nhook\n" {
		t.Errorf("deps: %v\n%q", err, out)
	}
	out, err = run(hookScript("deps", []string{"echo hook"}, false, dir), "false")
	if err == nil || strings.Contains(out, "hook") {
		t.Errorf("deps hooks ran after a failed install: %v\n%q", err, out)
	}
	if out, err = run(hookScript("deps", []string{"echo hook"}, false, dir)); err != nil || !strings.HasSuffix(out, "hook\n") {
		t.Errorf("deps without a command: %v\n%q", err, out)
	}
}

// TestHookAssets: the scripts are planned 0755 under .railswitch/hooks.
func TestHookAssets(t *testing.T) {
	ws := testfixture.Workspace(t, fixtureDir)
	m := mustBuild(t, fixtureInputs(t, ws, func(p *config.Project) { p.Hooks.Start = []string{"true"} }))
	plan, err := m.Plan()
	if err != nil {
		t.Fatal(err)
	}
	f, ok := plan.Files[startHookRel]
	if !ok || f.Mode != 0o755 || !strings.Contains(string(f.Data), "(cd '/app' && exec sh -ec 'true')\nexec \"$@\"\n") {
		t.Errorf("start.sh %v %o:\n%s", ok, f.Mode, f.Data)
	}
	if _, ok := plan.Files[depsHookRel]; ok {
		t.Error("deps.sh planned without deps hooks")
	}
}

// TestHookChangeRestarts: a changed start or deps hook changes the app's or
// deps's unit hash (up restarts a running one); the unit text alone doesn't.
func TestHookChangeRestarts(t *testing.T) {
	ws := testfixture.Workspace(t, fixtureDir)
	hash := func(h config.Hooks) (string, string) {
		units, err := Quadlet(mustBuild(t, fixtureInputs(t, ws, func(p *config.Project) { p.Hooks = h })))
		if err != nil {
			t.Fatal(err)
		}
		key := unithash.Key("filial")
		return unithash.Read(units["filial-master-rails.container"], key), unithash.Read(units["filial-master-deps.container"], key)
	}
	a1, d1 := hash(config.Hooks{Start: []string{"one"}, Deps: []string{"one"}})
	a2, d2 := hash(config.Hooks{Start: []string{"two"}, Deps: []string{"one"}})
	a3, d3 := hash(config.Hooks{Start: []string{"two"}, Deps: []string{"two"}})
	if a1 == a2 || d1 != d2 || a2 != a3 || d2 == d3 {
		t.Errorf("hashes app %s %s %s, deps %s %s %s", a1, a2, a3, d1, d2, d3)
	}
}

// TestCreateHooksNeedHealth: without the app's health check up couldn't
// tell when the app is ready for them.
func TestCreateHooksNeedHealth(t *testing.T) {
	ws := testfixture.Workspace(t, fixtureDir)
	err := buildErr(fixtureInputs(t, ws, func(p *config.Project) {
		p.Hooks.Create = []string{"true"}
		s := p.Services["rails"]
		s.Health = "none"
		if p.Services == nil {
			p.Services = map[string]config.ServiceOverride{}
		}
		p.Services["rails"] = s
	}))
	if err == nil || !strings.Contains(err.Error(), "[hooks] create need the app's health check") {
		t.Errorf("err = %v", err)
	}
}
