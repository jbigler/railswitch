package cli

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/workspace"
)

// project show offers the packs with the app's service: a local copy of
// rails, never a pack with another app.
func TestProjectShowPackChoices(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "pack", "eject", "rails@1")
	mustRun(t, "-C", ws, "pack", "new", "other")
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
