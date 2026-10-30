package pack

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestClaudeApplyDefault: the default's items replace the home's (the old
// ones backed up), plugin records one by one, and .claude.json merges with
// the home's projects kept and the default's dropped.
func TestClaudeApplyDefault(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	def, home := t.TempDir(), t.TempDir()
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(p string) string {
		t.Helper()
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	write(filepath.Join(def, ".claude", "settings.json"), `{"theme":"dark"}`)
	write(filepath.Join(def, ".claude", "skills", "new", "SKILL.md"), "new")
	write(filepath.Join(def, ".claude", "plugins", "installed_plugins.json"), `{"plugins":{"p":[]}}`)
	write(filepath.Join(def, ".claude.json"), `{"theme":"x","hasCompletedOnboarding":true,"projects":{"/app":{"from":"default"}}}`)
	write(filepath.Join(home, ".claude", "settings.json"), `{"theme":"light"}`)
	write(filepath.Join(home, ".claude", "skills", "old", "SKILL.md"), "old")
	write(filepath.Join(home, ".claude", "plugins", "known_marketplaces.json"), `{"kept":true}`)
	write(filepath.Join(home, ".claude", "history.jsonl"), "kept")
	write(filepath.Join(home, ".claude.json"), `{"numStartups":3,"projects":{"/app":{"hasTrustDialogAccepted":true}}}`)

	out, err := exec.Command("sh", "builtin/agents/claude/apply-default.sh", def, home).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got := read(filepath.Join(home, ".claude", "settings.json")); got != `{"theme":"dark"}` {
		t.Errorf("settings.json = %s", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "old")); !os.IsNotExist(err) {
		t.Errorf("skills not replaced: %v", err)
	}
	if read(filepath.Join(home, ".claude", "skills", "new", "SKILL.md")) != "new" ||
		read(filepath.Join(home, ".claude", "plugins", "known_marketplaces.json")) != `{"kept":true}` ||
		read(filepath.Join(home, ".claude", "history.jsonl")) != "kept" {
		t.Error("an item the default lacks changed, or a new one is missing")
	}
	if read(filepath.Join(home, ".railswitch-default-backup", ".claude", "settings.json")) != `{"theme":"light"}` ||
		read(filepath.Join(home, ".railswitch-default-backup", ".claude", "skills", "old", "SKILL.md")) != "old" {
		t.Error("replaced items not backed up")
	}
	var cj map[string]any
	if err := json.Unmarshal([]byte(read(filepath.Join(home, ".claude.json"))), &cj); err != nil {
		t.Fatal(err)
	}
	proj, _ := cj["projects"].(map[string]any)["/app"].(map[string]any)
	if cj["theme"] != "x" || cj["hasCompletedOnboarding"] != true || cj["numStartups"] != float64(3) || proj["hasTrustDialogAccepted"] != true || proj["from"] != nil {
		t.Errorf(".claude.json = %v", cj)
	}
}

// TestClaudeMemory: railswitch's instructions go to railswitch.md, which
// CLAUDE.md imports once; the user's lines in CLAUDE.md survive later
// starts; a home from before keeps only its import lines.
func TestClaudeMemory(t *testing.T) {
	src := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(src, []byte("# Container instructions\nuse the browser\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(dir string) {
		t.Helper()
		if out, err := exec.Command("sh", "builtin/agents/claude/memory.sh", src, dir).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	read := func(p string) string {
		t.Helper()
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	fresh := filepath.Join(t.TempDir(), ".claude")
	run(fresh)
	if got := read(filepath.Join(fresh, "CLAUDE.md")); got != "@railswitch.md\n" {
		t.Errorf("fresh CLAUDE.md = %q", got)
	}
	if read(filepath.Join(fresh, "railswitch.md")) != read(src) {
		t.Error("railswitch.md is not the package's")
	}
	// A tool adds its line (rtk init); the next start keeps it, once.
	if err := os.WriteFile(filepath.Join(fresh, "CLAUDE.md"), []byte("@railswitch.md\n@RTK.md\nmy note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(fresh)
	run(fresh)
	if got := read(filepath.Join(fresh, "CLAUDE.md")); got != "@railswitch.md\n@RTK.md\nmy note\n" {
		t.Errorf("CLAUDE.md after restarts = %q", got)
	}

	// An old home: CLAUDE.md is the old copy plus the import a setup just added.
	old := t.TempDir()
	if err := os.WriteFile(filepath.Join(old, "CLAUDE.md"), []byte("# Container instructions\nold text\n@RTK.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(old)
	if got := read(filepath.Join(old, "CLAUDE.md")); got != "@railswitch.md\n@RTK.md\n" {
		t.Errorf("migrated CLAUDE.md = %q", got)
	}
}
