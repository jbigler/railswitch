package lockstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/pack"
)

func pkg() fstest.MapFS {
	return fstest.MapFS{
		"pack.toml":                 {Data: []byte("name = \"x\"\n")},
		"deps.allow":                {Data: []byte("example.com\n")},
		"entrypoints/entrypoint.sh": {Data: []byte("#!/bin/sh\n")},
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := Store{Dir: filepath.Join(t.TempDir(), "locked")}
	sha, err := s.Put(pkg())
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := pack.ContentHash(pkg()); sha != want {
		t.Fatalf("Put = %s, want the content hash %s", sha, want)
	}
	got, err := s.Get(sha)
	if err != nil {
		t.Fatal(err)
	}
	for name, f := range pkg() {
		b, err := fs.ReadFile(got, name)
		if err != nil || string(b) != string(f.Data) {
			t.Errorf("%s = %q (%v), want %q", name, b, err, f.Data)
		}
	}
	for rel, want := range map[string]os.FileMode{"": 0o755, "entrypoints": 0o755, "pack.toml": 0o644, "entrypoints/entrypoint.sh": 0o755} {
		fi, err := os.Stat(filepath.Join(s.Dir, sha, rel))
		if err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v (%v), want %v", rel, fi.Mode().Perm(), err, want)
		}
	}
	if _, err := s.Get(strings.Repeat("0", 64)); !errors.Is(err, ErrMissing) {
		t.Errorf("Get of an unknown sha: %v", err)
	}
	if _, err := s.Get("../x"); !errors.Is(err, ErrMissing) {
		t.Errorf("Get of a path: %v", err)
	}
}

func TestStoreTampered(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	sha, err := s.Put(pkg())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, sha, "deps.allow"), []byte("evil.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(sha); !errors.Is(err, ErrMissing) {
		t.Fatalf("Get of an altered copy: %v, want ErrMissing", err)
	}
	// A second Put leaves an existing dir as it is, so the copy stays missing.
	if _, err := s.Put(pkg()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("pack.toml", filepath.Join(s.Dir, sha, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(sha); !errors.Is(err, ErrMissing) {
		t.Fatalf("Get of a copy holding a symlink: %v", err)
	}
}

func TestStorePutIdempotent(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	a, err := s.Put(pkg())
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Put(pkg())
	if err != nil || a != b {
		t.Fatalf("second Put = %s, %v; want %s", b, err, a)
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != a {
		t.Fatalf("store holds %v (%v), want only %s", entries, err, a)
	}
}

func TestStoreRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pack.toml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "deps.allow")); err != nil {
		t.Fatal(err)
	}
	s := Store{Dir: t.TempDir()}
	if _, err := s.Put(os.DirFS(dir)); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Put of a symlink: %v", err)
	}
	if entries, _ := os.ReadDir(s.Dir); len(entries) != 0 {
		t.Errorf("store holds %v after a refused Put", entries)
	}
}

func TestStoreLimits(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	big := fstest.MapFS{"pack.toml": {Data: make([]byte, maxFile+1)}}
	if _, err := s.Put(big); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Errorf("Put of a file over 1 MiB: %v", err)
	}
	many := fstest.MapFS{}
	for i := range maxFiles + 1 {
		many[fmt.Sprintf("f%d", i)] = &fstest.MapFile{Data: []byte("x")}
	}
	if _, err := s.Put(many); err == nil || !strings.Contains(err.Error(), "files") {
		t.Errorf("Put of %d files: %v", maxFiles+1, err)
	}
	// A copy grown on disk past a limit reads as missing.
	sha, err := s.Put(pkg())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, sha, "pack.toml"), make([]byte, maxFile+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(sha); !errors.Is(err, ErrMissing) {
		t.Errorf("Get of an oversized copy: %v", err)
	}
}
