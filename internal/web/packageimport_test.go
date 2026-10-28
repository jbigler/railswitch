package web

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

const pkgImportJSON = `{"ref":"local:rails@1","kind":"pack","sha256":"` + importSHA + `","status":"update","replaces":{"ref":"local:rails@1","sha256":"` + tplSHA + `"},
 "used":true,"hash":"` + importSHA + `","flags":1,"restart":["main"],"warnings":[],
 "packages":[{"ref":"local:rails@1","kind":"pack","status":"changed","from":"` + tplSHA + `","to":"` + importSHA + `","also_locked_in":[],
  "files":[{"path":"deps.allow","status":"changed","diff":"@@ -1 +1,2 @@\n a\n+evil.example\n","flags":[{"check":"firewall","line":2,"text":"evil.example"}]}]}]}`

// The Library page reviews a package folder in the workspace; Import
// runs the accept (with --as and --restart) as a project job; Export
// writes a folder.
func TestPackageImportPage(t *testing.T) {
	f := libraryFixture(t)
	key := "--json -C /ws/ftest pack import " + importDirPath + " --as filial-rails"
	f.cli.set(key, pkgImportJSON)
	body := f.do(req("GET", "/p/ftest/library", nil)).Body.String()
	contains(t, "library", body, `action="/p/ftest/library/import"`, `name="as"`)
	w := f.do(req("GET", "/p/ftest/library/import?dir="+url.QueryEscape(importDirPath)+"&as=filial-rails", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body = w.Body.String()
	contains(t, "review", body, "Import package", `<span class="mono">local:rails@1</span> replaces`, "deps.allow",
		`name="as" value="filial-rails"`, `Restart after importing: <span class="mono">main</span>`, `data-next="/p/ftest/library"`)
	noCSPViolation(t, "package import", body)
	if body := f.do(req("GET", "/p/ftest/library/import?dir=%2Fx&as=Bad%20Name", nil)).Body.String(); !strings.Contains(body, "full path") {
		t.Error("a bad name was read")
	}

	post := func(kv ...string) *http.Response {
		return f.do(req("POST", "/p/ftest/library/import", form(kv...))).Result()
	}
	if c := post("dir", importDirPath, "as", "filial-rails", "hash", importSHA).StatusCode; c != http.StatusBadRequest {
		t.Errorf("no tick: %d", c)
	}
	if c := post("dir", importDirPath, "as", "filial-rails", "hash", importSHA, "ack", "1", "restart", "1").StatusCode; c != http.StatusAccepted {
		t.Fatalf("import: %d", c)
	}
	want := []string{testExe, "-C", "/ws/ftest", "pack", "import", importDirPath, "--as", "filial-rails", "--accept", importSHA, "--restart"}
	if got := f.run.wait(t, 1)[0]; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
	f.endedIn(t, "ftest")

	if c := f.do(req("POST", "/p/ftest/library/export", form("ref", "local:rails@1", "to", "rel/dir"))).Code; c != http.StatusBadRequest {
		t.Errorf("relative export: %d", c)
	}
	if c := f.do(req("POST", "/p/ftest/library/export", form("ref", "local:rails@1", "to", "/home/u/share/rails"))).Code; c != http.StatusAccepted {
		t.Fatalf("export: %d", c)
	}
	if got, want := f.run.wait(t, 2)[1], []string{testExe, "-C", "/ws/ftest", "pack", "export", "local:rails@1", "--to", "/home/u/share/rails"}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
}
