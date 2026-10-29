package template

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
)

const filialProject = "../../testdata/fixtures/filial/project.toml"

const minimalStack = `schema = 1
name = "rails-pg"
version = 1

[stack]
pack = "rails@1"
app = "rails"

[options]
database = "postgres@1"
cache = "redis@1"

[versions]
postgres = "16"

[env]
TZ = "UTC"

[[ask]]
key = "options.cache"
prompt = "Cache"
default = "redis@1"

[[ask]]
key = "env.TZ"
prompt = "Time zone"
default = "UTC"

[[secret]]
name = "rails_master_key"
services = ["rails"]
env = "RAILS_MASTER_KEY"
`

func stackFS(stack string, extra ...string) fstest.MapFS {
	m := fstest.MapFS{StackFile: {Data: []byte(stack)}}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i]] = &fstest.MapFile{Data: []byte(extra[i+1])}
	}
	return m
}

func filial(t *testing.T) *config.Project {
	t.Helper()
	p, err := config.Load(filialProject)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDecodeStrict(t *testing.T) {
	tp, err := Decode(stackFS(minimalStack, "README.md", "hi\n", "containerfiles/Containerfile.pg-client", "FROM x\n"))
	if err != nil {
		t.Fatal(err)
	}
	if tp.Ref() != "rails-pg@1" || len(tp.Ask) != 2 || !slices.Equal(tp.Secrets, []string{"rails_master_key"}) || string(tp.Readme) != "hi\n" || string(tp.Containerfiles["pg-client"]) != "FROM x\n" {
		t.Errorf("decoded %+v", tp)
	}
	if s := tp.Lines.Secrets["rails_master_key"]; s.Env != "RAILS_MASTER_KEY" {
		t.Errorf("secret declaration = %+v", s)
	}
	for _, tc := range []struct{ name, stack, want string }{
		{"project table", minimalStack + "\n[project]\nname = \"x\"\n", "unknown key project"},
		{"worktrees", minimalStack + "\n[worktrees.feat.versions]\npostgres = \"17\"\n", "unknown key worktrees"},
		{"secrets table", minimalStack + "\n[secrets.x]\nservices = [\"rails\"]\n", "unknown key secrets"},
		{"share", minimalStack + "\n[share.env]\nX = \"1\"\n", "unknown key share"},
		{"secret value", strings.Replace(minimalStack, `name = "rails_master_key"`, "name = \"rails_master_key\"\nvalue = \"hunter2\"", 1), "unknown key secret.value"},
		{"newer schema", strings.Replace(minimalStack, "schema = 1", "schema = 2", 1), "newer railswitch"},
		{"no schema", strings.Replace(minimalStack, "schema = 1\n", "", 1), "schema: missing"},
		{"bad name", strings.Replace(minimalStack, `"rails-pg"`, `"Rails"`, 1), "name:"},
		{"no version", strings.Replace(minimalStack, "version = 1\n", "", 1), "version:"},
		{"bad ask key", strings.Replace(minimalStack, `key = "env.TZ"`, `key = "project.name"`, 1), "is not options.<key>"},
		{"ask twice", strings.Replace(minimalStack, `key = "env.TZ"`, `key = "options.cache"`, 1), "asked twice"},
		{"bad default", strings.Replace(minimalStack, `default = "redis@1"`, `default = "redis"`, 1), "options.cache"},
		{"addon missing", minimalStack + "\n[image]\naddons = [\"containerfiles/Containerfile.mine\"]\n", "no file of the template"},
	} {
		if _, err := Decode(stackFS(tc.stack)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v; want %q", tc.name, err, tc.want)
		}
	}
	for _, path := range []string{"notes.txt", "containerfiles/Dockerfile", "containerfiles/Containerfile.A", "packs/x/pack.toml", ".hidden"} {
		if _, err := Decode(stackFS(minimalStack, path, "x")); err == nil {
			t.Errorf("%s: decoded", path)
		}
	}
	if _, err := Decode(fstest.MapFS{"README.md": {Data: []byte("x")}}); err == nil || !strings.Contains(err.Error(), "no stack.toml") {
		t.Errorf("no stack.toml: %v", err)
	}
}

func TestTemplateRefusesSymlink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x.stack")
	if err := os.MkdirAll(filepath.Join(dir, "containerfiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, StackFile), []byte(minimalStack), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDir(dir); err != nil {
		t.Fatalf("a plain template: %v", err)
	}
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("FROM x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "containerfiles", "Containerfile.pg-client")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDir(dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a symlinked Containerfile: %v", err)
	}
	link := filepath.Join(t.TempDir(), "y.stack")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDir(link); err == nil {
		t.Error("a symlinked template directory was read")
	}
}

