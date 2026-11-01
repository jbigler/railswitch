package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Every agent mounts the shared notes and railswitch-notes; nothing else
// does (D141).
func TestNotesMounts(t *testing.T) {
	m := mustBuild(t, fixtureInputs(t, fixtureWS(t), nil))
	for _, s := range m.Services {
		has := slices.ContainsFunc(s.Mounts, func(mt Mount) bool { return mt.Target == NotesMount && mt.Options == "z" }) &&
			slices.ContainsFunc(s.Mounts, func(mt Mount) bool { return mt.Target == NotesIndex && mt.Options == "ro,z" })
		if has != (s.Role == RoleAgent) {
			t.Errorf("%s (%s) mounts the notes: %v", s.ID, s.Role, has)
		}
	}
}

// railswitch-notes prints the rule, then each .md note's name, newest
// first; other files and an agent's MEMORY.md index are left out.
func TestNotesScript(t *testing.T) {
	home := t.TempDir()
	run := func() string {
		cmd := exec.Command("sh", "-c", notesScript)
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	if out := run(); !strings.Contains(out, "# Project notes") || !strings.Contains(out, "(no notes yet)") {
		t.Errorf("no folder:\n%s", out)
	}
	dir := filepath.Join(home, "notes")
	writeFile(t, filepath.Join(dir, "x.txt"), "not a note\n")
	writeFile(t, filepath.Join(dir, "MEMORY.md"), "- [Old](old.md)\n")
	writeFile(t, filepath.Join(dir, "older_note.md"), "x\n")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "older_note.md"), old, old); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "run_db_prepare_after_a_schema_change.md"), "details\n")
	out := run()
	i, j := strings.Index(out, "- run_db_prepare_after_a_schema_change.md\n"), strings.Index(out, "- older_note.md\n")
	if i < 0 || j < 0 || i > j {
		t.Errorf("listing not newest first:\n%s", out)
	}
	if strings.Contains(out, "x.txt") || strings.Contains(out, "MEMORY.md") || strings.Contains(out, "no notes yet") {
		t.Errorf("index:\n%s", out)
	}
}
