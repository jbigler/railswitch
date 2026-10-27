// Package gitguard is the host-side tamper check of the app repo's git state
// (D60). Containers mount the base worktree's .git writable (commits, hook
// editing), so the places where a write becomes code the host's git runs
// are watched: the installed hooks against their backups in the workspace
// repo's githooks/ (tracked, D38), the key names of .git/config against an
// allowlist (never their values: a remote URL can carry a credential), and
// each linked worktree's .git pointer. Nothing refuses: findings are
// warnings.
package gitguard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/tomlfile"
)

// Finding is one thing to review. Kind is hook-missing-backup, hook-differs,
// hook-not-installed, config-key or pointer; Path is the file concerned.
type Finding struct {
	Kind, Path, Detail string
}

func (f Finding) String() string { return f.Kind + " " + f.Path + ": " + f.Detail }

// ConfigAllowed are the .git/config keys git, gh and gh-stack write in
// normal use; any other is a finding. Widen it when a legitimate tool adds
// one.
var ConfigAllowed = regexp.MustCompile(`^(core\.(repositoryformatversion|filemode|bare|logallrefupdates|ignorecase|precomposeunicode|symlinks)|branch\..+\.(remote|merge|rebase|pushremote|description)|remote\..+\.(url|pushurl|fetch|tagopt|prune|gh-resolved)|remote\.pushdefault|rerere\.(enabled|autoupdate)|extensions\.worktreeconfig|user\.(name|email))$`)

// BackupDir is the workspace repo's hook backups, relative to its root.
const BackupDir = "githooks"

// pointerLimit bounds a worktree's .git pointer file.
const pointerLimit = 4096

// Check compares baseDir's installed hooks to ws's backups byte for byte
// (and the executable bit), checks the key names of baseDir's .git/config
// against ConfigAllowed, and checks the .git pointer of each linked
// worktree slugs (<ws>/<slug>/.git; a missing one is skipped). A base
// worktree without .git has nothing to guard.
func Check(ctx context.Context, r sys.Runner, ws, baseDir string, slugs []string) ([]Finding, error) {
	git := filepath.Join(baseDir, ".git")
	if fi, err := os.Lstat(git); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", git)
	}
	out, err := checkHooks(ws, baseDir)
	if err != nil {
		return nil, err
	}
	cfg, err := checkConfig(ctx, r, ws, baseDir)
	if err != nil {
		return nil, err
	}
	out = append(out, cfg...)
	for _, slug := range slugs {
		if f, ok := checkPointer(ws, baseDir, slug); !ok {
			out = append(out, f)
		}
	}
	return out, nil
}

func hooksDir(baseDir string) string { return filepath.Join(baseDir, ".git", "hooks") }

// hookFile is one hook's content and permission bits.
type hookFile struct {
	data []byte
	perm fs.FileMode
}

// readHook reads a hook through safefile: a container may have written it.
func readHook(path string) (hookFile, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return hookFile{}, err
	}
	data, err := safefile.Read(path, safefile.Limit)
	if err != nil {
		return hookFile{}, err
	}
	return hookFile{data: data, perm: fi.Mode().Perm()}, nil
}

// same is the same bytes and executable bit: a hook that lost +x is
// skipped by git, as good as missing.
func (h hookFile) same(o hookFile) bool {
	return (h.perm&0o111 != 0) == (o.perm&0o111 != 0) && bytes.Equal(h.data, o.data)
}

// hookNames lists dir's hook names: every entry but directories, dot files and
// *.sample (git's examples, never run). A missing dir has none.
func hookNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if n := e.Name(); !e.IsDir() && validName(n) {
			out = append(out, n)
		}
	}
	return out, nil
}

func validName(n string) bool {
	return n != "" && !strings.ContainsRune(n, '/') && !strings.HasPrefix(n, ".") && !strings.HasSuffix(n, ".sample")
}

func checkHooks(ws, baseDir string) ([]Finding, error) {
	hooks, backups := hooksDir(baseDir), filepath.Join(ws, BackupDir)
	installed, err := hookNames(hooks)
	if err != nil {
		return nil, err
	}
	saved, err := hookNames(backups)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, n := range installed {
		path, bpath := filepath.Join(hooks, n), filepath.Join(backups, n)
		if !slices.Contains(saved, n) {
			out = append(out, Finding{"hook-missing-backup", path, fmt.Sprintf("no backup; read it, and if it is yours: railswitch hooks backup %s, then commit %s", n, bpath)})
			continue
		}
		h, herr := readHook(path)
		b, berr := readHook(bpath)
		if herr != nil || berr != nil || !h.same(b) {
			detail := fmt.Sprintf("differs from its backup %s (content or executable bit); keep the installed one: railswitch hooks backup %s; keep the backup: railswitch hooks restore --force", bpath, n)
			if err := errors.Join(herr, berr); err != nil {
				detail = fmt.Sprintf("unreadable (%v); keep the backup: railswitch hooks restore --force", err)
			}
			out = append(out, Finding{"hook-differs", path, detail})
		}
	}
	for _, n := range saved {
		if !slices.Contains(installed, n) {
			out = append(out, Finding{"hook-not-installed", filepath.Join(backups, n), "backed up but not installed; reinstall: railswitch hooks restore; retire: git rm it"})
		}
	}
	return out, nil
}

