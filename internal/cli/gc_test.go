package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/lockstore"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/sys"
)

func TestGCImageVerdict(t *testing.T) {
	keep := gcKeep{tags: map[string]bool{"localhost/shop/base:ruby3-aaaaaaaa": true}, prefixes: map[string]bool{"shop": true, "odd": true}, blocked: map[string]bool{"odd": true}}
	for _, c := range []struct {
		name    string
		img     podman.Image
		verdict string
	}{
		{"localhost/shop/base:ruby3-aaaaaaaa", podman.Image{}, ""},              // current
		{"localhost/shop/base:ruby3-bbbbbbbb", podman.Image{}, "remove"},        // an older build
		{"localhost/shop/base:ruby3-cccccccc", podman.Image{Containers: 1}, ""}, // a container uses it
		{"localhost/odd/base:ruby3-bbbbbbbb", podman.Image{}, ""},               // its project doesn't load
		{"localhost/gone/base:ruby3-bbbbbbbb", podman.Image{Labels: map[string]string{podman.PrefixLabel: "gone"}}, "remove"},
		{"localhost/gone/base:ruby3-bbbbbbbb", podman.Image{}, "unsure"}, // no label: maybe yours
		{"localhost/mine:latest", podman.Image{}, ""},                    // not railswitch's shape
		{"docker.io/library/postgres:16", podman.Image{}, ""},
	} {
		if got := gcImageVerdict(c.name, c.img, keep); got != c.verdict {
			t.Errorf("%s %+v: %q, want %q", c.name, c.img, got, c.verdict)
		}
	}
}

// gc removes an older build and an unreferenced stored copy, keeps the
// current build, a used copy and anything a container uses, and removes
// volumes only with --volumes.
func TestGC(t *testing.T) {
	ws, dirs := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	var list struct{ Steps []struct{ Tag string } }
	if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "-w", "master", "--json", "containerfile", "list")), &list); err != nil {
		t.Fatal(err)
	}
	current := list.Steps[0].Tag
	old := strings.TrimSuffix(current, current[len(current)-8:]) + "deadbeef"
	store := lockstore.Dir(dirs)
	stale := filepath.Join(store, strings.Repeat("a", 64))
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stale, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	f := unitsFake()
	f.On([]string{"podman", "images", "--format", "json"}, sys.Result{Stdout: []byte(fmt.Sprintf(`[{"Id":"1","Names":[%q],"Size":1000,"Containers":0},{"Id":"2","Names":[%q],"Size":2000,"Containers":0},{"Id":"2","Names":[%q],"Size":2000,"Containers":0}]`, current, old, old))}, nil)
	// A removed worktree's database, another tool's volume of the same
	// prefix (a compose project), and an unrelated one.
	f.On([]string{"podman", "volume", "ls"}, sys.Result{Stdout: []byte("filial-gone-db-data-pg16\nfilial_compose_data\nother_vol\n")}, nil)
	f.On([]string{"podman", "rmi"}, sys.Result{}, nil)
	f.On([]string{"podman", "volume", "rm"}, sys.Result{}, nil)
	code, out, errs := runWith(t, f, "--json", "gc")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var doc gcDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Images) != 1 || doc.Images[0].Name != old || doc.Bytes != 2000 {
		t.Errorf("images %+v bytes %d", doc.Images, doc.Bytes)
	}
	if !slices.Equal(doc.Store, []string{strings.Repeat("a", 64)}) {
		t.Errorf("store %v", doc.Store)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("the stale stored copy is still there")
	}
	ents, _ := os.ReadDir(store)
	if len(ents) == 0 {
		t.Error("gc removed the copies the lock pins")
	}
	if !slices.Equal(doc.Volumes, []string{"filial-gone-db-data-pg16"}) {
		t.Errorf("volumes %v", doc.Volumes)
	}
	var rmi, vrm int
	for _, c := range f.Calls {
		switch {
		case slices.Equal(c, []string{"podman", "rmi", old}):
			rmi++
		case len(c) > 2 && c[1] == "rmi":
			t.Errorf("removed %v", c)
		case len(c) > 2 && c[1] == "volume" && c[2] == "rm":
			vrm++
		}
	}
	if rmi != 1 || vrm != 0 {
		t.Errorf("rmi %d, volume rm %d (no --volumes)", rmi, vrm)
	}
	before := len(f.Calls)
	if code, _, errs := runWith(t, f, "gc", "--volumes"); code != 1 || !strings.Contains(errs, "pass --yes") || !strings.Contains(errs, "filial-gone-db-data-pg16") {
		t.Errorf("--volumes without a terminal: exit %d %s", code, errs)
	}
	for _, c := range f.Calls[before:] {
		if len(c) > 1 && (c[1] == "rmi" || len(c) > 2 && c[2] == "rm") {
			t.Errorf("a refused gc --volumes removed %v", c)
		}
	}
	if code, _, _ := runWith(t, f, "gc", "--volumes", "--yes"); code != 0 || !slices.ContainsFunc(f.Calls, func(c []string) bool {
		return slices.Equal(c, []string{"podman", "volume", "rm", "filial-gone-db-data-pg16"})
	}) {
		t.Errorf("--volumes --yes: exit %d %v", code, f.Calls)
	}
}
