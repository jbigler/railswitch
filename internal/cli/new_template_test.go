package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/lockstore"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/template"
	"github.com/jbigler/railswitch/internal/testfixture"
)

const railsPGStack = `schema = 1
name = "rails-pg"
version = 1
description = "Rails with PostgreSQL"

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

[tasks]
seed = "bin/rails db:seed"

[[ask]]
key = "options.cache"
prompt = "Cache"
default = "redis@1"

[[ask]]
key = "versions.postgres"
prompt = "PostgreSQL version"
default = "16"

[[ask]]
key = "env.TZ"
prompt = "Time zone"
default = "UTC"

[[secret]]
name = "rails_master_key"
services = ["rails"]
env = "RAILS_MASTER_KEY"
`

// editedPGClient is the built-in Containerfile.pg-client under an eject header,
// with a line added: an edited Containerfile, as a template keeps it.
func editedPGClient(t *testing.T) string {
	t.Helper()
	cat, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	def, err := fs.ReadFile(cat.Options[pack.Ref{Name: "postgres", Version: 1}].Files, "Containerfile.pg-client")
	if err != nil {
		t.Fatal(err)
	}
	return "# Ejected from postgres@1 Containerfile.pg-client (sha256 " + strings.Repeat("0", 64) + ") by railswitch test. Edit freely: railswitch builds this file.\n" + string(def) + editedRun
}

// writeTemplate writes the template rails-pg into dir.
func writeTemplate(t *testing.T, dir string) {
	t.Helper()
	testfixture.WriteFile(t, filepath.Join(dir, "stack.toml"), railsPGStack)
	testfixture.WriteFile(t, filepath.Join(dir, "containerfiles", "Containerfile.pg-client"), editedPGClient(t))
}

// localTemplate is a new machine (newEnv) with rails-pg@1 in its store.
func localTemplate(t *testing.T) machine.Dirs {
	t.Helper()
	dirs := newEnv(t)
	writeTemplate(t, template.LocalDir(dirs, "rails-pg"))
	return dirs
}

const wantTemplateTOML = `schema = 1

[project]
name = "shop"
prefix = "shop"
repo = "%REPO%"
base_branch = "main"

[stack]
pack = "rails@1"
app = "rails"

[options]                  # one per slot; "none" turns a slot off; another key adds a service of that name
database = "postgres@1"
cache = "none"

[versions]                 # the options' image pins
postgres = "17"

[env]
TZ = "Asia/Bangkok"

[tasks]
seed = "bin/rails db:seed"

[template]                 # the template this project started from (D111): never applied again
ref = "rails-pg@1"
sha256 = "%SHA%"

[image]
base = "containerfiles/Containerfile.base"
addons = ["containerfiles/Containerfile.pg-client"]

[secrets.rails_master_key]
services = ["rails"]
env = "RAILS_MASTER_KEY"
`

