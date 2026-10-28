package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/template"
)

func importJSON(t *testing.T, dir string) (int, templateImportDoc, string) {
	t.Helper()
	code, out, errs := runIn(t, "--json", "template", "import", dir)
	var doc templateImportDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("import %s: exit %d: %v\n%s\n%s", dir, code, err, out, errs)
	}
	return code, doc, errs
}

// A folder someone handed over is reviewed, then saved only with the
// hash its review printed; a later folder of the same name must have a
// higher version, and its review is the diff from the stored one.
func TestTemplateImport(t *testing.T) {
	ws, dirs := lockedWS(t)
	dir := filepath.Join(t.TempDir(), "filial-stack")
	mustRun(t, "-C", ws, "template", "export", "--name", "filial-stack", "--to", dir)
	store := template.LocalDir(dirs, "filial-stack")

	code, doc, _ := importJSON(t, dir)
	if code != 0 || doc.Status != "new" || doc.Ref != "filial-stack@1" || doc.Hash != doc.SHA256 || doc.Replaces != nil || len(doc.Packages) != 1 {
		t.Fatalf("new: exit %d %+v", code, doc)
	}
	if p := doc.Packages[0]; p.Kind != "template" || p.Status != "added" || p.To != doc.SHA256 || len(p.Files) == 0 || p.Files[0].Status != "added" {
		t.Errorf("package = %+v", p)
	}
	if _, err := os.Lstat(store); err == nil {
		t.Fatal("a review saved the template")
	}
	if code, _, errs := runIn(t, "template", "import", dir, "--accept", strings.Repeat("0", 64)); code != 1 || !strings.Contains(errs, "changed since its review") {
		t.Errorf("another hash: exit %d: %s", code, errs)
	}
	if out := mustRun(t, "template", "import", dir, "--accept", doc.SHA256); !strings.Contains(out, "imported template filial-stack@1") {
		t.Errorf("accept: %s", out)
	}
	if out := mustRun(t, "template", "ls"); !strings.Contains(out, "filial-stack@1\tmachine") {
		t.Errorf("ls after import:\n%s", out)
	}
	if code, doc, _ := importJSON(t, dir); code != 0 || doc.Status != "same" || doc.Packages[0].Status != "unchanged" || len(doc.Packages[0].Files) != 0 {
		t.Errorf("same: exit %d %+v", code, doc)
	}

	// Other content at the same version is refused, and never saved.
	stack := filepath.Join(dir, "stack.toml")
	v1 := readFile(t, stack)
	writeFile(t, stack, v1+"\n[[secret]]\nname = \"extra_key\"\nservices = [\"rails\"]\n")
	code, doc, _ = importJSON(t, dir)
	if code != 1 || doc.Status != "older" || doc.Replaces == nil || doc.Replaces.Ref != "filial-stack@1" {
		t.Errorf("older: exit %d %+v", code, doc)
	}
	if code, _, errs := runIn(t, "template", "import", dir, "--accept", doc.SHA256); code != 1 || !strings.Contains(errs, "must have a higher version") {
		t.Errorf("accept older: exit %d: %s", code, errs)
	}

	// Version 2: the diff from version 1, the new secret flagged.
	writeFile(t, stack, strings.Replace(readFile(t, stack), "version = 1\n", "version = 2\n", 1))
	code, doc, _ = importJSON(t, dir)
	if code != 0 || doc.Status != "update" || doc.Packages[0].Status != "changed" || doc.Packages[0].From != doc.Replaces.SHA256 || doc.Flags == 0 {
		t.Fatalf("update: exit %d %+v", code, doc)
	}
	if f := doc.Packages[0].Files; len(f) != 1 || f[0].Path != "stack.toml" || f[0].Flags[0].Check != "privilege" {
		t.Errorf("update files = %+v", f)
	}
	mustRun(t, "template", "import", dir, "--accept", doc.SHA256)
	if !strings.Contains(readFile(t, filepath.Join(store, "stack.toml")), "version = 2\n") {
		t.Error("the update was not saved")
	}
}

// A folder is hostile: a symlink, a file a template can't hold, or a
// symlinked folder refuses the import before anything is saved.
func TestTemplateImportRefusesHostile(t *testing.T) {
	ws, dirs := lockedWS(t)
	good := filepath.Join(t.TempDir(), "tpl")
	mustRun(t, "-C", ws, "template", "export", "--name", "filial-stack", "--to", good)
	for name, mod := range map[string]func(dir string){
		"symlinked file": func(dir string) {
			if err := os.Symlink("/etc/passwd", filepath.Join(dir, "README.md")); err != nil {
				t.Fatal(err)
			}
		},
		"extra file": func(dir string) { writeFile(t, filepath.Join(dir, "run.sh"), "#!/bin/sh\n") },
		"extra dir": func(dir string) {
			if err := os.MkdirAll(filepath.Join(dir, "packs", "x"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(dir, "packs", "x", "pack.toml"), "")
		},
		"tools": func(dir string) {
			writeFile(t, filepath.Join(dir, "stack.toml"), readFile(t, filepath.Join(good, "stack.toml"))+"\n[tools.nvim]\nconfig_repo = \"https://h.example/c\"\n")
		},
	} {
		dir := filepath.Join(t.TempDir(), "tpl")
		if err := os.CopyFS(dir, os.DirFS(good)); err != nil {
			t.Fatal(err)
		}
		mod(dir)
		if code, _, errs := runIn(t, "template", "import", dir); code != 1 || errs == "" {
			t.Errorf("%s: exit %d: %s", name, code, errs)
		}
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runIn(t, "template", "import", link); code != 1 || !strings.Contains(errs, "not a template folder") {
		t.Errorf("symlinked folder: exit %d: %s", code, errs)
	}
	if _, err := os.Lstat(template.LocalDir(dirs, "filial-stack")); err == nil {
		t.Error("a refused import saved the template")
	}
}

// A template that names a library this machine hasn't added warns.
func TestTemplateImportWarnsLibrary(t *testing.T) {
	ws, _ := lockedWS(t)
	dir := filepath.Join(t.TempDir(), "tpl")
	mustRun(t, "-C", ws, "template", "export", "--name", "filial-stack", "--to", dir)
	stack := filepath.Join(dir, "stack.toml")
	writeFile(t, stack, strings.Replace(readFile(t, stack), `cache = "redis@1"`, `cache = "acme:redis@1"`, 1))
	_, doc, _ := importJSON(t, dir)
	if len(doc.Warnings) != 1 || !strings.Contains(doc.Warnings[0], "library acme, which this machine hasn't added") {
		t.Errorf("warnings = %q", doc.Warnings)
	}
}
