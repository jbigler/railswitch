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

func TestLoadConfigPaths(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	d := Dirs{Config: t.TempDir()}
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(d.Config, "config.toml"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("schema = 1\n[secrets]\ndriver = \"pass\"\ndriver_opts = { key = \"~/k\", root = \"~/.local/share/railswitch/pass\", gpghomedir = \"/g\" }\n")
	c, err := LoadConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"key": "~/k", "root": "/home/u/.local/share/railswitch/pass", "gpghomedir": "/g"}
	if !reflect.DeepEqual(c.Secrets.DriverOpts, want) {
		t.Fatalf("opts = %v", c.Secrets.DriverOpts)
	}
	write("schema = 1\n[secrets]\ndriver_opts = { root = \"rel/pass\" }\n")
	if _, err := LoadConfig(d); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("relative root: %v", err)
	}
	write("[secrets]\ndriver = \"pass\"\n")
	if _, err := LoadConfig(d); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("no schema: %v", err)
	}
}

func TestLoadConfigMounts(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	d := Dirs{Config: t.TempDir()}
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(d.Config, "config.toml"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("schema = 1\n[[mount]]\nservice = \"nvim\"\nsource = \"~/.local/state/kitty\"\ntarget = \"/kitty\"\nprojects = [\"filial\"]\n" +
		"[[mount]]\nservice = \"agent-claude\"\nsource = \"/usr/bin/kitten\"\ntarget = \"/usr/bin/kitten\"\nro = false\noptional = true\nrelabel = false\n")
	c, err := LoadConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Mounts) != 2 {
		t.Fatalf("mounts = %+v", c.Mounts)
	}
	m := c.Mounts[0]
	if m.Service != "nvim" || m.Source != "/home/u/.local/state/kitty" || m.Target != "/kitty" || !m.ReadOnly() || !m.Relabels() || m.Optional || !reflect.DeepEqual(m.Projects, []string{"filial"}) {
		t.Errorf("mount[0] = %+v", m)
	}
	if m := c.Mounts[1]; m.ReadOnly() || !m.Optional || m.Relabels() {
		t.Errorf("mount[1] = %+v", m)
	}
	for body, want := range map[string]string{
		"service = \"nvim\"\nsource = \"/s\"\ntarget = \"rel\"\n":                      "mount[0].target",
		"service = \"nvim\"\nsource = \"/s\"\ntarget = \"/app\"\n":                     "mount[0].target",
		"service = \"nvim\"\nsource = \"rel\"\ntarget = \"/t\"\n":                      "mount[0].source",
		"service = \"Bad_\"\nsource = \"/s\"\ntarget = \"/t\"\n":                       "mount[0].service",
		"service = \"nvim\"\nsource = \"/s\"\ntarget = \"/t\"\nprojects = [\"a-b\"]\n": "mount[0].projects",
	} {
		write("schema = 1\n[[mount]]\n" + body)
		if _, err := LoadConfig(d); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %s", body, err, want)
		}
	}
}
