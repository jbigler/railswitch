package safefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestTreeCopy: dirs and regular files come along; a symlink (to a file
// outside, or a dir) is skipped, and limits refuse.
func TestTreeCopy(t *testing.T) {
	src, dst, outside := t.TempDir(), t.TempDir(), t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600))
	must(os.MkdirAll(filepath.Join(src, ".claude", "skills", "x"), 0o755))
	must(os.WriteFile(filepath.Join(src, ".claude", "settings.json"), []byte("{}"), 0o600))
	must(os.WriteFile(filepath.Join(src, ".claude", "skills", "x", "run.sh"), []byte("#!/bin/sh"), 0o700))
	must(os.Symlink(filepath.Join(outside, "secret"), filepath.Join(src, ".claude", "skills", "x", "leak")))
	must(os.Symlink(outside, filepath.Join(src, ".claude", "agents")))

	c := &TreeCopy{Files: 100, Bytes: 1 << 20}
	for _, rel := range []string{".claude/settings.json", ".claude/skills", ".claude/agents", ".claude/missing"} {
		must(c.Copy(src, rel, dst))
	}
	if data, err := os.ReadFile(filepath.Join(dst, ".claude", "settings.json")); err != nil || string(data) != "{}" {
		t.Errorf("settings.json: %q %v", data, err)
	}
	if fi, err := os.Stat(filepath.Join(dst, ".claude", "skills", "x", "run.sh")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("run.sh: %v %v", fi, err)
	}
	for _, p := range []string{".claude/skills/x/leak", ".claude/agents"} {
		if _, err := os.Lstat(filepath.Join(dst, filepath.FromSlash(p))); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s copied: %v", p, err)
		}
	}
	if len(c.Skipped) != 2 {
		t.Errorf("Skipped = %v", c.Skipped)
	}

	small := &TreeCopy{Files: 100, Bytes: 1}
	if err := small.Copy(src, ".claude/settings.json", t.TempDir()); !errors.Is(err, ErrRefused) {
		t.Errorf("byte limit: %v", err)
	}
	few := &TreeCopy{Files: 2, Bytes: 1 << 20}
	if err := few.Copy(src, ".claude", t.TempDir()); !errors.Is(err, ErrRefused) {
		t.Errorf("file limit: %v", err)
	}
}
