package detect

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/sys"
)

func builtins(t *testing.T) *pack.Catalog {
	t.Helper()
	c, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func choose(t *testing.T, cat *pack.Catalog, files map[string]string, packRef string) Result {
	t.Helper()
	m := map[string][]byte{}
	for k, v := range files {
		m[k] = []byte(v)
	}
	r, err := Choose(cat, m, packRef)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func option(t *testing.T, r Result, key string) OptionMatch {
	t.Helper()
	for _, o := range r.Options {
		if o.Key == key {
			return o
		}
	}
	t.Fatalf("no option %s in %+v", key, r.Options)
	return OptionMatch{}
}

func TestPaths(t *testing.T) {
	got := Paths(builtins(t))
	if want := []string{"Gemfile", "config/application.rb"}; !slices.Equal(got, want) {
		t.Errorf("Paths = %v; want %v", got, want)
	}
}

// TestChooseRails: a pack needs every one of its files; options fire on a
// matching line, not a commented one; the rest fall back to the pack's
// defaults.
func TestChooseRails(t *testing.T) {
	cat := builtins(t)
	r := choose(t, cat, map[string]string{
		"Gemfile":               "source 'https://rubygems.org'\ngem 'pg'\r\n# gem \"sidekiq\"\ngem 'redis', '~> 5'\n",
		"config/application.rb": "module App; end\n",
	}, "")
	if r.Pack == nil || r.Pack.Ref != "rails@1" || !slices.Equal(r.Pack.Evidence, []string{"Gemfile", "config/application.rb"}) {
		t.Fatalf("pack = %+v", r.Pack)
	}
	if db := option(t, r, "database"); !reflect.DeepEqual(db, OptionMatch{Key: "database", Ref: "postgres@1", Source: "repo", Evidence: "Gemfile: pg", Others: []string{}}) {
		t.Errorf("database = %+v", db)
	}
	if c := option(t, r, "cache"); c.Ref != "redis@1" || c.Source != "repo" || c.Evidence != "Gemfile: redis" {
		t.Errorf("cache = %+v", c)
	}
	jobs := option(t, r, "jobs")
	if want := cat.Packs[pack.Ref{Name: "rails", Version: 1}].Defaults["jobs"]; want == "" || jobs.Ref != want || jobs.Source != "default" || jobs.Evidence != "" {
		t.Errorf("jobs = %+v; want the pack default %q from source default", jobs, want)
	}
	if m := option(t, r, "mail"); m.Ref != "none" || m.Source != "default" {
		t.Errorf("mail = %+v", m)
	}
	var keys []string
	for _, o := range r.Options {
		keys = append(keys, o.Key)
	}
	if want := []string{"database", "cache", "storage", "jobs", "mail", "browser"}; !slices.Equal(keys, want) {
		t.Errorf("keys = %v; want config.Slots order", keys)
	}

	r = choose(t, cat, map[string]string{"Gemfile": "gem 'pg'\n"}, "")
	if r.Pack != nil || r.Options == nil || len(r.Options) != 0 {
		t.Errorf("Gemfile only: %+v; want no pack, no options", r)
	}
	b, _ := json.Marshal(r)
	if string(b) != `{"branch":"","pack":null,"options":[]}` {
		t.Errorf("JSON = %s", b)
	}
}

// fixture is a catalog of two packs and three database options, a and b of
// which detect the same line.
func fixture(t *testing.T) *pack.Catalog {
	t.Helper()
	packToml := func(name, defaultDB string) string {
		return `name = "` + name + `"
version = 1
[app]
service = "web"
server = "run"
port = 3000
install = "fetch"
[image]
file = "Containerfile.base"
tag = "x"
os = "debian"
[defaults]
database = "` + defaultDB + `"
[detect]
files = ["mix.exs"]
`
	}
	optToml := func(name, worksWith, match string) string {
		return `name = "` + name + `"
version = 1
slot = "database"
service = "db"
works_with = [` + worksWith + `]
[image]
pull = "x"
[[detect]]
file = "deps.txt"
match = '` + match + `'
`
	}
	c, err := pack.Load(fstest.MapFS{
		"packs/p/pack.toml":          {Data: []byte(packToml("p", "b@1"))},
		"packs/p/Containerfile.base": {Data: []byte("FROM x\n")},
		"packs/q/pack.toml":          {Data: []byte(packToml("q", "none"))},
		"packs/q/Containerfile.base": {Data: []byte("FROM x\n")},
		"options/a/option.toml":      {Data: []byte(optToml("a", `"*"`, "^db$"))},
		"options/b/option.toml":      {Data: []byte(optToml("b", `"p", "q"`, "^db$"))},
		"options/c/option.toml":      {Data: []byte(optToml("c", `"q"`, "^db$"))},
		"options/d/option.toml":      {Data: []byte(optToml("d", `"*"`, "^other$"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestChooseDefaultWins: the pack's default wins when it matched; the other
// matches that work with the pack go in others; the first matching pack by
// ref wins and the rest are others.
func TestChooseDefaultWins(t *testing.T) {
	r := choose(t, fixture(t), map[string]string{"mix.exs": "", "deps.txt": "db\n"}, "")
	if r.Pack == nil || r.Pack.Ref != "p@1" || !slices.Equal(r.Pack.Others, []string{"q@1"}) {
		t.Fatalf("pack = %+v", r.Pack)
	}
	db := option(t, r, "database")
	if db.Ref != "b@1" || db.Source != "repo" || db.Evidence != "deps.txt: ^db$" || !slices.Equal(db.Others, []string{"a@1"}) {
		t.Errorf("database = %+v; want b@1 (the default) with others [a@1] (c@1 doesn't work with p)", db)
	}
}

// TestChoosePackFlag: --pack picks the pack; with no default matched the
// first match by ref wins.
func TestChoosePackFlag(t *testing.T) {
	r := choose(t, fixture(t), map[string]string{"mix.exs": "", "deps.txt": "db\n"}, "q@1")
	if r.Pack == nil || r.Pack.Ref != "q@1" || !slices.Equal(r.Pack.Evidence, []string{"mix.exs"}) || !slices.Equal(r.Pack.Others, []string{"p@1"}) {
		t.Fatalf("pack = %+v", r.Pack)
	}
	db := option(t, r, "database")
	if db.Ref != "a@1" || db.Source != "repo" || !slices.Equal(db.Others, []string{"b@1", "c@1"}) {
		t.Errorf("database = %+v", db)
	}

	r = choose(t, fixture(t), map[string]string{}, "q@1")
	if r.Pack == nil || r.Pack.Ref != "q@1" || len(r.Pack.Evidence) != 0 || r.Pack.Evidence == nil {
		t.Fatalf("pack with no files = %+v", r.Pack)
	}
	if db := option(t, r, "database"); db.Ref != "none" || db.Source != "default" {
		t.Errorf("database = %+v", db)
	}

	for _, bad := range []string{"zz@1", "nope", "local:p@1"} {
		if _, err := Choose(fixture(t), nil, bad); err == nil {
			t.Errorf("Choose --pack %q: no error", bad)
		}
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFetchBareRepo: only named, regular, small files come back; the temp
// dir is gone after success and after a failed fetch.
func TestFetchBareRepo(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	runtime := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtime)

	src := t.TempDir()
	git(t, src, "init", "-q", "-b", "trunk")
	write(t, filepath.Join(src, "Gemfile"), []byte("gem 'pg'\n"))
	write(t, filepath.Join(src, "sub", "Gemfile"), []byte("nested\n"))
	write(t, filepath.Join(src, "package.json"), []byte(strings.Repeat("x", 2<<20)))
	write(t, filepath.Join(src, "README"), []byte("unrelated\n"))
	if err := os.MkdirAll(filepath.Join(src, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "config", "application.rb")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(src, "etc")); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", ".")
	git(t, src, "commit", "-q", "-m", "init")
	bare := filepath.Join(t.TempDir(), "app.git")
	git(t, src, "clone", "-q", "--bare", src, bare)
	// A template hook is the one a clone installs and would run.
	tmpl := t.TempDir()
	write(t, filepath.Join(tmpl, "hooks", "post-checkout"), []byte("#!/bin/sh\ntouch "+filepath.Join(runtime, "HOOK-RAN")+"\n"))
	if err := os.Chmod(filepath.Join(tmpl, "hooks", "post-checkout"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_TEMPLATE_DIR", tmpl)

	paths := []string{"Gemfile", "config/application.rb", "package.json", "missing.txt", "etc", "etc/passwd"}
	files, err := Fetch(context.Background(), sys.Exec{}, "file://"+bare, "trunk", paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || string(files["Gemfile"]) != "gem 'pg'\n" {
		keys := []string{}
		for k := range files {
			keys = append(keys, k)
		}
		t.Errorf("files = %v; want only Gemfile", keys)
	}
	if ents, _ := os.ReadDir(runtime); len(ents) != 0 {
		t.Errorf("%s after Fetch: %v; want empty (temp dir removed, no hook ran)", runtime, ents)
	}

	if _, err := Fetch(context.Background(), sys.Exec{}, "file://"+bare, "no-such-branch", paths); err == nil || !strings.Contains(err.Error(), "no-such-branch") {
		t.Errorf("bad branch: err = %v", err)
	}
	if ents, _ := os.ReadDir(runtime); len(ents) != 0 {
		t.Errorf("%s after a failed Fetch: %v; want empty", runtime, ents)
	}
}

func TestFetchRefusesFlags(t *testing.T) {
	for _, tc := range [][2]string{{"-uhttps://x", "main"}, {"--upload-pack=touch /tmp/x", "main"}, {"file:///x", "-b"}, {"file:///x\n", "main"}, {"", "main"}} {
		if _, err := Fetch(context.Background(), nil, tc[0], tc[1], nil); err == nil {
			t.Errorf("Fetch(%q, %q): no error", tc[0], tc[1])
		}
	}
}
