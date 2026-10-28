package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
)

// D70: pack eject copies a built-in pack whole; switching the stack to the
// local copy locks it as local and renders.
func TestPackEjectRails(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	out := mustRun(t, "-C", ws, "pack", "eject", "rails@1")
	if !strings.Contains(out, `[stack] pack = "local:rails@1"`) {
		t.Errorf("stdout %q does not say how to use it", out)
	}
	cat, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	builtin := cat.Packs[pack.Ref{Name: "rails", Version: 1}].Files
	n := 0
	if err := fs.WalkDir(builtin, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		want, err := fs.ReadFile(builtin, p)
		if err != nil {
			return err
		}
		if got := readFile(t, filepath.Join(ws, "packs", "rails", p)); got != string(want) {
			t.Errorf("packs/rails/%s differs from the built-in", p)
		}
		return nil
	}); err != nil || n < 4 {
		t.Fatalf("walk: %v (%d files)", err, n)
	}
	if fi, err := os.Stat(filepath.Join(ws, "packs", "rails", "entrypoints", "entrypoint-app.sh")); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("entrypoint not executable: %v %v", fi, err)
	}

	editProject(t, ws, `pack = "rails@1"`, `pack = "local:rails@1"`)
	mustRun(t, "-C", ws, "lock")
	mustRun(t, "-C", ws, "render")
	l, err := config.ReadLock(filepath.Join(ws, "stack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if l.Packs["rails"].Source != "local" {
		t.Errorf("lock pack rails = %+v", l.Packs["rails"])
	}
}

func TestPackEjectAs(t *testing.T) {
	ws, _ := filialWS(t)
	out := mustRun(t, "-C", ws, "pack", "eject", "postgres@1", "--as", "pg")
	if !strings.Contains(out, `[options] database = "local:pg@1"`) {
		t.Errorf("stdout %q", out)
	}
	// Only the top-level name changes; [pin] name = "postgres" stays.
	if got := readFile(t, filepath.Join(ws, "options", "pg", "option.toml")); !strings.HasPrefix(got, "name = \"pg\"\n") || strings.Count(got, `name = "postgres"`) != 1 {
		t.Errorf("option.toml:\n%.400s", got)
	}
	editProject(t, ws, `database = "postgres@1"`, `database = "local:pg@1"`)
	mustRun(t, "-C", ws, "lock")
	if l, err := config.ReadLock(filepath.Join(ws, "stack.lock")); err != nil || l.Options["pg"].Source != "local" {
		t.Errorf("lock option pg = %+v, %v", l.Options["pg"], err)
	}
}

// C5: eject never overwrites a local package, and copies built-ins only.
func TestPackEjectRefusesExisting(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "pack", "eject", "rails@1")
	code, _, errs := runIn(t, "-C", ws, "pack", "eject", "rails@1")
	if code != 1 || !strings.Contains(errs, filepath.Join(ws, "packs", "rails")+" already exists") {
		t.Errorf("second eject: exit %d, stderr %q", code, errs)
	}
	for args, want := range map[string]string{
		"local:rails@1":       "local:rails@1 is already local",
		"nope@1":              "no built-in pack or option nope@1",
		"rails@1 --as Rails!": `--as "Rails!" must match`,
	} {
		code, _, errs := runIn(t, append([]string{"-C", ws, "pack", "eject"}, strings.Fields(args)...)...)
		if code == 0 || !strings.Contains(errs, want) {
			t.Errorf("eject %s: exit %d, stderr %q; want %q", args, code, errs, want)
		}
	}
}

// Review Focus 3: a fresh skeleton breaks nothing until a ref names it.
func TestPackNewThenRender(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	mustRun(t, "-C", ws, "pack", "new", "web")
	for _, f := range []string{"pack.toml", "Containerfile.base", "entrypoints/entrypoint-app.sh", "README.md"} {
		if _, err := os.Stat(filepath.Join(ws, "packs", "web", f)); err != nil {
			t.Errorf("pack new: %v", err)
		}
	}
	mustRun(t, "-C", ws, "render")
	code, _, errs := runIn(t, "-C", ws, "pack", "show", "local:web@1")
	if code != 1 || !strings.Contains(errs, "packs/web/pack.toml") {
		t.Errorf("show skeleton: exit %d, stderr %q", code, errs)
	}
	if code, _, errs := runIn(t, "-C", ws, "pack", "new", "web"); code != 1 || !strings.Contains(errs, "already exists") {
		t.Errorf("second new: exit %d, stderr %q", code, errs)
	}
}

