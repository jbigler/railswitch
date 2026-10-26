package machine

import (
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func newRegistry(t *testing.T, prefixes ...string) *Registry {
	t.Helper()
	r := &Registry{Schema: SchemaVersion}
	for _, p := range prefixes {
		if _, err := r.AddProject(p, "/ws/"+p, nil); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func addWT(t *testing.T, r *Registry, prefix, slug string) WorktreeEntry {
	t.Helper()
	w, err := r.AddWorktree(prefix, slug, nil)
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

func TestWorktreeIDLowestFree(t *testing.T) {
	r := newRegistry(t, "a", "b")
	addWT(t, r, "a", "master")
	addWT(t, r, "b", "master")
	addWT(t, r, "a", "feat")
	if err := r.RemoveWorktree("b", "master"); err != nil {
		t.Fatal(err)
	}
	if w := addWT(t, r, "b", "other"); w.ID != 2 {
		t.Errorf("ID after removing 2 = %d; want 2 (lowest free)", w.ID)
	}
	if w := addWT(t, r, "b", "next"); w.ID != 4 {
		t.Errorf("next ID = %d; want 4", w.ID)
	}
	if got := r.WorktreesOf("a"); len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Errorf("WorktreesOf(a) = %+v", got)
	}
}

// TestWorktreeIDCeiling: IDs stay in 1..99 so per-worktree port ranges
// (playwright 18888+ID, storage 19000+ID) never overlap; the 100th
// worktree is refused.
func TestWorktreeIDCeiling(t *testing.T) {
	r := newRegistry(t, "a")
	for i := 1; i <= 99; i++ {
		addWT(t, r, "a", fmt.Sprintf("w%d", i))
	}
	_, err := r.AddWorktree("a", "w100", nil)
	if err == nil || !strings.Contains(err.Error(), "at most 99 worktrees per machine; remove one with railswitch wt rm") {
		t.Fatalf("100th AddWorktree = %v", err)
	}
	if err := r.RemoveWorktree("a", "w50"); err != nil {
		t.Fatal(err)
	}
	if w := addWT(t, r, "a", "w100"); w.ID != 50 {
		t.Errorf("ID = %d; want 50", w.ID)
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
	if _, err := r.AddWorktree("a", "master", nil); !errors.Is(err, ErrSlugTaken) {
		t.Errorf("second AddWorktree(a, master) = %v; want ErrSlugTaken", err)
	}
	addWT(t, r, "b", "master") // same slug, other project: fine
}

func TestAddWorktreeUnknownProject(t *testing.T) {
	r := newRegistry(t)
	if _, err := r.AddWorktree("nope", "master", nil); err == nil {
		t.Error("AddWorktree on an unregistered project succeeded")
	}
}

func TestPrefixTaken(t *testing.T) {
	r := newRegistry(t, "a")
	if _, err := r.AddProject("a", "/elsewhere", nil); !errors.Is(err, ErrPrefixTaken) {
		t.Errorf("second AddProject(a) = %v; want ErrPrefixTaken", err)
	}
}

func TestRemoveProject(t *testing.T) {
	r := newRegistry(t, "api", "apiv2")
	addWT(t, r, "api", "master")
	addWT(t, r, "apiv2", "master")
	if err := r.RemoveProject("api"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Project("api"); ok || len(r.WorktreesOf("api")) != 0 {
		t.Errorf("api is still registered: %+v", r)
	}
	if _, ok := r.Project("apiv2"); !ok || len(r.WorktreesOf("apiv2")) != 1 {
		t.Errorf("apiv2 was touched: %+v", r)
	}
	if err := r.RemoveProject("api"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("second RemoveProject(api) = %v; want ErrNotExist", err)
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
	if _, err := r.AddProject("last", "/ws", nil); err == nil {
		t.Error("AddProject with a full pool succeeded")
	}
}

// TestMachineSubnetReserved: 10.214.255.0/24 belongs to the machine network
// and is never handed to a project.
func TestMachineSubnetReserved(t *testing.T) {
	r := &Registry{Schema: SchemaVersion}
	for n := range MachineSubnet {
		r.Projects = append(r.Projects, ProjectEntry{Prefix: fmt.Sprintf("p%d", n), Subnet: n})
	}
	if e, err := r.AddProject("last", "/ws", nil); err == nil {
		t.Errorf("AddProject with 0..254 used = %+v; want an error (255 is the machine's)", e)
	}
	r = &Registry{Schema: SchemaVersion}
	e, err := r.AddProject("b", "/ws/b", []netip.Prefix{netip.MustParsePrefix("10.214.255.0/24")})
	if err != nil || e.Subnet != 0 {
		t.Errorf("AddProject with 10.214.255.0/24 taken = %+v, %v; want subnet 0", e, err)
	}
}

func TestAddWorktreeSkipsBusy(t *testing.T) {
	for _, c := range []struct {
		busy map[int]bool
		want int
	}{
		{map[int]bool{13001: true}, 2},
		{map[int]bool{13001: true, 19002: true}, 3},
	} {
		r := newRegistry(t, "a")
		if err := r.SetPorts("a", map[string]int{"app": 13000, "s3": 19000}); err != nil {
			t.Fatal(err)
		}
		w, err := r.AddWorktree("a", "master", c.busy)
		if err != nil || w.ID != c.want {
			t.Errorf("busy %v: AddWorktree = %+v, %v; want ID %d", c.busy, w, err, c.want)
		}
	}
}

// TestAddWorktreeAllBusy: no free ID names the first blocking port.
func TestAddWorktreeAllBusy(t *testing.T) {
	r := newRegistry(t, "a")
	if err := r.SetPorts("a", map[string]int{"app": 13000}); err != nil {
		t.Fatal(err)
	}
	busy := map[int]bool{}
	for id := 1; id <= MaxWorktreeID; id++ {
		busy[13000+id] = true
	}
	_, err := r.AddWorktree("a", "master", busy)
	if err == nil || !strings.Contains(err.Error(), "port 13001") {
		t.Errorf("AddWorktree = %v; want an error naming port 13001", err)
	}
}

func TestSetPortsConflict(t *testing.T) {
	r := newRegistry(t, "a", "b")
	if err := r.SetPorts("a", map[string]int{"app": 13000}); err != nil {
		t.Fatal(err)
	}
	err := r.SetPorts("b", map[string]int{"x": 13050})
	if err == nil {
		t.Fatal("SetPorts(b, x=13050) succeeded within 99 of a's 13000")
	}
	if want := "project b: port x base 13050 is less than 100 from project a's port app base 13000, so their base+ID ranges overlap"; err.Error() != want {
		t.Errorf("error = %q; want %q", err, want)
	}
	if pe, _ := r.Project("b"); pe.Ports != nil {
		t.Errorf("b ports = %v after a refused SetPorts", pe.Ports)
	}
	if err := r.SetPorts("b", map[string]int{"app": 13000}); err != nil {
		t.Errorf("SetPorts(b, app=13000), the same base as a: %v", err)
	}
	if pe, _ := r.Project("b"); pe.Ports["app"] != 13000 {
		t.Errorf("b ports = %v", pe.Ports)
	}
}

func TestSetPortsOwnSpacing(t *testing.T) {
	r := newRegistry(t, "a")
	err := r.SetPorts("a", map[string]int{"app": 13000, "x": 13050})
	if err == nil {
		t.Fatal("SetPorts with two own bases 50 apart succeeded")
	}
	if want := "project a: port app base 13000 and port x base 13050 are less than 100 apart, so their base+ID ranges overlap"; err.Error() != want {
		t.Errorf("error = %q; want %q", err, want)
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
	if _, err := l.AddProject("filial", "/home/u/filial", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := l.AddWorktree("filial", "master", nil); err != nil {
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
	if err := os.WriteFile(filepath.Join(dir, "registry.toml"), []byte("schema = 2\nproject = \"changed type\"\n"), 0o600); err != nil {
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

func TestOpenMissingSchema(t *testing.T) {
	for name, src := range map[string]string{"empty": "", "no schema": "[[project]]\nprefix = \"a\"\nsubnet = 0\n"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "registry.toml"), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		l, err := Open(dir)
		if err == nil {
			l.Close()
			t.Errorf("%s: Open accepted a registry.toml without schema = 1", name)
		} else if !strings.Contains(err.Error(), "schema") {
			t.Errorf("%s: Open = %v; want a schema error", name, err)
		}
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

func TestAddProjectSkipsTaken(t *testing.T) {
	r := newRegistry(t, "a") // a on 0
	e, err := r.AddProject("b", "/ws/b", []netip.Prefix{netip.MustParsePrefix("10.214.1.0/24"), netip.MustParsePrefix("10.88.0.0/16")})
	if err != nil || e.Subnet != 2 {
		t.Fatalf("AddProject(b) = %+v, %v; want subnet 2", e, err)
	}
	r = &Registry{Schema: SchemaVersion}
	e, err = r.AddProject("demo", "/ws/demo", []netip.Prefix{netip.MustParsePrefix("10.214.0.0/24")})
	if err != nil || e.Subnet != 1 {
		t.Fatalf("AddProject(demo) = %+v, %v; want subnet 1", e, err)
	}
}

func TestAddProjectTakenPool(t *testing.T) {
	r := &Registry{Schema: SchemaVersion}
	_, err := r.AddProject("demo", "/ws/demo", []netip.Prefix{netip.MustParsePrefix("10.214.0.0/16")})
	if err == nil || !strings.Contains(err.Error(), "10.214.0.0/16") || !strings.Contains(err.Error(), "podman network") {
		t.Fatalf("err = %v; want one naming the taken 10.214.0.0/16", err)
	}
	if len(r.Projects) != 0 {
		t.Fatalf("Projects = %+v", r.Projects)
	}
}

func TestMoveSubnet(t *testing.T) {
	r := newRegistry(t, "demo", "b") // demo 0, b 1
	old, n, err := r.MoveSubnet("demo", []netip.Prefix{netip.MustParsePrefix("10.214.0.0/24"), netip.MustParsePrefix("10.214.2.0/23")})
	if err != nil || old != 0 || n != 4 {
		t.Fatalf("MoveSubnet = %d, %d, %v; want 0, 4", old, n, err)
	}
	if e, _ := r.Project("demo"); e.Subnet != 4 {
		t.Fatalf("demo = %+v", e)
	}
	if e, _ := r.Project("b"); e.Subnet != 1 {
		t.Fatalf("b moved: %+v", e)
	}
	if _, _, err := r.MoveSubnet("nope", nil); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unknown project: %v", err)
	}
	if _, _, err := r.MoveSubnet("demo", []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}); err == nil || !strings.Contains(err.Error(), "10.0.0.0/8") {
		t.Fatalf("full pool: %v", err)
	}
	if e, _ := r.Project("demo"); e.Subnet != 4 {
		t.Fatalf("failed move changed demo: %+v", e)
	}
}

func TestSubnetPrefix(t *testing.T) {
	if got := SubnetPrefix(3); got.String() != Subnet(3) {
		t.Fatalf("SubnetPrefix(3) = %v", got)
	}
}

func TestSubnetUsers(t *testing.T) {
	nets := map[string][]netip.Prefix{
		"filial_proxy": {netip.MustParsePrefix("10.214.0.0/24")},
		"old_proxy":    {netip.MustParsePrefix("10.214.0.0/24")},
		"wide":         {netip.MustParsePrefix("fd00::/64"), netip.MustParsePrefix("10.0.0.0/8")},
		"podman":       {netip.MustParsePrefix("10.88.0.0/16")},
	}
	if got := SubnetUsers(nets, "filial_proxy", 0); !slices.Equal(got, []string{"old_proxy", "wide"}) {
		t.Fatalf("SubnetUsers = %v", got)
	}
}

func TestShareRoundTrip(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.AddProject("filial", "/home/u/filial", nil); err != nil {
		t.Fatal(err)
	}
	addWT(t, l.Registry, "filial", "master")
	addWT(t, l.Registry, "filial", "feat")
	if err := l.SetShare("filial", "nope", &ShareEntry{Host: "h", IP: "100.64.0.7"}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("SetShare of an unregistered slug = %v; want fs.ErrNotExist", err)
	}
	if err := l.SetShare("filial", "feat", &ShareEntry{Host: "box.tail1234.ts.net", IP: "100.64.0.7", Ports: []int{13002, 19002}}); err != nil {
		t.Fatal(err)
	}
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	l.Close()
	data, err := os.ReadFile(filepath.Join(dir, "registry.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "[worktree.share]"); n != 1 {
		t.Errorf("registry.toml has %d share tables; want 1 (an unshared worktree has none):\n%s", n, data)
	}
	r, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	w := r.WorktreesOf("filial")
	if w[0].Share != nil || w[1].Share == nil || !reflect.DeepEqual(*w[1].Share, ShareEntry{Host: "box.tail1234.ts.net", IP: "100.64.0.7", Ports: []int{13002, 19002}}) {
		t.Errorf("shares = %+v, %+v", w[0].Share, w[1].Share)
	}
	if err := r.SetShare("filial", "feat", nil); err != nil || r.WorktreesOf("filial")[1].Share != nil {
		t.Errorf("SetShare nil: %v, %+v", err, r.WorktreesOf("filial")[1].Share)
	}
}

func TestNetworks(t *testing.T) {
	r := newRegistry(t, "b", "a")
	addWT(t, r, "a", "main")
	addWT(t, r, "b", "main")
	addWT(t, r, "a", "feat")
	if got, want := r.Networks(""), []string{"railswitch_machine", "a_proxy", "a-feat-dev", "a-main-dev", "b_proxy", "b-main-dev"}; !slices.Equal(got, want) {
		t.Errorf("Networks(\"\") = %q; want %q", got, want)
	}
	if got, want := r.Networks("a"), []string{"a_proxy", "a-feat-dev", "a-main-dev"}; !slices.Equal(got, want) {
		t.Errorf("Networks(a) = %q; want %q", got, want)
	}
}
