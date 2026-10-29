package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/workspace"
)

// project show offers the packs with the app's service: a local copy of
// rails, never a pack with another app.
func TestProjectShowPackChoices(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "pack", "eject", "rails@1")
	mustRun(t, "-C", ws, "pack", "new", "other")
	// A renamed copy keeps the app service, but sidekiq works with rails
	// by name: never offered.
	mustRun(t, "-C", ws, "pack", "eject", "rails@1", "--as", "renamed")
	editProject(t, ws, `jobs     = "none"`, `jobs     = "sidekiq@1"`)
	var doc projectDoc
	if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "--json", "project", "show")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Pack.Ref != "rails@1" || !slices.Equal(doc.Pack.Choices, []string{"local:rails@1", "rails@1"}) {
		t.Errorf("pack = %+v", doc.Pack)
	}
}

// A pack edit sets [stack] pack, locks and renders: the project then runs
// the local copy.
func TestSettingsApplyPack(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "pack", "eject", "rails@1")
	path := filepath.Join(ws, "project.toml")
	before := readFile(t, path)
	edits := `[{"op":"pack","ref":"local:rails@1"}]`
	code, out, errs := runStdin(t, unitsFake(), edits, "-C", ws, "settings", "apply", "--if-hash", workspace.Hash([]byte(before)))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errs)
	}
	after := readFile(t, path)
	if !strings.Contains(after, `pack = "local:rails@1"`) || strings.Count(after, "\n") != strings.Count(before, "\n") {
		t.Errorf("project.toml:\n%s", after)
	}
	lock, err := config.ReadLock(filepath.Join(ws, "stack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if e := lock.Packs["rails"]; e.Source != "local" {
		t.Errorf("lock pack entry %+v", e)
	}
}

// The newest built-in never counts a library or local package of the same
// name, which a loaded catalog also holds.
func TestNewestBuiltinsSkipsLibrary(t *testing.T) {
	m := map[pack.Ref]bool{{Name: "rails", Version: 1}: true, {Name: "rails", Version: 2, Library: "acme"}: true, {Name: "rails", Version: 3, Local: true}: true}
	for range 20 {
		if got := newestBuiltins(m); len(got) != 1 || got[0] != (pack.Ref{Name: "rails", Version: 1}) {
			t.Fatalf("got %v", got)
		}
	}
}

// Firewall edits set the mode and the allow lists; the rendered app.allow
// lists the host.
func TestSettingsApplyFirewall(t *testing.T) {
	ws, _ := lockedWS(t)
	before := readFile(t, filepath.Join(ws, "project.toml"))
	edits := `[{"op":"firewall.mode","value":"enforce"},{"op":"firewall.allow.add","key":"app","value":"pay.example.com"}]`
	if code, out, errs := runStdin(t, unitsFake(), edits, "-C", ws, "settings", "apply", "--if-hash", workspace.Hash([]byte(before))); code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errs)
	}
	if !strings.Contains(readFile(t, filepath.Join(ws, ".railswitch", "firewall", "app.allow")), "pay.example.com\n") {
		t.Error("app.allow lacks the host")
	}
	var doc projectDoc
	if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "--json", "project", "show")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Firewall.Mode != "enforce" || doc.Firewall.Effective != "enforce" || !slices.Equal(doc.Firewall.Allow["app"], []string{"pay.example.com"}) || doc.Firewall.Allow["agent"] == nil {
		t.Errorf("firewall = %+v", doc.Firewall)
	}
}

// An allow-list edit restarts the running worktree (the hook reads the
// lists only at a container's start), and a bad host is blocked in the
// preview, not only refused by apply.
func TestSettingsPreviewFirewallEdits(t *testing.T) {
	ws, _ := lockedWS(t)
	code, out, errs := runStdin(t, unitsFake(), `[{"op":"firewall.allow.add","key":"app","value":"pay.example.com"}]`, "-C", ws, "--json", "settings", "preview")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var p previewDoc
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Restart, []string{"master"}) {
		t.Errorf("restart %v; want [master]", p.Restart)
	}
	code, out, errs = runStdin(t, unitsFake(), `[{"op":"firewall.allow.add","key":"app","value":"bad host"}]`, "-C", ws, "--json", "settings", "preview")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Edits) != 1 || p.Edits[0].Impact != "blocked" || !strings.Contains(p.Edits[0].Error, "not a host name") {
		t.Errorf("bad host: %+v", p.Edits)
	}
}

