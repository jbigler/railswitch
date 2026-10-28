package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	builtinfs "github.com/jbigler/railswitch/internal/pack/builtin"
	"github.com/jbigler/railswitch/internal/template"
	"github.com/jbigler/railswitch/internal/testfixture"
)

// bundledRails writes the template rails-own@1 into dirs' store: the
// rails-pg lines on local:rails@1, with packs/rails/ a copy of the
// built-in rails pack whose deps.allow gains a host.
func bundledRails(t *testing.T, dir string) {
	t.Helper()
	stack := strings.Replace(strings.Replace(railsPGStack, `pack = "rails@1"`, `pack = "local:rails@1"`, 1), `name = "rails-pg"`, `name = "rails-own"`, 1)
	testfixture.WriteFile(t, filepath.Join(dir, "stack.toml"), stack)
	err := fs.WalkDir(builtinfs.FS, "packs/rails", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(builtinfs.FS, p)
		if p == "packs/rails/deps.allow" {
			data = append(data, "own.example\n"...)
		}
		testfixture.WriteFile(t, filepath.Join(dir, filepath.FromSlash(p)), string(data))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A template bundling a local pack starts a project on it: the pack is
// the new workspace's packs/rails/, locked as local, the edit kept.
func TestNewTemplateBundled(t *testing.T) {
	dirs := newEnv(t)
	bundledRails(t, template.LocalDir(dirs, "rails-own"))
	if out := mustRun(t, "template", "ls"); !strings.Contains(out, "rails-own@1\tmachine\tlocal:rails@1") {
		t.Fatalf("ls:\n%s", out)
	}
	repo := bareRepo(t, "main")
	ws := filepath.Join(t.TempDir(), "shop")
	mustNew(t, ws, "--repo", repo, "--prefix", "shop", "--branch", "main", "--template", "rails-own@1")
	if !strings.HasSuffix(readFile(t, filepath.Join(ws, "packs", "rails", "deps.allow")), "own.example\n") {
		t.Error("the bundled pack was not written")
	}
	if fi, err := os.Stat(filepath.Join(ws, "packs", "rails", "entrypoints", "entrypoint-app.sh")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("entrypoint mode: %v %v", fi, err)
	}
	lock, err := config.ReadLock(filepath.Join(ws, "stack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if lock.Packs["rails"].Source != "local" {
		t.Errorf("lock %+v", lock.Packs["rails"])
	}
	if !strings.Contains(readFile(t, filepath.Join(ws, ".railswitch", "firewall", "deps.allow")), "own.example") {
		t.Error("the bundled pack's host is not in the rendered firewall list")
	}
}

// Export bundles the project's local pack, as the project runs it.
func TestExportBundles(t *testing.T) {
	ws, _ := localRails(t)
	allow := filepath.Join(ws, "packs", "rails", "deps.allow")
	writeFile(t, allow, readFile(t, allow)+"own.example\n")
	mustRun(t, "-C", ws, "lock", "--accept", lockReviewHash(t, ws))
	to := filepath.Join(t.TempDir(), "tpl")
	out := mustRun(t, "-C", ws, "template", "export", "--name", "own", "--to", to)
	if !strings.Contains(out, "bundles local:rails@1 (packs/rails/)") || !strings.HasSuffix(readFile(t, filepath.Join(to, "packs", "rails", "deps.allow")), "own.example\n") {
		t.Errorf("export:\n%s", out)
	}
}

// lockReviewHash is the hash of ws's lock review.
func lockReviewHash(t *testing.T, ws string) string {
	t.Helper()
	var doc struct{ Hash string }
	if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "--json", "lock", "--review")), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Hash
}

// Importing a template folder with a bundled pack new here shows the pack
// as its changes from the closest built-in.
func TestTemplateImportBundled(t *testing.T) {
	newEnv(t)
	dir := filepath.Join(t.TempDir(), "rails-own")
	bundledRails(t, dir)
	code, doc, errs := importJSON(t, dir)
	if code != 0 || doc.BundlesComparedWith["packs/rails"] != "rails@1" {
		t.Fatalf("exit %d %+v %s", code, doc, errs)
	}
	var paths []string
	for _, f := range doc.Packages[0].Files {
		paths = append(paths, f.Path)
	}
	if strings.Join(paths, " ") != "packs/rails/deps.allow stack.toml" || doc.Flags == 0 {
		t.Errorf("files %q, flags %d", paths, doc.Flags)
	}
}
