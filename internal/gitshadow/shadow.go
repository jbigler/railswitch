// Package gitshadow builds a host-private stand-in for a repository's
// common git directory, for tools railswitch runs in a worktree that a
// container can write (gh stack). Git is pointed at it with GIT_COMMON_DIR:
// its config is an allowlisted copy of the real one (no filter, include,
// fsmonitor, hooksPath or alias a container wrote ever reaches git), the
// refs and objects are symlinks to the real ones, and whatever a tool
// creates there (gh-stack's lock files) lands in the private directory,
// not in the container-writable .git. A symlinked gh-stack file can't be
// rewritten through: gh-stack's atomic write refuses to replace a symlink.
//
// A writable shadow (NewWritable) is for a tool that changes the
// repository (gh stack push, a branch switch, a rebase): refs, objects and
// reflogs land in the real repository through the links, a worktree's
// HEAD and index are in its own git dir already, and Commit carries back
// what lands in the shadow instead: gh-stack's catalog, a rewritten
// packed-refs and the few config keys a tool may set (branch upstreams,
// gh-stack's and rerere's choices).
package gitshadow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
)

// links are the common dir's entries the shadow points at; one the repo
// lacks reads as absent. info/, hooks/ and config are never linked.
var links = []string{"HEAD", "objects", "refs", "packed-refs", "shallow", "gh-stack", "reftable"}

// skipAdmin says which worktree admin files are never linked: config.worktree
// (read only under extensions.worktreeConfig, which the copy never
// carries), and gh-stack's per-worktree files (an old catalog, or one a
// container planted: gh-stack refuses a symlinked one, failing every read).
func skipAdmin(name string) bool {
	return name == "config.worktree" || strings.HasPrefix(name, "gh-stack")
}

// maxAdmin bounds the worktree mirror: a container can fill
// .git/worktrees; above this many entries the shadow fails closed.
const maxAdmin = 10000

// allowed are the config keys the copy keeps, as `git config --list` names
// them (section and name lowercase, the subsection verbatim): the repo
// format, remotes and branch upstreams, and gh-stack's and rerere's
// choices.
var allowed = regexp.MustCompile(`^(?:core\.(?:repositoryformatversion|bare|filemode|ignorecase|symlinks|precomposeunicode)|extensions\.(?:objectformat|refstorage)|remote\..+\.(?:url|fetch|gh-resolved)|remote\.pushdefault|branch\..+\.(?:remote|merge|pushremote)|gh-stack\.(?:remote|rerere-declined)|rerere\.(?:enabled|autoupdate))$`)

// writeBack are the keys Commit carries back to the real config: what
// gh-stack and git set while pushing and rebasing.
var writeBack = regexp.MustCompile(`^(?:branch\..+\.(?:remote|merge)|gh-stack\.(?:remote|rerere-declined)|rerere\.(?:enabled|autoupdate))$`)

// maxEntries bounds the copy: a container can write any number of remote
// lines; above this the shadow fails closed.
const maxEntries = 2000

// overrides go to every call too, as command-line config (they win over
// any file), with GIT_OPTIONAL_LOCKS=0: a read never writes the index. No
// automatic gc: its pack-refs would prune the real loose refs while the
// packed-refs it writes stays in the shadow.
var overrides = [][2]string{
	{"core.fsmonitor", "false"}, {"core.hooksPath", "/dev/null"},
	{"diff.ignoreSubmodules", "all"}, {"submodule.recurse", "false"},
	{"gc.auto", "0"}, {"maintenance.auto", "false"},
}

// Shadow is one repository's private common dir.
type Shadow struct {
	Dir string // the shadow common dir; remove with Remove
	tmp string

	// A writable shadow's: the real common dir, gh-stack's catalog as read
	// (nil: there was none), packed-refs as found, the config as copied.
	gitDir      string
	write       bool
	ghStack     []byte
	packedRefs  stamp
	configBytes []byte
	config      []entry
}

// entry is one config line the copy keeps: key as git lists it.
type entry struct{ key, value string }

// stamp tells whether a file changed: absent, or its inode, size and
// modification time.
type stamp struct {
	exists bool
	ino    uint64
	size   int64
	mtime  time.Time
}

func stampOf(path string) (stamp, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return stamp{}, nil
	}
	if err != nil {
		return stamp{}, err
	}
	st := stamp{exists: true, size: fi.Size(), mtime: fi.ModTime()}
	if sys, ok := fi.Sys().(*syscall.Stat_t); ok {
		st.ino = sys.Ino
	}
	return st, nil
}

