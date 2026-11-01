package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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

// railswitch-notes prints the rule, then each .md note's file and first
// line, newest first; other files are left out.
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
	for name, body := range map[string]string{
		"test-db.md":  "# Run db:prepare after a schema change\ndetails\n",
		"x.txt":       "not a note\n",
		"MEMORY.md":   "- [Old](old.md)\n",
		"imported.md": "---\nname: ci-cache\ndescription: CI caches gems per Ruby version\nmetadata:\n  type: project\n---\n\nbody\n",
		"named.md":    "---\nname: only-a-name\n---\nbody\n",
	} {
		writeFile(t, filepath.Join(dir, name), body)
	}
	out := run()
	for _, want := range []string{"- test-db.md: Run db:prepare after a schema change\n", "- imported.md: CI caches gems per Ruby version\n", "- named.md: only-a-name\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("index lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "x.txt") || strings.Contains(out, "MEMORY.md") || strings.Contains(out, "no notes yet") {
		t.Errorf("index:\n%s", out)
	}
}