// The dry run's project.toml is the template's lines, the answers
// applied, then the flags on top (--option beats the cache answer); its
// template block names each question's answer.
func TestNewTemplateDryRun(t *testing.T) {
	dirs := localTemplate(t)
	repo := bareRepo(t, "main")
	ws := filepath.Join(t.TempDir(), "shop")
	doc := dryRun(t, gitHost(), ws, "--repo", repo, "--prefix", "shop", "--branch", "main", "--template", "rails-pg@1",
		"--answer", "env.TZ=Asia/Bangkok", "--answer", "options.cache=redis@1", "--answer", "versions.postgres=17", "--option", "cache=none")
	_, sha, err := template.Resolve("rails-pg@1", dirs, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer("%REPO%", repo, "%SHA%", sha).Replace(wantTemplateTOML)
	if doc.ProjectTOML != want {
		t.Errorf("project_toml:\n%s\nwant:\n%s", doc.ProjectTOML, want)
	}
	tp := doc.Template
	if tp == nil || tp.Ref != "rails-pg@1" || tp.SHA256 != sha || len(tp.Ask) != 3 {
		t.Fatalf("template = %+v", tp)
	}
	if a := tp.Ask[2]; a != (dryAnswer{Key: "env.TZ", Prompt: "Time zone", Default: "UTC", Answer: "Asia/Bangkok"}) {
		t.Errorf("ask[2] = %+v", a)
	}
	if !slices.Contains(doc.Steps, "Write the template's 1 Containerfiles") {
		t.Errorf("steps = %q", doc.Steps)
	}
	if doc.Install != nil {
		t.Errorf("a local template has an install review: %+v", doc.Install)
	}
	// Without an answer, the default: an empty answer sets the value empty.
	doc = dryRun(t, gitHost(), ws, "--repo", repo, "--prefix", "shop", "--branch", "main", "--template", "rails-pg@1", "--answer", "env.TZ=")
	if !strings.Contains(doc.ProjectTOML, "cache = \"redis@1\"\n") || !strings.Contains(doc.ProjectTOML, "[env]\nTZ = \"\"\n") || doc.Template.Ask[2].Answer != "" {
		t.Errorf("defaults and an empty answer:\n%s", doc.ProjectTOML)
	}
	// No agent unless the template or --agent names one.
	if strings.Contains(doc.ProjectTOML, "[agents.") {
		t.Errorf("the default agent was added to a template's project:\n%s", doc.ProjectTOML)
	}
}

// A real run writes the dry run's project.toml, records the template and
// stores its content, and the template's Containerfile replaces the one
// the eject wrote.
func TestNewTemplateContainerfiles(t *testing.T) {
	dirs := localTemplate(t)
	repo := bareRepo(t, "main")
	ws := filepath.Join(t.TempDir(), "shop")
	args := []string{ws, "--repo", repo, "--prefix", "shop", "--branch", "main", "--template", "rails-pg@1"}
	doc := dryRun(t, gitHost(), args...)
	mustNew(t, args...)
	if got := readFile(t, filepath.Join(ws, "containerfiles", "Containerfile.pg-client")); got != editedPGClient(t) {
		t.Errorf("Containerfile.pg-client is not the template's:\n%s", got)
	}
	headerOK(t, filepath.Join(ws, "containerfiles", "Containerfile.base"))
	if got := readFile(t, filepath.Join(ws, "project.toml")); got != doc.ProjectTOML {
		t.Errorf("project.toml differs from the dry run's:\n%s\n---\n%s", got, doc.ProjectTOML)
	}
	if _, err := (lockstore.Store{Dir: lockstore.Dir(dirs)}).Get(doc.Template.SHA256); err != nil {
		t.Errorf("the template is not stored: %v", err)
	}
}

func TestNewTemplateRecorded(t *testing.T) {
	localTemplate(t)
	repo := bareRepo(t, "main")
	ws := filepath.Join(t.TempDir(), "shop")
	out := mustNew(t, ws, "--repo", repo, "--prefix", "shop", "--branch", "main", "--template", "rails-pg@1")
	if !strings.Contains(out, "next: railswitch secret set rails_master_key") {
		t.Errorf("the template's secret is not offered:\n%s", out)
	}
	code, show, errs := runIn(t, "-C", ws, "--json", "project", "show")
	if code != 0 {
		t.Fatalf("project show: %s", errs)
	}
	toml := readFile(t, filepath.Join(ws, "project.toml"))
	if !strings.Contains(toml, "[template]") || !strings.Contains(toml, `ref = "rails-pg@1"`) || show == "" {
		t.Errorf("project.toml:\n%s", toml)
	}
}

// An answer the template doesn't ask, an option answer the pack can't
// use, and --answer without --template are refused before any clone.
func TestNewTemplateBadAnswer(t *testing.T) {
	localTemplate(t)
	repo := bareRepo(t, "main")
	ws := filepath.Join(t.TempDir(), "shop")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--template", "rails-pg@1", "--answer", "versions.redis=7"}, "template rails-pg@1 asks no such question (it asks: options.cache, versions.postgres, env.TZ)"},
		{[]string{"--template", "rails-pg@1", "--answer", "options.cache=nosuch@1"}, "nosuch@1"},
		{[]string{"--template", "rails-pg@1", "--answer", "options.cache=postgres"}, "options.cache"},
		{[]string{"--template", "rails-pg@1", "--answer", "env.TZ=x", "--answer", "env.TZ=y"}, "given twice"},
		{[]string{"--answer", "env.TZ=x"}, "--answer answers a --template question"},
		{[]string{"--template", "nosuch@1"}, "no template nosuch@1 on this machine"},
	} {
		f := gitHost()
		code, _, errs := runWith(t, f, append([]string{"project", "new", ws, "--repo", repo, "--prefix", "shop", "--branch", "main"}, tc.args...)...)
		if code == 0 || !strings.Contains(errs, tc.want) {
			t.Errorf("%q: exit %d: %s; want %q", tc.args, code, errs, tc.want)
		}
		for _, c := range f.Calls {
			if slices.Contains(c, "clone") {
				t.Errorf("%q: cloned before refusing", tc.args)
			}
		}
		if exists(ws) {
			t.Fatalf("%q: left %s", tc.args, ws)
		}
	}
}

