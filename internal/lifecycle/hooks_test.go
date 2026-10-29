package lifecycle

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
	"github.com/jbigler/railswitch/internal/workspace"
)

// hookWS is a registered api project with [hooks] create, loaded.
func hookWS(t *testing.T) (*L, string) {
	t.Helper()
	_, wss, f := projects(t, "api")
	ws := wss["api"]
	toml := filepath.Join(ws, "project.toml")
	data, err := os.ReadFile(toml)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("\n[hooks]\ncreate = [\"bin/rails db:seed\", \"echo 'done'\"]\n")...)
	if err := os.WriteFile(toml, data, 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := workspace.Load(ctx, ws, "")
	if err != nil {
		t.Fatal(err)
	}
	return &L{R: f, WS: w, Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}, ws
}

func fake(l *L) *systest.Fake { return l.R.(*systest.Fake) }

// TestCreateHooks (D126): with the marker, each create hook runs in order
// in the app, then the marker goes; without it, nothing runs.
func TestCreateHooks(t *testing.T) {
	l, ws := hookWS(t)
	m, err := l.model(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.createHooks(ctx, m, "master", true); err != nil {
		t.Fatal(err)
	}
	if n := len(nonGit(fake(l))); n != 0 {
		t.Fatalf("ran %d commands without a marker", n)
	}
	if err := render.MarkCreatePending(ws, "master"); err != nil {
		t.Fatal(err)
	}
	if err := l.createHooks(ctx, m, "master", true); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"podman", "exec", "-w", "/app", "api-master-rails", "sh", "-ec", "bin/rails db:seed"},
		{"podman", "exec", "-w", "/app", "api-master-rails", "sh", "-ec", "echo 'done'"},
	}
	if got := nonGit(fake(l)); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("calls %q, want %q", got, want)
	}
	if _, err := os.Stat(render.CreatePending(ws, "master")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("marker left: %v", err)
	}
}

// TestCreateHookFails: a failing hook stops the rest and keeps the marker.
func TestCreateHookFails(t *testing.T) {
	l, ws := hookWS(t)
	m, err := l.model(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fake(l).On([]string{"podman", "exec", "-w", "/app", "api-master-rails", "sh", "-ec", "bin/rails db:seed"}, sys.Result{ExitCode: 1}, errors.New("exit status 1"))
	if err := render.MarkCreatePending(ws, "master"); err != nil {
		t.Fatal(err)
	}
	err = l.createHooks(ctx, m, "master", true)
	if err == nil || !strings.Contains(err.Error(), "the next up runs it again") {
		t.Fatalf("err = %v", err)
	}
	if n := len(nonGit(fake(l))); n != 1 {
		t.Errorf("%d calls after the failure, want 1", n)
	}
	if _, err := os.Stat(render.CreatePending(ws, "master")); err != nil {
		t.Errorf("marker gone: %v", err)
	}
}

// TestCreateHooksResume: a retry skips the hooks recorded as done, and a
// route that never came up runs none and keeps the marker.
func TestCreateHooksResume(t *testing.T) {
	l, ws := hookWS(t)
	m, err := l.model(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := render.MarkCreatePending(ws, "master"); err != nil {
		t.Fatal(err)
	}
	if err := l.createHooks(ctx, m, "master", false); err != nil {
		t.Fatal(err)
	}
	if n := len(nonGit(fake(l))); n != 0 {
		t.Fatalf("ran %d commands with the app not ready", n)
	}
	if err := os.WriteFile(render.CreatePending(ws, "master"), []byte("bin/rails db:seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.createHooks(ctx, m, "master", true); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"podman", "exec", "-w", "/app", "api-master-rails", "sh", "-ec", "echo 'done'"}}
	if got := nonGit(fake(l)); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("calls %q, want %q", got, want)
	}
	if _, err := os.Stat(render.CreatePending(ws, "master")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("marker left: %v", err)
	}
}
