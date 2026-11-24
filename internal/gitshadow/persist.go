package gitshadow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
)

// upstreamKey is a branch upstream entry Persist may copy back, and
// upstream its values: a remote's name, or a branch ref.
var (
	upstreamKey  = regexp.MustCompile(`^branch\.[^\n\x00]+\.(?:remote|merge)$`)
	remoteValue  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	mergeValue   = regexp.MustCompile(`^refs/heads/[^\s\x00-\x1f\x7f~^:?*\[\\]{1,250}$`)
	upstreamList = `^branch\..*\.(remote|merge)$`
)

// NewForWrite is New for a tool that changes gh-stack's catalog (gh stack
// checkout, D230): the shadow holds a private copy of the catalog (none
// when the repository has none) instead of a link, which gh-stack refuses
// to write over. Persist then carries the new catalog back.
func NewForWrite(ctx context.Context, r sys.Runner, gitDir string) (*Shadow, error) {
	s, err := New(ctx, r, gitDir)
	if err != nil {
		return nil, err
	}
	cat := filepath.Join(s.Dir, "gh-stack")
	if err := os.Remove(cat); err != nil {
		s.Remove()
		return nil, err
	}
	data, err := safefile.Read(filepath.Join(gitDir, "gh-stack"), safefile.Limit)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s, nil
	case err != nil:
		s.Remove()
		return nil, fmt.Errorf("git safety: read %s/gh-stack: %w", gitDir, err)
	}
	if err := writeNew(cat, data); err != nil {
		s.Remove()
		return nil, err
	}
	return s, nil
}

// Persist copies back to the repository at gitDir what a tool wrote in the
// shadow that belongs there (gh stack checkout, D230): gh-stack's catalog,
// when the tool wrote one in place of the link, and the branch upstreams
// it set (branch.<name>.remote and .merge, values checked). Nothing else
// of the shadow reaches the repository. Each file is written whole to a
// new private file in gitDir, then renamed over the old one: a symlink a
// container planted there is replaced, never followed.
func (s *Shadow) Persist(ctx context.Context, r sys.Runner, gitDir string) error {
	cat := filepath.Join(s.Dir, "gh-stack")
	if fi, err := os.Lstat(cat); err == nil && fi.Mode().IsRegular() {
		data, err := safefile.Read(cat, safefile.Limit)
		if err != nil {
			return fmt.Errorf("git safety: the new gh-stack catalog: %w", err)
		}
		if err := replace(filepath.Join(gitDir, "gh-stack"), data); err != nil {
			return fmt.Errorf("git safety: write %s/gh-stack: %w", gitDir, err)
		}
	}
	return s.persistUpstreams(ctx, r, gitDir)
}

// persistUpstreams sets in gitDir's config each branch upstream the
// shadow's config has and it lacks, editing a private copy with git
// config --file (no includes, no other config) and renaming it into place.
func (s *Shadow) persistUpstreams(ctx context.Context, r sys.Runner, gitDir string) error {
	want, err := listUpstreams(ctx, r, s.tmp, filepath.Join(s.Dir, "config"))
	if err != nil {
		return err
	}
	data, err := safefile.Read(filepath.Join(gitDir, "config"), safefile.Limit)
	if err != nil {
		return fmt.Errorf("git safety: read %s/config: %w", gitDir, err)
	}
	dir := filepath.Join(s.tmp, "persist")
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	copyPath := filepath.Join(dir, "config")
	if err := writeNew(copyPath, data); err != nil {
		return err
	}
	have, err := listUpstreams(ctx, r, s.tmp, copyPath)
	if err != nil {
		return err
	}
	changed := false
	for k, v := range want {
		if have[k] == v || !upstreamKey.MatchString(k) {
			continue
		}
		if strings.HasSuffix(k, ".remote") && !remoteValue.MatchString(v) || strings.HasSuffix(k, ".merge") && !mergeValue.MatchString(v) {
			continue
		}
		if _, err := r.Run(ctx, gitConfig(s.tmp, copyPath, k, v)); err != nil {
			return fmt.Errorf("git safety: set %s: %w", k, err)
		}
		changed = true
	}
	if !changed {
		return nil
	}
	out, err := os.ReadFile(copyPath)
	if err != nil {
		return err
	}
	if err := replace(filepath.Join(gitDir, "config"), out); err != nil {
		return fmt.Errorf("git safety: write %s/config: %w", gitDir, err)
	}
	return nil
}

// listUpstreams is the branch upstream entries of the config file at path.
func listUpstreams(ctx context.Context, r sys.Runner, tmp, path string) (map[string]string, error) {
	c := gitConfig(tmp, path, "", "")
	c.Args = []string{"config", "--no-includes", "--file", path, "--get-regexp", "-z", upstreamList}
	res, err := r.Run(ctx, c)
	out := map[string]string{}
	if errors.Is(err, sys.ErrExit) && res.ExitCode == 1 { // none
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("git safety: list %s: %w", path, err)
	}
	for entry := range strings.SplitSeq(string(res.Stdout), "\x00") {
		if k, v, ok := strings.Cut(entry, "\n"); ok {
			out[k] = v
		}
	}
	return out, nil
}

// gitConfig is `git config --no-includes --file path key value`, with no
// system or global config and no repository around it.
func gitConfig(tmp, path, key, value string) sys.Cmd {
	return sys.Cmd{
		Name: "git", Args: []string{"config", "--no-includes", "--file", path, key, value},
		Dir: tmp,
		Env: append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CEILING_DIRECTORIES="+tmp),
	}
}

// replace writes data to a new file beside path, with the mode of the file
// it replaces (0644 for none: containers read the repository), and renames
// it over path.
func replace(path string, data []byte) error {
	mode := fs.FileMode(0o644)
	if fi, err := os.Lstat(path); err == nil && fi.Mode().IsRegular() {
		mode = fi.Mode().Perm()
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), ".railswitch-"+filepath.Base(path)+"-"+hex.EncodeToString(b))
	if err := writeNew(tmp, data); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
