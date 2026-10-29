package config

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateGenerated (D132): the values move unchanged; the old file
// goes only when it is exactly railswitch's, an edited value included;
// one you added to is kept; a workspace already moved is left alone.
func TestMigrateGenerated(t *testing.T) {
	values := map[string]string{"DB_PASSWORD": "pw", "S3_SECRET_KEY": "rustfsadmin"}
	for name, c := range map[string]struct {
		legacy   string
		keepsOld bool
	}{
		"railswitch's":  {string(GeneratedText(values)), false},
		"one you added": {string(GeneratedText(values)) + "[tools]\nnode = \"22\"\n", true},
	} {
		root := t.TempDir()
		old := filepath.Join(root, LegacyGeneratedFile)
		if err := os.WriteFile(old, []byte(c.legacy), 0o600); err != nil {
			t.Fatal(err)
		}
		before, err := LoadGenerated(root)
		if err != nil || !maps.Equal(before, values) {
			t.Fatalf("%s: before = %v, %v", name, before, err)
		}
		moved, err := MigrateGenerated(root)
		if err != nil || !moved {
			t.Fatalf("%s: moved %v, %v", name, moved, err)
		}
		after, err := LoadGenerated(root)
		if err != nil || !maps.Equal(after, values) {
			t.Errorf("%s: after = %v, %v; want the same values", name, after, err)
		}
		if fi, err := os.Stat(filepath.Join(root, GeneratedFile)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: new file %v, %v; want 0600", name, fi, err)
		}
		_, err = os.Stat(old)
		if kept := err == nil; kept != c.keepsOld {
			t.Errorf("%s: old file kept %v; want %v", name, kept, c.keepsOld)
		}
		if moved, err := MigrateGenerated(root); moved || err != nil {
			t.Errorf("%s: second run moved %v, %v", name, moved, err)
		}
	}
	// Nothing to move: nothing written.
	root := t.TempDir()
	if moved, err := MigrateGenerated(root); moved || err != nil {
		t.Errorf("empty: moved %v, %v", moved, err)
	}
	if _, err := os.Stat(filepath.Join(root, GeneratedFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("empty workspace got a generated file: %v", err)
	}
}
