package pack

import (
	"io/fs"
	"maps"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/pack/builtin"
)

func builtins(t *testing.T) *Catalog {
	t.Helper()
	cat, err := LoadBuiltins()
	if err != nil {
		t.Fatalf("LoadBuiltins: %v", err)
	}
	return cat
}

func TestLoadBuiltins(t *testing.T) {
	cat := builtins(t)
	if cat.Packs[Ref{"rails", 1}] == nil {
		t.Errorf("no pack rails@1; have %v", slices.Collect(maps.Keys(cat.Packs)))
	}
	for _, name := range []string{"postgres", "redis", "rustfs", "playwright", "sidekiq"} {
		if cat.Options[Ref{name, 1}] == nil {
			t.Errorf("no option %s@1", name)
		}
	}
	if cat.Tools["nvim"] == nil {
		t.Errorf("no tool nvim")
	}
	// Every package carries its own directory.
	if _, err := fs.Stat(cat.Packs[Ref{"rails", 1}].Files, "Containerfile.base"); err != nil {
		t.Errorf("rails Files: %v", err)
	}
	if _, err := fs.Stat(cat.Options[Ref{"postgres", 1}].Files, "initdb/restore-dump.sh"); err != nil {
		t.Errorf("postgres Files: %v", err)
	}
	if _, err := fs.Stat(cat.Tools["nvim"].Files, "Containerfile.nvim"); err != nil {
		t.Errorf("nvim Files: %v", err)
	}
	// Every built-in hashes (stack.lock records it, Task 12).
	var dirs []fs.FS
	for _, p := range cat.Packs {
		dirs = append(dirs, p.Files)
	}
	for _, o := range cat.Options {
		dirs = append(dirs, o.Files)
	}
	for _, tl := range cat.Tools {
		dirs = append(dirs, tl.Files)
	}
	for _, d := range dirs {
		mustHash(t, d)
	}
}

func TestParseRef(t *testing.T) {
	r, err := ParseRef("rails@1")
	if err != nil || r != (Ref{"rails", 1}) || r.String() != "rails@1" {
		t.Errorf(`ParseRef("rails@1") = %v, %v`, r, err)
	}
	for _, bad := range []string{"rails", "rails@x", "@1", "rails@0", "rails@01", "Rails@1", "rails@1@2", ""} {
		if _, err := ParseRef(bad); err == nil {
			t.Errorf("ParseRef(%q): no error", bad)
		}
	}
}

func TestContentHashStable(t *testing.T) {
	a := fstest.MapFS{}
	a["pack.toml"] = &fstest.MapFile{Data: []byte("name = \"x\"\n")}
	a["entrypoints/e.sh"] = &fstest.MapFile{Data: []byte("#!/bin/sh\n")}
	a["a.txt"] = &fstest.MapFile{Data: []byte("a")}
	b := fstest.MapFS{}
	b["a.txt"] = &fstest.MapFile{Data: []byte("a")}
	b["entrypoints/e.sh"] = &fstest.MapFile{Data: []byte("#!/bin/sh\n")}
	b["pack.toml"] = &fstest.MapFile{Data: []byte("name = \"x\"\n")}

	ha, err := ContentHash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := ContentHash(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Errorf("hash differs by insertion order: %s vs %s", ha, hb)
	}
	if len(ha) != 64 || strings.ToLower(ha) != ha {
		t.Errorf("hash %q is not lowercase sha256 hex", ha)
	}
	b["a.txt"] = &fstest.MapFile{Data: []byte("b")}
	if hc, _ := ContentHash(b); hc == ha {
		t.Errorf("changing one byte kept the hash")
	}
	// Moving bytes between path and content must change the hash too.
	c := fstest.MapFS{"ab": {Data: []byte("c")}}
	d := fstest.MapFS{"a": {Data: []byte("bc")}}
	if hc, _ := ContentHash(c); hc == mustHash(t, d) {
		t.Errorf("path/content boundary is ambiguous")
	}
}

