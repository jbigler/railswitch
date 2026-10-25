package machine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	d := Dirs{Config: t.TempDir()}
	c, err := LoadConfig(d)
	if err != nil || !reflect.DeepEqual(c, &Config{}) {
		t.Fatalf("missing: got %+v %v", c, err)
	}
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(d.Config, "config.toml"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("schema = 1\n[secrets]\ndriver = \"pass\"\ndriver_opts = { key = \"ABC\", root = \"/x\" }\n")
	c, err = LoadConfig(d)
	want := &Config{Schema: 1, Secrets: SecretsConfig{Driver: "pass", DriverOpts: map[string]string{"key": "ABC", "root": "/x"}}}
	if err != nil || !reflect.DeepEqual(c, want) {
		t.Fatalf("pass: got %+v %v", c, err)
	}
	write("schema = 2\n")
	if _, err := LoadConfig(d); err == nil || !strings.Contains(err.Error(), "newer railswitch") {
		t.Fatalf("schema 2: got %v", err)
	}
	write("schema = 1\n[secrets]\ndrivr = \"pass\"\n")
	if _, err := LoadConfig(d); err == nil || !strings.Contains(err.Error(), "drivr") {
		t.Fatalf("unknown key: got %v", err)
	}
}