// New builds a shadow of gitDir (a repository's common dir: the base
// worktree's .git) in a new private directory. Anything it can't make
// safely is an error: the caller then runs nothing.
func New(ctx context.Context, r sys.Runner, gitDir string) (*Shadow, error) {
	return newShadow(ctx, r, gitDir, false)
}

// NewWritable is New for a tool that changes the repository: gh-stack's
// catalog is a private copy (gh-stack replaces it, which it refuses to do
// to a link), the reflogs are linked, and Commit carries back what the
// tool changed in the shadow. Call Commit, then Remove.
func NewWritable(ctx context.Context, r sys.Runner, gitDir string) (*Shadow, error) {
	return newShadow(ctx, r, gitDir, true)
}

func newShadow(ctx context.Context, r sys.Runner, gitDir string, write bool) (*Shadow, error) {
	fi, err := os.Lstat(gitDir)
	if err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("git safety: %s is not a git directory", gitDir)
	}
	tmp, err := sys.RuntimeTemp("railswitch-gitshadow-", time.Hour)
	if err != nil {
		return nil, err
	}
	s := &Shadow{Dir: filepath.Join(tmp, "common"), tmp: tmp, gitDir: gitDir, write: write}
	if err := s.build(ctx, r, gitDir); err != nil {
		s.Remove()
		return nil, fmt.Errorf("git safety: %w", err)
	}
	return s, nil
}

// Remove deletes the shadow (never the real repository: only links point
// out of it).
func (s *Shadow) Remove() { os.RemoveAll(s.tmp) }

// Env is environ for a git (or gh) call that must see the repository only
// through the shadow: Defused, with GIT_COMMON_DIR the shadow.
func (s *Shadow) Env(environ []string) []string {
	return append(Defused(environ), "GIT_COMMON_DIR="+s.Dir)
}

// Defused is environ for a git (or gh) call on the real repository: the
// overrides as GIT_CONFIG_* (the caller's own GIT_CONFIG_* and git
// directory variables dropped), GIT_OPTIONAL_LOCKS=0, no prompt. It keeps
// hooks and fsmonitor from running, not the rest of the repository's
// config: for a tool that refuses the shadow (gh stack rebase).
func Defused(environ []string) []string {
	env := make([]string, 0, len(environ)+16)
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "GIT_CONFIG_") && k != "GIT_CONFIG_GLOBAL" && k != "GIT_CONFIG_NOSYSTEM" && k != "GIT_CONFIG_SYSTEM" ||
			k == "GIT_DIR" || k == "GIT_WORK_TREE" || k == "GIT_COMMON_DIR" || k == "GIT_OPTIONAL_LOCKS" || k == "GIT_TERMINAL_PROMPT" {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(overrides)))
	for i, o := range overrides {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, o[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, o[1]))
	}
	return env
}

func (s *Shadow) build(ctx context.Context, r sys.Runner, gitDir string) error {
	if err := os.Mkdir(s.Dir, 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(s.tmp, "scratch"), 0o700); err != nil {
		return err
	}
	for _, name := range links {
		if s.write && name == "gh-stack" {
			continue
		}
		if err := os.Symlink(filepath.Join(gitDir, name), filepath.Join(s.Dir, name)); err != nil {
			return err
		}
	}
	if s.write {
		if err := s.prepareWrite(gitDir); err != nil {
			return err
		}
	}
	data, err := safefile.Read(filepath.Join(gitDir, "config"), safefile.Limit)
	if err != nil {
		return fmt.Errorf("read %s/config: %w", gitDir, err)
	}
	entries, err := s.listConfig(ctx, r, data)
	if err != nil {
		return fmt.Errorf("%s/config: %w", gitDir, err)
	}
	text, err := formatConfig(entries)
	if err != nil {
		return fmt.Errorf("%s/config: %w", gitDir, err)
	}
	s.configBytes, s.config = data, entries
	if err := writeNew(filepath.Join(s.Dir, "config"), text); err != nil {
		return err
	}
	return mirrorWorktrees(filepath.Join(gitDir, "worktrees"), filepath.Join(s.Dir, "worktrees"))
}

