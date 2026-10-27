//go:build integration

package lifecycle_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// envRow is one row of `railswitch --json env` (docs/json.md).
type envRow struct {
	Name  string  `json:"name"`
	Value *string `json:"value"`
}

// TestE2EPhoenix is the Stage 5 acceptance run (docs/stage5-acceptance.md):
// a fresh `mix phx.new` app runs from the user-written pack local:phoenix@1
// (testdata/fixtures/phoenix/packs/phoenix, copied into the workspace),
// with no Phoenix knowledge in railswitch. Two worktrees each get their
// database; editing the pack makes up refuse until railswitch lock.
//
//	RAILSWITCH_E2E_PHX_REPO=$HOME/tmp/rs-phx/hello RAILSWITCH_E2E_PREFIX=s5p \
//	  go test -tags integration ./internal/lifecycle/ -run E2EPhoenix -timeout 60m -v
func TestE2EPhoenix(t *testing.T) {
	repo := os.Getenv("RAILSWITCH_E2E_PHX_REPO")
	if repo == "" {
		t.Skip("RAILSWITCH_E2E_PHX_REPO is not set")
	}
	prefix := os.Getenv("RAILSWITCH_E2E_PREFIX")
	if prefix == "" {
		prefix = "s5p"
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
	parent, err := os.MkdirTemp("", "rs-e2e5-")
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(parent, "rs-"+prefix)
	t.Logf("workspace %s", ws)

	rs := func(t *testing.T, timeout time.Duration, args ...string) string {
		t.Helper()
		out, errOut, err := railswitch(ctx, bin, timeout, args...)
		if err != nil {
			t.Fatalf("railswitch %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, out, errOut)
		}
		return out
	}

	// The pack is in the workspace before project new (D43).
	packDir := filepath.Join(ws, "packs", "phoenix")
	if err := os.CopyFS(packDir, os.DirFS(filepath.Join("..", "..", "testdata", "fixtures", "phoenix", "packs", "phoenix"))); err != nil {
		t.Fatal(err)
	}
	rs(t, 5*time.Minute, "project", "new", ws, "--repo", repo, "--prefix", prefix,
		"--pack", "local:phoenix@1", "--option", "database=postgres@1", "--agent", "none")
	t.Cleanup(func() {
		if out, errOut, err := railswitch(context.Background(), bin, projectRmTimeout, "project", "rm", prefix, "--yes", "--purge"); err != nil {
			t.Logf("cleanup: railswitch project rm %s: %v\n%s%s", prefix, err, out, errOut)
		}
	})
	wts := worktrees(t, rs(t, time.Minute, "-C", ws, "--json", "wt", "ls"))
	if len(wts) != 1 {
		t.Fatalf("after new: want 1 worktree, got %+v", wts)
	}
	base := wts[0].Slug

	up := func(slug string) {
		t.Helper()
		url := "http://" + slug + "." + prefix + ".localhost/"
		if out := rs(t, 30*time.Minute, "-C", ws, "-w", slug, "up"); lastLine(out) != url+" ready" {
			t.Fatalf("up %s did not report the route ready; output:\n%s", slug, out)
		}
		if code := httpStatus(ctx, t, url); code != "200" {
			t.Fatalf("GET %s: status %s, want 200", url, code)
		}
	}
	pgdatabase := func(slug string) string {
		t.Helper()
		var rows []envRow
		out := rs(t, time.Minute, "-C", ws, "-w", slug, "--json", "env", "web")
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("env --json: %v\n%s", err, out)
		}
		for _, r := range rows {
			if r.Name == "PGDATABASE" && r.Value != nil {
				return *r.Value
			}
		}
		t.Fatalf("%s: no PGDATABASE in env web:\n%s", slug, out)
		return ""
	}

	up(base)

	// A second worktree: its own ports, host and database.
	const feat = "feat-x"
	rs(t, 2*time.Minute, "-C", ws, "wt", "add", feat)
	up(feat)
	if a, b := pgdatabase(base), pgdatabase(feat); a == b {
		t.Errorf("both worktrees use database %q", a)
	}

	// An edit to the local pack is a lock change.
	readme := filepath.Join(packDir, "README.md")
	f, err := os.OpenFile(readme, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("\n<!-- edited -->\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	if out, errOut, err := railswitch(ctx, bin, 5*time.Minute, "-C", ws, "-w", base, "up"); err == nil || !strings.Contains(errOut, "stack.lock is out of date for local:phoenix@1") {
		t.Fatalf("up after editing the pack: err %v, want a stale-lock refusal\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	rs(t, time.Minute, "-C", ws, "lock")
	up(base)
}