func TestExportDropsProjectLines(t *testing.T) {
	p := filial(t)
	tp, dropped, err := Export(p, map[string][]byte{}, nil, "filial-stack", 1)
	if err != nil {
		t.Fatal(err)
	}
	stack := string(tp.Encode())
	for _, s := range []string{"[project]", "prefix", "base_branch", "git@github.com", "[worktrees", "postgres-trial", "Filial Rails", "from =", "[secrets.", "[tools", "[agents", "nvim", "agent-"} {
		if strings.Contains(stack, s) {
			t.Errorf("stack.toml holds %q:\n%s", s, stack)
		}
	}
	want := []string{"from (project-only)", "[project] name (project-only)", "[project] prefix (project-only)", "[project] repo (project-only)",
		"[project] base_branch (project-only)", "[worktrees.feat] (project-only)", "[worktrees.postgres-trial] (project-only)",
		"[tools.nvim] (each person's own)", "[agents.claude] (each person's own)", "[agents.probe] (each person's own)",
		"[secrets.github_token] (only the editor or agents receive it)", "[secrets.probe_key] (only the editor or agents receive it)"}
	if !slices.Equal(dropped, want) {
		t.Errorf("dropped = %q; want %q", dropped, want)
	}
	for _, s := range []string{"[services.rails.env]\nHEADLESS_SYSTEM_TESTS = \"0\"", "allow.deps = [\"github.com\"]", "[[links]]\nlabel = \"Admin\""} {
		if !strings.Contains(stack, s) {
			t.Errorf("stack.toml lacks %q:\n%s", s, stack)
		}
	}
	if strings.Contains(stack, "[image]") {
		t.Errorf("an option's addon is in [image] with no template file:\n%s", stack)
	}
}

// The editor and the agents are each person's choice (D112): a secret
// they share with the app keeps only the app, and their overrides and
// allow.agent are dropped.
func TestExportDropsPersonal(t *testing.T) {
	p := filial(t)
	p.Secrets = map[string]config.Secret{"shared": {Services: []string{"rails", "agent-claude", "nvim"}}}
	p.Services = map[string]config.ServiceOverride{"agent-claude": {Memory: "2g"}, "rails": {Memory: "1g"}}
	p.Firewall.Allow = map[string][]string{"agent": {"api.example"}, "deps": {"github.com"}}
	tp, dropped, err := Export(p, nil, nil, "filial-stack", 1)
	if err != nil {
		t.Fatal(err)
	}
	if s := tp.Lines.Secrets["shared"]; !slices.Equal(s.Services, []string{"rails"}) {
		t.Errorf("shared = %+v", s)
	}
	if _, ok := tp.Lines.Services["agent-claude"]; ok || tp.Lines.Services["rails"].Memory != "1g" {
		t.Errorf("services = %+v", tp.Lines.Services)
	}
	if _, ok := tp.Lines.Firewall.Allow["agent"]; ok || len(tp.Lines.Firewall.Allow["deps"]) != 1 {
		t.Errorf("allow = %+v", tp.Lines.Firewall.Allow)
	}
	for _, d := range []string{"[firewall] allow.agent (each person's own)", "[services.agent-claude] (each person's own)"} {
		if !slices.Contains(dropped, d) {
			t.Errorf("dropped %q lacks %q", dropped, d)
		}
	}
	if p.Firewall.Allow["agent"] == nil || p.Services["agent-claude"].Memory == "" || len(p.Secrets["shared"].Services) != 3 {
		t.Error("Export changed the project it read")
	}
}

