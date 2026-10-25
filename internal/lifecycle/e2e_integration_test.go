//go:build integration

package lifecycle_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/naming"
)

// cleanupReserve is the part of the test deadline kept back for cleanup: the
// steps run under a context that expires this long before `go test` would
// kill the binary, and cleanup runs on a fresh context within it.
const cleanupReserve = cleanupCommands*cleanupTimeout + time.Minute

// cleanupTimeout bounds one cleanup command; cleanupCommands is the most
// that run (the second worktree's down and wt rm, the base's down).
const (
	cleanupTimeout  = 2 * time.Minute
	cleanupCommands = 3
)

// TestE2ECleanupReserve: the reserve holds every cleanup command at its
// worst case, plus a minute for the test's own work.
func TestE2ECleanupReserve(t *testing.T) {
	if want := cleanupCommands*cleanupTimeout + time.Minute; cleanupReserve < want {
		t.Errorf("cleanupReserve = %v; want at least %v", cleanupReserve, want)
	}
}

// TestE2E is the Stage 2 acceptance run (docs/stage2-acceptance.md, steps
// 3-8) against real podman and systemd: a fresh Rails app is set up, started,
// queried, given a second worktree, and that worktree is torn down again.
//
// RAILSWITCH_E2E_REPO names the app's git repo (e.g. /tmp/rs-demo/demo); the
// test is skipped when it is unset. RAILSWITCH_E2E_PREFIX overrides the
// project prefix (default "demo"); it must not be registered yet.
//
//	RAILSWITCH_E2E_REPO=/tmp/rs-demo/demo go test -tags integration ./internal/lifecycle/ -run E2E -timeout 45m
func TestE2E(t *testing.T) {
	repo := os.Getenv("RAILSWITCH_E2E_REPO")
	if repo == "" {
		t.Skip("RAILSWITCH_E2E_REPO is not set")
	}
	prefix := os.Getenv("RAILSWITCH_E2E_PREFIX")
	if prefix == "" {
		prefix = "demo"
	}
	ctx := context.Background()
	if d, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, d.Add(-cleanupReserve))
		t.Cleanup(cancel)
	}
	bin := buildBinary(t)

	// The workspace is not a t.TempDir: files the containers write there are
	// owned by subordinate uids, which the test cannot remove.
	parent, err := os.MkdirTemp("", "rs-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(parent, "rs-demo")
	t.Logf("workspace %s", ws)

	rs := func(t *testing.T, timeout time.Duration, args ...string) string {
		t.Helper()
		out, errOut, err := railswitch(ctx, bin, timeout, args...)
		if err != nil {
			t.Fatalf("railswitch %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, out, errOut)
		}
		return out
	}
	// cleanup runs a teardown command on its own context: the steps' context
	// may have expired.
	cleanup := func(t *testing.T, args ...string) {
		if out, errOut, err := railswitch(context.Background(), bin, cleanupTimeout, args...); err != nil {
			t.Logf("cleanup: railswitch %s: %v\n%s%s", strings.Join(args, " "), err, out, errOut)
		}
	}

	// Step 3: new + up of the base worktree.
	rs(t, 5*time.Minute, "new", ws, "--repo", repo, "--prefix", prefix)
	wts := worktrees(t, rs(t, time.Minute, "-C", ws, "--json", "wt", "ls"))
	if len(wts) != 1 {
		t.Fatalf("after new: want 1 worktree, got %+v", wts)
	}
	base := wts[0]
	t.Cleanup(func() { cleanup(t, "-C", ws, "-w", base.Slug, "down", "--yes") })
	rs(t, 10*time.Minute, "-C", ws, "-w", base.Slug, "up")

	// Step 4: the app answers through Traefik.
	if code := httpStatus(ctx, t, "http://"+base.Slug+"."+prefix+".localhost/"); code != "200" {
		t.Fatalf("step 4: GET %s.%s.localhost: status %s, want 200", base.Slug, prefix, code)
	}

	// Step 5: the app reaches its database.
	out := rs(t, 2*time.Minute, "-C", ws, "-w", base.Slug, "exec", "--",
		"bin/rails", "runner", `puts ActiveRecord::Base.connection.select_value("select 1")`)
	if strings.TrimSpace(lastLine(out)) != "1" {
		t.Fatalf("step 5: rails runner printed %q, want 1", out)
	}

	// Step 6: rails runs with profile none in log mode; nothing is unfirewalled.
	out = rs(t, 2*time.Minute, "-C", ws, "-w", base.Slug, "fw", "show")
	railsC := naming.Container(prefix, base.Slug, "rails")
	if !strings.Contains(out, railsC+"  profiles=none mode=log") {
		t.Errorf("step 6: fw show does not list %s with profiles=none mode=log:\n%s", railsC, out)
	}
	if strings.Contains(out, "NO FIREWALL") {
		t.Errorf("step 6: fw show reports an unfirewalled container:\n%s", out)
	}

	// Step 7: a second worktree comes up on its own ports and host name.
	const slug = "feat-x"
	rs(t, 2*time.Minute, "-C", ws, "wt", "add", slug)
	featDone := false
	t.Cleanup(func() {
		if featDone {
			return
		}
		cleanup(t, "-C", ws, "-w", slug, "down", "--yes")
		cleanup(t, "-C", ws, "wt", "rm", "--force", slug)
	})
	var feat worktree
	for _, w := range worktrees(t, rs(t, time.Minute, "-C", ws, "--json", "wt", "ls")) {
		if w.Slug == slug {
			feat = w
		}
	}
	if feat.ID == 0 {
		t.Fatalf("step 7: %s missing from wt ls", slug)
	}
	t.Logf("%s has ID %d (2 on a machine with no other worktree: ports 55434/33002)", slug, feat.ID)
	rs(t, 5*time.Minute, "-C", ws, "-w", slug, "up")
	if code := httpStatus(ctx, t, "http://"+slug+"."+prefix+".localhost/"); code != "200" {
		t.Fatalf("step 7: GET %s.%s.localhost: status %s, want 200", slug, prefix, code)
	}
	wantPort(ctx, t, naming.Container(prefix, slug, "db"), 5432, 55432+feat.ID)
	debug := 33000 + feat.ID
	wantPort(ctx, t, naming.Container(prefix, slug, "rails"), debug, debug)

	// Step 8: down + wt rm leave nothing of feat-x; the base keeps running.
	gone := prefix + "-" + slug + "-"
	hasGone := func(n string) bool { return strings.HasPrefix(n, gone) }
	if !slices.ContainsFunc(lines(podman(ctx, t, "volume", "ls", "--format", "{{.Name}}")), hasGone) {
		t.Fatalf("step 8: no %s* volume exists before down; the teardown check would prove nothing", gone)
	}
	rs(t, 3*time.Minute, "-C", ws, "-w", slug, "down", "--yes")
	rs(t, 2*time.Minute, "-C", ws, "wt", "rm", slug)
	featDone = true
	for _, names := range [][]string{
		lines(podman(ctx, t, "ps", "-a", "--format", "{{.Names}}")),
		lines(podman(ctx, t, "volume", "ls", "--format", "{{.Name}}")),
	} {
		for _, n := range names {
			if hasGone(n) {
				t.Errorf("step 8: %s still exists", n)
			}
		}
	}
	running := lines(podman(ctx, t, "ps", "--format", "{{.Names}}"))
	for _, svc := range []string{"rails", "db", "cache"} {
		if c := naming.Container(prefix, base.Slug, svc); !slices.Contains(running, c) {
			t.Errorf("step 8: %s is not running; running: %v", c, running)
		}
	}
}

type worktree struct {
	Slug string `json:"slug"`
	ID   int    `json:"id"`
}

func worktrees(t *testing.T, out string) []worktree {
	t.Helper()
	var ws []worktree
	if err := json.Unmarshal([]byte(out), &ws); err != nil {
		t.Fatalf("wt ls --json: %v\n%s", err, out)
	}
	return ws
}

// buildBinary builds cmd/railswitch into a temp dir.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "railswitch")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/jbigler/railswitch/cmd/railswitch")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// railswitch runs bin with args, bounded by timeout and ctx, and returns its
// stdout and stderr separately.
func railswitch(ctx context.Context, bin string, timeout time.Duration, args ...string) (string, string, error) {
	return run(ctx, timeout, bin, args...)
}

func run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return out.String(), errOut.String(), err
}

