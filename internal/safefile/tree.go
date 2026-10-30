package safefile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"syscall"
)

// TreeCopy copies paths out of an untrusted directory (an agent's home)
// into a trusted one, within limits shared by every Copy.
type TreeCopy struct {
	Files   int   // most files and directories in all
	Bytes   int64 // most bytes in all
	Skipped []string

	files int
	bytes int64
}

// Copy copies rel (a slash path) under src to the same path under dst,
// reading src through an os.Root so nothing outside it is reached:
// directories and regular files only; a symlink, device, socket or FIFO is
// skipped and listed in Skipped. Files are written 0644, or 0755 when their
// owner may execute them, directories 0755 (dst is the caller's to keep
// private). A missing rel copies nothing.
func (c *TreeCopy) Copy(src, rel, dst string) error {
	root, err := os.OpenRoot(src)
	if err != nil {
		return err
	}
	defer root.Close()
	return c.copy(root, rel, dst)
}

func (c *TreeCopy) copy(root *os.Root, rel, dst string) error {
	fi, err := root.Lstat(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.files++; c.files > c.Files {
		return fmt.Errorf("more than %d files: %w", c.Files, ErrRefused)
	}
	out := filepath.Join(dst, filepath.FromSlash(rel))
	switch {
	case fi.IsDir():
		if err := os.MkdirAll(out, 0o755); err != nil {
			return err
		}
		f, err := root.Open(rel)
		if err != nil {
			return err
		}
		ents, err := f.ReadDir(-1)
		f.Close()
		if err != nil {
			return err
		}
		for _, e := range ents {
			if err := c.copy(root, path.Join(rel, e.Name()), dst); err != nil {
				return err
			}
		}
		return nil
	case !fi.Mode().IsRegular():
		c.Skipped = append(c.Skipped, rel)
		return nil
	}
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if fi, err = f.Stat(); err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		c.Skipped = append(c.Skipped, rel)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if fi.Mode()&0o100 != 0 {
		mode = 0o755
	}
	w, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return err
	}
	n, err := io.Copy(w, io.LimitReader(f, c.Bytes-c.bytes+1))
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if c.bytes += n; c.bytes > c.Bytes {
		return fmt.Errorf("more than %d bytes: %w", c.Bytes, ErrRefused)
	}
	return nil
}
