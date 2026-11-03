package office

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	static := fstest.MapFS{
		"office/sets/cyberpunk/set.toml":       {Data: []byte(strings.Replace(goodToml, `name = "cabin"`, `name = "cyberpunk"`, 1))},
		"office/sets/cyberpunk/Furn_Desk.gltf": {Data: []byte(gltfBox(deskMin, deskMax, "Furn_Desk.bin"))},
	}
	return &Store{Data: t.TempDir(), Static: static, List: testList()}
}

// Review focus: re-adding a set replaces it whole and leaves no temp dirs.
func TestAddReplaceAtomic(t *testing.T) {
	s := testStore(t)
	v1 := goodSet()
	v1["tex/old.png"] = "x"
	v1["Furn_Desk.gltf"] = strings.Replace(gltfBox(deskMin, deskMax, "Furn_Desk.bin"), `"buffers"`, `"images":[{"uri":"tex/old.png"}],"buffers"`, 1)
	if r, err := s.Add(writeSet(t, v1)); err != nil || !r.OK() {
		t.Fatalf("add v1: %v %+v", err, r)
	}
	v2 := goodSet()
	v2["set.toml"] = strings.Replace(goodToml, "version = 3", "version = 4", 1)
	if _, err := s.Add(writeSet(t, v2)); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(s.Base())
	if len(entries) != 1 || entries[0].Name() != "cabin" {
		t.Fatalf("office dir: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(s.Base(), "cabin", "tex", "old.png")); err == nil {
		t.Error("v1's texture survived the replace")
	}
	e, ok := s.Get("cabin")
	if !ok || e.Version != 4 || e.Status != "ok" || e.Present != 1 || e.Missing != 1 || e.Source != "installed" {
		t.Errorf("cabin: %+v", e)
	}
	fi, _ := os.Stat(filepath.Join(s.Base(), "cabin", "Furn_Desk.bin"))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", fi.Mode())
	}
}

func TestAddRefuses(t *testing.T) {
	s := testStore(t)
	bad := goodSet()
	bad["notes.txt"] = "stray"
	if r, err := s.Add(writeSet(t, bad)); err != nil || r.OK() {
		t.Errorf("a refused set: %v ok=%v", err, r.OK())
	}
	builtin := goodSet()
	builtin["set.toml"] = strings.Replace(goodToml, `name = "cabin"`, `name = "cyberpunk"`, 1)
	if _, err := s.Add(writeSet(t, builtin)); err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Errorf("a built-in name: %v", err)
	}
	if entries, _ := os.ReadDir(s.Base()); len(entries) != 0 {
		t.Errorf("refused adds left %v", entries)
	}
}

func TestLsStatus(t *testing.T) {
	s := testStore(t)
	if _, err := s.Add(writeSet(t, goodSet())); err != nil {
		t.Fatal(err)
	}
	status := func() map[string]Entry {
		t.Helper()
		ls, err := s.Ls()
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]Entry{}
		for _, e := range ls {
			m[e.Name] = e
		}
		return m
	}
	m := status()
	if m["cyberpunk"].Status != "ok" || m["cyberpunk"].Source != "builtin" || len(m["cyberpunk"].Have) != 1 || m["cabin"].Status != "ok" || len(m["cabin"].Have) != 1 {
		t.Fatalf("ls: %+v", m)
	}
	s.List.Version = 2 // a newer piece list
	if m := status(); m["cabin"].Status != "stale" || m["cyberpunk"].Status != "stale" {
		t.Errorf("after a list bump: %+v", m)
	}
	s.List.Version = 1
	if err := os.WriteFile(filepath.Join(s.Base(), "cabin", "Furn_Desk.bin"), []byte("edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	if m := status(); m["cabin"].Status != "broken" || !strings.Contains(m["cabin"].Reason, "changed since") {
		t.Errorf("after an edit: %+v", m["cabin"])
	}
	if err := os.RemoveAll(filepath.Join(s.Base(), "cabin")); err != nil {
		t.Fatal(err)
	}
	if _, ok := status()["cabin"]; ok {
		t.Error("a set removed by hand is still listed")
	}
}

func TestRemove(t *testing.T) {
	s := testStore(t)
	if _, err := s.Add(writeSet(t, goodSet())); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("cyberpunk"); err == nil {
		t.Error("removed a built-in")
	}
	if err := s.Remove("../x"); err == nil {
		t.Error("removed a bad name")
	}
	if err := s.Remove("cabin"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("cabin"); ok {
		t.Error("cabin still there")
	}
	if err := s.Remove("cabin"); err == nil {
		t.Error("removed it twice")
	}
}
