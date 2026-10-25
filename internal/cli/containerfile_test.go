package cli

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
)

// builtinDefault is a built-in default Containerfile's bytes.
func builtinDefault(t *testing.T, files func(*pack.Catalog) fs.FS, name string) []byte {
	t.Helper()
	cat, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(files(cat), name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func railsFiles(c *pack.Catalog) fs.FS {
	return c.Packs[pack.Ref{Name: "rails", Version: 1}].Files
}
func postgresFiles(c *pack.Catalog) fs.FS {
	return c.Options[pack.Ref{Name: "postgres", Version: 1}].Files
}
func playwrightFiles(c *pack.Catalog) fs.FS {
	return c.Options[pack.Ref{Name: "playwright", Version: 1}].Files
}
func nvimFiles(c *pack.Catalog) fs.FS { return c.Tools["nvim"].Files }

// ejected is what eject writes for a default from ref.
func ejected(ref, file string, def []byte) string {
	return fmt.Sprintf("# Ejected from %s %s (sha256 %x) by railswitch dev. Edit freely: railswitch builds this file.\n", ref, file, sha256.Sum256(def)) + string(def)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func removeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// editProject replaces old with new in ws's project.toml.
func editProject(t *testing.T, ws, old, new string) {
	t.Helper()
	path := filepath.Join(ws, "project.toml")
	data := readFile(t, path)
	if !strings.Contains(data, old) {
		t.Fatalf("project.toml lacks %q", old)
	}
	writeFile(t, path, strings.Replace(data, old, new, 1))
}

// stripImage removes the fixture's [image] table (the end of the file).
func stripImage(t *testing.T, ws string) {
	t.Helper()
	path := filepath.Join(ws, "project.toml")
	data := readFile(t, path)
	i := strings.Index(data, "\n[image]\n")
	if i < 0 {
		t.Fatal("fixture has no [image]")
	}
	writeFile(t, path, data[:i+1])
}

func loadProject(t *testing.T, ws string) *config.Project {
	t.Helper()
	p, err := config.Load(filepath.Join(ws, "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type listDoc struct {
	Steps []struct {
		Name string   `json:"name"`
		File string   `json:"file"`
		Tag  string   `json:"tag"`
		From []string `json:"from"`
	} `json:"steps"`
	Warnings []string `json:"warnings"`
}

// list is `containerfile list --json` of ws's master worktree.
func list(t *testing.T, ws string) listDoc {
	t.Helper()
	var d listDoc
	out := mustRun(t, "-C", ws, "-w", "master", "--json", "containerfile", "list")
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if d.Steps == nil || d.Warnings == nil {
		t.Fatalf("null steps or warnings: %s", out)
	}
	return d
}

func (d listDoc) tag(t *testing.T, name string) string {
	t.Helper()
	for _, s := range d.Steps {
		if s.Name == name {
			return s.Tag
		}
	}
	t.Fatalf("no step %q in %+v", name, d.Steps)
	return ""
}

func (d listDoc) names() []string {
	var out []string
	for _, s := range d.Steps {
		out = append(out, s.Name)
	}
	return out
}

func TestEjectBase(t *testing.T) {
	ws, _ := filialWS(t)
	stripImage(t, ws)
	file := filepath.Join(ws, "containerfiles", "Containerfile.base")
	removeFile(t, file)
	mustRun(t, "-C", ws, "containerfile", "eject", "base")

	want := ejected("rails@1", "Containerfile.base", builtinDefault(t, railsFiles, "Containerfile.base"))
	if got := readFile(t, file); got != want {
		t.Fatalf("ejected file:\n%s\nwant:\n%s", got, want)
	}
	if p := loadProject(t, ws); p.Image.Base != "containerfiles/Containerfile.base" {
		t.Fatalf("[image] base = %q", p.Image.Base)
	}

	mustRun(t, "-C", ws, "lock")
	before := list(t, ws).tag(t, "base")
	writeFile(t, file, want+"# mine\n")
	if after := list(t, ws).tag(t, "base"); after == before {
		t.Errorf("base tag %s unchanged after editing the file", after)
	}

	code, _, errs := runIn(t, "-C", ws, "containerfile", "eject", "base")
	if code != 1 || !strings.Contains(errs, "--force") {
		t.Fatalf("second eject: exit %d, stderr %q", code, errs)
	}
	mustRun(t, "-C", ws, "containerfile", "eject", "base", "--force")
	if got := readFile(t, file); got != want {
		t.Fatalf("--force did not restore the default:\n%s", got)
	}
}

func TestEjectOptionAddon(t *testing.T) {
	ws, _ := filialWS(t)
	editProject(t, ws, "addons = [\"containerfiles/Containerfile.pg-client\"]\n", "")
	file := filepath.Join(ws, "containerfiles", "Containerfile.pg-client")
	removeFile(t, file)
	mustRun(t, "-C", ws, "lock")
	if w := list(t, ws).Warnings; len(w) != 1 || !strings.Contains(w[0], "pg-client") {
		t.Fatalf("warnings before eject = %q; want the pg-client one", w)
	}

	mustRun(t, "-C", ws, "containerfile", "eject", "pg-client")
	want := ejected("postgres@1", "Containerfile.pg-client", builtinDefault(t, postgresFiles, "Containerfile.pg-client"))
	if got := readFile(t, file); got != want {
		t.Fatalf("ejected file:\n%s\nwant:\n%s", got, want)
	}
	if a := loadProject(t, ws).Image.Addons; !slices.Equal(a, []string{"containerfiles/Containerfile.pg-client"}) {
		t.Fatalf("[image] addons = %q", a)
	}
	d := list(t, ws)
	if len(d.Warnings) != 0 {
		t.Errorf("warnings after eject = %q", d.Warnings)
	}
	d.tag(t, "addon pg-client")
	// --force over a listed addon re-writes the file and lists it once.
	mustRun(t, "-C", ws, "containerfile", "eject", "pg-client", "--force")
	if a := loadProject(t, ws).Image.Addons; len(a) != 1 {
		t.Errorf("[image] addons after --force = %q", a)
	}

	// A pack of another OS refuses the addon, naming both.
	removeFile(t, file)
	cat, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	st, err := cat.Resolve(loadProject(t, ws))
	if err != nil {
		t.Fatal(err)
	}
	alpine := *st.Pack
	alpine.Image.OS = "alpine"
	st.Pack = &alpine
	err = Eject(ws, cat, st, "pg-client", false)
	if err == nil || !strings.Contains(err.Error(), "alpine") || !strings.Contains(err.Error(), "debian") {
		t.Fatalf("err = %v; want one naming alpine and debian", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("refused eject wrote %s (%v)", file, err)
	}
}

func TestEjectTool(t *testing.T) {
	ws, _ := filialWS(t)
	file := filepath.Join(ws, "containerfiles", "Containerfile.nvim")
	removeFile(t, file)
	proj := readFile(t, filepath.Join(ws, "project.toml"))
	mustRun(t, "-C", ws, "containerfile", "eject", "nvim")
	if got, want := readFile(t, file), ejected("nvim", "Containerfile.nvim", builtinDefault(t, nvimFiles, "Containerfile.nvim")); got != want {
		t.Fatalf("ejected file:\n%s\nwant:\n%s", got, want)
	}
	if got := readFile(t, filepath.Join(ws, "project.toml")); got != proj {
		t.Errorf("project.toml changed:\n%s", got)
	}
}

func TestEjectStandalone(t *testing.T) {
	ws, _ := filialWS(t)
	file := filepath.Join(ws, "containerfiles", "Containerfile.playwright")
	removeFile(t, file)
	proj := readFile(t, filepath.Join(ws, "project.toml"))
	mustRun(t, "-C", ws, "containerfile", "eject", "playwright")
	def := builtinDefault(t, playwrightFiles, "Containerfile.playwright")
	if !bytes.Contains(def, []byte("\nFROM mcr.microsoft.com/playwright:v${PLAYWRIGHT_VERSION}-noble\n")) {
		t.Fatalf("playwright default has another FROM:\n%s", def)
	}
	if got, want := readFile(t, file), ejected("playwright@1", "Containerfile.playwright", def); got != want {
		t.Fatalf("ejected file:\n%s\nwant:\n%s", got, want)
	}
	if got := readFile(t, filepath.Join(ws, "project.toml")); got != proj {
		t.Errorf("project.toml changed:\n%s", got)
	}
	mustRun(t, "-C", ws, "lock")
	tag := list(t, ws).tag(t, "playwright")
	if !regexp.MustCompile(`^localhost/filial/playwright:1\.54\.0-[0-9a-f]{8}$`).MatchString(tag) {
		t.Errorf("playwright tag = %q", tag)
	}
}

func TestEjectNoWorkspace(t *testing.T) {
	root := t.TempDir()
	data := readFile(t, filepath.Join(filialFixture, "project.toml"))
	i := strings.Index(data, "\n[image]\n")
	if i < 0 {
		t.Fatal("fixture has no [image]")
	}
	writeFile(t, filepath.Join(root, "project.toml"), data[:i+1])
	cat, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	st, err := cat.Resolve(loadProject(t, root))
	if err != nil {
		t.Fatal(err)
	}
	if err := Eject(root, cat, st, "base", false); err != nil {
		t.Fatal(err)
	}
	if p := loadProject(t, root); p.Image.Base != "containerfiles/Containerfile.base" {
		t.Fatalf("[image] base = %q", p.Image.Base)
	}
	if _, err := os.Stat(filepath.Join(root, "containerfiles", "Containerfile.base")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "stack.lock")); !os.IsNotExist(err) {
		t.Errorf("Eject wrote stack.lock (%v)", err)
	}
}

const vipsSkeleton = `# Addon for Filial Rails. railswitch builds it on the previous image in the chain (see: railswitch containerfile list).
ARG BASE_IMAGE
FROM ${BASE_IMAGE}
USER root
# RUN apt-get update && apt-get install -y --no-install-recommends <packages> && rm -rf /var/lib/apt/lists/*
USER appuser
`

func TestNewAddon(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	mustRun(t, "-C", ws, "containerfile", "new", "vips")
	if got := readFile(t, filepath.Join(ws, "containerfiles", "Containerfile.vips")); got != vipsSkeleton {
		t.Fatalf("skeleton:\n%s\nwant:\n%s", got, vipsSkeleton)
	}
	if a := loadProject(t, ws).Image.Addons; !slices.Equal(a, []string{"containerfiles/Containerfile.pg-client", "containerfiles/Containerfile.vips"}) {
		t.Fatalf("[image] addons = %q", a)
	}
	// list builds the model, which runs the Containerfile contract check on
	// every chain step: it passes for the skeleton.
	if n, want := list(t, ws).names(), []string{"base", "addon pg-client", "addon vips", "nvim", "playwright"}; !slices.Equal(n, want) {
		t.Fatalf("steps = %q; want %q", n, want)
	}
	out := mustRun(t, "-C", ws, "-w", "master", "containerfile", "list")
	sc := bufio.NewScanner(strings.NewReader(out))
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) != 5 || !strings.HasPrefix(lines[2], "addon vips") || !strings.Contains(lines[2], "containerfiles/Containerfile.vips") || !strings.Contains(lines[2], "localhost/filial/base:") {
		t.Errorf("list text:\n%s", out)
	}

	for _, name := range []string{"base", "pg-client", "nvim", "playwright", "Bad_Name", "-x"} {
		if code, _, errs := runIn(t, "-C", ws, "containerfile", "new", name); code != 2 {
			t.Errorf("new %s: exit %d; want 2 (stderr %q)", name, code, errs)
		}
	}
	if code, _, errs := runIn(t, "-C", ws, "containerfile", "new", "vips"); code != 1 || !strings.Contains(errs, "exists") {
		t.Errorf("second new vips: exit %d, stderr %q", code, errs)
	}
}

func TestShowDefault(t *testing.T) {
	ws, _ := filialWS(t)
	def := string(builtinDefault(t, railsFiles, "Containerfile.base"))
	if out := mustRun(t, "-C", ws, "containerfile", "show", "base"); out != def {
		t.Fatalf("show base:\n%s", out)
	}
	file := filepath.Join(ws, "containerfiles", "Containerfile.base")
	writeFile(t, file, def+"# mine\n")
	if out := mustRun(t, "-C", ws, "containerfile", "show", "base"); out != def+"# mine\n" {
		t.Errorf("show base after edit:\n%s", out)
	}
	if out := mustRun(t, "-C", ws, "containerfile", "show", "base", "--default"); out != def {
		t.Errorf("show base --default:\n%s", out)
	}
	if code, _, _ := runIn(t, "-C", ws, "containerfile", "show", "../../project.toml"); code != 2 {
		t.Errorf("show ../../project.toml: exit %d; want 2", code)
	}
	removeFile(t, file)
	if out := mustRun(t, "-C", ws, "containerfile", "show", "base"); out != def {
		t.Errorf("show base without a workspace file:\n%s", out)
	}
	if out := mustRun(t, "-C", ws, "containerfile", "show", "--default", "playwright"); out != string(builtinDefault(t, playwrightFiles, "Containerfile.playwright")) {
		t.Errorf("show --default playwright:\n%s", out)
	}
	if code, _, _ := runIn(t, "-C", ws, "containerfile", "show", "nope"); code != 1 {
		t.Errorf("show nope: exit %d; want 1", code)
	}
}

// TestBuiltinContainerfilesSelfContained: an ejected default builds
// unchanged with containerfiles/ as its context, so no built-in reads the
// build context (only COPY --from= of another stage or image).
func TestBuiltinContainerfilesSelfContained(t *testing.T) {
	cat, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	type file struct {
		files fs.FS
		name  string
	}
	var all []file
	for _, p := range cat.Packs {
		all = append(all, file{p.Files, p.Image.File})
	}
	for _, o := range cat.Options {
		if o.AppAddon != nil {
			all = append(all, file{o.Files, o.AppAddon.File})
		}
		if o.Image.Build != "" {
			all = append(all, file{o.Files, o.Image.Build})
		}
	}
	for _, tl := range cat.Tools {
		all = append(all, file{tl.Files, tl.Image.File})
	}
	if len(all) < 4 {
		t.Fatalf("found %d built-in Containerfiles; want base, pg-client, playwright and nvim at least", len(all))
	}
	for _, f := range all {
		data, err := fs.ReadFile(f.files, f.name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if op := strings.ToUpper(fields[0]); op != "COPY" && op != "ADD" {
				continue
			}
			if !slices.ContainsFunc(fields[1:], func(f string) bool { return strings.HasPrefix(f, "--from=") }) {
				t.Errorf("%s:%d reads the build context: %s", f.name, i+1, line)
			}
		}
	}
}