func podman(ctx context.Context, t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, err := run(ctx, 30*time.Second, "podman", args...)
	if err != nil {
		t.Fatalf("podman %s: %v\n%s", strings.Join(args, " "), err, errOut)
	}
	return out
}

// wantPort checks that container publishes ctr on 127.0.0.1:host.
func wantPort(ctx context.Context, t *testing.T, container string, ctr, host int) {
	t.Helper()
	out := podman(ctx, t, "port", container, strconv.Itoa(ctr)+"/tcp")
	want := "127.0.0.1:" + strconv.Itoa(host)
	if !slices.Contains(lines(out), want) {
		t.Errorf("podman port %s %d: got %q, want %s", container, ctr, out, want)
	}
}

// httpStatus GETs url with curl, as the procedure does: curl resolves any
// *.localhost name to the loopback address itself.
func httpStatus(ctx context.Context, t *testing.T, url string) string {
	t.Helper()
	out, errOut, err := run(ctx, time.Minute, "curl", "-sS", "--max-time", "30", "-o", "/dev/null", "-w", "%{http_code}", url)
	if err != nil {
		t.Fatalf("curl %s: %v (status %q)\n%s", url, err, out, errOut)
	}
	return out
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func lastLine(s string) string {
	ls := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return ls[len(ls)-1]
}
