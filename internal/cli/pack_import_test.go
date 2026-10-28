package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

func packImportJSON(t *testing.T, f *systest.Fake, ws string, args ...string) (int, packImportDoc, string) {
	t.Helper()
	code, out, errs := runStdin(t, f, "", append([]string{"-C", ws, "--json", "pack", "import"}, args...)...)
	var doc packImportDoc
	if code == 0 {
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("pack import %q: %v\n%s", args, err, out)
		}
	}
	return code, doc, errs
}

// A package folder is reviewed, then written as a new local package with
// the reviewed hash; a changed folder is an update, shown as the diff
// from the local package there.
func TestPackImportUnused(t *testing.T) {
	ws, _ := lockedWS(t)
	folder := filepath.Join(t.TempDir(), "redis")
	mustRun(t, "-C", ws, "pack", "export", "redis@1", "--to", folder)
	lock := readFile(t, filepath.Join(ws, "stack.lock"))

	writeFile(t, filepath.Join(folder, "README.md"), "# ours\n")
	code, doc, errs := packImportJSON(t, fakeHost(), ws, folder, "--as", "myredis")
	if code != 0 || doc.Status != "new" || doc.Ref != "local:myredis@1" || doc.Kind != "option" || doc.Used || doc.Replaces != nil {
		t.Fatalf("new: exit %d %+v %s", code, doc, errs)
	}
	// A new package is shown as its changes from the closest built-in.
	if doc.ComparedWith != "redis@1" || len(doc.Packages[0].Files) != 2 {
		t.Errorf("compared with %q: %+v", doc.ComparedWith, doc.Packages[0].Files)
	}
	dir := filepath.Join(ws, "options", "myredis")
	if _, err := os.Lstat(dir); err == nil {
		t.Fatal("a review wrote the package")
	}
	if code, _, errs := runIn(t, "-C", ws, "pack", "import", folder, "--as", "myredis", "--accept", strings.Repeat("0", 64)); code != 1 || !strings.Contains(errs, "changed since its review") {
		t.Errorf("another hash: exit %d: %s", code, errs)
	}
	mustRun(t, "-C", ws, "pack", "import", folder, "--as", "myredis", "--accept", doc.SHA256)
	if !strings.Contains(readFile(t, filepath.Join(dir, "option.toml")), `name = "myredis"`) {
		t.Error("the manifest's name was not set")
	}
	if readFile(t, filepath.Join(ws, "stack.lock")) != lock {
		t.Error("stack.lock changed for a package the project does not use")
	}
	if _, doc, _ := packImportJSON(t, fakeHost(), ws, folder, "--as", "myredis"); doc.Status != "same" {
		t.Errorf("same: %+v", doc)
	}

	writeFile(t, filepath.Join(folder, "README.md"), "# ours, again\n")
	code, doc, _ = packImportJSON(t, fakeHost(), ws, folder, "--as", "myredis")
	if code != 0 || doc.Status != "update" || doc.ComparedWith != "" || doc.Replaces == nil || doc.Replaces.Ref != "local:myredis@1" || len(doc.Packages[0].Files) != 1 || doc.Packages[0].Files[0].Path != "README.md" {
		t.Fatalf("update: exit %d %+v", code, doc)
	}
	mustRun(t, "-C", ws, "pack", "import", folder, "--as", "myredis", "--accept", doc.SHA256)
	if readFile(t, filepath.Join(dir, "README.md")) != "# ours, again\n" {
		t.Error("the update was not written")
	}
}

