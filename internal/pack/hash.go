package pack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
)

// ContentHash is a package directory's content hash (D14): sha256 over every
// regular file in sorted slash-path order, each as `path \x00 len \x00 bytes`
// with len in decimal. Lowercase hex. Any other non-directory entry (e.g. a
// symlink) is an error: it is not content the hash could vouch for.
func ContentHash(fsys fs.FS) (string, error) {
	var paths []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
		case d.Type().IsRegular():
			paths = append(paths, p)
		default:
			return fmt.Errorf("%s: not a regular file (%s)", p, d.Type())
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	slices.Sort(paths)

	h := sha256.New()
	for _, p := range paths {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return "", err
		}
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write([]byte(strconv.Itoa(len(data))))
		h.Write([]byte{0})
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
