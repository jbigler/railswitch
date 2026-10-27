package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// gitWS is lockedWS with master a git repo and feat a worktree of it on
// branch feat.
func gitWS(t *testing.T) (string, machine.Dirs) {
	t.Helper()
	ws, dirs := lockedWS(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR", "GIT_COMMITTER"} {
		t.Setenv(k+"_NAME", "Test")
		t.Setenv(k+"_EMAIL", "test@example.com")
	}
	master := filepath.Join(ws, "master")
	gitIn(t, master, "init", "-q", "-b", "master")
	gitIn(t, master, "add", ".")
	gitIn(t, master, "commit", "-q", "-m", "init")
	if err := os.RemoveAll(filepath.Join(ws, "feat")); err != nil {
		t.Fatal(err)
	}
	gitIn(t, master, "worktree", "add", "-q", "-b", "feat", filepath.Join(ws, "feat"))
	return ws, dirs
}

func TestListJSON(t *testing.T) {
	ws, dirs := gitWS(t)
	shareSlug(t, dirs, "feat")
	f := fakeHost()
	f.Passthrough = []string{"git"}
	code, out, errs := runWith(t, f, "-C", ws, "--json", "wt", "ls")
	if code != 0 {
		t.Fatalf("wt ls: exit %d: %s", code, errs)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := []map[string]any{
		{"slug": "master", "id": float64(1), "branch": "master", "dir": filepath.Join(ws, "master"), "dirty": false, "share": nil},
		{"slug": "feat", "id": float64(2), "branch": "feat", "dir": filepath.Join(ws, "feat"), "dirty": false,
			"share": map[string]any{"host": "box.tail1234.ts.net", "ip": "100.64.0.7"}},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %v; want %v", rows, want)
	}
	for i := range want {
		for k, v := range want[i] {
			if !reflect.DeepEqual(rows[i][k], v) {
				t.Errorf("row %d %s = %v; want %v", i, k, rows[i][k], v)
			}
		}
		if len(rows[i]) != len(want[i]) {
			t.Errorf("row %d has fields %v; want exactly slug, id, branch, dir, dirty, share", i, rows[i])
		}
	}
}

func TestWtAddRm(t *testing.T) {
	ws, dirs := gitWS(t)
	f := fakeHost()
	f.Passthrough = []string{"git"}
	if code, out, errs := runWith(t, f, "-C", ws, "wt", "add", "feat-x"); code != 0 {
		t.Fatalf("wt add: exit %d\n%s%s", code, out, errs)
	}
	unit := filepath.Join(dirs.Quadlet, "filial-feat-x-rails.container")
	if _, err := os.Stat(unit); err != nil {
		t.Fatalf("wt add rendered no unit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "feat-x", "node_modules")); err != nil {
		t.Errorf("node_modules not pre-created: %v", err)
	}
	n := len(f.Calls)
	if code, out, errs := runWith(t, f, "-C", ws, "wt", "rm", "feat-x"); code != 0 {
		t.Fatalf("wt rm: exit %d\n%s%s", code, out, errs)
	}
	if _, err := os.Stat(unit); err == nil {
		t.Error("wt rm left the unit")
	}
	for _, want := range [][]string{
		{"podman", "rm", "-f", "--ignore", "filial-feat-x-rails"},
		{"podman", "network", "rm", "-f", "filial-feat-x-dev"},
	} {
		if !slices.ContainsFunc(f.Calls[n:], func(c []string) bool { return slices.Equal(c, want) }) {
			t.Errorf("no call %q", want)
		}
	}
	for _, c := range f.Calls[n:] {
		if c[0] == "podman" || c[0] == "systemctl" {
			for _, a := range c {
				if strings.HasPrefix(a, "filial-") && !strings.HasPrefix(a, "filial-feat-x-") {
					t.Errorf("call names another worktree's object: %q", c)
				}
			}
		}
	}
	if _, err := os.Stat(filepath.Join(ws, "feat-x")); err == nil {
		t.Error("wt rm left the checkout")
	}
}

func TestWtAddUp(t *testing.T) {
	ws, _ := gitWS(t)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})) // every router is there
	t.Cleanup(srv.Close)
	old := traefikAPI
	t.Cleanup(func() { traefikAPI = old })
	traefikAPI = srv.URL
	f := fakeHost()
	f.Passthrough = []string{"git"}
	f.On([]string{"podman", "image", "inspect", "--format", "{{.Digest}}"}, sys.Result{Stdout: []byte("sha256:aa\n")}, nil)
	code, out, errs := runWith(t, f, "-C", ws, "wt", "add", "--up", "feat-x")
	if code != 0 {
		t.Fatalf("wt add --up: exit %d\n%s%s", code, out, errs)
	}
	if !strings.Contains(out, "added worktree feat-x") || !strings.Contains(out, "http://feat-x.") {
		t.Errorf("stdout %q; want the add line and up's ready URL", out)
	}
	want := []string{"systemctl", "--user", "start", "filial-feat-x-rails.service"}
	if !slices.ContainsFunc(f.Calls, func(c []string) bool { return slices.Equal(c, want) }) {
		t.Errorf("no call %q", want)
	}
	for _, c := range f.Calls {
		if c[0] == "systemctl" && slices.Contains(c, "start") && slices.ContainsFunc(c, func(a string) bool {
			return strings.HasPrefix(a, "filial-feat-") && !strings.HasPrefix(a, "filial-feat-x-")
		}) {
			t.Errorf("started another worktree: %q", c)
		}
	}
}