// A library template is part of the install review: its stack.toml and
// Containerfiles shown and flagged, created only with --accept-install.
func TestNewLibraryTemplateNeedsReview(t *testing.T) {
	dirs := newEnv(t)
	lib := testfixture.Library(t, dirs, "acme", acmeID, "options/redis")
	writeTemplate(t, filepath.Join(lib.Dir(), "templates", "rails-pg"))
	lib.Publish(t)
	repo := bareRepo(t, "main")
	ws := filepath.Join(t.TempDir(), "shop")
	f := gitHost()
	f.Passthrough = append(f.Passthrough, "ssh-keygen")
	args := []string{ws, "--repo", repo, "--prefix", "shop", "--branch", "main", "--template", "acme:rails-pg@1"}
	doc := dryRun(t, f, args...)
	in := doc.Install
	if in == nil || len(in.Packages) != 1 || in.Packages[0].Kind != "template" || in.Packages[0].Ref != "acme:rails-pg@1" || in.Packages[0].Status != "added" {
		t.Fatalf("install = %+v", in)
	}
	var paths []string
	for _, file := range in.Packages[0].Files {
		paths = append(paths, file.Path)
		if file.Path == "containerfiles/Containerfile.pg-client" && len(file.Flags) == 0 {
			t.Error("the template's Containerfile is not flagged (its FROM and RUN lines)")
		}
	}
	if !slices.Equal(paths, []string{"containerfiles/Containerfile.pg-client", "stack.toml"}) {
		t.Errorf("reviewed files = %q", paths)
	}
	code, _, errs := runWith(t, f, append([]string{"project", "new"}, args...)...)
	if code != 1 || !strings.Contains(errs, "installing template acme:rails-pg@1 needs review") {
		t.Fatalf("without --accept-install: exit %d: %s", code, errs)
	}
	if exists(ws) {
		t.Fatal("a refused template left the workspace")
	}
	// Changed after the review: the hash covers the template.
	testfixture.WriteFile(t, filepath.Join(lib.Dir(), "templates", "rails-pg", "README.md"), "changed\n")
	lib.Publish(t)
	code, _, errs = runWith(t, f, append([]string{"project", "new", "--accept-install", in.Hash}, args...)...)
	if code != 1 || !strings.Contains(errs, "the install changed since it was reviewed") {
		t.Fatalf("a changed template: exit %d: %s", code, errs)
	}
	in = dryRun(t, f, args...).Install
	if code, out, errs := runWith(t, f, append([]string{"project", "new", "--accept-install", in.Hash}, args...)...); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if got := readFile(t, filepath.Join(ws, "containerfiles", "Containerfile.pg-client")); got != editedPGClient(t) {
		t.Error("the library template's Containerfile is not in place")
	}
	if toml := readFile(t, filepath.Join(ws, "project.toml")); !strings.Contains(toml, `ref = "acme:rails-pg@1"`) {
		t.Errorf("project.toml:\n%s", toml)
	}
	if _, err := os.Stat(filepath.Join(ws, "stack.lock")); err != nil {
		t.Error(err)
	}
}
