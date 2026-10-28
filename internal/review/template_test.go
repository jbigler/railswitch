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
