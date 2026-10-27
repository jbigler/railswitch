//go:build integration

package lifecycle_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/naming"
)

// agentRow is one row of `railswitch --json agent ls` (docs/json.md).
type agentRow struct {
	Name    string `json:"name"`
	Package string `json:"package"`
	Run     string `json:"run"`
}

var versionRe = regexp.MustCompile(`\d+\.\d+`)

// TestE2EAgents is the Stage 4 agent acceptance run
// (docs/stage4-acceptance.md) against real podman and systemd: a project with
// the default on-demand agent and a local service agent, "probe"; the probe
// runs as a unit with its secret, behind its own enforced firewall profile;
// the on-demand agent runs through `railswitch agent` without the probe's
// secret; `agent ls` lists both.
//
//	RAILSWITCH_E2E_REPO=/tmp/rs-demo/demo RAILSWITCH_E2E_PREFIX=s4a \
//	  go test -tags integration ./internal/lifecycle/ -run E2EAgents -timeout 60m -v
func TestE2EAgents(t *testing.T) {
	repo := os.Getenv("RAILSWITCH_E2E_REPO")
	if repo == "" {
		t.Skip("RAILSWITCH_E2E_REPO is not set")
	}
	prefix := os.Getenv("RAILSWITCH_E2E_PREFIX")
	if prefix == "" {
		prefix = "s4a"
	}
	if !e2ePrefixRe.MatchString(prefix) {
		t.Fatalf("prefix %q must match %s", prefix, e2ePrefixRe)
	}
	ctx := context.Background()
	if d, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, d.Add(-projectRmTimeout-time.Minute))
		t.Cleanup(cancel)
	}
	bin := buildBinary(t)
	parent, err := os.MkdirTemp("", "rs-e2e4-")
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(parent, "rs-"+prefix)
	quadletDir := filepath.Join(os.Getenv("HOME"), ".config", "containers", "systemd")
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		quadletDir = filepath.Join(x, "containers", "systemd")
	}

	rs := func(t *testing.T, timeout time.Duration, args ...string) string {
		t.Helper()
		out, errOut, err := railswitch(ctx, bin, timeout, args...)
		if err != nil {
			t.Fatalf("railswitch %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, out, errOut)
		}
		return out
	}
	rsIn := func(t *testing.T, stdin string, args ...string) {
		t.Helper()
		if out, errOut, err := runStdin(ctx, time.Minute, stdin, bin, args...); err != nil {
			t.Fatalf("railswitch %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, out, errOut)
		}
	}

	rs(t, 5*time.Minute, "project", "new", ws, "--repo", repo, "--prefix", prefix)
	t.Cleanup(func() {
		if out, errOut, err := railswitch(context.Background(), bin, projectRmTimeout, "project", "rm", prefix, "--yes", "--purge"); err != nil {
			t.Logf("cleanup: railswitch project rm %s: %v\n%s%s", prefix, err, out, errOut)
		}
	})
	wts := worktrees(t, rs(t, time.Minute, "-C", ws, "--json", "wt", "ls"))
	if len(wts) != 1 {
		t.Fatalf("after new: want 1 worktree, got %+v", wts)
	}
	slug := wts[0].Slug

	// The local probe agent: <ws>/agents/probe, run as a service.
	if err := os.CopyFS(filepath.Join(ws, "agents", "probe"), os.DirFS(filepath.Join("testdata", "agents", "probe"))); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(ws, "project.toml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("\n[agents.probe]\nuse = \"local:probe@1\"\nrun = \"service\"\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}

	w := []string{"-C", ws, "-w", slug}
	rs(t, 5*time.Minute, append(w, "lock")...)
	rs(t, time.Minute, append(w, "containerfile", "eject", "probe")...)
	rsIn(t, "probe-secret-value\n", append(w, "secret", "add", "probe_key", "--service", "agent-probe")...)
	// claude --version needs no valid token.
	rsIn(t, "dummy-token\n", append(w, "secret", "set", "claude_token")...)
	rs(t, 20*time.Minute, append(w, "up")...)

	probe := naming.Container(prefix, slug, "agent-probe")
	claude := naming.Container(prefix, slug, "agent-claude")

	// The probe runs as a unit; the on-demand agent has none.
	if out, _, _ := run(ctx, 30*time.Second, "systemctl", "--user", "is-active", probe+".service"); strings.TrimSpace(out) != "active" {
		t.Errorf("systemctl --user is-active %s.service: %q, want active", probe, strings.TrimSpace(out))
	}
	if _, err := os.Stat(filepath.Join(quadletDir, claude+".container")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s.container: stat err %v, want not-exist (the agent is on-demand)", claude, err)
	}

	// The probe has its secret.
	if _, errOut, err := run(ctx, 30*time.Second, "podman", "exec", probe, "test", "-f", "/run/secrets/probe_key"); err != nil {
		t.Errorf("podman exec %s test -f /run/secrets/probe_key: %v\n%s", probe, err, errOut)
	}

	// The on-demand agent runs; a first argument without "-" takes the
	// entrypoint's exec "$@" branch, and the probe's secret isn't there.
	if out := rs(t, 10*time.Minute, append(w, "agent", "claude", "--", "--version")...); !versionRe.MatchString(out) {
		t.Errorf("railswitch agent claude -- --version printed %q, want a version", out)
	}
	rs(t, 10*time.Minute, append(w, "agent", "claude", "--", "sh", "-c", "test ! -e /run/secrets/probe_key")...)

	// The probe's own firewall profile, enforced.
	ann := func(key string) string {
		return strings.TrimSpace(podman(ctx, t, "inspect", "--format", `{{index .Config.Annotations "`+key+`"}}`, probe))
	}
	if got := ann(prefix + ".firewall"); got != "agent-probe" {
		t.Errorf("%s annotation %s.firewall=%q, want agent-probe", probe, prefix, got)
	}
	if got := ann(prefix + ".firewall.mode"); got != "enforce" {
		t.Errorf("%s annotation %s.firewall.mode=%q, want enforce", probe, prefix, got)
	}
	curl := func(url string) int {
		_, _, err := run(ctx, 30*time.Second, "podman", "exec", probe, "curl", "-sS", "-o", "/dev/null", "-m", "5", url)
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		if err != nil {
			t.Fatalf("podman exec %s curl %s: %v", probe, url, err)
		}
		return 0
	}
	if code := curl("https://api.anthropic.com"); code != 7 && code != 28 {
		t.Errorf("probe curl https://api.anthropic.com: exit %d, want 7 or 28 (blocked)", code)
	}
	if code := curl("https://example.com"); code != 0 {
		t.Errorf("probe curl https://example.com: exit %d, want 0 (allowed)", code)
	}

	// agent ls lists both, with their run modes.
	var rows []agentRow
	if out := rs(t, time.Minute, append([]string{"--json"}, append(w, "agent", "ls")...)...); json.Unmarshal([]byte(out), &rows) != nil {
		t.Fatalf("agent ls --json does not parse:\n%s", out)
	}
	runs := map[string]string{}
	for _, r := range rows {
		runs[r.Name] = r.Run
	}
	if runs["claude"] != "on-demand" || runs["probe"] != "service" {
		t.Errorf("agent ls: runs %v, want claude on-demand and probe service", runs)
	}
}

// runStdin runs name with stdin as its standard input.
func runStdin(ctx context.Context, timeout time.Duration, stdin, name string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return out.String(), errOut.String(), err
}
