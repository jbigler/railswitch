package cli

import (
	"encoding/json"
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
