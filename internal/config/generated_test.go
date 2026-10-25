package config

import (
	"os"
	"path/filepath"
	"reflect"
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
