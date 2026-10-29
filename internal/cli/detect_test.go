package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/detect"
)

// railsRepo is a file:// URL of a bare repo whose HEAD is trunk, holding a
// Rails-like Gemfile and config/application.rb.
func railsRepo(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	gitIn(t, src, "init", "-q", "-b", "trunk")
	if err := os.MkdirAll(filepath.Join(src, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "Gemfile"), "gem \"pg\"\n# gem \"redis\"\n")
	writeFile(t, filepath.Join(src, "config", "application.rb"), "module App; end\n")
	gitIn(t, src, "add", ".")
	gitIn(t, src, "commit", "-q", "-m", "init")
	gitIn(t, src, "branch", "other")
	bare := filepath.Join(t.TempDir(), "app.git")
	gitIn(t, src, "clone", "-q", "--bare", src, bare)
	return "file://" + bare
}

func TestProjectDetectCLI(t *testing.T) {
	newEnv(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	repo := railsRepo(t)

	code, out, errs := runWith(t, gitHost(), "--json", "project", "detect", repo)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var res detect.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if res.Branch != "trunk" || res.Pack == nil || res.Pack.Ref != "rails@1" {
		t.Fatalf("result = %s", out)
	}
	for _, o := range res.Options {
		switch o.Key {
		case "database":
			if o.Ref != "postgres@1" || o.Source != "repo" || o.Evidence != "Gemfile: pg" {
				t.Errorf("database = %+v", o)
			}
		case "cache":
			if o.Source != "default" {
				t.Errorf("cache = %+v; a commented gem is no evidence", o)
			}
		}
	}
	for _, field := range []string{`"others":[]`, `"evidence":["Gemfile","config/application.rb"]`} {
		if !strings.Contains(out, field) {
			t.Errorf("JSON lacks %s: %s", field, out)
		}
	}

	code, out, errs = runWith(t, gitHost(), "--json", "project", "detect", repo, "--branch", "other")
	if code != 0 || !strings.Contains(out, `"branch":"other"`) {
		t.Errorf("--branch other: exit %d, %s %s", code, out, errs)
	}

	code, out, errs = runWith(t, gitHost(), "project", "detect", repo)
	if code != 0 || !strings.Contains(out, "rails@1") || !strings.Contains(out, "postgres@1") || !strings.Contains(out, "Gemfile: pg") {
		t.Errorf("text: exit %d\n%s%s", code, out, errs)
	}

	code, _, errs = runWith(t, gitHost(), "--json", "project", "detect", repo, "--branch", "nope")
	if code != 1 || !strings.Contains(errs, "nope") {
		t.Errorf("bad branch: exit %d, %s", code, errs)
	}
	if ents, _ := os.ReadDir(os.Getenv("XDG_RUNTIME_DIR")); len(ents) != 0 {
		t.Errorf("temp dirs left: %v", ents)
	}
}

// TestProjectDetectRefusals: a repo that would read as a flag and an
// unknown --pack fail before git runs.
func TestProjectDetectRefusals(t *testing.T) {
	newEnv(t)
	for _, args := range [][]string{
		{"project", "detect", "--", "--upload-pack=touch x"},
		{"project", "detect", "-x"},
		{"project", "detect", "file:///nowhere", "--pack", "zz@1"},
		{"project", "detect"},
	} {
		f := fakeHost()
		code, _, errs := runWith(t, f, args...)
		if code == 0 {
			t.Errorf("%v: exit 0", args)
		}
		for _, c := range f.Calls {
			if c[0] == "git" {
				t.Errorf("%v ran %v (%s)", args, c, errs)
			}
		}
	}
}

// project new with neither --pack nor --template reads the repository as
// the New project page does (D124): the detected pack and options, an
// --option beating the detected one, what it found on stderr with --json.
func TestNewDetects(t *testing.T) {
	newEnv(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	repo := railsRepo(t)
	ws := filepath.Join(t.TempDir(), "shop")
	code, out, errs := runWith(t, gitHost(), "--json", "project", "new", "--dry-run", ws, "--repo", repo, "--prefix", "shop", "--agent", "none")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var doc newDryRunDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !strings.Contains(doc.ProjectTOML, `database = "postgres@1"`) || !strings.Contains(doc.ProjectTOML, `base_branch = "trunk"`) {
		t.Errorf("project.toml:\n%s", doc.ProjectTOML)
	}
	if !strings.Contains(errs, "detected pack rails@1") || !strings.Contains(errs, "detected database postgres@1 (Gemfile: pg)") {
		t.Errorf("stderr: %s", errs)
	}
	code, out, errs = runWith(t, gitHost(), "--json", "project", "new", "--dry-run", ws, "--repo", repo, "--prefix", "shop", "--agent", "none", "--option", "database=mariadb@1")
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || !strings.Contains(doc.ProjectTOML, `database = "mariadb@1"`) || strings.Contains(errs, "detected database") {
		t.Errorf("--option over detection: exit %d %s\n%s", code, errs, doc.ProjectTOML)
	}
}

// A repository no pack matches is refused, naming --pack.
func TestNewDetectNoPack(t *testing.T) {
	newEnv(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	repo := bareRepo(t, "main")
	code, _, errs := runWith(t, gitHost(), "project", "new", filepath.Join(t.TempDir(), "x"), "--repo", repo, "--prefix", "xx", "--dry-run")
	if code != 1 || !strings.Contains(errs, "pass --pack") {
		t.Errorf("exit %d: %s", code, errs)
	}
}
