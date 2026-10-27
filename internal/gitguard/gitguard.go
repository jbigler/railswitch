// Package gitguard is the host-side tamper check of the app repo's git state
// (D60). Containers mount the base worktree's .git writable (commits, hook
// editing), so the places where a write becomes code the host's git runs
// are watched: the installed hooks against their backups in the workspace
// repo's githooks/ (tracked, D38), the key names of the config files
// (.git/config, config.worktree, worktrees/*/config.worktree and the
// submodule gitdirs' under modules/**) against an allowlist (never their
// values: a remote URL can carry a credential), the commondir redirections
// (none in .git or modules/**, "../.." in each worktrees/*), each
// worktrees/*/gitdir, each linked worktree's .git pointer, any hook in
// modules/**, and that worktrees/, modules/ and the dirs under them are
// real directories, never symlinks. Nothing refuses: findings are warnings.
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
// hook-not-installed, config-key, pointer, commondir, gitdir,
// submodule-hook or not-a-dir; Path is the file concerned, printable.
type Finding struct {
	Kind, Path, Detail string
}

func (f Finding) String() string { return f.Kind + " " + f.Path + ": " + f.Detail }

// ConfigAllowed are the .git/config keys git, gh and gh-stack write in
// normal use; any other is a finding. Widen it when a legitimate tool adds
// one. extensions.worktreeconfig is not in it: Check reads every
// config.worktree too, but enabling them is still worth a look.
var ConfigAllowed = regexp.MustCompile(`^(core\.(repositoryformatversion|filemode|bare|logallrefupdates|ignorecase|precomposeunicode|symlinks)|submodule\..+\.(url|active|branch)|branch\..+\.(remote|merge|rebase|pushremote|description)|remote\..+\.(url|pushurl|fetch|tagopt|prune|gh-resolved)|remote\.pushdefault|rerere\.(enabled|autoupdate)|user\.(name|email))$`)

// moduleAllowed are the keys a submodule's git dir config may hold besides
// ConfigAllowed: git writes core.worktree there, never in the base repo.
var moduleAllowed = regexp.MustCompile(`^core\.worktree$`)

// BackupDir is the workspace repo's hook backups, relative to its root.
const BackupDir = "githooks"

// pointerLimit bounds a worktree's .git pointer file.
const pointerLimit = 4096

// Check compares baseDir's installed hooks to ws's backups byte for byte
// (and the executable bit) and checks every place a write in baseDir's .git
// redirects host git to a config or hooks the writer chose:
//   - the key names of .git/config, .git/config.worktree and
//     .git/worktrees/*/config.worktree against ConfigAllowed;
//   - .git/commondir must not exist;
//   - each .git/worktrees/<name>/ must have commondir "../.." and gitdir
//     <ws>/<slug>/.git for one of slugs;
//   - each linked worktree's pointer <ws>/<slug>/.git must name such a
//     <name> (a missing pointer is skipped);
//   - submodule gitdirs under .git/modules/**: config key names (also
//     core.worktree allowed), no commondir, and every installed hook (kind
//     submodule-hook);
//   - .git/worktrees, .git/modules and every dir walked under them is a
//     real directory, never followed through a symlink (kind not-a-dir).
//
// A config file that cannot be read is its own finding. Paths are made
// printable (a container chooses names under worktrees/ and modules/). A
// base worktree without .git has nothing to guard.
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
	out = append(out, noCommondir(git)...)
	wt, good := checkWorktrees(ws, git, slugs)
	out = append(out, wt...)
	mods, modConfigs := checkModules(filepath.Join(git, "modules"))
	out = append(out, mods...)
	for _, path := range configFiles(git) {
		out = append(out, checkConfig(ctx, r, ws, path, false)...)
	}
	for _, path := range modConfigs {
		out = append(out, checkConfig(ctx, r, ws, path, true)...)
	}
	for _, slug := range slugs {
		if f, ok := checkPointer(ws, baseDir, slug, good); !ok {
			out = append(out, f)
		}
	}
	for i := range out {
		out[i].Path, out[i].Detail = printable(out[i].Path), printable(out[i].Detail)
	}
	return out, nil
}

// noCommondir reports gitDir/commondir when it exists: host git would read
// that dir's config and hooks instead.
func noCommondir(gitDir string) []Finding {
	path := filepath.Join(gitDir, "commondir")
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return []Finding{{"commondir", path, "exists: host git reads the config and hooks of the dir it names; remove it"}}
}

// checkWorktrees checks every entry of git/worktrees/ (not only those of
// slugs: a pointer may name any): commondir must read "../.." and gitdir
// <ws>/<slug>/.git for one of slugs. good are the entries that pass.
func checkWorktrees(ws, git string, slugs []string) (out []Finding, good map[string]bool) {
	good = map[string]bool{}
	dir := filepath.Join(git, "worktrees")
	entries, out := realDir(dir)
	gitdirs := map[string]bool{}
	wsReal, _ := filepath.EvalSymlinks(ws)
	for _, slug := range slugs {
		gitdirs[filepath.Join(ws, slug, ".git")] = true
		if wsReal != "" {
			gitdirs[filepath.Join(wsReal, slug, ".git")] = true
		}
	}
	for _, e := range entries {
		name, path := e.Name(), filepath.Join(dir, e.Name())
		if !e.IsDir() {
			out = append(out, notDir(path, e.Type()))
			continue
		}
		ok := true
		cd := filepath.Join(path, "commondir")
		if line, err := readLine(cd); err != nil {
			ok = false
			out = append(out, Finding{"commondir", cd, fmt.Sprintf("unreadable (%v); it must read ../..", err)})
		} else if line != "../.." {
			ok = false
			out = append(out, Finding{"commondir", cd, fmt.Sprintf("reads %q; it must read ../..", line)})
		}
		gd := filepath.Join(path, "gitdir")
		if line, err := readLine(gd); err != nil {
			ok = false
			out = append(out, Finding{"gitdir", gd, fmt.Sprintf("unreadable (%v); it must name a worktree's .git under %s", err, ws)})
		} else if !gitdirs[line] {
			ok = false
			out = append(out, Finding{"gitdir", gd, fmt.Sprintf("reads %q, not a worktree's .git under %s", line, ws)})
		}
		if ok {
			good[name] = true
		}
	}
	return out, good
}

