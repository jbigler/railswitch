package web

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

const importSHA = "2222222222222222222222222222222222222222222222222222222222222222"

const importJSON = `{"ref":"rails-stack@2","sha256":"` + importSHA + `","status":"update","replaces":{"ref":"rails-stack@1","sha256":"` + tplSHA + `"},
 "hash":"` + importSHA + `","flags":1,"restart":[],"warnings":["local:x@1 is a workspace-local package"],
 "packages":[{"ref":"rails-stack@2","kind":"template","status":"changed","from":"` + tplSHA + `","to":"` + importSHA + `","also_locked_in":[],
  "files":[{"path":"stack.toml","status":"changed","diff":"@@ -1 +1,2 @@\n schema = 1\n+[firewall]\n","flags":[{"check":"firewall","line":0,"text":"firewall.mode: (none) -> off"}]}]}]}`

const importDirPath = "/home/u/Downloads/rails stack; $(id)"

func importFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.cli.set("--json template import "+importDirPath, importJSON)
	return f
}

// The machine page reviews a folder with the review component; Import
// runs the accept with the reviewed hash, the path as one argv element.
func TestTemplateImportPage(t *testing.T) {
	f := importFixture(t)
	w := f.do(req("GET", "/machine/templates/import?dir="+url.QueryEscape(importDirPath), nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	contains(t, "import page", body, "Import template", `<span class="mono">rails-stack@2</span> replaces <span class="mono">rails-stack@1</span>`,
		"local:x@1 is a workspace-local package", `<span class="mono">stack.toml</span>`, "firewall.mode: (none) -&gt; off",
		`name="hash" value="`+importSHA+`"`, "I&#39;ve read the 1 flagged item", `data-ack-required`)
	noCSPViolation(t, "import page", body)

	for _, bad := range []string{"", "relative/dir", "/a/../b", "/a\nb"} {
		if body := f.do(req("GET", "/machine/templates/import?dir="+url.QueryEscape(bad), nil)).Body.String(); !strings.Contains(body, "full path") {
			t.Errorf("dir %q accepted", bad)
		}
	}

	post := func(kv ...string) int { return f.do(req("POST", "/machine/templates/import", form(kv...))).Code }
	if c := post("dir", importDirPath, "hash", importSHA); c != http.StatusBadRequest {
		t.Errorf("no tick: %d", c)
	}
	if c := post("dir", importDirPath, "hash", tplSHA, "ack", "1"); c != http.StatusBadRequest {
		t.Errorf("another hash: %d", c)
	}
	if c := post("dir", "rel", "hash", importSHA, "ack", "1"); c != http.StatusBadRequest {
		t.Errorf("relative dir: %d", c)
	}
	if w := f.do(req("POST", "/machine/templates/import", form("dir", importDirPath, "hash", importSHA, "ack", "1"))); w.Code != http.StatusAccepted {
		t.Fatalf("import: %d %s", w.Code, w.Body)
	}
	if got, want := f.run.wait(t, 1)[0], []string{testExe, "template", "import", importDirPath, "--accept", importSHA}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}

	f.cli.set("--json template import "+importDirPath, strings.Replace(importJSON, `"status":"update"`, `"status":"same"`, 1))
	if c := post("dir", importDirPath, "hash", importSHA, "ack", "1"); c != http.StatusBadRequest {
		t.Errorf("same: %d", c)
	}
}

// The machine page lists the templates and links the import.
func TestMachineTemplates(t *testing.T) {
	f := signersFixture(t)
	f.cli.set("--json template ls", `{"templates":[{"ref":"rails-stack@1","name":"rails-stack","version":1,"description":"Our Rails","source":"machine","packages":["rails@1"]}]}`)
	body := f.do(req("GET", "/machine", nil)).Body.String()
	contains(t, "machine page", body, `id="templates"`, `<td class="mono">rails-stack@1</td>`, "Our Rails", `action="/machine/templates/import"`)
}
