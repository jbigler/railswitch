package gitshadow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

func git(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(env, "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repo is a base worktree (main) and a linked one (feat), whose common
// config holds what a container could write: a filter, an include, an
// fsmonitor and a hooks path, next to a remote and a branch upstream.
func repo(t *testing.T) (base, feat string) {
	t.Helper()
	root := t.TempDir()
	base = filepath.Join(root, "main")
	git(t, root, os.Environ(), "init", "-q", "-b", "main", base)
	git(t, base, os.Environ(), "-c", "user.email=x@x", "-c", "user.name=x", "commit", "-q", "--allow-empty", "-m", "one")
	feat = filepath.Join(root, "feat")
	git(t, base, os.Environ(), "worktree", "add", "-q", "-b", "feat", feat)
	f, err := os.OpenFile(filepath.Join(base, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(`[remote "origin"]
	url = https://github.com/o/r.git
	fetch = +refs/heads/*:refs/remotes/origin/*
[branch "main"]
	remote = origin
	merge = refs/heads/main
[filter "evil"]
	clean = touch /tmp/pwned-filter
[include]
	path = /etc/passwd
[core]
	fsmonitor = touch /tmp/pwned-fsmonitor
	hooksPath = /tmp/evil-hooks
[alias]
	status = !touch /tmp/pwned-alias
[gh-stack]
	remote = origin
`); err != nil {
		t.Fatal(err)
	}
	return base, feat
}

// TestShadowConfig: the shadow's config keeps only the allowlist.
func TestShadowConfig(t *testing.T) {
	base, _ := repo(t)
	s, err := New(context.Background(), sys.Exec{}, filepath.Join(base, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Remove()
	data, err := os.ReadFile(filepath.Join(s.Dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(data)
	for _, want := range []string{"https://github.com/o/r.git", "+refs/heads/*:refs/remotes/origin/*", "merge = \"refs/heads/main\"", "repositoryformatversion", "[gh-stack]"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("shadow config lacks %q:\n%s", want, cfg)
		}
	}
	for _, bad := range []string{"filter", "include", "fsmonitor", "hooksPath", "alias", "pwned", "passwd"} {
		if strings.Contains(strings.ToLower(cfg), strings.ToLower(bad)) {
			t.Errorf("shadow config keeps %q:\n%s", bad, cfg)
		}
	}
	if fi, err := os.Stat(s.Dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("shadow dir mode %v, %v; want 0700", fi, err)
	}
	s.Remove()
	if _, err := os.Stat(filepath.Join(base, ".git", "refs")); err != nil {
		t.Errorf("Remove touched the real repository: %v", err)
	}
}

// TestShadowEnv: git run in either worktree through the shadow sees it as
// the common dir, resolves HEAD and refs, reads no repo config, and
// creates what a tool creates in the common dir (a lock) in the shadow,
// never in the real .git, even when the real one holds a symlink there.
func TestShadowEnv(t *testing.T) {
	base, feat := repo(t)
	real := filepath.Join(base, ".git")
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.Symlink(victim, filepath.Join(real, "gh-stack.lock")); err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), sys.Exec{}, real)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Remove()
	env := s.Env(os.Environ())
	for _, dir := range []string{base, feat} {
		common := git(t, dir, env, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if common != s.Dir {
			t.Errorf("%s: common dir %q; want the shadow %q", dir, common, s.Dir)
		}
		if got := git(t, dir, env, "symbolic-ref", "--short", "HEAD"); got != filepath.Base(dir) {
			t.Errorf("%s: HEAD %q", dir, got)
		}
		if got := git(t, dir, env, "config", "--get", "remote.origin.url"); got != "https://github.com/o/r.git" {
			t.Errorf("%s: remote %q", dir, got)
		}
	}
	if got := git(t, feat, env, "config", "--get", "core.fsmonitor"); got != "false" {
		t.Errorf("fsmonitor = %q; want the override", got)
	}
	// A tool's lock in the common dir lands in the shadow.
	lock := filepath.Join(git(t, feat, env, "rev-parse", "--path-format=absolute", "--git-common-dir"), "gh-stack.lock")
	if err := os.WriteFile(lock, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(victim); !os.IsNotExist(err) {
		t.Errorf("a lock reached the real .git's symlink target: %v", err)
	}
	if !slices.ContainsFunc(env, func(kv string) bool { return kv == "GIT_OPTIONAL_LOCKS=0" }) {
		t.Error("no GIT_OPTIONAL_LOCKS=0")
	}
}

// TestShadowRefuses: no real git directory (a file, a symlink) builds no
// shadow.
func TestShadowRefuses(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "git")
	if err := os.WriteFile(file, []byte("gitdir: /x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{file, link, filepath.Join(dir, "none")} {
		if s, err := New(context.Background(), sys.Exec{}, p); err == nil {
			s.Remove()
			t.Errorf("%s: a shadow was built", p)
		}
	}
}

// A worktree's own gh-stack file (an old catalog, or a planted one) is not
// linked into the mirror: gh-stack refuses a symlinked one.
func TestShadowSkipsWorktreeGhStack(t *testing.T) {
	base, _ := repo(t)
	admin := filepath.Join(base, ".git", "worktrees", "feat")
	if err := os.WriteFile(filepath.Join(admin, "gh-stack"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), sys.Exec{}, filepath.Join(base, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Remove()
	if _, err := os.Lstat(filepath.Join(s.Dir, "worktrees", "feat", "gh-stack")); !os.IsNotExist(err) {
		t.Errorf("the worktree's gh-stack was mirrored: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(s.Dir, "worktrees", "feat", "HEAD")); err != nil {
		t.Errorf("the worktree's HEAD wasn't: %v", err)
	}
}

// TestWritable: through a writable shadow, commits, a switch, reflogs and
// packed refs reach the real repository; Commit carries back gh-stack's
// catalog and the upstream a tool set, never another key.
func TestWritable(t *testing.T) {
	base, feat := repo(t)
	real := filepath.Join(base, ".git")
	if err := os.WriteFile(filepath.Join(real, "gh-stack"), []byte(`{"stacks":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := NewWritable(ctx, sys.Exec{}, real)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Remove()
	env := s.Env(os.Environ())
	git(t, feat, env, "-c", "user.email=x@x", "-c", "user.name=x", "commit", "-q", "--allow-empty", "-m", "two")
	git(t, base, env, "switch", "-q", "-c", "next")
	git(t, base, env, "config", "branch.next.remote", "origin")
	git(t, base, env, "config", "alias.st", "!touch /tmp/pwned")
	git(t, base, env, "pack-refs", "--all")
	if err := os.WriteFile(filepath.Join(s.Dir, "gh-stack"), []byte(`{"stacks":[1]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(ctx, sys.Exec{}); err != nil {
		t.Fatal(err)
	}
	// The real config's planted include breaks plain git: read through a
	// fresh shadow, whose links are the real repository.
	after, err := New(ctx, sys.Exec{}, real)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Remove()
	plain := after.Env(os.Environ())
	if got := git(t, feat, plain, "log", "-1", "--format=%s", "feat"); got != "two" {
		t.Errorf("feat's commit = %q", got)
	}
	if got := git(t, base, plain, "symbolic-ref", "--short", "HEAD"); got != "next" {
		t.Errorf("base HEAD = %q", got)
	}
	if got := git(t, base, plain, "reflog", "-1", "--format=%gs", "feat"); !strings.Contains(got, "two") {
		t.Errorf("feat's reflog = %q", got)
	}
	if data, _ := os.ReadFile(filepath.Join(real, "packed-refs")); !strings.Contains(string(data), "refs/heads/feat") {
		t.Errorf("packed-refs not carried back:\n%s", data)
	}
	if data, _ := os.ReadFile(filepath.Join(real, "gh-stack")); string(data) != `{"stacks":[1]}` {
		t.Errorf("gh-stack = %s", data)
	}
	cfg, _ := os.ReadFile(filepath.Join(real, "config"))
	if got := git(t, base, plain, "config", "branch.next.remote"); got != "origin" {
		t.Errorf("upstream not carried back:\n%s", cfg)
	}
	if strings.Contains(string(cfg), "/tmp/pwned\n") || !strings.Contains(string(cfg), "pwned-filter") {
		t.Errorf("config: an alias carried back, or the rest not kept:\n%s", cfg)
	}
}

// TestWritableConflict: a catalog changed in the real repository meanwhile
// is kept, and Commit says so.
func TestWritableConflict(t *testing.T) {
	base, _ := repo(t)
	real := filepath.Join(base, ".git", "gh-stack")
	if err := os.WriteFile(real, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := NewWritable(ctx, sys.Exec{}, filepath.Join(base, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Remove()
	if err := os.WriteFile(filepath.Join(s.Dir, "gh-stack"), []byte("ours"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("theirs"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(ctx, sys.Exec{}); err == nil || !strings.Contains(err.Error(), "changed meanwhile") {
		t.Errorf("Commit = %v", err)
	}
	if data, _ := os.ReadFile(real); string(data) != "theirs" {
		t.Errorf("gh-stack = %s", data)
	}
}
