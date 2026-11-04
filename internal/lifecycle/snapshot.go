package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"
)

// Snapshot is a named copy of a worktree's databases and seeded volumes
// (D157): what db dump writes, kept in its own dir.
type Snapshot struct {
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	Bytes   int64     `json:"bytes"`
	Files   []string  `json:"files"`
}

// snapshotName is a snapshot's name: a bare file name, no leading dot or
// dash. ls, restore and rm are the subcommands' words.
var snapshotName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// CheckSnapshotName refuses a name db snapshot can't keep.
func CheckSnapshotName(name string) error {
	if !snapshotName.MatchString(name) || name == "ls" || name == "restore" || name == "rm" {
		return fmt.Errorf("snapshot name %q: letters, digits, '_', '.' and '-', at most 64, starting with a letter or digit, and not ls, restore or rm", name)
	}
	return nil
}

// SnapshotsDir holds worktree slug's snapshots, one dir each: in the
// workspace's .railswitch/, which no container mounts (D157).
func SnapshotsDir(ws, slug string) string {
	return filepath.Join(ws, ".railswitch", "snapshots", slug)
}

// DBSnapshot dumps worktree slug's databases and seeded volumes (DBDump)
// into the new snapshot name: a 0700 temp dir in SnapshotsDir, renamed to
// name once every dump succeeded, removed otherwise. An existing name is
// refused. It returns the snapshot's dir.
func (l *L) DBSnapshot(ctx context.Context, slug, name string) (string, error) {
	if err := CheckSnapshotName(name); err != nil {
		return "", err
	}
	root := SnapshotsDir(l.WS.Root, slug)
	dst := filepath.Join(root, name)
	if _, err := os.Lstat(dst); err == nil {
		return "", fmt.Errorf("snapshot %s of worktree %s exists: railswitch db snapshot rm %s first, or pick another name", name, slug, name)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(root, "."+name+".tmp-")
	if err != nil {
		return "", err
	}
	if _, err := l.dumpTo(ctx, slug, tmp, dst); err != nil {
		return "", errors.Join(err, os.RemoveAll(tmp))
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", errors.Join(err, os.RemoveAll(tmp))
	}
	fmt.Fprintf(l.Out, "snapshot %s of worktree %s: %s\n", name, slug, dst)
	return dst, nil
}

// Snapshots are worktree slug's snapshots, newest first; none is [].
// A temp dir of a snapshot still being taken, or left by a crash, is not
// one.
func (l *L) Snapshots(slug string) ([]Snapshot, error) {
	root := SnapshotsDir(l.WS.Root, slug)
	ents, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return []Snapshot{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Snapshot{}
	for _, ent := range ents {
		if !ent.IsDir() || CheckSnapshotName(ent.Name()) != nil {
			continue
		}
		fi, err := ent.Info()
		if err != nil {
			return nil, err
		}
		s := Snapshot{Name: ent.Name(), Created: fi.ModTime(), Files: []string{}}
		files, err := os.ReadDir(filepath.Join(root, ent.Name()))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if !f.Type().IsRegular() {
				continue
			}
			fi, err := f.Info()
			if err != nil {
				return nil, err
			}
			s.Files = append(s.Files, f.Name())
			s.Bytes += fi.Size()
		}
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Snapshot) int { return b.Created.Compare(a.Created) })
	return out, nil
}

// snapshot is snapshot name's dir: a real dir, not a symlink.
func (l *L) snapshot(slug, name string) (string, error) {
	if err := CheckSnapshotName(name); err != nil {
		return "", err
	}
	dir := filepath.Join(SnapshotsDir(l.WS.Root, slug), name)
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("worktree %s has no snapshot %s (railswitch db snapshot ls)", slug, name)
	}
	return dir, nil
}

// DBSnapshotRestore resets worktree slug's databases and seeded volumes
// to snapshot name: DBRestore from its dir, asking first unless yes.
func (l *L) DBSnapshotRestore(ctx context.Context, slug, name string, yes bool) ([]string, error) {
	dir, err := l.snapshot(slug, name)
	if err != nil {
		return nil, err
	}
	return l.DBRestore(ctx, slug, dir, nil, yes)
}

// DBSnapshotRemove deletes snapshot name, by its exact path.
func (l *L) DBSnapshotRemove(slug, name string) error {
	dir, err := l.snapshot(slug, name)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	fmt.Fprintf(l.Out, "removed snapshot %s of worktree %s\n", name, slug)
	return nil
}
