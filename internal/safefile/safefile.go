// Package safefile reads files from untrusted locations (worktrees):
// regular files only, never blocking, bounded size. A symlink as the final
// path component is refused; symlinks in parent directories are followed.
package safefile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// Limit is the default maximum size of a hostile file (1 MiB).
const Limit = 1 << 20

// ErrRefused reports a file that is not a regular file or exceeds the limit.
var ErrRefused = errors.New("refused")

// Read returns the contents of path if it is a regular file (not a symlink)
// of at most limit bytes. Otherwise it returns an error wrapping ErrRefused.
// A missing file gives an error satisfying errors.Is(err, fs.ErrNotExist).
func Read(path string, limit int64) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file: %w", path, ErrRefused)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("%s: symlink: %w", path, ErrRefused)
		}
		return nil, err
	}
	defer f.Close()
	fi, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file: %w", path, ErrRefused)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: larger than %d bytes: %w", path, limit, ErrRefused)
	}
	return data, nil
}
