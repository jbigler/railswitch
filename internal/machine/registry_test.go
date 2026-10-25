package machine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func newRegistry(t *testing.T, prefixes ...string) *Registry {
	t.Helper()
	r := &Registry{Schema: SchemaVersion}
	for _, p := range prefixes {
		if _, err := r.AddProject(p, "/ws/"+p); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func addWT(t *testing.T, r *Registry, prefix, slug string) WorktreeEntry {
	t.Helper()
	w, err := r.AddWorktree(prefix, slug)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestWorktreeIDsMachineWide(t *testing.T) {
	r := newRegistry(t, "a", "b")
	for i, c := range []struct{ prefix, slug string }{{"a", "master"}, {"b", "master"}, {"a", "feat"}} {
		if w := addWT(t, r, c.prefix, c.slug); w.ID != i+1 {
			t.Errorf("%s/%s ID = %d; want %d", c.prefix, c.slug, w.ID, i+1)
		}
	}
}

func TestWorktreeIDNotReused(t *testing.T) {
	r := newRegistry(t, "a", "b")
	addWT(t, r, "a", "master")
	addWT(t, r, "b", "master")
	addWT(t, r, "a", "feat")
	if err := r.RemoveWorktree("b", "master"); err != nil {
		t.Fatal(err)
	}
	if w := addWT(t, r, "b", "other"); w.ID != 4 {
		t.Errorf("ID after removing 2 = %d; want 4", w.ID)
	}
	if got := r.WorktreesOf("a"); len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Errorf("WorktreesOf(a) = %+v", got)
	}
}

func TestRemoveWorktreeUnknown(t *testing.T) {
	r := newRegistry(t, "api", "api-v2")
	addWT(t, r, "api-v2", "master")
	if err := r.RemoveWorktree("api", "master"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("RemoveWorktree(api, master) = %v; want ErrNotExist", err)
	}
	if got := r.WorktreesOf("api-v2"); len(got) != 1 {
		t.Errorf("api-v2 worktrees = %+v", got)
	}
}

func TestSlugTaken(t *testing.T) {
	r := newRegistry(t, "a", "b")
	addWT(t, r, "a", "master")
	if _, err := r.AddWorktree("a", "master"); !errors.Is(err, ErrSlugTaken) {
		t.Errorf("second AddWorktree(a, master) = %v; want ErrSlugTaken", err)
	}
	addWT(t, r, "b", "master") // same slug, other project: fine
}

func TestAddWorktreeUnknownProject(t *testing.T) {
	r := newRegistry(t)
	if _, err := r.AddWorktree("nope", "master"); err == nil {
		t.Error("AddWorktree on an unregistered project succeeded")
	}
}

func TestPrefixTaken(t *testing.T) {
	r := newRegistry(t, "a")
	if _, err := r.AddProject("a", "/elsewhere"); !errors.Is(err, ErrPrefixTaken) {
		t.Errorf("second AddProject(a) = %v; want ErrPrefixTaken", err)
	}
}

func TestSubnets(t *testing.T) {
	r := newRegistry(t, "a", "b", "c")
	for i, p := range []string{"a", "b", "c"} {
		e, ok := r.Project(p)
		if !ok || e.Subnet != i {
			t.Errorf("Project(%s) = %+v, %v; want subnet %d", p, e, ok, i)
		}
	}
	if _, ok := r.Project("d"); ok {
		t.Error("Project(d) found")
	}
}

func TestSubnetPoolExhausted(t *testing.T) {
	r := &Registry{Schema: SchemaVersion}
	for n := range 256 {
		r.Projects = append(r.Projects, ProjectEntry{Prefix: fmt.Sprintf("p%d", n), Subnet: n})
	}
	if _, err := r.AddProject("last", "/ws"); err == nil {
		t.Error("AddProject with a full pool succeeded")
	}
}

func TestAddresses(t *testing.T) {
	for got, want := range map[string]string{
		Subnet(3):    "10.214.3.0/24",
		Gateway(3):   "10.214.3.1",
		TraefikIP(3): "10.214.3.2",
		IPRange(3):   "10.214.3.128/25",
	} {
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	}
}

func TestOpenNewFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var head struct {
		Schema int `toml:"schema"`
	}
	if _, err := toml.DecodeFile(filepath.Join(dir, "registry.toml"), &head); err != nil {
		t.Fatal(err)
	}
	if head.Schema != 1 || l.Schema != 1 {
		t.Errorf("schema in file = %d, in memory = %d; want 1", head.Schema, l.Schema)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.AddProject("filial", "/home/u/filial"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.AddWorktree("filial", "master"); err != nil {
		t.Fatal(err)
	}
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	p, ok := l.Project("filial")
	if !ok || p.Workspace != "/home/u/filial" || p.Subnet != 0 {
		t.Errorf("Project = %+v, %v", p, ok)
	}
	if w := l.WorktreesOf("filial"); len(w) != 1 || w[0] != (WorktreeEntry{Prefix: "filial", Slug: "master", ID: 1}) {
		t.Errorf("WorktreesOf = %+v", w)
	}
}

func TestOpenNewerSchema(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "registry.toml"), []byte("schema = 2\nprojects = \"changed type\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Open(dir)
	if err == nil {
		l.Close()
		t.Fatal("Open accepted schema = 2")
	}
	if !strings.Contains(err.Error(), "newer railswitch") {
		t.Errorf("Open = %v; want newer railswitch", err)
	}
	// The lock was released: a second Open does not hang.
	done := make(chan error, 1)
	go func() { _, err := Open(dir); done <- err }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Open after a failed Open blocked")
	}
}

func TestOpenUnknownKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "registry.toml"), []byte("schema = 1\nprojcts = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if l, err := Open(dir); err == nil || !strings.Contains(err.Error(), "projcts") {
		if l != nil {
			l.Close()
		}
		t.Fatalf("Open = %v; want an error naming projcts", err)
	}
}

func TestOpenBlocks(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	opened := make(chan *Locked)
	go func() {
		close(started)
		l, err := Open(dir)
		if err != nil {
			t.Error(err)
		}
		opened <- l
	}()
	<-started
	select {
	case <-opened:
		t.Fatal("second Open returned while the first was held")
	case <-time.After(200 * time.Millisecond):
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case l := <-opened:
		if l != nil {
			l.Close()
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second Open still blocked after Close")
	}
}

func TestDefaultDirs(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	d, err := DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	want := Dirs{Config: "/home/u/.config/railswitch", Data: "/home/u/.local/share/railswitch", Quadlet: "/home/u/.config/containers/systemd"}
	if d != want {
		t.Errorf("DefaultDirs = %+v; want %+v", d, want)
	}
	t.Setenv("XDG_CONFIG_HOME", "/x/cfg")
	t.Setenv("XDG_DATA_HOME", "/x/data")
	d, err = DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	want = Dirs{Config: "/x/cfg/railswitch", Data: "/x/data/railswitch", Quadlet: "/x/cfg/containers/systemd"}
	if d != want {
		t.Errorf("DefaultDirs (XDG) = %+v; want %+v", d, want)
	}
}
