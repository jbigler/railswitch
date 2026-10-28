package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/libraries"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/template"
	"github.com/jbigler/railswitch/internal/testfixture"
)

// filialTemplate is the Filial project as a template, exported into this
// machine's store; it returns the template's directory.
func filialTemplate(t *testing.T, ws string, dirs machine.Dirs, args ...string) string {
	t.Helper()
	mustRun(t, append([]string{"-C", ws, "template", "export", "--name", "filial-stack"}, args...)...)
	return template.LocalDir(dirs, "filial-stack")
}

const editedRun = "RUN echo edited\n"

// A template keeps a Containerfile only when its body differs from its
// default, and every user addon; the option addon it doesn't keep is left
// out of [image].
func TestExportOnlyEditedContainerfiles(t *testing.T) {
	ws, dirs := lockedWS(t)
	pg := filepath.Join(ws, "containerfiles", "Containerfile.pg-client")
	writeFile(t, pg, readFile(t, pg)+editedRun)
	mustRun(t, "-C", ws, "containerfile", "new", "extra")
	mustRun(t, "-C", ws, "lock")
	dir := filialTemplate(t, ws, dirs)

	ents, err := os.ReadDir(filepath.Join(dir, "containerfiles"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if want := []string{"Containerfile.extra", "Containerfile.pg-client"}; !slices.Equal(names, want) {
		t.Errorf("containerfiles = %q; want %q (base, nvim, agent images unedited)", names, want)
	}
	if got := readFile(t, filepath.Join(dir, "containerfiles", "Containerfile.pg-client")); !strings.HasSuffix(got, editedRun) {
		t.Errorf("Containerfile.pg-client is not the edited file:\n%s", got)
	}
	stack := readFile(t, filepath.Join(dir, "stack.toml"))
	if !strings.Contains(stack, `addons = ["containerfiles/Containerfile.extra"]`) {
		t.Errorf("stack.toml [image] lacks the user addon alone:\n%s", stack)
	}
}

func TestExportVersionExists(t *testing.T) {
	ws, dirs := lockedWS(t)
	out := mustRun(t, "-C", ws, "template", "export", "--name", "filial-stack")
	if !strings.Contains(out, "wrote template filial-stack@1") || !strings.Contains(out, "dropped [project] prefix (project-only)") || !strings.Contains(out, "asks env.TZ") {
		t.Errorf("export output:\n%s", out)
	}
	dir := template.LocalDir(dirs, "filial-stack")
	before := readFile(t, filepath.Join(dir, "stack.toml"))
	code, _, errs := runIn(t, "-C", ws, "template", "export", "--name", "filial-stack", "--version", "1")
	if code != 1 || !strings.Contains(errs, "filial-stack@1 is on this machine: a new version must be higher than 1") {
		t.Errorf("a second export of version 1: exit %d: %s", code, errs)
	}
	if readFile(t, filepath.Join(dir, "stack.toml")) != before {
		t.Error("a refused export changed the template")
	}
	mustRun(t, "-C", ws, "template", "export", "--name", "filial-stack")
	if got := readFile(t, filepath.Join(dir, "stack.toml")); !strings.Contains(got, "version = 2\n") {
		t.Errorf("the next export is not version 2:\n%s", got)
	}
	to := filepath.Join(t.TempDir(), "lib", "templates", "filial-stack")
	mustRun(t, "-C", ws, "template", "export", "--name", "filial-stack", "--to", to)
	if !strings.Contains(readFile(t, filepath.Join(to, "stack.toml")), "version = 1\n") {
		t.Error("--to did not write version 1")
	}
	if code, _, errs := runIn(t, "-C", ws, "template", "export", "--name", "filial-stack", "--to", to); code != 1 || !strings.Contains(errs, "already exists") {
		t.Errorf("--to an existing dir: exit %d: %s", code, errs)
	}
}

// Export refuses a project whose lock is stale or waits for review.
func TestExportRefusesStaleLock(t *testing.T) {
	ws, _ := lockedWS(t)
	path := filepath.Join(ws, "project.toml")
	writeFile(t, path, strings.Replace(readFile(t, path), `cache    = "redis@1"`, `cache    = "none"`, 1))
	if code, _, errs := runIn(t, "-C", ws, "template", "export", "--name", "x"); code != 1 || !strings.Contains(errs, "stack.lock is out of date") {
		t.Errorf("exit %d: %s", code, errs)
	}
}

// addLibrary records the checkout alias (of acmeID) in libraries.toml.
func addLibrary(t *testing.T, dirs machine.Dirs, alias string) {
	t.Helper()
	err := libraries.Update(dirs, func(c *libraries.Config) (bool, error) {
		return true, c.Add(libraries.Library{Alias: alias, URL: "https://example.org/acme.git", ID: acmeID})
	})
	if err != nil {
		t.Fatal(err)
	}
}

type templateLsDoc struct {
	Templates []templateRow `json:"templates"`
}

func TestTemplateLsShow(t *testing.T) {
	ws, dirs := lockedWS(t)
	filialTemplate(t, ws, dirs, "--description", "Rails with everything")
	lib := testfixture.Library(t, dirs, "acme", acmeID, "options/redis")
	testfixture.WriteFile(t, filepath.Join(lib.Dir(), "templates", "rails-pg", "stack.toml"), "schema = 1\nname = \"rails-pg\"\nversion = 3\n\n[stack]\npack = \"rails@1\"\napp = \"rails\"\n\n[options]\ndatabase = \"postgres@1\"\ncache = \"acme:redis@1\"\n")
	lib.Publish(t)
	addLibrary(t, dirs, "acme")
	// A broken local template is left out, with its reason.
	testfixture.WriteFile(t, filepath.Join(template.Dir(dirs), "broken.stack", "stack.toml"), "schema = 1\nname = \"broken\"\n[project]\n")

	code, out, errs := runIn(t, "--json", "template", "ls")
	if code != 0 || strings.Contains(out, "null") {
		t.Fatalf("exit %d: %s\n%s", code, out, errs)
	}
	if !strings.Contains(errs, "template broken") {
		t.Errorf("the broken template's reason is not on stderr: %s", errs)
	}
	var ls templateLsDoc
	if err := json.Unmarshal([]byte(out), &ls); err != nil {
		t.Fatal(err)
	}
	want := []templateRow{
		{Ref: "filial-stack@1", Name: "filial-stack", Version: 1, Description: "Rails with everything", Source: "machine", Packages: []string{"rails@1", "postgres@1", "redis@1", "rustfs@1", "playwright@1", "claude@1", "local:probe@1"}},
		{Ref: "acme:rails-pg@3", Name: "rails-pg", Version: 3, Description: "", Source: "library acme", Packages: []string{"rails@1", "postgres@1", "acme:redis@1"}},
	}
	if len(ls.Templates) != len(want) {
		t.Fatalf("templates = %+v", ls.Templates)
	}
	for i := range want {
		if g, w := ls.Templates[i], want[i]; g.Ref != w.Ref || g.Name != w.Name || g.Version != w.Version || g.Description != w.Description || g.Source != w.Source || !slices.Equal(g.Packages, w.Packages) {
			t.Errorf("templates[%d] = %+v; want %+v", i, g, w)
		}
	}

	code, out, errs = runIn(t, "--json", "template", "show", "filial-stack@1")
	if code != 0 || strings.Contains(out, "null") {
		t.Fatalf("show: exit %d: %s\n%s", code, out, errs)
	}
	var show templateShowDoc
	if err := json.Unmarshal([]byte(out), &show); err != nil {
		t.Fatal(err)
	}
	if show.Ref != "filial-stack@1" || len(show.SHA256) != 64 || !strings.HasPrefix(show.StackTOML, "schema = 1\nname = \"filial-stack\"") || len(show.Ask) != 10 || !slices.Equal(show.Secrets, []string{"github_token", "probe_key", "twilio_auth_token"}) || len(show.Containerfiles) != 0 {
		t.Errorf("show = %+v", show)
	}
	if code, _, errs := runIn(t, "template", "show", "filial-stack@2"); code != 1 || !strings.Contains(errs, "declares filial-stack@1") {
		t.Errorf("show of a version not on this machine: exit %d: %s", code, errs)
	}
	if code, _, errs := runIn(t, "template", "show", "local:x@1"); code != 1 || !strings.Contains(errs, "never a local: ref") {
		t.Errorf("show local: exit %d: %s", code, errs)
	}
	if code, out, _ := runIn(t, "--json", "template", "show", "acme:rails-pg@3"); code != 0 || !strings.Contains(out, `"ref":"acme:rails-pg@3"`) {
		t.Errorf("show of a library template: exit %d: %s", code, out)
	}
}
