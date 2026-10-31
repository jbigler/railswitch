//go:build integration

package lifecycle_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/naming"
)

// memoryDoc is the part of `railswitch --json memory` (docs/json.md) the
// run reads.
type memoryDoc struct {
	Server struct {
		State string `json:"state"`
	} `json:"server"`
	Schema int `json:"schema"`
	Issues *struct {
		Total      int `json:"total"`
		InProgress int `json:"in_progress"`
	} `json:"issues"`
	Memories  int `json:"memories"`
	Worktrees []struct {
		Slug    string `json:"slug"`
		Ready   []struct{ ID string } `json:"ready"`
		Claimed []struct {
			ID       string `json:"id"`
			Assignee string `json:"assignee"`
		} `json:"claimed"`
	} `json:"worktrees"`
	Activity []struct {
		Seq      int64  `json:"seq"`
		Op       string `json:"op"`
		Worktree string `json:"worktree"`
		Agent    string `json:"agent"`
	} `json:"activity"`
	Cursor int64  `json:"cursor"`
	Error  string `json:"error"`
}

// TestE2EMemory is shared agent memory (D136–D143) against real podman and
// systemd, with the shell test agent (testdata/agents/shell) in two
// worktrees: a project without [memory] has no server, network, bd or
// beads env; with it, what agent A records agent B's prime shows; two
// agents racing for one claim, one wins; the agents' firewall gains only
// the memory network's subnet and the app can't reach the server; the
// report reads counts, claims and activity with a cursor; removing a
// worktree keeps the memory; backup, a removed volume and restore bring it
// back.
//
//	RAILSWITCH_E2E_REPO=$HOME/rs-demo/src/demo RAILSWITCH_E2E_PREFIX=s9mem \
//	  go test -tags integration ./internal/lifecycle/ -run E2EMemory -timeout 60m -v
func TestE2EMemory(t *testing.T) {
	repo := os.Getenv("RAILSWITCH_E2E_REPO")
	if repo == "" {
		t.Skip("RAILSWITCH_E2E_REPO is not set")
	}
	prefix := os.Getenv("RAILSWITCH_E2E_PREFIX")
	if prefix == "" {
		prefix = "s9mem"
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
	parent, err := os.MkdirTemp("", "rs-e2em-")
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
	// agent runs `sh -c script` in the shell agent of worktree slug.
	agent := func(t *testing.T, slug, script string) string {
		t.Helper()
		return rs(t, 20*time.Minute, "-C", ws, "-w", slug, "agent", "shell", "--", "sh", "-c", script)
	}
	report := func(t *testing.T, args ...string) memoryDoc {
		t.Helper()
		out := rs(t, 5*time.Minute, append([]string{"-C", ws, "--json", "memory"}, args...)...)
		var doc memoryDoc
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("memory --json: %v\n%s", err, out)
		}
		if doc.Error != "" {
			t.Fatalf("memory --json error: %s", doc.Error)
		}
		return doc
	}

	rs(t, 5*time.Minute, "project", "new", ws, "--repo", repo, "--prefix", prefix, "--agent", "none")
	t.Cleanup(func() {
		if out, errOut, err := railswitch(context.Background(), bin, projectRmTimeout, "project", "rm", prefix, "--yes", "--purge"); err != nil {
			t.Logf("cleanup: railswitch project rm %s: %v\n%s%s", prefix, err, out, errOut)
		}
	})
	a := worktrees(t, rs(t, time.Minute, "-C", ws, "--json", "wt", "ls"))[0].Slug
	if err := os.CopyFS(filepath.Join(ws, "agents", "shell"), os.DirFS(filepath.Join("testdata", "agents", "shell"))); err != nil {
		t.Fatal(err)
	}
	rs(t, 5*time.Minute, "-C", ws, "agent", "add", "shell", "--use", "local:shell@1", "--run", "on-demand")
	rs(t, 30*time.Minute, "-C", ws, "-w", a, "up")

	// Without [memory]: nothing of it.
	agent(t, a, `test -z "${BEADS_DIR:-}" && ! command -v bd && ! test -e /usr/local/bin/railswitch-prime`)
	for _, f := range []string{naming.MemoryUnit(prefix) + ".container", naming.MemoryUnit(prefix) + ".network"} {
		if _, err := os.Stat(filepath.Join(quadletDir, f)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("without [memory]: %s exists (%v)", f, err)
		}
	}
	if _, _, err := run(ctx, 30*time.Second, "podman", "network", "exists", naming.MemoryNetwork(prefix)); err == nil {
		t.Errorf("without [memory]: network %s exists", naming.MemoryNetwork(prefix))
	}
	before := agentLocal(t, ws)

	// With [memory].
	appendFile(t, filepath.Join(ws, "project.toml"), "\n[memory]\nguidance = \"No patient data, ever.\"\n")
	rs(t, time.Minute, "-C", ws, "render")
	b := "memory-b"
	rs(t, 30*time.Minute, "-C", ws, "wt", "add", "--up", b)

	// A records; B's prime (saved by its entrypoint at start) shows it.
	id := strings.TrimSpace(lastLine(agent(t, a, `bd remember "the demo uses podman, never docker" --key demo-podman >/dev/null &&
		bd create "Add a health endpoint" -l worktree:`+a+` --json | jq -r .id`)))
	if id == "" {
		t.Fatal("bd create printed no id")
	}
	prime := agent(t, b, `cat ~/.railswitch-prime.md`)
	for _, want := range []string{"No patient data, ever.", "worktree:" + b, "demo-podman", "the demo uses podman, never docker"} {
		if !strings.Contains(prime, want) {
			t.Errorf("B's prime lacks %q:\n%s", want, prime)
		}
	}
	if list := agent(t, b, `bd list --json | jq -r '.[] | .id + " " + .created_by'`); !strings.Contains(list, id+" "+a+"/shell") {
		t.Errorf("B's bd list lacks %s by %s/shell:\n%s", id, a, list)
	}

	// The firewall: A's agent gains exactly the memory network's subnet.
	after := agentLocal(t, ws)
	sub := strings.TrimSpace(podman(ctx, t, "network", "inspect", naming.MemoryNetwork(prefix), "--format", "{{range .Subnets}}{{.Subnet}} {{end}}"))
	if internal := strings.TrimSpace(podman(ctx, t, "network", "inspect", naming.MemoryNetwork(prefix), "--format", "{{.Internal}}")); internal != "true" {
		t.Errorf("%s internal = %s", naming.MemoryNetwork(prefix), internal)
	}
	if added := minus(after, before); !slices.Equal(added, []string{sub}) {
		t.Errorf("the agent's local subnets went from %v to %v; want only %s added", before, after, sub)
	}
	if out, _, _ := run(ctx, 30*time.Second, "podman", "exec", naming.Container(prefix, a, "rails"), "getent", "hosts", "memory"); strings.TrimSpace(out) != "" {
		t.Errorf("the app resolves the memory server: %s", out)
	}
	if ports := podman(ctx, t, "port", naming.MemoryNetwork(prefix)); strings.TrimSpace(ports) != "" {
		t.Errorf("the memory server publishes %s", ports)
	}

	// Two agents race for one claim at the same second: one wins.
	race := strings.TrimSpace(lastLine(agent(t, a, `bd create "Race me" -l worktree:`+a+` --json | jq -r .id`)))
	at := strconv.FormatInt(time.Now().Add(90*time.Second).Unix(), 10)
	claim := `while [ "$(date +%s)" -lt ` + at + ` ]; do sleep 0.05; done; bd update ` + race + ` --claim --json`
	var wg sync.WaitGroup
	codes := map[string]int{}
	var mu sync.Mutex
	for _, slug := range []string{a, b} {
		wg.Go(func() {
			_, errOut, err := railswitch(ctx, bin, 20*time.Minute, "-C", ws, "-w", slug, "agent", "shell", "--", "sh", "-c", claim)
			code := 0
			if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
				code = ee.ExitCode()
			} else if err != nil {
				t.Errorf("%s claim: %v", slug, err)
			}
			if code != 0 && !strings.Contains(errOut, "already claimed") {
				t.Errorf("%s lost the claim without saying so: exit %d\n%s", slug, code, errOut)
			}
			mu.Lock()
			codes[slug] = code
			mu.Unlock()
		})
	}
	wg.Wait()
	if (codes[a] == 0) == (codes[b] == 0) {
		t.Errorf("claim exit codes %v; want exactly one 0", codes)
	}

	// The report: counts, the claim, activity with a cursor.
	doc := report(t)
	if doc.Server.State != "active" || doc.Schema == 0 || doc.Issues == nil || doc.Issues.Total != 2 || doc.Issues.InProgress != 1 || doc.Memories != 1 {
		t.Errorf("memory report: %+v", doc)
	}
	var claimed int
	for _, w := range doc.Worktrees {
		claimed += len(w.Claimed)
	}
	if claimed != 1 || len(doc.Activity) < 3 || doc.Cursor != doc.Activity[len(doc.Activity)-1].Seq || doc.Activity[0].Worktree != a || doc.Activity[0].Agent != "shell" {
		t.Errorf("memory report: claimed %d, activity %+v, cursor %d", claimed, doc.Activity, doc.Cursor)
	}
	if next := report(t, "--since", strconv.FormatInt(doc.Cursor, 10)); len(next.Activity) != 0 || next.Cursor != doc.Cursor {
		t.Errorf("memory --since %d: activity %+v cursor %d; want none, the same cursor", doc.Cursor, next.Activity, next.Cursor)
	}

	// Removing a worktree keeps the memory.
	rs(t, 10*time.Minute, "-C", ws, "wt", "rm", "--force", b)
	if doc := report(t); doc.Issues == nil || doc.Issues.Total != 2 || doc.Memories != 1 || doc.Server.State != "active" {
		t.Errorf("after wt rm %s: %+v", b, doc)
	}

	// Backup, remove the volume, restore.
	backup := filepath.Join(parent, "memory.jsonl")
	rs(t, 5*time.Minute, "-C", ws, "memory", "backup", "--to", backup)
	if fi, err := os.Stat(backup); err != nil || fi.Mode().Perm() != 0o600 || fi.Size() == 0 {
		t.Fatalf("backup %s: %v %v", backup, fi, err)
	}
	if _, errOut, err := run(ctx, 2*time.Minute, "systemctl", "--user", "stop", naming.MemoryUnit(prefix)+".service"); err != nil {
		t.Fatalf("stop the memory server: %v\n%s", err, errOut)
	}
	podman(ctx, t, "volume", "rm", naming.MemoryNetwork(prefix))
	if doc := report(t); doc.Server.State == "active" || doc.Issues != nil {
		t.Errorf("after the volume went: %+v", doc)
	}
	rs(t, 5*time.Minute, "-C", ws, "memory", "restore", "--yes", backup)
	if doc := report(t); doc.Issues == nil || doc.Issues.Total != 2 || doc.Issues.InProgress != 1 || doc.Memories != 1 {
		t.Errorf("after restore: %+v", doc)
	}
}

// agentLocal is the local subnets the firewall hook gave the newest shell
// agent container (hook.log: "profiles=agent-shell … local=<cidr> …").
func agentLocal(t *testing.T, ws string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ws, ".railswitch", "firewall", "hook.log"))
	if err != nil {
		t.Fatal(err)
	}
	var last string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.Contains(l, "profiles=agent-shell ") {
			last = l
		}
	}
	_, local, ok := strings.Cut(last, "local=")
	if !ok {
		t.Fatalf("hook.log has no agent-shell line:\n%s", data)
	}
	f := strings.Fields(local)
	slices.Sort(f)
	return f
}

// minus is what after holds that before doesn't.
func minus(after, before []string) []string {
	var out []string
	for _, s := range after {
		if !slices.Contains(before, s) {
			out = append(out, s)
		}
	}
	return out
}

func appendFile(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
}