// readLine is a small file a container may have written, one trailing
// newline trimmed.
func readLine(path string) (string, error) {
	data, err := safefile.Read(path, pointerLimit)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(data), "\n"), nil
}

// checkModules walks the submodule gitdirs under dir (.git/modules; names
// may hold slashes and submodules nest under <gitdir>/modules). A dir with
// a HEAD or config is a gitdir: it must have no commondir, and every hook
// installed in it is a finding. configs are its config files to check.
func checkModules(dir string) (out []Finding, configs []string) {
	entries, out := realDir(dir)
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if !e.IsDir() {
			out = append(out, notDir(path, e.Type()))
			continue
		}
		if !isGitDir(path) {
			o, c := checkModules(path)
			out, configs = append(out, o...), append(configs, c...)
			continue
		}
		out = append(out, noCommondir(path)...)
		for _, f := range []string{"config", "config.worktree"} {
			if _, err := os.Lstat(filepath.Join(path, f)); err == nil {
				configs = append(configs, filepath.Join(path, f))
			}
		}
		hooks := filepath.Join(path, "hooks")
		names, f := realDir(hooks)
		out = append(out, f...)
		for _, n := range names {
			if validName(n.Name()) && !n.IsDir() {
				out = append(out, Finding{"submodule-hook", filepath.Join(hooks, n.Name()), "a hook in a submodule's git dir runs on the host; read it, and remove it unless it is yours"})
			}
		}
		o, c := checkModules(filepath.Join(path, "modules"))
		out, configs = append(out, o...), append(configs, c...)
	}
	return out, configs
}

// realDir lists path's entries when it is a real directory (Lstat: a
// symlink is never followed); a missing path has none. Anything else is a
// not-a-dir finding: host git would follow it to a dir the writer chose.
func realDir(path string) ([]fs.DirEntry, []Finding) {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []Finding{{"not-a-dir", path, fmt.Sprintf("unreadable (%v)", err)}}
	}
	if !fi.IsDir() {
		return nil, []Finding{notDir(path, fi.Mode())}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, []Finding{{"not-a-dir", path, fmt.Sprintf("unreadable (%v)", err)}}
	}
	return entries, nil
}

// notDir is the finding for path, which must be a real directory.
func notDir(path string, mode fs.FileMode) Finding {
	what := "not a directory"
	if mode&fs.ModeSymlink != 0 {
		what = "a symlink, not a directory"
	}
	return Finding{"not-a-dir", path, what + ": host git follows it; inspect and remove it"}
}

func isGitDir(path string) bool {
	for _, f := range []string{"HEAD", "config"} {
		if _, err := os.Lstat(filepath.Join(path, f)); err == nil {
			return true
		}
	}
	return false
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

// configFiles are the config files host git reads from git (a base .git):
// config, config.worktree and each linked worktree's
// worktrees/<name>/config.worktree, those that exist.
func configFiles(git string) []string {
	paths := []string{filepath.Join(git, "config"), filepath.Join(git, "config.worktree")}
	entries, _ := realDir(filepath.Join(git, "worktrees")) // findings: checkWorktrees
	for _, e := range entries {
		if e.IsDir() {
			paths = append(paths, filepath.Join(git, "worktrees", e.Name(), "config.worktree"))
		}
	}
	var out []string
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// checkConfig lists the key names of the config file path (never values)
// and reports each outside ConfigAllowed (and moduleAllowed for a
// submodule's config: module). Host git runs defused (no hooks,
// no fsmonitor) in ws, the workspace repo.
func checkConfig(ctx context.Context, r sys.Runner, ws, path string, module bool) []Finding {
	res, err := r.Run(ctx, sys.Cmd{
		Name: "git",
		Args: []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "config", "--file", path, "--name-only", "--list"},
		Dir:  ws,
	})
	if err != nil {
		if errors.Is(err, sys.ErrExit) {
			return []Finding{{"config-key", path, "does not parse (git config exit " + strconv.Itoa(res.ExitCode) + "): inspect it"}}
		}
		return []Finding{{"config-key", path, fmt.Sprintf("could not read (%v): inspect it", err)}}
	}
	var out []Finding
	seen := map[string]bool{}
	for _, name := range strings.Split(string(res.Stdout), "\n") {
		if name == "" || seen[name] || ConfigAllowed.MatchString(name) || module && moduleAllowed.MatchString(name) {
			continue
		}
		seen[name] = true
		out = append(out, Finding{"config-key", path, printable(name)})
	}
	return out
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
// worktree reads a config and hooks the writer chose. <name> must be one of
// good (checkWorktrees). ok is true when it does, or when the file is
// missing (no such worktree yet).
func checkPointer(ws, baseDir, slug string, good map[string]bool) (Finding, bool) {
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
		if good[name] {
			return Finding{}, true
		}
		return Finding{"pointer", path, fmt.Sprintf("names %s, which is missing or fails its commondir/gitdir check", printable(filepath.Join(want, name)))}, false
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
