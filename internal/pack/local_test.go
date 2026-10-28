package pack

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
)

// writeLocal writes the files of fsys under dir (e.g. "packs/p") to
// root/<top>/<as>/, the way a workspace holds a local package.
func writeLocal(t *testing.T, root, top, as string, files map[string]string) {
	t.Helper()
	for name, data := range files {
		p := filepath.Join(root, top, as, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// minimalFiles is the files of one package of minimal(), keyed by their
// path inside the package. Its option works with any pack.
func minimalFiles(dir string) map[string]string {
	out := map[string]string{}
	for name, f := range minimal() {
		if rel, ok := strings.CutPrefix(name, dir+"/"); ok {
			out[rel] = string(f.Data)
		}
	}
	if o, ok := out["option.toml"]; ok {
		out["option.toml"] = strings.Replace(o, `slot = "cache"`, `slot = "cache"`+"\nworks_with = [\"*\"]", 1)
	}
	return out
}

func localCatalog(t *testing.T, root string) *Catalog {
	t.Helper()
	c, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func localProject(pack string, options map[string]string) *config.Project {
	return &config.Project{
		Schema:  1,
		Project: config.Meta{Prefix: "pp", BaseBranch: "main"},
		Stack:   config.StackRef{Pack: pack, App: "web"},
		Options: options,
	}
}

func TestParseRefLocal(t *testing.T) {
	r, err := ParseRef("local:phoenix@1")
	if err != nil || r != (Ref{Name: "phoenix", Version: 1, Local: true}) {
		t.Fatalf("ParseRef = %+v, %v", r, err)
	}
	if r.String() != "local:phoenix@1" {
		t.Errorf("String = %q", r.String())
	}
	for _, bad := range []string{"local:Phx@1", "local:phx", "local:@1", "local:phx@0"} {
		if _, err := ParseRef(bad); err == nil || !strings.Contains(err.Error(), "local:name@N") {
			t.Errorf("ParseRef(%q) = %v", bad, err)
		}
	}
}

func TestResolveLocalPack(t *testing.T) {
	root := t.TempDir()
	writeLocal(t, root, "packs", "p", minimalFiles("packs/p"))
	c := localCatalog(t, root)
	st, err := c.Resolve(localProject("local:p@1", nil))
	if err != nil {
		t.Fatal(err)
	}
	if st.Pack.Name != "p" || st.PackRef != (Ref{Name: "p", Version: 1, Local: true}) {
		t.Fatalf("pack %s, ref %+v", st.Pack.Name, st.PackRef)
	}
	if _, err := fs.Stat(st.Pack.Files, "Containerfile.base"); err != nil {
		t.Errorf("local pack Files: %v", err)
	}
	again, err := c.Pack(st.PackRef)
	if err != nil || again != st.Pack {
		t.Errorf("second Pack = %p, %v; want the cached %p", again, err, st.Pack)
	}
}

func TestResolveLocalOption(t *testing.T) {
	root := t.TempDir()
	writeLocal(t, root, "packs", "p", minimalFiles("packs/p"))
	writeLocal(t, root, "options", "o", minimalFiles("options/o"))
	st, err := localCatalog(t, root).Resolve(localProject("local:p@1", map[string]string{"cache": "local:o@1"}))
	if err != nil {
		t.Fatal(err)
	}
	if ch := st.Option("cache"); ch == nil || ch.Ref != (Ref{Name: "o", Version: 1, Local: true}) || ch.Option.Name != "o" {
		t.Fatalf("cache = %+v", ch)
	}
}

func TestVolumeData(t *testing.T) {
	resolve := func(data string) (*Stack, error) {
		root := t.TempDir()
		writeLocal(t, root, "packs", "p", minimalFiles("packs/p"))
		files := minimalFiles("options/o")
		files["option.toml"] += "\n[[volumes]]\nname = \"scratch\"\nmount = \"/scratch\"\n" + data + "\n"
		writeLocal(t, root, "options", "o", files)
		return localCatalog(t, root).Resolve(localProject("local:p@1", map[string]string{"cache": "local:o@1"}))
	}
	for data, want := range map[string]string{"": "", `data = "persistent"`: "persistent", `data = "cache"`: "cache"} {
		st, err := resolve(data)
		if err != nil {
			t.Fatalf("%q: %v", data, err)
		}
		if got := st.Option("cache").Option.Volumes[0].Data; got != want {
			t.Errorf("%q: Data = %q, want %q", data, got, want)
		}
	}
	if _, err := resolve(`data = "temp"`); err == nil || !strings.Contains(err.Error(), `data must be "persistent" or "cache"`) {
		t.Errorf("data = temp: %v", err)
	}
}

func TestLocalPackVersionMismatch(t *testing.T) {
	root := t.TempDir()
	files := minimalFiles("packs/p")
	files["pack.toml"] = strings.Replace(files["pack.toml"], "version = 1", "version = 2", 1)
	writeLocal(t, root, "packs", "p", files)
	_, err := localCatalog(t, root).Resolve(localProject("local:p@1", nil))
	if err == nil || !strings.Contains(err.Error(), "packs/p/pack.toml") || !strings.Contains(err.Error(), "version 2 does not match the ref local:p@1") {
		t.Fatalf("Resolve = %v", err)
	}
}

func TestLocalPackMissingDir(t *testing.T) {
	root := t.TempDir()
	_, err := localCatalog(t, root).Resolve(localProject("local:nope@1", nil))
	want := filepath.Join(root, "packs", "nope") + " does not exist"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Resolve = %v; want %q", err, want)
	}
	if _, err := builtins(t).Pack(Ref{Name: "nope", Version: 1, Local: true}); err == nil || !strings.Contains(err.Error(), "need a workspace") {
		t.Errorf("no root: %v", err)
	}
}

// A broken package under packs/ that no ref names breaks nothing (C1).
func TestBrokenUnusedLocalPackIgnored(t *testing.T) {
	root := t.TempDir()
	writeLocal(t, root, "packs", "broken", map[string]string{"pack.toml": "name = \"broken\"\n"})
	writeLocal(t, root, "options", "junk", map[string]string{"option.toml": "nonsense = true\n"})
	writeLocal(t, root, "agents", "half", map[string]string{"agent.toml": ""})
	p := localProject("rails@1", nil)
	p.Stack.App = ""
	if _, err := localCatalog(t, root).Resolve(p); err != nil {
		t.Fatalf("Resolve with broken unused local packages: %v", err)
	}
}

// A local copy may keep a built-in's name: both resolve, and lists tell them apart.
func TestLocalSharesBuiltinName(t *testing.T) {
	root := t.TempDir()
	c := localCatalog(t, root)
	rails := c.Packs[Ref{Name: "rails", Version: 1}]
	files := map[string]string{}
	if err := fs.WalkDir(rails.Files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(rails.Files, p)
		files[p] = string(data)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	writeLocal(t, root, "packs", "rails", files)
	p := localProject("local:rails@1", map[string]string{"database": "postgres@1"})
	p.Stack.App = ""
	st, err := c.Resolve(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pack == rails || !st.PackRef.Local {
		t.Errorf("resolved the built-in, not the local copy")
	}
	if got := refList([]Ref{{Name: "rails", Version: 1}, {Name: "rails", Version: 1, Local: true}}); got != "local:rails@1, rails@1" {
		t.Errorf("refList = %q", got)
	}
}

func TestResolvePortCollisionAcrossPackages(t *testing.T) {
	root := t.TempDir()
	files := minimalFiles("options/o")
	files["option.toml"] = strings.Replace(files["option.toml"], "base = 55432", "base = 13000", 1)
	writeLocal(t, root, "options", "o", files)
	p := localProject("rails@1", map[string]string{"cache": "local:o@1"})
	p.Stack.App = ""
	_, err := localCatalog(t, root).Resolve(p)
	if err == nil || !strings.Contains(err.Error(), "ports: base 13000 is used by pack rails@1 and option local:o@1") {
		t.Fatalf("Resolve = %v", err)
	}

	files["option.toml"] = strings.Replace(files["option.toml"], "base = 13000", "base = 40000", 1)
	files["option.toml"] = strings.Replace(files["option.toml"], `name = "o"`+"\nenv", `name = "app"`+"\nenv", 1)
	root = t.TempDir()
	writeLocal(t, root, "options", "o", files)
	_, err = localCatalog(t, root).Resolve(p)
	if err == nil || !strings.Contains(err.Error(), `ports: name "app" is used by pack rails@1 and option local:o@1`) {
		t.Fatalf("Resolve = %v", err)
	}
}
