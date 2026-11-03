package web

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/office"
)

const setLsJSON = `{"active": "cabin", "sets": [
 {"name": "cyberpunk", "source": "builtin", "version": 1, "status": "ok", "look": {"bloom": 0.28}, "have": ["Furn_Desk", "Deco_Mug"]},
 {"name": "cabin", "source": "installed", "version": 3, "status": "ok", "look": {"bloom": 0}, "have": ["Furn_Desk"]},
 {"name": "attic", "source": "installed", "version": 1, "status": "broken", "reason": "Furn_Desk.bin: changed since it was added", "look": {}, "have": []}]}`

func setJSON(t *testing.T, f *fixture, q string) setDoc {
	t.Helper()
	w := f.do(req("GET", "/office/set.json"+q, nil))
	var d setDoc
	if err := json.Unmarshal(w.Body.Bytes(), &d); w.Code != 200 || err != nil {
		t.Fatalf("set.json%s: %d %v %s", q, w.Code, err, w.Body)
	}
	return d
}

// D153: set.json is the set a page draws: the active one, a named one, or
// the built-in one with why.
func TestOfficeSetJSON(t *testing.T) {
	f := newFixture(t)
	f.cli.set("--json office set ls", setLsJSON)
	if d := setJSON(t, f, ""); d.Name != "cabin" || d.Base != "/office/sets/cabin/" || d.Fallback != "" || !slices.Equal(d.Have, []string{"Furn_Desk"}) || string(d.Look) != `{"bloom":0}` {
		t.Errorf("active: %+v", d)
	}
	if d := setJSON(t, f, "?name=cyberpunk"); d.Name != "cyberpunk" || d.Base != "/static/office/sets/cyberpunk/" {
		t.Errorf("named: %+v", d)
	}
	if d := setJSON(t, f, "?name=attic"); d.Name != "cyberpunk" || !strings.Contains(d.Fallback, "attic is broken: Furn_Desk.bin") {
		t.Errorf("broken: %+v", d)
	}
	// review focus: the active set deleted by hand
	f.cli.set("--json office set ls", strings.Replace(setLsJSON, `"active": "cabin"`, `"active": "gone"`, 1))
	if d := setJSON(t, f, ""); d.Name != "cyberpunk" || d.Fallback != "there is no set gone" {
		t.Errorf("gone: %+v", d)
	}
}

// D153: an installed set's files are served only when its index.json lists
// them, never a symlink or a path out of the folder.
func TestOfficeSetFiles(t *testing.T) {
	f := newFixture(t)
	f.s.c.Data = t.TempDir()
	dir := filepath.Join(f.s.c.Data, "office", "cabin")
	os.MkdirAll(dir, 0o700)
	for name, c := range map[string]string{"Furn_Desk.gltf": `{"x":1}`, "Furn_Desk.bin": "bin", "extra.bin": "not indexed",
		"index.json": `{"files": {"Furn_Desk.gltf": "aa", "Furn_Desk.bin": "bb", "Deco_Mug.bin": "cc"}, "pieces": ["Furn_Desk"]}`} {
		os.WriteFile(filepath.Join(dir, name), []byte(c), 0o600)
	}
	os.Symlink("/etc/hostname", filepath.Join(dir, "Deco_Mug.bin"))
	if w := f.do(req("GET", "/office/sets/cabin/Furn_Desk.gltf", nil)); w.Code != 200 || w.Body.String() != `{"x":1}` || w.Header().Get("Content-Type") != "model/gltf+json" {
		t.Errorf("indexed: %d %q %q", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	for _, p := range []string{"extra.bin", "index.json", "Deco_Mug.bin", "../cabin/Furn_Desk.bin", "%2e%2e/x", "nope.bin"} {
		if w := f.do(req("GET", "/office/sets/cabin/"+p, nil)); w.Code == 200 { // 404, or the mux's 307 to a cleaned path
			t.Errorf("%s: %d %q", p, w.Code, w.Body)
		}
	}
	if w := f.do(req("GET", "/office/sets/Cabin/Furn_Desk.gltf", nil)); w.Code != 404 {
		t.Errorf("bad name: %d", w.Code)
	}
}

// D153: the page answers for every listed set; the bare path goes to the active one.
func TestOfficeRoutes(t *testing.T) {
	f := newFixture(t)
	f.cli.set("--json office set ls", setLsJSON)
	for p, code := range map[string]int{"/p/ftest/virtual-office/cabin/": 200, "/p/ftest/virtual-office/cyberpunk/": 200, "/p/ftest/virtual-office/nope/": 404} {
		if w := f.do(req("GET", p, nil)); w.Code != code {
			t.Errorf("%s: %d; want %d", p, w.Code, code)
		}
	}
	for _, p := range []string{"/p/ftest/virtual-office", "/p/ftest/virtual-office/cyberpunk"} {
		if w := f.do(req("GET", p, nil)); w.Code != 302 || w.Header().Get("Location") != "/p/ftest/virtual-office/cabin/" {
			t.Errorf("%s: %d %q", p, w.Code, w.Header().Get("Location"))
		}
	}
	if w := f.do(req("GET", "/p/ftest/virtual-office/cabin/", nil)); !strings.Contains(w.Body.String(), `data-set="cabin"`) {
		t.Errorf("the page doesn't name its set")
	}
}

// D153: the set names office refuses are exactly the fixed paths under
// /p/{prefix}/virtual-office/ that a set's page would sit beside.
func TestReservedSetNamesMatchRoutes(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range regexp.MustCompile(`"(?:GET|POST) /p/\{prefix\}/virtual-office/([a-z-]+)`).FindAllStringSubmatch(string(src), -1) {
		if m[1] != "cyberpunk" && !slices.Contains(got, m[1]) { // D150's old link, not an API
			got = append(got, m[1])
		}
	}
	slices.Sort(got)
	want := slices.Clone(office.ReservedNames)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("virtual-office paths %v; office.ReservedNames %v", got, want)
	}
}

func TestMachineOfficePicker(t *testing.T) {
	f := newFixture(t)
	f.cli.set("--json office set ls", setLsJSON)
	body := f.do(req("GET", "/machine", nil)).Body.String()
	contains(t, "machine page", body, `action="/machine/office/use"`, `<option value="cabin" selected>`, `<option value="cyberpunk">`)
	if strings.Contains(body, `<option value="attic"`) {
		t.Error("a broken set is offered")
	}
	r := req("POST", "/machine/office/use", url.Values{"name": {"../x"}})
	r.Header.Set("Accept", "application/json")
	if w := f.do(r); w.Code != 400 {
		t.Errorf("bad name: %d", w.Code)
	}
	r = req("POST", "/machine/office/use", url.Values{"name": {"cyberpunk"}})
	r.Header.Set("Accept", "application/json")
	if w := f.do(r); w.Code != 202 {
		t.Fatalf("use: %d %s", w.Code, w.Body)
	}
	if got := f.run.wait(t, 1)[0]; !slices.Equal(got, []string{testExe, "office", "set", "use", "cyberpunk"}) {
		t.Errorf("argv %q", got)
	}
}
