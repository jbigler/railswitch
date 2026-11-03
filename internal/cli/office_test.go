package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/dashconf"
	"github.com/jbigler/railswitch/internal/office"
	"github.com/jbigler/railswitch/internal/sys/systest"
	"github.com/jbigler/railswitch/internal/web"
)

// officeSet writes a set folder named name holding one piece of the real
// list, Furn_Desk, at its listed box.
func officeSet(t *testing.T, name string, pieces int) string {
	t.Helper()
	list, err := office.LoadPieces(web.StaticFS())
	if err != nil {
		t.Fatal(err)
	}
	p := list.Pieces["Furn_Desk"]
	dir := t.TempDir()
	gltf := fmt.Sprintf(`{"asset":{"version":"2.0"},"scene":0,"scenes":[{"nodes":[0]}],"nodes":[{"name":"Furn_Desk","mesh":0}],
 "meshes":[{"primitives":[{"attributes":{"POSITION":0}}]}],"accessors":[{"min":[%g,%g,%g],"max":[%g,%g,%g]}],
 "buffers":[{"uri":"Furn_Desk.bin","byteLength":4}]}`, p.Min[0], p.Min[1], p.Min[2], p.Max[0], p.Max[1], p.Max[2])
	toml := fmt.Sprintf("schema = 1\nname = %q\nversion = 1\npieces = %d\ndescription = \"test\"\n", name, pieces)
	for f, c := range map[string]string{"set.toml": toml, "Furn_Desk.gltf": gltf, "Furn_Desk.bin": "abcd"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// D153: office set check|add|ls|use|rm, against the real piece list.
func TestOfficeSet(t *testing.T) {
	d := dashEnv(t)
	f := &systest.Fake{}
	list, _ := office.LoadPieces(web.StaticFS())
	set := officeSet(t, "cabin", list.Version)

	code, out, _ := runWith(t, f, "--json", "office", "set", "check", set)
	var r office.Report
	if err := json.Unmarshal([]byte(out), &r); code != 0 || err != nil || r.Name != "cabin" || len(r.Results) != len(list.Pieces) {
		t.Fatalf("check: exit %d %v %s", code, err, out)
	}
	if err := os.WriteFile(filepath.Join(set, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runWith(t, f, "office", "set", "check", set); code != 1 || !strings.Contains(out, "stray.txt") {
		t.Errorf("a stray file: exit %d %s", code, out)
	}
	if code, _, _ := runWith(t, f, "office", "set", "add", set); code != 1 {
		t.Errorf("add of a refused set: exit %d", code)
	}
	os.Remove(filepath.Join(set, "stray.txt"))
	if code, out, errs := runWith(t, f, "office", "set", "add", set); code != 0 || !strings.Contains(out, "added cabin") {
		t.Fatalf("add: exit %d %s %s", code, out, errs)
	}

	var ls officeLs
	_, out, _ = runWith(t, f, "--json", "office", "set", "ls")
	if err := json.Unmarshal([]byte(out), &ls); err != nil || ls.Active != "cyberpunk" || len(ls.Sets) != 2 || ls.Sets[0].Name != "cyberpunk" || len(ls.Sets[0].Have) != len(list.Pieces) || ls.Sets[1].Have != nil {
		t.Fatalf("ls: %v %s", err, out)
	}

	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"use", "nope"}, 1, "no set"},
		{[]string{"rm", "cyberpunk"}, 1, "built in"},
		{[]string{"use", "cabin"}, 0, "uses cabin"},
		{[]string{"rm", "cabin"}, 0, "uses cyberpunk again"},
		{[]string{"use"}, 2, "usage"},
		{[]string{"paint"}, 2, "unknown subcommand"},
	} {
		code, out, errs := runWith(t, f, append([]string{"office", "set"}, c.args...)...)
		if code != c.code || !strings.Contains(out+errs, c.want) {
			t.Errorf("%v: exit %d %s%s", c.args, code, out, errs)
		}
		if c.args[0] == "use" && c.code == 0 {
			if conf, _ := dashconf.Load(d); conf.OfficeSet != "cabin" {
				t.Errorf("use: [office] set = %q", conf.OfficeSet)
			}
		}
	}
	if conf, _ := dashconf.Load(d); conf.OfficeSet != "" {
		t.Errorf("rm of the active set left [office] set = %q", conf.OfficeSet)
	}

	// a set built for another piece list is refused at add, and listed stale if it got in
	if code, out, _ := runWith(t, f, "office", "set", "add", officeSet(t, "old", list.Version+1)); code != 1 || !strings.Contains(out, "built for piece list") {
		t.Errorf("a stale set: exit %d %s", code, out)
	}
}
