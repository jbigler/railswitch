package office

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gltfBox is a one-mesh glTF whose POSITION spans lo..hi, its buffer at uri.
func gltfBox(lo, hi [3]float64, uri string) string {
	return fmt.Sprintf(`{"asset":{"version":"2.0"},"scene":0,"scenes":[{"nodes":[0]}],
 "nodes":[{"name":"P","mesh":0}],"meshes":[{"primitives":[{"attributes":{"POSITION":0}}]}],
 "accessors":[{"min":[%g,%g,%g],"max":[%g,%g,%g]}],"buffers":[{"uri":%q,"byteLength":4}]}`,
		lo[0], lo[1], lo[2], hi[0], hi[1], hi[2], uri)
}

const goodToml = `schema = 1
name = "cabin"
version = 3
pieces = 1
description = "Pine, wool and lamplight"
[look]
background = "#1b1612"
bloom = 0
`

var deskMin, deskMax = [3]float64{-1, 0, -0.4}, [3]float64{1, 0.75, 0.4}

func testList() *Pieces {
	return &Pieces{Version: 1, Pieces: map[string]Piece{
		"Furn_Desk": {Min: deskMin, Max: deskMax},
		"Deco_Mug":  {Min: [3]float64{-0.05, 0, -0.05}, Max: [3]float64{0.05, 0.1, 0.05}},
	}}
}