// checkConfig lists the key names of baseDir's .git/config (never values)
// and reports each outside ConfigAllowed. Host git runs defused (no hooks,
// no fsmonitor) in ws, the workspace repo.
func checkConfig(ctx context.Context, r sys.Runner, ws, baseDir string) ([]Finding, error) {
	path := filepath.Join(baseDir, ".git", "config")
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	res, err := r.Run(ctx, sys.Cmd{
		Name: "git",
		Args: []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "config", "--file", path, "--name-only", "--list"},
		Dir:  ws,
	})
	if err != nil {
		if errors.Is(err, sys.ErrExit) {
			return []Finding{{"config-key", path, "does not parse (git config exit " + strconv.Itoa(res.ExitCode) + "): inspect it"}}, nil
		}
		return nil, err
	}
	var out []Finding
	seen := map[string]bool{}
	for _, name := range strings.Split(string(res.Stdout), "\n") {
		if name == "" || seen[name] || ConfigAllowed.MatchString(name) {
			continue
		}
		seen[name] = true
		out = append(out, Finding{"config-key", path, printable(name)})
	}
	return out, nil
}

// printable is s, quoted when it holds a control character (a subsection
// name a container chose reaches the terminal).
func printable(s string) string {
	if strings.IndexFunc(s, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		return strconv.Quote(s)
	}
	return s
}

// checkPointer checks <ws>/<slug>/.git reads exactly
// "gitdir: <base>/.git/worktrees/<name>": anywhere else, host git in that
// worktree reads a config and hooks the writer chose. ok is true when it
// does, or when the file is missing (no such worktree yet).
func checkPointer(ws, baseDir, slug string) (Finding, bool) {
	path := filepath.Join(ws, slug, ".git")
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return Finding{}, true
	}
	want := filepath.Join(baseDir, ".git", "worktrees")
	data, err := safefile.Read(path, pointerLimit)
	if err != nil {
		return Finding{"pointer", path, fmt.Sprintf("unreadable (%v); it must point into %s", err, want)}, false
	}
	line := strings.TrimSuffix(string(data), "\n")
	if name, ok := strings.CutPrefix(line, "gitdir: "+want+"/"); ok &&
		name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\n\r") {
		return Finding{}, true
	}
	return Finding{"pointer", path, fmt.Sprintf("does not point into %s: %q", want, line)}, false
}

// Backup copies baseDir's installed hooks names (every one when names is
// empty) to <ws>/githooks/, mode kept, and returns the names it wrote; a
// backup already the same is left alone.
func Backup(ws, baseDir string, names []string) ([]string, error) {
	hooks := hooksDir(baseDir)
	if len(names) == 0 {
		var err error
		if names, err = hookNames(hooks); err != nil {
			return nil, err
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("no hooks installed in %s", hooks)
		}
	}
	for _, n := range names {
		if !validName(n) {
			return nil, fmt.Errorf("not a hook name: %q", n)
		}
	}
	backups := filepath.Join(ws, BackupDir)
	if err := os.MkdirAll(backups, 0o755); err != nil {
		return nil, err
	}
	var wrote []string
	for _, n := range names {
		h, err := readHook(filepath.Join(hooks, n))
		if err != nil {
			return wrote, fmt.Errorf("hook %s: %w", n, err)
		}
		if b, err := readHook(filepath.Join(backups, n)); err == nil && h.same(b) {
			continue
		}
		if err := tomlfile.WriteAtomic(filepath.Join(backups, n), h.data, h.perm); err != nil {
			return wrote, err
		}
		wrote = append(wrote, n)
	}
	return wrote, nil
}

// Restore installs every backup in <ws>/githooks/ into baseDir's hooks. An
// installed hook that differs is kept unless force; the kept ones' names
// are returned.
func Restore(ws, baseDir string, force bool) ([]string, error) {
	backups, hooks := filepath.Join(ws, BackupDir), hooksDir(baseDir)
	saved, err := hookNames(backups)
	if err != nil {
		return nil, err
	}
	if len(saved) == 0 {
		return nil, fmt.Errorf("nothing to restore: %s has no hooks", backups)
	}
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		return nil, err
	}
	var kept []string
	for _, n := range saved {
		b, err := readHook(filepath.Join(backups, n))
		if err != nil {
			return kept, fmt.Errorf("backup %s: %w", n, err)
		}
		dst := filepath.Join(hooks, n)
		if _, err := os.Lstat(dst); err == nil {
			if h, err := readHook(dst); err == nil && h.same(b) {
				continue
			}
			if !force {
				kept = append(kept, n)
				continue
			}
		}
		if err := tomlfile.WriteAtomic(dst, b.data, b.perm); err != nil {
			return kept, err
		}
	}
	return kept, nil
}
