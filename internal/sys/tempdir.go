package sys

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// RuntimeTemp is a new temp dir named prefix* under $XDG_RUNTIME_DIR (else
// os.TempDir), for a hostile repository's clone. It first removes the
// ones a killed process left behind: real directories (not symlinks)
// named prefix*, owned by this user, last modified more than staleAfter
// ago, which must be far past the deadline of the work that uses them.
func RuntimeTemp(prefix string, staleAfter time.Duration) (string, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	removeStale(base, prefix, time.Now().Add(-staleAfter))
	return os.MkdirTemp(base, prefix)
}

func removeStale(base, prefix string, before time.Time) {
	ents, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		p := filepath.Join(base, e.Name())
		fi, err := os.Lstat(p)
		if err != nil || !fi.IsDir() || fi.ModTime().After(before) {
			continue
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
			continue
		}
		_ = os.RemoveAll(p)
	}
}
