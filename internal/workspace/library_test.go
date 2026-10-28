package workspace

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/testfixture"
)

const acmeID = "example.org/acme/stack-library"

// acmeLibrary is a trusted checkout of library acme holding a copy of the
// built-in redis option, on the machine projectRoot set up.
func acmeLibrary(t *testing.T) *testfixture.Lib {
	t.Helper()
	dirs, err := machine.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	return testfixture.Library(t, dirs, "acme", acmeID, "options/redis")
}

// setOption points project.toml's [options] key at ref.
func setOption(t *testing.T, root, key, ref string) {
	t.Helper()
	path := filepath.Join(root, "project.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := regexp.MustCompile(`(?m)^`+key+`\s*= "[^"]*"`).ReplaceAll(data, []byte(key+` = "`+ref+`"`))
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

var acmeRedis = pack.Ref{Name: "redis", Version: 1, Library: "acme"}

func TestLockLibraryEntry(t *testing.T) {
	root := projectRoot(t)
	lib := acmeLibrary(t)
	setOption(t, root, "cache", "acme:redis@1")
	if err := Lock(context.Background(), root, Accept{All: true}); err != nil {
		t.Fatal(err)
	}
	e := lockOf(t, root).Options["redis"]
	if e.Source != "library "+acmeID || e.Commit != lib.Commit || e.Signer != lib.Key.Fingerprint || e.Version != "1" || len(e.SHA256) != 64 {
		t.Fatalf("option redis = %+v", e)
	}
	w, err := Load(context.Background(), root, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.CheckLock(); err != nil {
		t.Fatalf("CheckLock = %v", err)
	}
	if ch := w.Stack.Option("cache"); ch == nil || ch.Ref != acmeRedis {
		t.Fatalf("cache = %+v", ch)
	}
	// The built-in redis@1, byte for byte the same, is another package.
	setOption(t, root, "cache", "redis@1")
	if err := checkLock(t, root); err == nil {
		t.Fatal("CheckLock accepts the built-in in place of the library's redis")
	}
}