func mustHash(t *testing.T, fsys fs.FS) string {
	t.Helper()
	h, err := ContentHash(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// minimal is a catalog with one valid pack and one valid option; tests edit it.
func minimal() fstest.MapFS {
	return fstest.MapFS{
		"packs/p/pack.toml": {Data: []byte(`name = "p"
version = 1
[app]
service = "web"
server = "run"
port = 3000
install = "fetch --frozen"
[image]
file = "Containerfile.base"
tag = "x"
os = "debian"
`)},
		"packs/p/Containerfile.base": {Data: []byte("FROM docker.io/library/debian:trixie-slim\n")},
		"options/o/option.toml": {Data: []byte(`name = "o"
version = 1
slot = "cache"
service = "cache"
[image]
pull = "docker.io/library/redis:8"
[[ports]]
name = "o"
env = "O_PORT"
base = 55432
container = 1
`)},
	}
}

func TestMinimalCatalogLoads(t *testing.T) {
	if _, err := Load(minimal()); err != nil {
		t.Fatalf("Load(minimal): %v", err)
	}
}

// TestDepsNeedsInstallOrEntrypoint: a pack whose deps service would run
// nothing (no [app] install, no deps_entrypoint) is refused.
func TestDepsNeedsInstallOrEntrypoint(t *testing.T) {
	fsys := minimal()
	fsys["packs/p/pack.toml"].Data = []byte(strings.Replace(string(fsys["packs/p/pack.toml"].Data), "install = \"fetch --frozen\"\n", "", 1))
	_, err := Load(fsys)
	if err == nil || !strings.Contains(err.Error(), "app.install") || !strings.Contains(err.Error(), "app.deps_entrypoint") {
		t.Errorf("Load without install or deps_entrypoint: %v", err)
	}
}

func TestUnknownFieldInPackToml(t *testing.T) {
	fsys := minimal()
	fsys["packs/p/pack.toml"].Data = append(fsys["packs/p/pack.toml"].Data, []byte("[app2]\nx = 1\n")...)
	_, err := Load(fsys)
	if err == nil || !strings.Contains(err.Error(), "app2") || !strings.Contains(err.Error(), "packs/p/pack.toml") {
		t.Errorf("Load with unknown key: %v", err)
	}
}

func TestPortSamePort(t *testing.T) {
	fsys := minimal()
	opt := fsys["options/o/option.toml"]
	opt.Data = []byte(strings.Replace(string(opt.Data), "container = 1\n", "same_port = true\n", 1))
	cat, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load with same_port: %v", err)
	}
	if p := cat.Options[Ref{"o", 1}].Ports[0]; !p.SamePort || p.Container != 0 {
		t.Errorf("port = %+v; want SamePort and no container", p)
	}

	opt.Data = append(opt.Data, []byte("container = 5\n")...)
	if _, err := Load(fsys); err == nil || !strings.Contains(err.Error(), "same_port") {
		t.Errorf("Load with same_port and container: %v", err)
	}
}

func TestPortBasesUnique(t *testing.T) {
	fsys := minimal()
	fsys["options/q/option.toml"] = &fstest.MapFile{Data: []byte(`name = "q"
version = 1
slot = "database"
service = "db"
[image]
pull = "docker.io/library/postgres:16"
[[ports]]
name = "q"
env = "Q_PORT"
base = 55432
container = 5432
`)}
	_, err := Load(fsys)
	if err == nil || !strings.Contains(err.Error(), "55432") {
		t.Errorf("Load with a duplicate port base: %v", err)
	}
}

func TestRailsPackGeneric(t *testing.T) {
	p := builtins(t).Packs[Ref{"rails", 1}]
	// RUBY_DEBUG_HOST: the debug gem (in Rails' default Gemfile) listens on the published debug port.
	if got := slices.Sorted(maps.Keys(p.Env)); !slices.Equal(got, []string{"BINDING", "RAILS_DEVELOPMENT_HOSTS", "RAILS_ENV", "RUBY_DEBUG_HOST"}) {
		t.Errorf("rails [env] keys = %v, want exactly BINDING, RAILS_DEVELOPMENT_HOSTS, RAILS_ENV, RUBY_DEBUG_HOST", got)
	}
	if p.Defaults["browser"] != "none" {
		t.Errorf("defaults.browser = %q, want none", p.Defaults["browser"])
	}
	if p.App.Health == "" {
		t.Errorf("app.health is empty")
	}
	if p.Deps.FrozenEnv["BUNDLE_FROZEN"] != "true" {
		t.Errorf("deps.frozen_env = %v", p.Deps.FrozenEnv)
	}
}

func TestPlaywrightOptionEnv(t *testing.T) {
	o := builtins(t).Options[Ref{"playwright", 1}]
	for _, m := range []map[string]string{o.Env, o.Gives} {
		if _, ok := m["HEADLESS_SYSTEM_TESTS"]; ok {
			t.Errorf("playwright defines HEADLESS_SYSTEM_TESTS")
		}
	}
	if !maps.Equal(o.Gives, map[string]string{"PLAYWRIGHT_HOST": "ws://{service}:8888/connect"}) {
		t.Errorf("playwright gives = %v", o.Gives)
	}
	if !maps.Equal(o.Env, map[string]string{"CHROME_START_URL": "http://{worktree.host}"}) {
		t.Errorf("playwright env = %v", o.Env)
	}
}

var qualifiedPrefixes = []string{"docker.io/", "ghcr.io/", "quay.io/", "mcr.microsoft.com/"}

func TestBuiltinFromQualified(t *testing.T) {
	seen := 0
	err := fs.WalkDir(builtin.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasPrefix(d.Name(), "Containerfile") {
			return err
		}
		seen++
		data, err := fs.ReadFile(builtin.FS, path)
		if err != nil {
			return err
		}
		stages := map[string]bool{}
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 || !strings.EqualFold(f[0], "FROM") {
				continue
			}
			f = f[1:]
			for len(f) > 0 && strings.HasPrefix(f[0], "--") {
				f = f[1:]
			}
			if len(f) == 0 {
				t.Errorf("%s: FROM without an image", path)
				continue
			}
			img := f[0]
			switch {
			case img == "${BASE_IMAGE}", img == "scratch", stages[img]:
			case slices.ContainsFunc(qualifiedPrefixes, func(p string) bool { return strings.HasPrefix(img, p) }):
			default:
				t.Errorf("%s: FROM %s is not fully qualified (D36)", path, img)
			}
			if len(f) >= 3 && strings.EqualFold(f[1], "AS") {
				stages[f[2]] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < 4 {
		t.Errorf("found %d built-in Containerfiles, want at least 4", seen)
	}
}

func TestOptionImageKinds(t *testing.T) {
	cat := builtins(t)
	if got := cat.Options[Ref{"playwright", 1}].Image.Build; got != "Containerfile.playwright" {
		t.Errorf("playwright Image.Build = %q", got)
	}
	if got := cat.Options[Ref{"sidekiq", 1}].Image.Use; got != "app" {
		t.Errorf("sidekiq Image.Use = %q", got)
	}
	if got := cat.Options[Ref{"postgres", 1}].Image.Pull; got != "docker.io/library/postgres:{postgres}" {
		t.Errorf("postgres Image.Pull = %q", got)
	}

	for name, image := range map[string]string{
		"pull and use": "pull = \"docker.io/library/redis:8\"\nuse = \"app\"\n",
		"none":         "",
		"use not app":  "use = \"base\"\n",
		"build no tag": "build = \"Containerfile.o\"\n",
		"tag on pull":  "pull = \"docker.io/library/redis:8\"\ntag = \"x\"\n",
	} {
		fsys := minimal()
		fsys["options/o/Containerfile.o"] = &fstest.MapFile{Data: []byte("FROM docker.io/library/debian:trixie\n")}
		fsys["options/o/option.toml"] = &fstest.MapFile{Data: []byte("name = \"o\"\nversion = 1\nslot = \"cache\"\nservice = \"cache\"\n[image]\n" + image)}
		if _, err := Load(fsys); err == nil || !strings.Contains(err.Error(), "image") {
			t.Errorf("%s: Load error = %v, want an [image] error", name, err)
		}
	}
}

func TestMissingPackageFile(t *testing.T) {
	fsys := minimal()
	delete(fsys, "packs/p/Containerfile.base")
	if _, err := Load(fsys); err == nil || !strings.Contains(err.Error(), "Containerfile.base") {
		t.Errorf("Load without the image file: %v", err)
	}
}

func TestNameMatchesDirectory(t *testing.T) {
	fsys := minimal()
	fsys["packs/q/pack.toml"] = fsys["packs/p/pack.toml"]
	fsys["packs/q/Containerfile.base"] = fsys["packs/p/Containerfile.base"]
	if _, err := Load(fsys); err == nil || !strings.Contains(err.Error(), "packs/q") {
		t.Errorf("Load with name p in packs/q: %v", err)
	}
}

// TestRailsDepsEntrypointExecsArgs: given a command (the pack's install,
// or another pack reusing it), the deps entrypoint execs it after its setup
// instead of its own install.
func TestRailsDepsEntrypointExecsArgs(t *testing.T) {
	script, err := fs.ReadFile(builtin.FS, "packs/rails/entrypoints/entrypoint-deps.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	body := strings.Replace(string(script), "cd /app", "cd "+dir, 1)
	cmd := exec.Command("sh", "-c", body, "entrypoint-deps.sh", "sh", "-c", "pwd; echo installed")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != dir+"\ninstalled\n" {
		t.Errorf("entrypoint with args: %v\n%s", err, out)
	}
}