// A package the project uses is written, locked at the reviewed content
// and rendered: nothing waits for review afterwards.
func TestPackImportUsed(t *testing.T) {
	ws, dir := localRails(t)
	folder := filepath.Join(t.TempDir(), "rails")
	mustRun(t, "-C", ws, "pack", "export", "local:rails@1", "--to", folder)
	ep := filepath.Join(folder, "entrypoints", "entrypoint-app.sh")
	writeFile(t, ep, readFile(t, ep)+"# filial\n")
	before, err := config.ReadLock(filepath.Join(ws, "stack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	code, doc, errs := packImportJSON(t, unitsFake(), ws, folder)
	if code != 0 || doc.Status != "update" || !doc.Used || doc.Packages[0].Files[0].Path != "entrypoints/entrypoint-app.sh" {
		t.Fatalf("exit %d %+v %s", code, doc, errs)
	}
	if code, out, errs := runStdin(t, unitsFake(), "", "-C", ws, "pack", "import", folder, "--accept", doc.SHA256); code != 0 {
		t.Fatalf("accept: exit %d: %s\n%s", code, out, errs)
	}
	after, err := config.ReadLock(filepath.Join(ws, "stack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if after.Packs["rails"].SHA256 != doc.SHA256 || after.Packs["rails"].SHA256 == before.Packs["rails"].SHA256 {
		t.Errorf("lock sha %s, want %s", after.Packs["rails"].SHA256, doc.SHA256)
	}
	if !strings.HasSuffix(readFile(t, filepath.Join(dir, "entrypoints", "entrypoint-app.sh")), "# filial\n") {
		t.Error("not written")
	}
	if out := mustRun(t, "-C", ws, "render", "--check"); out != "" {
		t.Errorf("render --check:\n%s", out)
	}
	var rv struct{ Packages []any }
	if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "--json", "lock", "--review")), &rv); err != nil || len(rv.Packages) != 0 {
		t.Errorf("review after import: %v %+v", err, rv)
	}
}

// A used package with an update waiting refuses an import; another
// version of a used package refuses; a folder that isn't a package, holds
// a symlink or another kind's name refuses.
func TestPackImportRefuses(t *testing.T) {
	ws, dir := localRails(t)
	folder := filepath.Join(t.TempDir(), "rails")
	mustRun(t, "-C", ws, "pack", "export", "local:rails@1", "--to", folder)
	allow := filepath.Join(dir, "deps.allow")
	writeFile(t, allow, readFile(t, allow)+"evil.example\n")
	ep := filepath.Join(folder, "entrypoints", "entrypoint-app.sh")
	writeFile(t, ep, readFile(t, ep)+"# filial\n")
	if code, _, errs := runStdin(t, unitsFake(), "", "-C", ws, "pack", "import", folder); code != 1 || !strings.Contains(errs, "waiting for review") {
		t.Errorf("pending: exit %d: %s", code, errs)
	}

	manifest := filepath.Join(folder, "pack.toml")
	writeFile(t, manifest, strings.Replace(readFile(t, manifest), "version = 1", "version = 2", 1))
	if code, _, errs := runIn(t, "-C", ws, "pack", "import", folder); code != 1 || !strings.Contains(errs, "the project uses local:rails@1 and the folder is local:rails@2") {
		t.Errorf("version: exit %d: %s", code, errs)
	}

	for name, mod := range map[string]func(string){
		"no manifest":   func(d string) { os.Remove(filepath.Join(d, "pack.toml")) },
		"symlink":       func(d string) { os.Symlink("/etc/passwd", filepath.Join(d, "README.md")) },
		"bad path":      func(d string) { writeFile(t, filepath.Join(d, "evil.exe"), "x") },
		"two manifests": func(d string) { writeFile(t, filepath.Join(d, "option.toml"), "") },
	} {
		d := filepath.Join(t.TempDir(), "p")
		if err := os.CopyFS(d, os.DirFS(folder)); err != nil {
			t.Fatal(err)
		}
		mod(d)
		if code, _, errs := runIn(t, "-C", ws, "pack", "import", d, "--as", "other"); code != 1 || errs == "" {
			t.Errorf("%s: exit %d: %s", name, code, errs)
		}
	}
	if err := os.MkdirAll(filepath.Join(ws, "options", "dup"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runIn(t, "-C", ws, "pack", "import", folder, "--as", "dup"); code != 1 || !strings.Contains(errs, "would name two packages") {
		t.Errorf("dup: exit %d: %s", code, errs)
	}
}