// Switching the database option swaps its image addon (D123): the old
// option's addon leaves [image] addons (its file stays, edits and all), the
// new one's is ejected and added; switching back reuses the kept file.
func TestSettingsOptionSwapsAddon(t *testing.T) {
	ws, _ := lockedWS(t)
	pg := filepath.Join(ws, "containerfiles", "Containerfile.pg-client")
	writeFile(t, pg, readFile(t, pg)+"RUN echo mine\n")
	// The fixture holds every option's addon: this one must come from the eject.
	if err := os.Remove(filepath.Join(ws, "containerfiles", "Containerfile.mariadb-client")); err != nil {
		t.Fatal(err)
	}
	apply := func(ref string) {
		t.Helper()
		before := readFile(t, filepath.Join(ws, "project.toml"))
		edits := `[{"op":"option","key":"database","ref":"` + ref + `"}]`
		if code, out, errs := runStdin(t, unitsFake(), edits, "-C", ws, "settings", "apply", "--if-hash", workspace.Hash([]byte(before))); code != 0 {
			t.Fatalf("%s: exit %d: %s\n%s", ref, code, out, errs)
		}
	}
	apply("mariadb@1")
	p, err := config.Load(filepath.Join(ws, "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(p.Image.Addons, "containerfiles/Containerfile.pg-client") || !slices.Contains(p.Image.Addons, "containerfiles/Containerfile.mariadb-client") {
		t.Errorf("addons after mariadb: %v", p.Image.Addons)
	}
	if got := readFile(t, filepath.Join(ws, "containerfiles", "Containerfile.mariadb-client")); !strings.HasPrefix(got, "# Ejected from mariadb@1 Containerfile.mariadb-client") {
		t.Errorf("mariadb-client:\n%s", got)
	}
	if !strings.HasSuffix(readFile(t, pg), "RUN echo mine\n") {
		t.Error("the edited pg-client file was not kept")
	}
	apply("postgres@1")
	if p, err = config.Load(filepath.Join(ws, "project.toml")); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.Image.Addons, "containerfiles/Containerfile.pg-client") || slices.Contains(p.Image.Addons, "containerfiles/Containerfile.mariadb-client") {
		t.Errorf("addons after postgres: %v", p.Image.Addons)
	}
	if !strings.HasSuffix(readFile(t, pg), "RUN echo mine\n") {
		t.Error("switching back replaced your pg-client file")
	}
}

// An edit of another option leaves the addons as you set them: one you
// removed stays removed, one you added stays (D123).
func TestSettingsOtherOptionKeepsAddons(t *testing.T) {
	ws, _ := lockedWS(t)
	proj := filepath.Join(ws, "project.toml")
	writeFile(t, proj, strings.Replace(readFile(t, proj), `addons = ["containerfiles/Containerfile.pg-client"]`, `addons = ["containerfiles/Containerfile.mariadb-client"]`, 1))
	before := readFile(t, proj)
	edits := `[{"op":"option","key":"cache","ref":"none"}]`
	code, out, errs := runStdin(t, unitsFake(), edits, "-C", ws, "--json", "settings", "preview")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var p struct{ Writes []string }
	if err := json.Unmarshal([]byte(out), &p); err != nil || p.Writes == nil || len(p.Writes) != 0 {
		t.Errorf("writes %v (%v)", p.Writes, err)
	}
	if code, _, errs := runStdin(t, unitsFake(), edits, "-C", ws, "settings", "apply", "--if-hash", workspace.Hash([]byte(before))); code != 0 {
		t.Fatalf("apply: exit %d: %s", code, errs)
	}
	c, err := config.Load(proj)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Image.Addons, []string{"containerfiles/Containerfile.mariadb-client"}) {
		t.Errorf("addons %v", c.Image.Addons)
	}
}

// Choosing an option with its own image (playwright) in Settings ejects its
// Containerfile when the workspace has none: `containerfile eject` refuses
// one the stack doesn't use yet, so nothing else could add it.
func TestSettingsOptionEjectsItsImage(t *testing.T) {
	ws, _ := lockedWS(t)
	apply := func(ref string) {
		t.Helper()
		before := readFile(t, filepath.Join(ws, "project.toml"))
		edits := `[{"op":"option","key":"browser","ref":"` + ref + `"}]`
		if code, out, errs := runStdin(t, unitsFake(), edits, "-C", ws, "settings", "apply", "--if-hash", workspace.Hash([]byte(before))); code != 0 {
			t.Fatalf("%s: exit %d: %s\n%s", ref, code, out, errs)
		}
	}
	apply("none")
	pw := filepath.Join(ws, "containerfiles", "Containerfile.playwright")
	if err := os.Remove(pw); err != nil {
		t.Fatal(err)
	}
	apply("playwright@1")
	if got := readFile(t, pw); !strings.HasPrefix(got, "# Ejected from playwright@1 Containerfile.playwright") {
		t.Errorf("Containerfile.playwright:\n%s", got)
	}
	// Choosing it again keeps the file you have.
	writeFile(t, pw, readFile(t, pw)+"RUN echo mine\n")
	apply("none")
	apply("playwright@1")
	if !strings.HasSuffix(readFile(t, pw), "RUN echo mine\n") {
		t.Error("choosing playwright again replaced your Containerfile")
	}
}

// An option chosen again comes back at what the lock accepted (its
// [[removed]] entry), so its ejected Containerfile never carries a change
// made since, which no one has reviewed.
func TestSettingsOptionEjectsAcceptedImage(t *testing.T) {
	ws, _ := lockedWS(t)
	apply := func(ref string) {
		t.Helper()
		before := readFile(t, filepath.Join(ws, "project.toml"))
		edits := `[{"op":"option","key":"browser","ref":"` + ref + `"}]`
		if code, out, errs := runStdin(t, unitsFake(), edits, "-C", ws, "settings", "apply", "--if-hash", workspace.Hash([]byte(before))); code != 0 {
			t.Fatalf("%s: exit %d: %s\n%s", ref, code, out, errs)
		}
	}
	mustRun(t, "-C", ws, "pack", "eject", "playwright@1")
	apply("local:playwright@1")
	apply("none")
	local := filepath.Join(ws, "options", "playwright", "Containerfile.playwright")
	writeFile(t, local, readFile(t, local)+"RUN echo UNREVIEWED\n")
	pw := filepath.Join(ws, "containerfiles", "Containerfile.playwright")
	if err := os.Remove(pw); err != nil {
		t.Fatal(err)
	}
	apply("local:playwright@1")
	if got := readFile(t, pw); strings.Contains(got, "UNREVIEWED") || !strings.HasPrefix(got, "# Ejected from local:playwright@1") {
		t.Errorf("ejected the unreviewed change:\n%s", got)
	}
}