// A stack.toml with [tools] or [agents] is refused by name.
func TestDecodeRefusesPersonal(t *testing.T) {
	for _, extra := range []string{"[tools.nvim]\nconfig_repo = \"https://h.example/c\"\n", "[agents.claude]\nuse = \"claude@1\"\n"} {
		_, err := Decode(fstest.MapFS{StackFile: {Data: []byte("schema = 1\nname = \"x\"\nversion = 1\n[stack]\npack = \"rails@1\"\napp = \"rails\"\n" + extra)}})
		if err == nil || !strings.Contains(err.Error(), "each person's choice") {
			t.Errorf("%q: %v", extra, err)
		}
	}
}

func TestExportQuestions(t *testing.T) {
	tp, _, err := Export(filial(t), nil, nil, "filial-stack", 1)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, a := range tp.Ask {
		keys = append(keys, a.Key+"="+a.Default)
	}
	want := []string{
		"options.database=postgres@1", "options.cache=redis@1", "options.storage=rustfs@1", "options.jobs=none", "options.mail=none", "options.browser=playwright@1",
		"versions.postgres=16", "versions.redis=8", "versions.rustfs=1.0.0-alpha.83",
		"env.TZ=Asia/Bangkok",
	}
	if !slices.Equal(keys, want) {
		t.Errorf("questions = %q; want %q", keys, want)
	}
	if !slices.Equal(tp.Secrets, []string{"twilio_auth_token"}) {
		t.Errorf("secrets = %q", tp.Secrets)
	}
	if s := tp.Lines.Secrets["twilio_auth_token"]; s.Env != "TWILIO_AUTH_TOKEN" || !slices.Equal(s.Services, []string{"rails"}) {
		t.Errorf("twilio declaration = %+v", s)
	}
	// Round trip: what Write writes decodes to the same stack.toml.
	dir := filepath.Join(t.TempDir(), "filial-stack.stack")
	if err := tp.Write(dir); err != nil {
		t.Fatal(err)
	}
	files, err := ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(files)
	if err != nil {
		t.Fatal(err)
	}
	if string(back.Encode()) != string(tp.Encode()) {
		t.Errorf("round trip changed stack.toml:\n%s\n---\n%s", tp.Encode(), back.Encode())
	}
}

