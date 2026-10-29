// Package gitshadow builds a host-private stand-in for a repository's
// common git directory, for tools railswitch runs in a worktree that a
// container can write (gh stack). Git is pointed at it with GIT_COMMON_DIR:
// its config is an allowlisted copy of the real one (no filter, include,
// fsmonitor, hooksPath or alias a container wrote ever reaches git), the
// refs and objects are symlinks to the real ones, and whatever a tool
// creates there (gh-stack's lock files) lands in the private directory,
// not in the container-writable .git. A symlinked gh-stack file can't be
// rewritten through: gh-stack's atomic write refuses to replace a symlink.
package gitshadow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
// format, remotes and branch upstreams, and gh-stack's remote choice.
var allowed = regexp.MustCompile(`^(?:core\.(?:repositoryformatversion|bare|filemode|ignorecase|symlinks|precomposeunicode)|extensions\.(?:objectformat|refstorage)|remote\..+\.(?:url|fetch|gh-resolved)|remote\.pushdefault|branch\..+\.(?:remote|merge|pushremote)|gh-stack\.remote)$`)

// maxEntries bounds the copy: a container can write any number of remote
// lines; above this the shadow fails closed.
const maxEntries = 2000

// overrides go to every call too, as command-line config (they win over
// any file), with GIT_OPTIONAL_LOCKS=0: a read never writes the index.
var overrides = [][2]string{
	{"core.fsmonitor", "false"}, {"core.hooksPath", "/dev/null"},
	{"diff.ignoreSubmodules", "all"}, {"submodule.recurse", "false"},
}

// Shadow is one repository's private common dir.
type Shadow struct {
	Dir string // the shadow common dir; remove with Remove
	tmp string
}

// New builds a shadow of gitDir (a repository's common dir: the base
// worktree's .git) in a new private directory. Anything it can't make
// safely is an error: the caller then runs nothing.
func New(ctx context.Context, r sys.Runner, gitDir string) (*Shadow, error) {
	fi, err := os.Lstat(gitDir)
	if err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("git safety: %s is not a git directory", gitDir)
	}
	tmp, err := sys.RuntimeTemp("railswitch-gitshadow-", time.Hour)
	if err != nil {
		return nil, err
	}
	s := &Shadow{Dir: filepath.Join(tmp, "common"), tmp: tmp}
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
// through the shadow: GIT_COMMON_DIR, the overrides as GIT_CONFIG_* (the
// caller's own GIT_CONFIG_* dropped), GIT_OPTIONAL_LOCKS=0, no prompt.
func (s *Shadow) Env(environ []string) []string {
	env := make([]string, 0, len(environ)+12)
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "GIT_CONFIG_") && k != "GIT_CONFIG_GLOBAL" && k != "GIT_CONFIG_NOSYSTEM" && k != "GIT_CONFIG_SYSTEM" ||
			k == "GIT_DIR" || k == "GIT_WORK_TREE" || k == "GIT_COMMON_DIR" || k == "GIT_OPTIONAL_LOCKS" || k == "GIT_TERMINAL_PROMPT" {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_COMMON_DIR="+s.Dir, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0",
		fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(overrides)))
	for i, o := range overrides {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, o[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, o[1]))
	}
	return env
}

func (s *Shadow) build(ctx context.Context, r sys.Runner, gitDir string) error {
	if err := os.Mkdir(s.Dir, 0o700); err != nil {
		return err
	}
	for _, name := range links {
		if err := os.Symlink(filepath.Join(gitDir, name), filepath.Join(s.Dir, name)); err != nil {
			return err
		}
	}
	text, err := s.sanitizedConfig(ctx, r, gitDir)
	if err != nil {
		return err
	}
	if err := writeNew(filepath.Join(s.Dir, "config"), text); err != nil {
		return err
	}
	return mirrorWorktrees(filepath.Join(gitDir, "worktrees"), filepath.Join(s.Dir, "worktrees"))
}

// sanitizedConfig is gitDir's config reduced to the allowlist, listed by
// git from a private copy of the bytes (no includes, no system or global
// config, no repository around it), then written back in git's syntax.
func (s *Shadow) sanitizedConfig(ctx context.Context, r sys.Runner, gitDir string) ([]byte, error) {
	data, err := safefile.Read(filepath.Join(gitDir, "config"), safefile.Limit)
	if err != nil {
		return nil, fmt.Errorf("read %s/config: %w", gitDir, err)
	}
	scratch := filepath.Join(s.tmp, "scratch")
	if err := os.Mkdir(scratch, 0o700); err != nil {
		return nil, err
	}
	copyPath := filepath.Join(scratch, "config")
	if err := writeNew(copyPath, data); err != nil {
		return nil, err
	}
	res, err := r.Run(ctx, sys.Cmd{
		Name: "git", Args: []string{"config", "--no-includes", "--file", copyPath, "--list", "-z"},
		Dir: scratch,
		Env: append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CEILING_DIRECTORIES="+s.tmp),
	})
	if err != nil {
		return nil, fmt.Errorf("list %s/config: %w", gitDir, err)
	}
	var b strings.Builder
	header, n := "", 0
	for entry := range strings.SplitSeq(string(res.Stdout), "\x00") {
		if entry == "" {
			continue
		}
		key, value, hasValue := strings.Cut(entry, "\n")
		if !allowed.MatchString(key) {
			continue
		}
		if !hasValue { // a bare key is boolean true; only core's booleans may be one
			if !strings.HasPrefix(key, "core.") {
				continue
			}
			value = "true"
		}
		if n++; n > maxEntries {
			return nil, fmt.Errorf("%s/config: more than %d entries kept", gitDir, maxEntries)
		}
		section, rest, _ := strings.Cut(key, ".")
		i := strings.LastIndex(rest, ".")
		sub, name := "", rest
		if i >= 0 {
			sub, name = rest[:i], rest[i+1:]
		}
		if name == "" || strings.ContainsAny(sub, "\n\x00") {
			return nil, fmt.Errorf("%s/config: bad key %q", gitDir, key)
		}
		h := "[" + section + "]\n"
		if sub != "" {
			h = "[" + section + " \"" + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(sub) + "\"]\n"
		}
		if h != header {
			b.WriteString(h)
			header = h
		}
		b.WriteString("\t" + name + " = \"" + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`, "\b", `\b`).Replace(value) + "\"\n")
	}
	return []byte(b.String()), nil
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
