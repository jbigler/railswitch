package pack

import (
	"strings"
	"testing"
)

// TestLinkPlaceholders: a package link's url gets the project-link
// placeholder check at load.
func TestLinkPlaceholders(t *testing.T) {
	fsys := minimal()
	fsys["options/o/option.toml"].Data = append(fsys["options/o/option.toml"].Data,
		[]byte("[[links]]\nlabel = \"X\"\nurl = \"{worktree.hots}/x\"\n")...)
	_, err := Load(fsys)
	if err == nil || !strings.Contains(err.Error(), "options/o/option.toml") || !strings.Contains(err.Error(), "{worktree.hots}") {
		t.Fatalf("Load = %v; want an error naming options/o/option.toml and {worktree.hots}", err)
	}

	fsys = minimal()
	fsys["options/o/option.toml"].Data = append(fsys["options/o/option.toml"].Data,
		[]byte("[[links]]\nlabel = \"X\"\nurl = \"x.{worktree.host}/{worktree.slug}/{branch}\"\n")...)
	if _, err := Load(fsys); err != nil {
		t.Fatalf("Load with allowed placeholders = %v", err)
	}
}

// TestTaskNameLs: a pack task named ls could never run (`task ls` lists
// the tasks): Load refuses it.
func TestTaskNameLs(t *testing.T) {
	fsys := minimal()
	fsys["packs/p/pack.toml"].Data = append(fsys["packs/p/pack.toml"].Data, []byte("\n[tasks]\nls = \"ls -la\"\n")...)
	_, err := Load(fsys)
	if err == nil || !strings.Contains(err.Error(), "packs/p/pack.toml") || !strings.Contains(err.Error(), `tasks.ls: "ls" is a railswitch task subcommand`) {
		t.Fatalf("Load = %v", err)
	}
	fsys = minimal()
	fsys["packs/p/pack.toml"].Data = append(fsys["packs/p/pack.toml"].Data, []byte("\n[tasks]\nlint = \"bin/lint\"\n")...)
	if _, err := Load(fsys); err != nil {
		t.Fatalf("Load with a task = %v", err)
	}
}
