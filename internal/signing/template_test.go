package signing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const tplStack = "schema = 1\nname = \"rails-pg\"\nversion = 2\n\n[stack]\npack = \"rails@1\"\napp = \"rails\"\n"

// A template is a library package of kind template (D109): BuildIndex
// lists it under its stack.toml's name and version, Verify signs it, and
// a template that doesn't decode is refused by BuildIndex.
func TestLibraryTemplateIndexed(t *testing.T) {
	k := keygen(t, "ed25519")
	dir := newLibrary(t)
	writeTestFile(t, filepath.Join(dir, "templates/rails-pg/stack.toml"), tplStack)
	writeTestFile(t, filepath.Join(dir, "templates/rails-pg/containerfiles/Containerfile.pg-client"), "FROM x\n")
	publish(t, dir, libA, 1, k, nil)
	data, err := os.ReadFile(filepath.Join(dir, IndexFile))
	if err != nil {
		t.Fatal(err)
	}
	idx, err := ParseIndex(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Packages) != 2 || idx.Packages[1].Ref != "rails-pg@2" || idx.Packages[1].Kind != "template" {
		t.Fatalf("index packages = %+v", idx.Packages)
	}
	res := verify(t, dir, trust(t, libA, k))
	wantStatus(t, res, StatusTrusted)
	if len(res.Packages) != 2 || res.Packages[1] != (PackageResult{Ref: "rails-pg@2", Kind: "template", Dir: "templates/rails-pg", Signed: true}) {
		t.Errorf("packages = %+v", res.Packages)
	}

	writeTestFile(t, filepath.Join(dir, "templates/rails-pg/containerfiles/Containerfile.pg-client"), "FROM y\n")
	res = verify(t, dir, trust(t, libA, k))
	if res.Packages[1].Signed || res.Packages[1].Reason != "content differs from library.toml" {
		t.Errorf("a changed template: %+v", res.Packages[1])
	}
	writeTestFile(t, filepath.Join(dir, "templates/rails-pg/stack.toml"), tplStack+"\n[project]\nname = \"x\"\n")
	if _, err := BuildIndex(dir, libA, 2, nil); err == nil || !strings.Contains(err.Error(), "unknown key project") {
		t.Errorf("BuildIndex of a template with [project]: %v", err)
	}
}

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
