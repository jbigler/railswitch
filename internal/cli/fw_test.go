package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

func TestRenderWritesFirewall(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	mustRun(t, "-C", ws, "render")
	fw := filepath.Join(ws, ".railswitch", "firewall")
	desc, err := os.ReadFile(filepath.Join(fw, "hooks.d", "filial-firewall.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"NETWORK_DIR=` + graphRoot + `/networks"`,
		`"path": "` + fw + `/hook.sh"`,
		`"` + fw + `"`,
	} {
		if !strings.Contains(string(desc), want) {
			t.Errorf("descriptor has no %s:\n%s", want, desc)
		}
	}
	for name, mode := range map[string]os.FileMode{"hook.sh": 0o755, "firewall.sh": 0o755, "deps.allow": 0o644, "agent.allow": 0o644} {
		fi, err := os.Stat(filepath.Join(fw, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != mode {
			t.Errorf("%s mode %04o, want %04o", name, fi.Mode().Perm(), mode)
		}
	}
	deps, err := os.ReadFile(filepath.Join(fw, "deps.allow"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(deps), "\ngithub.com\n"); n != 1 {
		t.Errorf("deps.allow lists github.com %d times:\n%s", n, deps)
	}
}

func TestRenderNeedsGraphRoot(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	code, _, errs := runWith(t, &systest.Fake{}, "-C", ws, "render")
	if code != 1 || !strings.Contains(errs, "graph root") {
		t.Fatalf("exit %d, stderr %q", code, errs)
	}
}

// fwCall is the firewall.sh call of a fw run: its argv and the env vars
// railswitch set (FIREWALL_DIR, PREFIX, WORKTREE, SELF_FIREWALLED).
func fwCall(t *testing.T, f *systest.Fake) ([]string, map[string]string) {
	t.Helper()
	for i, c := range f.Cmds {
		if strings.HasSuffix(c.Name, "/firewall.sh") {
			env := map[string]string{}
			for _, kv := range c.Env {
				k, v, _ := strings.Cut(kv, "=")
				switch k {
				case "FIREWALL_DIR", "PREFIX", "WORKTREE", "SELF_FIREWALLED":
					env[k] = v
				}
			}
			if c.Timeout <= 0 {
				t.Errorf("firewall.sh runs without a timeout")
			}
			return f.Calls[i], env
		}
	}
	t.Fatalf("no firewall.sh call in %v", f.Calls)
	return nil, nil
}

func TestFwShow(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	mustRun(t, "-C", ws, "render")
	script := filepath.Join(ws, ".railswitch", "firewall", "firewall.sh")

	f := fakeHost()
	if code, _, errs := runWith(t, f, "-C", ws, "fw", "show"); code != 0 {
		t.Fatalf("fw show: exit %d, stderr %q", code, errs)
	}
	argv, env := fwCall(t, f)
	if !slices.Equal(argv, []string{script, "show"}) {
		t.Errorf("argv %v", argv)
	}
	want := map[string]string{"FIREWALL_DIR": filepath.Join(ws, ".railswitch", "firewall"), "PREFIX": "filial", "WORKTREE": "", "SELF_FIREWALLED": "browser"}
	for k, v := range want {
		if got, ok := env[k]; !ok || got != v {
			t.Errorf("%s=%q (set %v), want %q", k, got, ok, v)
		}
	}

	f = fakeHost()
	if code, _, errs := runWith(t, f, "-C", filepath.Join(ws, "master"), "fw", "show", "rails"); code != 0 {
		t.Fatalf("fw show rails: exit %d, stderr %q", code, errs)
	}
	argv, env = fwCall(t, f)
	if !slices.Equal(argv, []string{script, "show", "rails"}) || env["WORKTREE"] != "master" {
		t.Errorf("argv %v, WORKTREE %q", argv, env["WORKTREE"])
	}

	// firewall.sh failing: its message is on stderr already; exit 1.
	f = fakeHost()
	f.On([]string{script}, sys.Result{ExitCode: 1}, sys.ErrExit)
	if code, _, _ := runWith(t, f, "-C", ws, "fw", "show"); code != 1 {
		t.Errorf("failing firewall.sh: exit %d", code)
	}
}

func TestFwAllow(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")

	// Not rendered yet: no firewall.sh.
	if code, _, errs := runIn(t, "-C", ws, "-w", "master", "fw", "allow", "nvim", "example.com"); code != 1 || !strings.Contains(errs, "railswitch render") {
		t.Errorf("before render: exit %d, stderr %q", code, errs)
	}
	mustRun(t, "-C", ws, "render")

	f := fakeHost()
	if code, _, errs := runWith(t, f, "-C", ws, "-w", "master", "fw", "allow", "nvim", "example.com", "10.1.2.3"); code != 0 {
		t.Fatalf("fw allow: exit %d, stderr %q", code, errs)
	}
	argv, env := fwCall(t, f)
	if !slices.Equal(argv, []string{filepath.Join(ws, ".railswitch", "firewall", "firewall.sh"), "allow", "nvim", "example.com", "10.1.2.3"}) || env["WORKTREE"] != "master" {
		t.Errorf("argv %v, WORKTREE %q", argv, env["WORKTREE"])
	}

	for _, args := range [][]string{
		{"-C", ws, "fw", "allow", "nvim", "example.com"}, // not in a worktree
		{"-C", ws, "-w", "master", "fw", "allow", "nvim"},
		{"-C", ws, "fw", "show", "a", "b"},
		{"-C", ws, "fw", "show", "Bad;svc"},
		{"-C", ws, "fw", "nope"},
		{"-C", ws, "fw"},
	} {
		if code, _, _ := runIn(t, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

// fw show agent-<n> prints the agent's allowlist (its profile's file) before
// the live state, which an on-demand agent rarely has.
func TestFwShowAgent(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	mustRun(t, "-C", ws, "render")

	f := fakeHost()
	code, out, errs := runWith(t, f, "-C", ws, "fw", "show", "agent-probe")
	if code != 0 {
		t.Fatalf("fw show agent-probe: exit %d, stderr %q", code, errs)
	}
	allow := filepath.Join(ws, ".railswitch", "firewall", "agent-probe.allow")
	if !strings.Contains(out, allow) || !strings.Contains(out, "\nprobe.example\n") {
		t.Errorf("stdout does not show %s:\n%s", allow, out)
	}
	if argv, _ := fwCall(t, f); !slices.Equal(argv, []string{filepath.Join(ws, ".railswitch", "firewall", "firewall.sh"), "show", "agent-probe"}) {
		t.Errorf("argv %v", argv)
	}

	// Not an agent: no allowlist printed.
	f = fakeHost()
	if code, out, _ := runWith(t, f, "-C", ws, "fw", "show", "nvim"); code != 0 || strings.Contains(out, ".allow") {
		t.Errorf("fw show nvim: exit %d, stdout %q", code, out)
	}
}

// The agent's CLAUDE.md tells the human to run fw allow agent-<n> <host>.
func TestFwAllowAgent(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	mustRun(t, "-C", ws, "render")
	f := fakeHost()
	if code, _, errs := runWith(t, f, "-C", ws, "-w", "master", "fw", "allow", "agent-claude", "example.com"); code != 0 {
		t.Fatalf("fw allow agent-claude: exit %d, stderr %q", code, errs)
	}
	argv, env := fwCall(t, f)
	if !slices.Equal(argv, []string{filepath.Join(ws, ".railswitch", "firewall", "firewall.sh"), "allow", "agent-claude", "example.com"}) || env["WORKTREE"] != "master" {
		t.Errorf("argv %v, WORKTREE %q", argv, env["WORKTREE"])
	}
}
