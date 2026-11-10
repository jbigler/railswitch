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

// TestLoadConfigLAN (D196): a [[lan]] entry gets port 443 when it names
// none; an address outside the LAN ranges, a bad port or name is refused.
func TestLoadConfigLAN(t *testing.T) {
	d := Dirs{Config: t.TempDir()}
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(d.Config, "config.toml"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("schema = 1\n[[lan]]\nservice = \"agent-claude\"\nhost = \"search.home.example.com\"\nprojects = [\"shop\"]\n" +
		"[[lan]]\nservice = \"nvim\"\nhost = \"192.168.1.20:8080\"\n")
	c, err := LoadConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.LAN) != 2 || c.LAN[0].Host != "search.home.example.com:443" || c.LAN[1].Host != "192.168.1.20:8080" || !reflect.DeepEqual(c.LAN[0].Projects, []string{"shop"}) {
		t.Errorf("lan = %+v", c.LAN)
	}
	for host, want := range map[string]string{
		"8.8.8.8":            "not a LAN address",
		"169.254.169.254":    "not a LAN address",
		"0.0.0.1":            "not a LAN address",
		"192.168.1.20:0":     "port",
		"192.168.1.20:443x":  "port",
		"192.168.1.20:08080": "port",
		"bad_name.lan":       "not a host name",
		"-x.lan":             "not a host name",
		"10.0.0.0/8":         "not a host name",
	} {
		write("schema = 1\n[[lan]]\nservice = \"nvim\"\nhost = \"" + host + "\"\n")
		if _, err := LoadConfig(d); err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "lan[0].host") {
			t.Errorf("%q: err = %v, want lan[0].host … %s", host, err, want)
		}
	}
	write("schema = 1\n[[lan]]\nservice = \"Bad_\"\nhost = \"x.lan\"\n")
	if _, err := LoadConfig(d); err == nil || !strings.Contains(err.Error(), "lan[0].service") {
		t.Errorf("bad service: %v", err)
	}
}
