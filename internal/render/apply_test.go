package render

import (
	"bytes"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/pack/builtin"
)

// filialPlan builds the Filial fixture's Plan in a fresh workspace and
// returns it with the workspace and an empty Quadlet dir.
func filialPlan(t *testing.T) (p *Plan, ws, qdir string) {
	t.Helper()
	ws = fixtureWS(t)
	p, err := mustBuild(t, fixtureInputs(t, ws, nil)).Plan()
	if err != nil {
		t.Fatal(err)
	}
	return p, ws, t.TempDir()
}

func mustApply(t *testing.T, p *Plan, ws, qdir string, check bool) *Diff {
	t.Helper()
	d, err := Apply(p, ws, qdir, "filial", check)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// planNames is every unit filename and workspace-relative file path of p,
// sorted.
func planNames(p *Plan) []string {
	names := slices.Collect(maps.Keys(p.Units))
	names = slices.AppendSeq(names, maps.Keys(p.Files))
	slices.Sort(names)
	return names
}

// markedUnit is a unit whose marker names prefix and worktree slug.
func markedUnit(prefix, slug string) string {
	return Marker + "/WS/" + prefix + "/project.toml (prefix " + prefix + ", worktree " + slug + "). Edit project.toml, not this file.\n# role: app\n\n[Container]\nImage=x\n"
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

func TestApplyFirstAddsEverything(t *testing.T) {
	p, ws, qdir := filialPlan(t)
	d := mustApply(t, p, ws, qdir, false)
	if want := planNames(p); !slices.Equal(d.Added, want) {
		t.Errorf("Added = %v\nwant %v", d.Added, want)
	}
	if len(d.Changed) != 0 || len(d.Removed) != 0 {
		t.Errorf("Changed = %v, Removed = %v; want none", d.Changed, d.Removed)
	}
	for name, data := range p.Units {
		got, err := os.ReadFile(filepath.Join(qdir, name))
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("%s: written %q (%v), want the plan's bytes", name, got, err)
		}
	}
	for rel, f := range p.Files {
		got, err := os.ReadFile(filepath.Join(ws, rel))
		if err != nil || !bytes.Equal(got, f.Data) {
			t.Errorf("%s: written %q (%v), want the plan's bytes", rel, got, err)
		}
	}
}

func TestApplySecondIsNoop(t *testing.T) {
	p, ws, qdir := filialPlan(t)
	mustApply(t, p, ws, qdir, false)
	// Backdate every written file so a rewrite shows whatever the clock's
	// resolution.
	old := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	paths := map[string]string{}
	for name := range p.Units {
		paths[name] = filepath.Join(qdir, name)
	}
	for rel := range p.Files {
		paths[rel] = filepath.Join(ws, rel)
	}
	for _, path := range paths {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	d := mustApply(t, p, ws, qdir, false)
	if len(d.Added)+len(d.Changed)+len(d.Removed) != 0 || d.Text != "" {
		t.Errorf("second apply: %+v; want an empty Diff", d)
	}
	for name, path := range paths {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !fi.ModTime().Equal(old) {
			t.Errorf("%s: mtime changed to %v", name, fi.ModTime())
		}
	}
}

func TestApplyChanged(t *testing.T) {
	p, ws, qdir := filialPlan(t)
	mustApply(t, p, ws, qdir, false)
	const unit = "filial-master-rails.container"
	path := filepath.Join(qdir, unit)
	stale := bytes.Replace(p.Units[unit], []byte("\nImage="), []byte("\nImage=old\nX-Old="), 1)
	if err := os.WriteFile(path, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	d := mustApply(t, p, ws, qdir, false)
	if !slices.Equal(d.Changed, []string{unit}) || len(d.Added)+len(d.Removed) != 0 {
		t.Errorf("Diff = %+v; want only %s changed", d, unit)
	}
	if !strings.Contains(d.Text, "--- a/"+unit+"\n+++ b/"+unit+"\n") || !strings.Contains(d.Text, "\n-Image=old\n") {
		t.Errorf("Text:\n%s", d.Text)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, p.Units[unit]) {
		t.Errorf("%s not rewritten", unit)
	}
}

func TestApplyOrphanUnits(t *testing.T) {
	p, ws, qdir := filialPlan(t)
	gone := filepath.Join(qdir, "filial-gone-rails.container")
	billing := filepath.Join(qdir, "billing-gone-rails.container")
	hand := filepath.Join(qdir, "filial-x.container")
	writeFile(t, gone, markedUnit("filial", "gone"))
	writeFile(t, billing, markedUnit("billing", "gone"))
	writeFile(t, hand, "[Container]\nImage=docker.io/library/alpine:3\n")

	d := mustApply(t, p, ws, qdir, false)
	if fileExists(t, gone) {
		t.Error("the orphan filial-gone-rails.container was not removed")
	}
	if !slices.Contains(d.Removed, "filial-gone-rails.container") {
		t.Errorf("Removed = %v; want filial-gone-rails.container", d.Removed)
	}
	if !strings.Contains(d.Text, "-Image=x\n") {
		t.Errorf("Text lacks the removed unit:\n%s", d.Text)
	}
	if got, _ := os.ReadFile(billing); string(got) != markedUnit("billing", "gone") {
		t.Errorf("billing unit touched: %q", got)
	}
	if !fileExists(t, hand) {
		t.Error("the hand-written filial-x.container was removed")
	}
	if slices.Contains(d.Removed, "billing-gone-rails.container") || slices.Contains(d.Removed, "filial-x.container") {
		t.Errorf("Removed = %v", d.Removed)
	}
}

func TestApplyOrphanEnvDir(t *testing.T) {
	p, ws, qdir := filialPlan(t)
	dir := filepath.Join(ws, ".railswitch", "env", "gone")
	writeFile(t, filepath.Join(dir, "rails.env"), "SECRET=x\n")
	d := mustApply(t, p, ws, qdir, false)
	if fileExists(t, dir) {
		t.Error("the env dir of the unregistered slug gone was not removed")
	}
	if !slices.Contains(d.Removed, ".railswitch/env/gone") {
		t.Errorf("Removed = %v; want .railswitch/env/gone", d.Removed)
	}
	if strings.Contains(d.Text, "SECRET=x") {
		t.Errorf("Text shows an env file's contents:\n%s", d.Text)
	}
	if !fileExists(t, filepath.Join(ws, ".railswitch", "env", "master", "rails.env")) {
		t.Error("master's env file missing")
	}
}

func TestApplyEnvFileModes(t *testing.T) {
	p, ws, qdir := filialPlan(t)
	// A pre-existing env dir that is too open is tightened.
	if err := os.MkdirAll(filepath.Join(ws, ".railswitch", "env", "master"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustApply(t, p, ws, qdir, false)
	n := 0
	for rel := range p.Files {
		if !strings.HasPrefix(rel, ".railswitch/env/") {
			continue
		}
		n++
		path := filepath.Join(ws, rel)
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v (%v), want 0600", rel, fi.Mode().Perm(), err)
		}
		if fi, err := os.Stat(filepath.Dir(path)); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: dir mode %v (%v), want 0700", rel, fi.Mode().Perm(), err)
		}
	}
	if n == 0 {
		t.Fatal("the plan has no env files")
	}
	data, err := os.ReadFile(filepath.Join(ws, ".railswitch", "env", "master", "rails.env"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(strings.Split(string(data), "\n"), "DATABASE_URL=postgres://postgres:s3cret@db/filial_dev") {
		t.Errorf("rails.env lacks DATABASE_URL verbatim:\n%s", data)
	}
}

func TestPlanAssets(t *testing.T) {
	p, _, _ := filialPlan(t)
	for rel, src := range map[string]string{
		".railswitch/assets/rails/entrypoints/entrypoint-app.sh": "packs/rails/entrypoints/entrypoint-app.sh",
		".railswitch/assets/postgres/initdb/restore-dump.sh":     "options/postgres/initdb/restore-dump.sh",
	} {
		f, ok := p.Files[rel]
		if !ok {
			t.Errorf("Plan has no %s", rel)
			continue
		}
		want, err := fs.ReadFile(builtin.FS, src)
		if err != nil {
			t.Fatal(err)
		}
		if f.Mode != 0o755 || !bytes.Equal(f.Data, want) {
			t.Errorf("%s: mode %v, data %d bytes; want 0755 and the embedded %s", rel, f.Mode, len(f.Data), src)
		}
	}
	if _, ok := p.Files[".railswitch/env/feat/db.env"]; !ok {
		t.Error("Plan has no .railswitch/env/feat/db.env")
	}
}

func TestApplyCheckWritesNothing(t *testing.T) {
	p, ws, qdir := filialPlan(t)
	gone := filepath.Join(qdir, "filial-gone-rails.container")
	writeFile(t, gone, markedUnit("filial", "gone"))
	d := mustApply(t, p, ws, qdir, true)
	if !strings.Contains(d.Text, "+Image=") {
		t.Errorf("Text lacks +Image=:\n%s", d.Text)
	}
	if want := planNames(p); !slices.Equal(d.Added, want) || !slices.Equal(d.Removed, []string{"filial-gone-rails.container"}) {
		t.Errorf("Added = %v, Removed = %v", d.Added, d.Removed)
	}
	if strings.Contains(d.Text, "s3cret") {
		t.Error("Text shows an env file's contents")
	}
	entries, err := os.ReadDir(qdir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !fileExists(t, gone) {
		t.Errorf("check mode changed the Quadlet dir: %v", entries)
	}
	if fileExists(t, filepath.Join(ws, ".railswitch")) {
		t.Error("check mode created .railswitch")
	}
}

// tempName matches *.tmp and tomlfile.WriteAtomic's temp files (".<name>.<random>").
var tempName = regexp.MustCompile(`\.tmp$|^\..+\.\d+$`)

func TestApplyPartialFailure(t *testing.T) {
	p, ws, qdir := filialPlan(t)
	if err := os.Chmod(qdir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(qdir, 0o755) })
	if _, err := Apply(p, ws, qdir, "filial", false); err == nil {
		t.Fatal("Apply into a read-only Quadlet dir succeeded")
	}
	for _, root := range []string{ws, qdir} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if tempName.MatchString(d.Name()) {
				t.Errorf("temp file left behind: %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if entries, _ := os.ReadDir(qdir); len(entries) != 0 {
		t.Errorf("Quadlet dir after the failure: %v", entries)
	}

	if err := os.Chmod(qdir, 0o755); err != nil {
		t.Fatal(err)
	}
	d := mustApply(t, p, ws, qdir, false)
	for name, data := range p.Units {
		if got, _ := os.ReadFile(filepath.Join(qdir, name)); !bytes.Equal(got, data) {
			t.Errorf("%s not written by the second run", name)
		}
	}
	if len(d.Added) != len(p.Units) {
		t.Errorf("second run Added = %v; want every unit", d.Added)
	}
}