// A new project's pack is written before project new creates its
// workspace, which then reads it as local:<name>@1.
func TestPackNewDirBeforeProject(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ws")
	out := mustRun(t, "pack", "new", "--dir", dir, "web")
	if !strings.Contains(out, "project new "+dir+" --pack local:web@1") {
		t.Errorf("stdout %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "packs", "web", "pack.toml")); err != nil {
		t.Fatal(err)
	}
	ws, _ := filialWS(t)
	if code, _, errs := runIn(t, "pack", "new", "--dir", ws, "web"); code != 2 || !strings.Contains(errs, "already a workspace") {
		t.Errorf("--dir on a workspace: exit %d, stderr %q", code, errs)
	}
}

type packShowDoc struct {
	Ref      string `json:"ref"`
	Kind     string `json:"kind"`
	Source   string `json:"source"`
	Runtimes []struct {
		Name     string   `json:"name"`
		From     []string `json:"from"`
		Fallback string   `json:"fallback"`
	} `json:"runtimes"`
	Pin *struct {
		Name    string `json:"name"`
		Default string `json:"default"`
	} `json:"pin"`
	Readme string `json:"readme"`
}

func TestPackShowJSON(t *testing.T) {
	newEnv(t)
	var doc packShowDoc
	if err := json.Unmarshal([]byte(mustRun(t, "-C", t.TempDir(), "--json", "pack", "show", "rails@1")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Ref != "rails@1" || doc.Kind != "pack" || doc.Source != "built in" {
		t.Errorf("doc = %+v", doc)
	}
	found := false
	for _, r := range doc.Runtimes {
		if r.Name == "ruby" {
			found = len(r.From) > 0 && r.From[0] == ".ruby-version"
		}
	}
	if !found {
		t.Errorf("runtimes = %+v; want ruby from .ruby-version first", doc.Runtimes)
	}
	doc = packShowDoc{}
	if err := json.Unmarshal([]byte(mustRun(t, "-C", t.TempDir(), "--json", "pack", "show", "playwright@1")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Kind != "option" || doc.Pin == nil || doc.Pin.Name != "playwright" || doc.Pin.Default != "1.60.0" {
		t.Errorf("playwright doc = %+v pin %+v", doc, doc.Pin)
	}
}

func TestPackShowReadme(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "pack", "eject", "rails@1", "--as", "mine")
	writeFile(t, filepath.Join(ws, "packs", "mine", "README.md"), "# mine\n\nChange config/x.rb.\n")
	out := mustRun(t, "-C", ws, "pack", "show", "local:mine@1")
	if !strings.Contains(out, "local "+filepath.Join(ws, "packs", "mine")) || !strings.HasSuffix(out, "# mine\n\nChange config/x.rb.\n") {
		t.Errorf("show:\n%s", out)
	}
}

// docs/json.md: pin's keys are name and default (case-sensitive).
func TestPackShowJSONPinKeys(t *testing.T) {
	newEnv(t)
	var doc map[string]any
	if err := json.Unmarshal([]byte(mustRun(t, "-C", t.TempDir(), "--json", "pack", "show", "postgres@1")), &doc); err != nil {
		t.Fatal(err)
	}
	pin, ok := doc["pin"].(map[string]any)
	if !ok || pin["name"] != "postgres" || pin["default"] != "16" {
		t.Errorf("pin = %v; want {\"name\": \"postgres\", \"default\": \"16\"}", doc["pin"])
	}
}

// docs/json.md `pack ls`: built-ins at their newest version, then the
// workspace's local packages, sorted by ref; slices never null.
func TestPackLsJSON(t *testing.T) {
	newEnv(t)
	out := mustRun(t, "-C", t.TempDir(), "--json", "pack", "ls")
	want := `{"packs":[{"ref":"rails@1","name":"rails"}],"options":[` +
		`{"ref":"playwright@1","name":"playwright","slot":"browser","works_with":["*"]},` +
		`{"ref":"postgres@1","name":"postgres","slot":"database","works_with":["*"]},` +
		`{"ref":"redis@1","name":"redis","slot":"cache","works_with":["*"]},` +
		`{"ref":"rustfs@1","name":"rustfs","slot":"storage","works_with":["*"]},` +
		`{"ref":"sidekiq@1","name":"sidekiq","slot":"jobs","works_with":["rails","hanami"]}]}` + "\n"
	if out != want {
		t.Errorf("pack ls =\n%s\nwant\n%s", out, want)
	}

	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "pack", "eject", "postgres@1", "--as", "mydb")
	mustRun(t, "-C", ws, "pack", "eject", "rails@1", "--as", "web")
	pt := filepath.Join(ws, "packs", "web", "pack.toml")
	writeFile(t, pt, readFile(t, pt)+"\nunknown_key = 1\n") // listed though it doesn't load: choosing it shows the error
	if code, _, _ := runIn(t, "-C", ws, "pack", "show", "local:web@1"); code != 1 {
		t.Fatalf("pack show of the broken pack: exit %d; want 1", code)
	}
	var doc struct {
		Packs []struct {
			Ref, Name string
		} `json:"packs"`
		Options []struct {
			Ref, Name, Slot string
			WorksWith       []string `json:"works_with"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "--json", "pack", "ls")), &doc); err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, p := range doc.Packs {
		refs = append(refs, p.Ref)
	}
	if strings.Join(refs, " ") != "local:web@1 rails@1" || doc.Packs[0].Name != "web" {
		t.Errorf("packs = %+v", doc.Packs)
	}
	if o := doc.Options[0]; o.Ref != "local:mydb@1" || o.Name != "mydb" || o.Slot != "database" || strings.Join(o.WorksWith, ",") != "*" {
		t.Errorf("options[0] = %+v", o)
	}
	if out := mustRun(t, "-C", ws, "pack", "ls"); !strings.Contains(out, "local:mydb@1") || !strings.Contains(out, "rails@1") {
		t.Errorf("text:\n%s", out)
	}
}
