package config

import (
	"errors"
	"testing"
)

func TestSetMemory(t *testing.T) {
	base := "schema = 1\n\n[project]\nprefix = \"demo\"\n"
	on, err := SetMemory(base, true)
	if err != nil || on != base+"\n[memory]\n" {
		t.Fatalf("on: %q, %v", on, err)
	}
	if again, _ := SetMemory(on, true); again != on {
		t.Errorf("on twice: %q", again)
	}
	g, err := SetMemoryGuidance(on, "No \"PHI\".")
	if err != nil || g != base+"\n[memory]\nguidance = \"No \\\"PHI\\\".\"\n" {
		t.Fatalf("guidance: %q, %v", g, err)
	}
	if p, err := Parse([]byte(g)); err != nil || p.Memory == nil || p.Memory.Guidance != `No "PHI".` {
		t.Fatalf("parse: %+v, %v", p, err)
	}
	if back, err := SetMemoryGuidance(g, ""); err != nil || back != on {
		t.Errorf("guidance removed: %q, %v", back, err)
	}
	if off, err := SetMemory(g+"\n[env]\nTZ = \"UTC\"\n", false); err != nil || off != base+"\n\n[env]\nTZ = \"UTC\"\n" {
		t.Errorf("off: %q, %v", off, err)
	}
	if fresh, err := SetMemoryGuidance(base, "x"); err != nil || fresh != base+"\n[memory]\nguidance = \"x\"\n" {
		t.Errorf("guidance without [memory]: %q, %v", fresh, err)
	}
	var be *BlockedError
	if _, err := SetMemory("schema = 1\nmemory = {guidance = \"x\"}\n", false); !errors.As(err, &be) {
		t.Errorf("inline memory: %v; want a BlockedError", err)
	}
}