func TestApplyAnswers(t *testing.T) {
	tp, err := Decode(stackFS(minimalStack))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Apply(tp, map[string]string{"env.TZ": "Asia/Bangkok", "options.cache": "none"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Env["TZ"] != "Asia/Bangkok" || p.Options["cache"] != "none" || p.Options["database"] != "postgres@1" || p.Versions["postgres"] != "16" {
		t.Errorf("applied = %+v", p)
	}
	if tp.Lines.Env["TZ"] != "UTC" || tp.Lines.Options["cache"] != "redis@1" {
		t.Error("Apply changed the template's lines")
	}
	if _, err := Apply(tp, map[string]string{"versions.redis": "7"}); err == nil || !strings.Contains(err.Error(), "asks no such question") {
		t.Errorf("an unasked key: %v", err)
	}
}

// A route to the editor is dropped with it.
func TestExportDropsPersonalRoute(t *testing.T) {
	p := filial(t)
	p.Routes = []config.Route{{Name: "lsp", Service: "nvim", Port: 9000}, {Name: "vite", Service: "rails", Port: 3036}}
	tp, dropped, err := Export(p, nil, nil, "filial-stack", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tp.Lines.Routes) != 1 || tp.Lines.Routes[0].Name != "vite" || !slices.Contains(dropped, "[[routes]] lsp (each person's own)") {
		t.Errorf("routes = %+v, dropped %q", tp.Lines.Routes, dropped)
	}
}

// A bundled package must be one the lines name, at the version its
// manifest declares; a local: ref the lines name must be bundled; only
// package files are allowed in it.
func TestDecodeBundled(t *testing.T) {
	stack := "schema = 1\nname = \"x\"\nversion = 1\n[stack]\npack = \"local:rails@1\"\napp = \"rails\"\n"
	manifest := "name = \"rails\"\nversion = 1\n"
	cases := map[string]fstest.MapFS{
		"missing":  {StackFile: {Data: []byte(stack)}},
		"unnamed":  {StackFile: {Data: []byte(stack)}, "packs/rails/pack.toml": {Data: []byte(manifest)}, "options/kv/option.toml": {Data: []byte("name = \"kv\"\nversion = 1\n")}},
		"version":  {StackFile: {Data: []byte(stack)}, "packs/rails/pack.toml": {Data: []byte("name = \"rails\"\nversion = 2\n")}},
		"bad file": {StackFile: {Data: []byte(stack)}, "packs/rails/pack.toml": {Data: []byte(manifest)}, "packs/rails/run.exe": {Data: []byte("x")}},
		"deep dir": {StackFile: {Data: []byte(stack)}, "packs/rails/pack.toml": {Data: []byte(manifest)}, "packs/rails/a/b/c.sh": {Data: []byte("x")}},
	}
	for name, files := range cases {
		if _, err := Decode(files); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
	tp, err := Decode(fstest.MapFS{StackFile: {Data: []byte(stack)}, "packs/rails/pack.toml": {Data: []byte(manifest)}, "packs/rails/entrypoints/e.sh": {Data: []byte("#!/bin/sh\n")}})
	if err != nil {
		t.Fatal(err)
	}
	b, ok := tp.Bundled[pack.Ref{Local: true, Name: "rails", Version: 1}]
	if !ok || b.Kind != pack.KindPack || b.Dir != "packs/rails" {
		t.Fatalf("bundled = %+v", tp.Bundled)
	}
	if _, err := fs.ReadFile(b.Files, "entrypoints/e.sh"); err != nil {
		t.Error(err)
	}
}

// The bundles are checked against the lines with the questions' defaults
// applied: a default naming a local option needs it bundled, and that
// bundle is accepted.
func TestDecodeBundledDefault(t *testing.T) {
	stack := "schema = 1\nname = \"x\"\nversion = 1\n[stack]\npack = \"rails@1\"\napp = \"rails\"\n[options]\ncache = \"redis@1\"\n[[ask]]\nkey = \"options.cache\"\nprompt = \"Cache\"\ndefault = \"local:kv@1\"\n"
	if _, err := Decode(fstest.MapFS{StackFile: {Data: []byte(stack)}}); err == nil || !strings.Contains(err.Error(), "no options/kv/") {
		t.Errorf("unbundled default: %v", err)
	}
	tp, err := Decode(fstest.MapFS{StackFile: {Data: []byte(stack)}, "options/kv/option.toml": {Data: []byte("name = \"kv\"\nversion = 1\n")}})
	if err != nil || len(tp.Bundled) != 1 {
		t.Errorf("bundled default: %v %+v", err, tp)
	}
}

// TestHooksCarried: a template exports and decodes [hooks] (D126).
func TestHooksCarried(t *testing.T) {
	p := filial(t)
	p.Hooks.Start = []string{"bin/rails db:test:prepare"}
	tp, _, err := Export(p, map[string][]byte{}, nil, "filial-stack", 1)
	if err != nil {
		t.Fatal(err)
	}
	stack := string(tp.Encode())
	if !strings.Contains(stack, "[hooks]") {
		t.Fatalf("no [hooks]:\n%s", stack)
	}
	got, err := Decode(stackFS(stack))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Lines.Hooks.Start, p.Hooks.Start) {
		t.Errorf("decoded hooks %+v", got.Lines.Hooks)
	}
}