// writeSet writes files (slash path → contents) into a new folder.
func writeSet(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, c := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func goodSet() map[string]string {
	return map[string]string{"set.toml": goodToml, "Furn_Desk.gltf": gltfBox(deskMin, deskMax, "Furn_Desk.bin"), "Furn_Desk.bin": "abcd"}
}

func result(r *Report, piece string) Result {
	for _, x := range r.Results {
		if x.Piece == piece {
			return x
		}
	}
	return Result{}
}

// D153: bounds go through every node's transform, nested; colliders don't count.
func TestBoundsTransforms(t *testing.T) {
	s := math.Sqrt(0.5)
	doc := fmt.Sprintf(`{"scene":0,"scenes":[{"nodes":[0,3]}],"nodes":[
 {"name":"Root","rotation":[%g,0,0,%g],"scale":[2,2,2],"children":[1,2]},
 {"name":"Leaf","mesh":0,"translation":[1,0,0]},
 {"name":"Root_COL","mesh":1},
 {"name":"Other","matrix":[1,0,0,0, 0,1,0,0, 0,0,1,0, 0,5,0,1],"mesh":0}],
 "meshes":[{"primitives":[{"attributes":{"POSITION":0}}]},{"primitives":[{"attributes":{"POSITION":1}}]}],
 "accessors":[{"min":[0,0,0],"max":[1,1,1]},{"min":[-9,-9,-9],"max":[9,9,9]}]}`, -s, s)
	lo, hi, err := Bounds([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	// Root: −90° about X then ×2: (x,y,z) → (2x, 2z, −2y); Leaf's box x 1..2 → 2..4,
	// y 0..1 → z 0..−2, z 0..1 → y 0..2. Other: 0..1 lifted 5.
	want := [2][3]float64{{0, 0, -2}, {4, 6, 1}}
	for k := range 3 {
		if math.Abs(lo[k]-want[0][k]) > 1e-9 || math.Abs(hi[k]-want[1][k]) > 1e-9 {
			t.Fatalf("bounds %v..%v; want %v..%v", lo, hi, want[0], want[1])
		}
	}
	for _, bad := range []string{`{"scenes":[{"nodes":[0]}],"nodes":[{"name":"E"}]}`, `{"scenes":[{"nodes":[0]}],"nodes":[{"mesh":0}],"meshes":[{"primitives":[{"attributes":{"POSITION":0}}]}],"accessors":[{}]}`, `nope`} {
		if _, _, err := Bounds([]byte(bad)); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

func TestCheck(t *testing.T) {
	r, err := Check(writeSet(t, goodSet()), testList())
	if err != nil || !r.OK() || result(r, "Furn_Desk").Status != "ok" || result(r, "Deco_Mug").Status != "missing" || r.Name != "cabin" {
		t.Fatalf("good set: %v %+v", err, r)
	}
	if strings.Join(r.Files, ",") != "Furn_Desk.bin,Furn_Desk.gltf,set.toml" {
		t.Errorf("files %v", r.Files)
	}
	for _, c := range []struct {
		name   string
		edit   func(map[string]string)
		reason string // a refused Furn_Desk's reason, else a folder error
	}{
		{"3 cm too wide", func(f map[string]string) {
			f["Furn_Desk.gltf"] = gltfBox([3]float64{-1.03, 0, -0.4}, deskMax, "Furn_Desk.bin")
		}, "Furn_Desk: x -1.03..1.00, y 0.00..0.75, z -0.40..0.40 m; the list says x -1.00..1.00, y 0.00..0.75, z -0.40..0.40 (±0.02)"},
		{"origin moved 10 cm", func(f map[string]string) {
			f["Furn_Desk.gltf"] = gltfBox([3]float64{-1, 0.1, -0.4}, [3]float64{1, 0.85, 0.4}, "Furn_Desk.bin")
		}, "Furn_Desk: x -1.00..1.00, y 0.10..0.85"},
		{"unknown piece", func(f map[string]string) { f["Furn_Throne.gltf"] = gltfBox(deskMin, deskMax, "Furn_Desk.bin") }, "Furn_Throne: not in the piece list"},
		{"not json", func(f map[string]string) { f["Furn_Desk.gltf"] = "{" }, "Furn_Desk: not a glTF"},
		{"parent URI", func(f map[string]string) { f["Furn_Desk.gltf"] = gltfBox(deskMin, deskMax, "../Furn_Desk.bin") }, `URI "../Furn_Desk.bin" leaves the folder`},
		{"absolute URI", func(f map[string]string) { f["Furn_Desk.gltf"] = gltfBox(deskMin, deskMax, "/etc/passwd") }, "is absolute"},
		{"data URI", func(f map[string]string) {
			f["Furn_Desk.gltf"] = gltfBox(deskMin, deskMax, "data:application/octet-stream;base64,AAAA")
		}, "has a scheme"},
		{"http URI", func(f map[string]string) { f["Furn_Desk.gltf"] = gltfBox(deskMin, deskMax, "http://x/y.bin") }, "has a scheme"},
		{"draco", func(f map[string]string) {
			f["Furn_Desk.gltf"] = strings.Replace(gltfBox(deskMin, deskMax, "Furn_Desk.bin"), `{"asset"`, `{"extensionsRequired":["KHR_draco_mesh_compression"],"asset"`, 1)
		}, "Furn_Desk: compressed (KHR_draco_mesh_compression)"},
		{"meshopt", func(f map[string]string) {
			f["Furn_Desk.gltf"] = strings.Replace(gltfBox(deskMin, deskMax, "Furn_Desk.bin"), `{"asset"`, `{"extensionsRequired":["KHR_materials_emissive_strength","EXT_meshopt_compression"],"asset"`, 1)
		}, "compressed (EXT_meshopt_compression)"},
		{"missing file", func(f map[string]string) { f["Furn_Desk.gltf"] = gltfBox(deskMin, deskMax, "gone.bin") }, "no such file"},
		{"stray file", func(f map[string]string) { f["notes.txt"] = "hi" }, "notes.txt: neither set.toml"},
		{"no set.toml", func(f map[string]string) { delete(f, "set.toml") }, "set.toml: missing"},
		{"bad set.toml", func(f map[string]string) { f["set.toml"] = strings.Replace(goodToml, "bloom = 0", "bloom = 4", 1) }, "look.bloom = 4"},
		{"other piece list", func(f map[string]string) { f["set.toml"] = strings.Replace(goodToml, "pieces = 1", "pieces = 2", 1) }, "built for piece list 2"},
	} {
		f := goodSet()
		c.edit(f)
		r, err := Check(writeSet(t, f), testList())
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		all := strings.Join(r.Errors, "\n")
		for _, x := range r.Results {
			all += "\n" + x.Reason
		}
		if r.OK() || !strings.Contains(all, c.reason) {
			t.Errorf("%s: ok=%v, want %q in\n%s", c.name, r.OK(), c.reason, all)
		}
	}
}

func TestCheckFolderRefusals(t *testing.T) {
	check := func(name, dir, want string) {
		t.Helper()
		r, err := Check(dir, testList())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r.OK() || !strings.Contains(strings.Join(r.Errors, "\n"), want) {
			t.Errorf("%s: want %q in %v", name, want, r.Errors)
		}
	}
	dir := writeSet(t, goodSet())
	if err := os.Symlink("/etc/hostname", filepath.Join(dir, "Deco_Mug.bin")); err != nil {
		t.Fatal(err)
	}
	check("symlinked file", dir, "Deco_Mug.bin: a symbolic link")

	real := writeSet(t, goodSet())
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	check("symlinked folder", link, "is a symbolic link")
	check("symlinked folder, trailing slash", link+"/", "is a symbolic link")

	big := writeSet(t, goodSet())
	if err := os.Truncate(filepath.Join(big, "Furn_Desk.bin"), MaxFileSize+1); err != nil {
		t.Fatal(err)
	}
	check("oversize file", big, "Furn_Desk.bin: 16777217 bytes")

	many := goodSet()
	for i := range MaxFiles {
		many[fmt.Sprintf("tex/%d.png", i)] = ""
	}
	check("too many files", writeSet(t, many), "more than 1000 files")
}

// Review focus: the folder named with a trailing slash or relatively is the same folder.
func TestCheckDirForms(t *testing.T) {
	dir := writeSet(t, goodSet())
	t.Chdir(filepath.Dir(dir))
	want, _ := Check(dir, testList())
	for _, d := range []string{dir + "/", filepath.Base(dir), "./" + filepath.Base(dir) + "/"} {
		got, err := Check(d, testList())
		if err != nil || !got.OK() || strings.Join(got.Files, ",") != strings.Join(want.Files, ",") {
			t.Errorf("%q: %v %+v", d, err, got)
		}
	}
}

func TestParseSet(t *testing.T) {
	if s, err := ParseSet([]byte(goodToml)); err != nil || s.Name != "cabin" || *s.Look.Background != "#1b1612" || *s.Look.Bloom != 0 || s.Look.Fog != nil {
		t.Fatalf("good: %v %+v", err, s)
	}
	for name, edit := range map[string][2]string{
		"unknown key": {"bloom = 0", "bloom = 0\nglow = 1"},
		"short color": {`"#1b1612"`, `"#abc"`},
		"bloom range": {"bloom = 0", "bloom = 4"},
		"newer":       {"schema = 1", "schema = 2"},
		"reserved":    {`name = "cabin"`, `name = "notes"`},
		"bad name":    {`name = "cabin"`, `name = "Cabin"`},
	} {
		if _, err := ParseSet([]byte(strings.Replace(goodToml, edit[0], edit[1], 1))); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// D153: the piece list describes the built-in set exactly: Go's bounds equal
// pieces.py's, and every model has an entry (the generator can't drift).
func TestCyberpunkMatchesPieces(t *testing.T) {
	static := os.DirFS("../web/static")
	list, err := LoadPieces(static)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Check("../web/static/office/sets/cyberpunk", list)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK() || len(r.Results) != len(list.Pieces) {
		b, _ := json.MarshalIndent(r, "", " ")
		t.Fatalf("cyberpunk against pieces.json:\n%s", b)
	}
	for _, x := range r.Results {
		if x.Status != "ok" {
			t.Errorf("%s: %s %s", x.Piece, x.Status, x.Reason)
			continue
		}
		p := list.Pieces[x.Piece]
		for k := range 3 { // pieces.py rounds to 1 mm
			if math.Abs(x.Min[k]-p.Min[k]) > 0.0006 || math.Abs(x.Max[k]-p.Max[k]) > 0.0006 {
				t.Errorf("%s: Go's box %v..%v, pieces.json %v..%v", x.Piece, *x.Min, *x.Max, p.Min, p.Max)
				break
			}
		}
	}
}