// prepareWrite copies gh-stack's catalog in as a file of the shadow's own,
// links the reflogs (a real directory only) and notes packed-refs.
func (s *Shadow) prepareWrite(gitDir string) error {
	data, err := safefile.Read(filepath.Join(gitDir, "gh-stack"), safefile.Limit)
	switch {
	case err == nil:
		if err := writeNew(filepath.Join(s.Dir, "gh-stack"), data); err != nil {
			return err
		}
		s.ghStack = data
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("read %s/gh-stack: %w", gitDir, err)
	}
	if fi, err := os.Lstat(filepath.Join(gitDir, "logs")); err == nil && fi.IsDir() {
		if err := os.Symlink(filepath.Join(gitDir, "logs"), filepath.Join(s.Dir, "logs")); err != nil {
			return err
		}
	}
	s.packedRefs, err = stampOf(filepath.Join(gitDir, "packed-refs"))
	return err
}

// listConfig is config text reduced to the allowlist, listed by git from a
// private copy of the bytes (no includes, no system or global config, no
// repository around it).
func (s *Shadow) listConfig(ctx context.Context, r sys.Runner, data []byte) ([]entry, error) {
	copyPath := filepath.Join(s.tmp, "scratch", "list")
	defer os.Remove(copyPath)
	if err := writeNew(copyPath, data); err != nil {
		return nil, err
	}
	res, err := r.Run(ctx, sys.Cmd{
		Name: "git", Args: []string{"config", "--no-includes", "--file", copyPath, "--list", "-z"},
		Dir: filepath.Dir(copyPath), Env: s.configEnv(),
	})
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	var entries []entry
	for line := range strings.SplitSeq(string(res.Stdout), "\x00") {
		if line == "" {
			continue
		}
		key, value, hasValue := strings.Cut(line, "\n")
		if !allowed.MatchString(key) {
			continue
		}
		if !hasValue { // a bare key is boolean true; only core's booleans may be one
			if !strings.HasPrefix(key, "core.") {
				continue
			}
			value = "true"
		}
		if len(entries) == maxEntries {
			return nil, fmt.Errorf("more than %d entries kept", maxEntries)
		}
		entries = append(entries, entry{key, value})
	}
	return entries, nil
}

// configEnv runs git on a private config file only.
func (s *Shadow) configEnv() []string {
	return append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CEILING_DIRECTORIES="+s.tmp)
}

