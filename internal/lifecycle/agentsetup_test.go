package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

// TestAgentSetupAgain: --again removes the setup markers from the agent's
// home and nothing else; a symlinked marker dir is removed as a link, its
// target kept; an agent with no setup list, or no home yet, says so.
func TestAgentSetupAgain(t *testing.T) {
	e := setup(t)
	var u *UsageError
	if _, err := e.l.AgentSetupAgain(ctx, "feat", "claude", false); !errors.As(err, &u) || !strings.Contains(err.Error(), "no setup commands") {
		t.Fatalf("no setup list: %v", err)
	}
	e.editProject(t, func(s string) string {
		return strings.Replace(s, "[agents.claude]\n", "[agents.claude]\nsetup = [\"true\"]\n", 1)
	})
	e.render(t)
	home := filepath.Join(e.ws, ".railswitch", "agent-home", "feat", "claude")
	outside := t.TempDir()
	keep := filepath.Join(outside, "keep")
	for p, data := range map[string]string{
		".railswitch-setup-0123456789abcdef": "", ".railswitch-setup-old": "",
		".claude/settings.json": "{}", ".railswitch-setupx": "", keep: "precious",
	} {
		if !filepath.IsAbs(p) {
			p = filepath.Join(home, p)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The agent made its marker dir a link out of its home.
	if err := os.Symlink(outside, filepath.Join(home, ".railswitch-setup.d")); err != nil {
		t.Fatal(err)
	}
	e.f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", "filial-feat-agent-claude"}, sys.Result{Stdout: []byte("true\n")}, nil)
	r, err := e.l.AgentSetupAgain(ctx, "feat", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Agent != "claude" || !slices.Equal(r.Cleared, []string{"feat"}) || !slices.Equal(r.Running, []string{"feat"}) {
		t.Errorf("result %+v", r)
	}
	ents, _ := os.ReadDir(home)
	var left []string
	for _, d := range ents {
		left = append(left, d.Name())
	}
	if slices.ContainsFunc(left, func(n string) bool { return strings.HasPrefix(n, ".railswitch-setup-") || n == ".railswitch-setup.d" }) ||
		!slices.Contains(left, ".claude") || !slices.Contains(left, ".railswitch-setupx") {
		t.Errorf("home holds %v", left)
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "precious" {
		t.Errorf("link target: %q %v", data, err)
	}

	// A real marker dir goes whole; --all reaches master too, whose agent isn't running.
	if err := os.MkdirAll(filepath.Join(home, ".railswitch-setup.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".railswitch-setup.d", "aa"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err = e.l.AgentSetupAgain(ctx, "feat", "claude", true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Cleared, []string{"feat", "master"}) && !slices.Equal(r.Cleared, []string{"master", "feat"}) {
		t.Errorf("--all cleared %v", r.Cleared)
	}
	if _, err := os.Lstat(filepath.Join(home, ".railswitch-setup.d")); !os.IsNotExist(err) {
		t.Errorf("marker dir: %v", err)
	}

	// No home yet: nothing cleared.
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	if r, err := e.l.AgentSetupAgain(ctx, "feat", "claude", false); err != nil || len(r.Cleared) != 0 {
		t.Errorf("no home: %+v %v", r, err)
	}
}
