package review

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// A library template's stack.toml is checked like a package manifest: the
// lines that widen what a new project reaches or hands on are flagged.
func TestTemplateStackFlags(t *testing.T) {
	stack := `schema = 1
name = "x"
version = 1
[stack]
pack = "rails@1"
[firewall]
mode = "off"
[[secret]]
name = "github_token"
scope = "machine"
git_host = "evil.example"
services = ["rails"]
[[routes]]
name = "admin"
service = "rails"
port = 3000
[tasks]
seed = "bin/rails db:seed"
[hooks]
deps = ["gem install retest"]
`
	files, err := Compare("template", nil, fstest.MapFS{"stack.toml": {Data: []byte(stack)}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		for _, fl := range f.Flags {
			got = append(got, fl.Check+" "+fl.Text)
		}
	}
	for _, want := range []string{
		"firewall firewall.mode: (none) -> off",
		"privilege secret.github_token.scope: (none) -> machine",
		"privilege secret.github_token.git_host: (none) -> evil.example",
		"privilege secret.github_token.services: (none) -> rails",
		"port routes.admin.port: (none) -> 3000",
		"runs-here hooks.deps: (none) -> \"gem install retest\"",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing flag %q in\n%s", want, strings.Join(got, "\n"))
		}
	}
	if slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, "tasks.") }) {
		t.Errorf("tasks flagged (they run in the firewalled app container): %v", got)
	}

	// Reordered tables and lists add no flag.
	reordered := strings.Replace(stack, `allow = ["evil.example"]`, `allow = [ "evil.example" ]`, 1)
	files, err = Compare("template", fstest.MapFS{"stack.toml": {Data: []byte(stack)}}, fstest.MapFS{"stack.toml": {Data: []byte(reordered)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if len(f.Flags) > 0 {
			t.Errorf("a reformatted stack.toml flags %v", f.Flags)
		}
	}
}

// A template's bundled package is checked as the package it is: its
// manifest's image is flagged, its paths shown under its directory.
func TestTemplateBundledFlags(t *testing.T) {
	stack := "schema = 1\nname = \"x\"\nversion = 1\n"
	files, err := Compare("template", nil, fstest.MapFS{
		"stack.toml":             {Data: []byte(stack)},
		"options/kv/option.toml": {Data: []byte("name = \"kv\"\nversion = 1\nslot = \"cache\"\n[image]\npull = \"docker.io/evil/kv\"\n")},
		"options/kv/deps.allow":  {Data: []byte("evil.example\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		for _, fl := range f.Flags {
			got = append(got, f.Path+" "+fl.Check)
		}
	}
	for _, want := range []string{"options/kv/option.toml image", "options/kv/deps.allow firewall"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	for _, f := range files {
		if f.Path == "options/kv/option.toml" && !strings.Contains(f.Diff, "+++ b/options/kv/option.toml") {
			t.Errorf("diff header:\n%s", f.Diff)
		}
	}
}

// TestTemplateHookFlags: hooks are compared in order and command by
// command; a split, a reorder and a removal are each flagged.
func TestTemplateHookFlags(t *testing.T) {
	stack := func(hooks string) fstest.MapFS {
		return fstest.MapFS{"stack.toml": {Data: []byte("schema = 1\nname = \"x\"\nversion = 1\n[stack]\npack = \"rails@1\"\n" + hooks)}}
	}
	flags := func(old, new string) []string {
		files, err := Compare("template", stack(old), stack(new))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, f := range files {
			for _, fl := range f.Flags {
				got = append(got, fl.Check+" "+fl.Text)
			}
		}
		return got
	}
	for _, c := range []struct{ old, new, want string }{
		{"[hooks]\nstart = [\"a, b\"]\n", "[hooks]\nstart = [\"a\", \"b\"]\n", `runs-here hooks.start: "a, b" -> "a" "b"`},
		{"[hooks]\nstart = [\"a\", \"b\"]\n", "[hooks]\nstart = [\"b\", \"a\"]\n", `runs-here hooks.start: "a" "b" -> "b" "a"`},
		{"[hooks]\ncreate = [\"a\"]\n", "", `runs-here hooks.create: "a" -> (none)`},
	} {
		if got := flags(c.old, c.new); !slices.Contains(got, c.want) {
			t.Errorf("%q -> %q: flags %q, want %q", c.old, c.new, got, c.want)
		}
	}
	if got := flags("[hooks]\nstart = [\"a\"]\n", "[hooks]\nstart = [ \"a\" ]\n"); len(got) != 0 {
		t.Errorf("reformatted hooks flagged: %q", got)
	}
}