// formatConfig writes entries in git's syntax.
func formatConfig(entries []entry) ([]byte, error) {
	var b strings.Builder
	header := ""
	for _, e := range entries {
		section, rest, _ := strings.Cut(e.key, ".")
		i := strings.LastIndex(rest, ".")
		sub, name := "", rest
		if i >= 0 {
			sub, name = rest[:i], rest[i+1:]
		}
		if name == "" || strings.ContainsAny(sub, "\n\x00") {
			return nil, fmt.Errorf("bad key %q", e.key)
		}
		h := "[" + section + "]\n"
		if sub != "" {
			h = "[" + section + " \"" + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(sub) + "\"]\n"
		}
		if h != header {
			b.WriteString(h)
			header = h
		}
		b.WriteString("\t" + name + " = \"" + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`, "\b", `\b`).Replace(e.value) + "\"\n")
	}
	return []byte(b.String()), nil
}

// Commit carries a writable shadow's changes back to the real repository:
// gh-stack's catalog, packed-refs when git rewrote it, and the writeBack
// keys of the config. Each is refused when the real file changed since
// the shadow was made (someone else wrote it meanwhile); the rest still
// go back. Every write is a new file renamed over the real one, so a
// symlink planted there is replaced, never followed.
func (s *Shadow) Commit(ctx context.Context, r sys.Runner) error {
	if !s.write {
		return errors.New("git safety: commit on a read-only shadow")
	}
	return errors.Join(s.commitGhStack(), s.commitPackedRefs(), s.commitConfig(ctx, r))
}

func (s *Shadow) commitGhStack() error {
	shadow, real := filepath.Join(s.Dir, "gh-stack"), filepath.Join(s.gitDir, "gh-stack")
	data, err := os.ReadFile(shadow)
	gone := errors.Is(err, os.ErrNotExist)
	if err != nil && !gone {
		return err
	}
	if gone && s.ghStack == nil || !gone && s.ghStack != nil && bytes.Equal(data, s.ghStack) {
		return nil
	}
	now, err := safefile.Read(real, safefile.Limit)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("git safety: %s: %w", real, err)
	}
	if (err == nil) != (s.ghStack != nil) || !bytes.Equal(now, s.ghStack) {
		return fmt.Errorf("git safety: %s changed meanwhile; gh stack's change is not kept", real)
	}
	if gone {
		return os.Remove(real)
	}
	return publish(real, data, 0o644)
}

// commitPackedRefs copies packed-refs back when git replaced the link with
// a file of its own (pack-refs, deleting a packed ref).
func (s *Shadow) commitPackedRefs() error {
	shadow, real := filepath.Join(s.Dir, "packed-refs"), filepath.Join(s.gitDir, "packed-refs")
	fi, err := os.Lstat(shadow)
	if err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	if now, err := stampOf(real); err != nil || now != s.packedRefs {
		return fmt.Errorf("git safety: %s changed meanwhile; the refs packed in this run are not kept", real)
	}
	data, err := os.ReadFile(shadow)
	if err != nil {
		return err
	}
	return publish(real, data, 0o644)
}

// commitConfig sets the writeBack keys whose values changed in the shadow
// on a private copy of the real config, then puts the copy in its place.
func (s *Shadow) commitConfig(ctx context.Context, r sys.Runner) error {
	data, err := os.ReadFile(filepath.Join(s.Dir, "config"))
	if err != nil {
		return err
	}
	after, err := s.listConfig(ctx, r, data)
	if err != nil {
		return fmt.Errorf("git safety: shadow config: %w", err)
	}
	changed := changedKeys(s.config, after)
	if len(changed) == 0 {
		return nil
	}
	real := filepath.Join(s.gitDir, "config")
	copyPath := filepath.Join(s.tmp, "scratch", "config")
	if err := writeNew(copyPath, s.configBytes); err != nil {
		return err
	}
	for _, key := range changed {
		argv := [][]string{{"config", "--file", copyPath, "--unset-all", key}}
		for _, e := range after {
			if e.key == key {
				argv = append(argv, []string{"config", "--file", copyPath, "--add", key, e.value})
			}
		}
		for i, args := range argv {
			res, err := r.Run(ctx, sys.Cmd{Name: "git", Args: args, Dir: filepath.Dir(copyPath), Env: s.configEnv()})
			// --unset-all exits 5 for a key the file lacks.
			if err != nil && !(i == 0 && errors.Is(err, sys.ErrExit) && res.ExitCode == 5) {
				return fmt.Errorf("git safety: set %s: %w", key, err)
			}
		}
	}
	now, err := safefile.Read(real, safefile.Limit)
	if err != nil {
		return fmt.Errorf("git safety: %s: %w", real, err)
	}
	if !bytes.Equal(now, s.configBytes) {
		return fmt.Errorf("git safety: %s changed meanwhile; not setting %s", real, strings.Join(changed, ", "))
	}
	text, err := os.ReadFile(copyPath)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Lstat(real); err == nil {
		mode = fi.Mode().Perm()
	}
	return publish(real, text, mode)
}

// changedKeys are the writeBack keys whose values differ between before
// and after, in after's order then before's.
func changedKeys(before, after []entry) []string {
	values := func(es []entry, key string) []string {
		var v []string
		for _, e := range es {
			if e.key == key {
				v = append(v, e.value)
			}
		}
		return v
	}
	var keys []string
	for _, e := range slices.Concat(after, before) {
		if writeBack.MatchString(e.key) && !slices.Contains(keys, e.key) && !slices.Equal(values(before, e.key), values(after, e.key)) {
			keys = append(keys, e.key)
		}
	}
	return keys
}

// publish puts data at path: a new file beside it, renamed over it.
func publish(path string, data []byte, mode os.FileMode) error {
	tmp := filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s.railswitch-%d", filepath.Base(path), time.Now().UnixNano()))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscallNoFollow, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, mode)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// mirrorWorktrees makes dst a real directory holding a real directory per
// real one in src (gh-stack lstats both levels and refuses a symlink),
// whose entries link to that worktree's admin files.
func mirrorWorktrees(src, dst string) error {
	if err := os.Mkdir(dst, 0o700); err != nil {
		return err
	}
	ents, err := os.ReadDir(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	n := 0
	for _, e := range ents {
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		admin, mirror := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		files, err := os.ReadDir(admin)
		if err != nil {
			continue
		}
		if err := os.Mkdir(mirror, 0o700); err != nil {
			return err
		}
		for _, f := range files {
			if skipAdmin(f.Name()) {
				continue
			}
			if n++; n > maxAdmin {
				return fmt.Errorf("%s: more than %d worktree admin entries", src, maxAdmin)
			}
			if err := os.Symlink(filepath.Join(admin, f.Name()), filepath.Join(mirror, f.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeNew writes data to a file that must not exist (0600, no symlink
// followed).
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscallNoFollow, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
