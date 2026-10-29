package config

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadGenerated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mise.local.toml")
	src := "[tools]\nruby = \"3.3.6\"\n\n[env]\nDB_PASSWORD = \"x\"\nPORT = 3000\nDEBUG = true\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadGenerated(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"DB_PASSWORD": "x"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ReadGenerated = %v; want %v", got, want)
	}
}

func TestReadGeneratedMissing(t *testing.T) {
	got, err := ReadGenerated(filepath.Join(t.TempDir(), "mise.local.toml"))
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("ReadGenerated(missing) = %v, %v; want an empty map, nil", got, err)
	}
}

func TestReadGeneratedErrorHidesValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mise.local.toml")
	if err := os.WriteFile(path, []byte("[env]\nA = \"ok\"\nDB_PASSWORD = \"s3cr3t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadGenerated(path)
	if err == nil || strings.Contains(err.Error(), "s3cr3t") || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("ReadGenerated = %v; want a line-3 error without the value", err)
	}
}

// TestMigrateGenerated (D132): the values move unchanged; the old file
// goes only when it is exactly railswitch's, an edited value included;
// one you added to is kept; a workspace already moved is left alone.
func TestMigrateGenerated(t *testing.T) {
	values := map[string]string{"DB_PASSWORD": "pw \"q\" ü", "S3_SECRET_KEY": "rustfsadmin"}
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
		if before, err := LoadGenerated(root); err != nil || !maps.Equal(before, values) {
			t.Fatalf("%s: before = %v, %v", name, before, err)
		}
		moved, kept, err := MigrateGenerated(root)
		if err != nil || !moved || kept != c.keepsOld {
			t.Fatalf("%s: moved %v kept %v, %v", name, moved, kept, err)
		}
		if after, err := LoadGenerated(root); err != nil || !maps.Equal(after, values) {
			t.Errorf("%s: after = %v, %v; want the same values", name, after, err)
		}
		if fi, err := os.Stat(filepath.Join(root, GeneratedFile)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: new file %v, %v; want 0600", name, fi, err)
		}
		if _, err := os.Stat(old); (err == nil) != c.keepsOld {
			t.Errorf("%s: old file kept %v; want %v", name, err == nil, c.keepsOld)
		}
		if moved, _, err := MigrateGenerated(root); moved || err != nil {
			t.Errorf("%s: second run moved %v, %v", name, moved, err)
		}
	}
	// Nothing to move: nothing written.
	root := t.TempDir()
	if moved, _, err := MigrateGenerated(root); moved || err != nil {
		t.Errorf("empty: moved %v, %v", moved, err)
	}
	if _, err := os.Stat(filepath.Join(root, GeneratedFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("empty workspace got a generated file: %v", err)
	}
}

// A name the new file can't hold as a bare key (one a user added, quoted)
// keeps the legacy file, and what is written still parses.
func TestMigrateGeneratedOddName(t *testing.T) {
	root := t.TempDir()
	legacy := "[env]\nDB_PASSWORD = \"pw\"\n\"npm_config_//r/:_authToken\" = \"tok\"\n\"A.B\" = \"x\"\n"
	if err := os.WriteFile(filepath.Join(root, LegacyGeneratedFile), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	moved, kept, err := MigrateGenerated(root)
	if err != nil || !moved || !kept {
		t.Fatalf("moved %v kept %v, %v; want moved and the legacy file kept", moved, kept, err)
	}
	got, err := LoadGenerated(root)
	if err != nil || got["DB_PASSWORD"] != "pw" {
		t.Errorf("after = %v, %v; want DB_PASSWORD readable", got, err)
	}
}

// A move interrupted before removing the legacy file: the next run removes
// it when it is railswitch's text for the moved values, never otherwise.
func TestMigrateGeneratedInterrupted(t *testing.T) {
	values := map[string]string{"DB_PASSWORD": "pw"}
	for name, legacy := range map[string]string{
		"railswitch's":     string(GeneratedText(values)),
		"different values": string(GeneratedText(map[string]string{"DB_PASSWORD": "other"})),
	} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, LegacyGeneratedFile), []byte(legacy), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := CreateGenerated(root, values); err != nil {
			t.Fatal(err)
		}
		if _, _, err := MigrateGenerated(root); err != nil {
			t.Fatal(err)
		}
		_, err := os.Stat(filepath.Join(root, LegacyGeneratedFile))
		if gone, want := errors.Is(err, fs.ErrNotExist), name == "railswitch's"; gone != want {
			t.Errorf("%s: legacy removed %v; want %v", name, gone, want)
		}
	}
}

// CreateGenerated never replaces values already there (a project joined
// again over a kept .railswitch/).
func TestCreateGeneratedRefusesExisting(t *testing.T) {
	root := t.TempDir()
	if err := CreateGenerated(root, map[string]string{"DB_PASSWORD": "first"}); err != nil {
		t.Fatal(err)
	}
	if err := CreateGenerated(root, map[string]string{"DB_PASSWORD": "second"}); !errors.Is(err, fs.ErrExist) {
		t.Errorf("second create: %v; want it refused", err)
	}
	if got, _ := LoadGenerated(root); got["DB_PASSWORD"] != "first" {
		t.Errorf("value = %q; want first kept", got["DB_PASSWORD"])
	}
}
