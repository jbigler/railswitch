package gitshadow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

// D230: Persist copies back the catalog a tool wrote in the shadow and the
// branch upstreams it set, replacing (never following) a planted symlink,
// keeping the rest of the real config and nothing else of the shadow's.
func TestPersist(t *testing.T) {
	base, _ := repo(t)
	gitDir := filepath.Join(base, ".git")
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(gitDir, "gh-stack")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sh, err := New(ctx, sys.Exec{}, gitDir)
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Remove()
	// What gh stack checkout does in the shadow: a new catalog in place of
	// the link, upstreams for the branches it made, and anything else.
	if err := os.Remove(filepath.Join(sh.Dir, "gh-stack")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sh.Dir, "gh-stack"), []byte(`{"stacks":[{"number":7,"branches":[{"branch":"jb/a"}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	shadowCfg := filepath.Join(sh.Dir, "config")
	for _, kv := range [][2]string{
		{"branch.jb/a.remote", "origin"}, {"branch.jb/a.merge", "refs/heads/jb/a"},
		{"branch.jb/b.remote", "-evil"}, {"branch.jb/b.merge", "refs/heads/x y"},
		{"core.sshCommand", "touch /tmp/pwned"},
	} {
		git(t, sh.tmp, os.Environ(), "config", "--file", shadowCfg, kv[0], kv[1])
	}
	if err := sh.Persist(ctx, sys.Exec{}, gitDir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Errorf("the symlink's target was written: %q", b)
	}
	fi, err := os.Lstat(filepath.Join(gitDir, "gh-stack"))
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o644 {
		t.Fatalf("gh-stack: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(filepath.Join(gitDir, "gh-stack")); !strings.Contains(string(b), `"number":7`) {
		t.Errorf("catalog %s", b)
	}
	cfg := filepath.Join(gitDir, "config")
	get := func(k string) string {
		out, _ := (sys.Exec{}).Run(ctx, sys.Cmd{Name: "git", Args: []string{"config", "--no-includes", "--file", cfg, "--get", k}})
		return strings.TrimSpace(string(out.Stdout))
	}
	if get("branch.jb/a.remote") != "origin" || get("branch.jb/a.merge") != "refs/heads/jb/a" {
		t.Errorf("upstream not copied back")
	}
	if get("branch.jb/b.remote") != "" || get("branch.jb/b.merge") != "" || get("core.sshcommand") != "" {
		t.Error("a bad value or another key reached the config")
	}
	if get("filter.evil.clean") == "" || get("branch.main.remote") != "origin" {
		t.Error("the config's other entries were lost")
	}
	if ents, _ := filepath.Glob(filepath.Join(gitDir, ".railswitch-*")); len(ents) != 0 {
		t.Errorf("temp files left: %v", ents)
	}
}

// Persist with nothing new changes nothing.
func TestPersistNothing(t *testing.T) {
	base, _ := repo(t)
	gitDir := filepath.Join(base, ".git")
	before, _ := os.ReadFile(filepath.Join(gitDir, "config"))
	ctx := context.Background()
	sh, err := New(ctx, sys.Exec{}, gitDir)
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Remove()
	if err := sh.Persist(ctx, sys.Exec{}, gitDir); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(filepath.Join(gitDir, "config")); string(after) != string(before) {
		t.Error("the config changed")
	}
	if _, err := os.Lstat(filepath.Join(gitDir, "gh-stack")); err == nil {
		t.Error("a catalog appeared")
	}
}
