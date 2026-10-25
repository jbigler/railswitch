package tomlfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type doc struct {
	Schema int    `toml:"schema"`
	Name   string `toml:"name"`
}

func TestDecode(t *testing.T) {
	var d doc
	if err := Decode([]byte("schema = 1\nname = \"x\"\n"), 1, &d); err != nil {
		t.Fatal(err)
	}
	if d != (doc{1, "x"}) {
		t.Errorf("Decode = %+v", d)
	}
}

func TestDecodeNewerSchemaBeforeTypes(t *testing.T) {
	var d doc
	err := Decode([]byte("schema = 2\nname = 3\nextra = 1\n"), 1, &d)
	var ne *NewerSchemaError
	if !errors.As(err, &ne) || ne.Schema != 2 || ne.Max != 1 || !strings.Contains(err.Error(), "newer railswitch") {
		t.Errorf("Decode = %v; want a NewerSchemaError for 2 > 1", err)
	}
}

func TestDecodeUnknownKeys(t *testing.T) {
	var d doc
	err := Decode([]byte("schema = 1\nnmae = \"x\"\n[tbl]\nk = 1\n"), 1, &d)
	if err == nil || !strings.Contains(err.Error(), "unknown key nmae") || !strings.Contains(err.Error(), "unknown key tbl") {
		t.Errorf("Decode = %v; want both unknown keys named", err)
	}
}

func TestWriteAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.toml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	fi, _ := os.Stat(path)
	if string(got) != "new" || fi.Mode().Perm() != 0o644 {
		t.Errorf("content %q, mode %v", got, fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}
}

// A failed rename (the target is a non-empty directory) leaves the target
// untouched and no temp file behind.
func TestWriteAtomicFailedRename(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.toml")
	if err := os.MkdirAll(filepath.Join(target, "keep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(target, []byte("new"), 0o644); err == nil {
		t.Fatal("WriteAtomic onto a non-empty directory succeeded")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "f.toml" || !entries[0].IsDir() {
		t.Errorf("dir after failure: %v", entries)
	}
}

// A failed temp creation (directory not writable) leaves the old content.
func TestWriteAtomicUnwritableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "f.toml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if err := WriteAtomic(path, []byte("new"), 0o600); err == nil {
		t.Fatal("WriteAtomic into a read-only dir succeeded")
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Errorf("content after failure = %q; want old", got)
	}
}
