package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

const filialFixture = "../../testdata/fixtures/filial"

// runIn runs railswitch with a fake runner (no host git config).
func runIn(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := dispatch(context.Background(), &systest.Fake{}, args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

// filialWS copies the Filial fixture to a temp workspace with the built-in
// default Containerfiles ejected into containerfiles/, points XDG_CONFIG_HOME
// and XDG_DATA_HOME into the temp dir, and registers the project with its
// master (ID 1) and feat (ID 2) worktrees.
func filialWS(t *testing.T) (ws string, dirs machine.Dirs) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	ws = filepath.Join(tmp, "filial")
	if err := os.CopyFS(ws, os.DirFS(filialFixture)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(ws, "db-dumps"), 0o700); err != nil {
		t.Fatal(err)
	}
	cat, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(ws, "containerfiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []struct {
		files fs.FS
		name  string
	}{
		{cat.Packs[pack.Ref{Name: "rails", Version: 1}].Files, "Containerfile.base"},
		{cat.Options[pack.Ref{Name: "postgres", Version: 1}].Files, "Containerfile.pg-client"},
		{cat.Tools["nvim"].Files, "Containerfile.nvim"},
		{cat.Options[pack.Ref{Name: "playwright", Version: 1}].Files, "Containerfile.playwright"},
	} {
		data, err := fs.ReadFile(d.files, d.name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, "containerfiles", d.name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dirs, err = machine.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	if _, err := reg.AddProject("filial", ws); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"master", "feat"} {
		if _, err := reg.AddWorktree("filial", slug); err != nil {
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
