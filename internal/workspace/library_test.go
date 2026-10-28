package workspace

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/signing"
	"github.com/jbigler/railswitch/internal/sys"
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
	if err := Lock(context.Background(), root, Accept{All: true, Run: sys.Exec{}}); err != nil {
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

// installRedis locks root with the built-ins, then points cache at
// acme:redis@1 without locking it.
func installRedis(t *testing.T) (string, *testfixture.Lib) {
	t.Helper()
	root := projectRoot(t)
	lib := acmeLibrary(t)
	if err := Lock(context.Background(), root, Accept{All: true}); err != nil {
		t.Fatal(err)
	}
	setOption(t, root, "cache", "acme:redis@1")
	return root, lib
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// D106: a library ref new to the lock is an Install review; no write that
// doesn't accept it locks it.
func TestReviewLibraryAdded(t *testing.T) {
	ctx := context.Background()
	root, _ := installRedis(t)
	pl, err := Review(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	var got []Change
	for _, c := range pl.Packages {
		if c.Ref.Library != "" {
			got = append(got, c)
		}
	}
	if len(got) != 1 || got[0].Ref != acmeRedis || got[0].Status != StatusAdded || got[0].Old != nil || got[0].New == nil {
		t.Fatalf("library changes = %+v", got)
	}
	in, err := Install(ctx, root, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if in == nil || len(in.Packages) != 1 || in.Packages[0].Status != StatusAdded || len(in.Install) != 1 || in.Install[0] != acmeRedis {
		t.Fatalf("Install = %+v", in)
	}
	lock := filepath.Join(root, "stack.lock")
	before := readFile(t, lock)
	err = Lock(ctx, root, Accept{Run: sys.Exec{}})
	if err == nil || !regexp.MustCompile(`installing acme:redis@1 needs review`).MatchString(err.Error()) {
		t.Fatalf("Lock without accepting = %v", err)
	}
	if string(readFile(t, lock)) != string(before) {
		t.Fatal("a refused install wrote stack.lock")
	}
	// Accepting exactly the review installs it, at the review's hash.
	if err := Lock(ctx, root, Accept{Install: in.Install, IfHash: Hash(in.Lock), Run: sys.Exec{}}); err != nil {
		t.Fatal(err)
	}
	if string(readFile(t, lock)) != string(in.Lock) {
		t.Fatal("the install wrote other bytes than it reviewed")
	}
	if e := lockOf(t, root).Options["redis"]; e.Source != "library "+acmeID {
		t.Fatalf("option redis = %+v", e)
	}
	if in, err := Install(ctx, root, nil, nil, nil); err != nil || in != nil {
		t.Fatalf("Install after the install = %+v, %v", in, err)
	}
}

// Accepting an install or an update verifies the library again against
// today's trust: after signer rm it refuses.
func TestAcceptReverifies(t *testing.T) {
	ctx := context.Background()
	root, lib := installRedis(t)
	in, err := Install(ctx, root, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	lib.Distrust(t)
	accept := Accept{Install: in.Install, IfHash: Hash(in.Lock), Run: sys.Exec{}}
	if err := Lock(ctx, root, accept); err == nil || !regexp.MustCompile(`signer of acme is no longer trusted`).MatchString(err.Error()) {
		t.Fatalf("install after signer rm = %v", err)
	}
	if e := lockOf(t, root).Options["redis"]; e.Source == "library "+acmeID {
		t.Fatal("the refused install was locked")
	}
	lib.Trust(t)
	if err := Lock(ctx, root, accept); err != nil {
		t.Fatal(err)
	}
	// An update: refetched with other content, then distrusted.
	testfixture.WriteFile(t, filepath.Join(lib.Dir(), "options", "redis", "NOTES.md"), "changed\n")
	lib.Publish(t)
	lib.Distrust(t)
	if err := Lock(ctx, root, Accept{All: true, Run: sys.Exec{}}); err == nil || !regexp.MustCompile(`no longer trusted`).MatchString(err.Error()) {
		t.Fatalf("update after signer rm = %v", err)
	}
}

// A refetch that changes a locked library package's content: the project
// keeps running the accepted copy, and the review shows the change.
func TestLibraryUpdatePending(t *testing.T) {
	ctx := context.Background()
	root, lib := installRedis(t)
	if err := Lock(ctx, root, Accept{All: true, Run: sys.Exec{}}); err != nil {
		t.Fatal(err)
	}
	accepted := lockOf(t, root).Options["redis"]
	testfixture.WriteFile(t, filepath.Join(lib.Dir(), "options", "redis", "NOTES.md"), "changed\n")
	lib.Publish(t)
	w, err := Load(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Pending) != 1 || w.Pending[0] != acmeRedis {
		t.Fatalf("Pending = %v", w.Pending)
	}
	if err := w.CheckLock(); err != nil {
		t.Fatalf("CheckLock = %v", err)
	}
	if _, err := fsStat(w.Stack.Option("cache").Option.Files, "NOTES.md"); err == nil {
		t.Fatal("the project runs the unreviewed update")
	}
	pl, err := Review(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Packages) != 1 || pl.Packages[0].Ref != acmeRedis || pl.Packages[0].Status != StatusChanged {
		t.Fatalf("review = %+v", pl.Packages)
	}
	// A write that accepts nothing keeps the accepted entry.
	if err := Lock(ctx, root, Accept{}); err != nil {
		t.Fatal(err)
	}
	if e := lockOf(t, root).Options["redis"]; e != accepted {
		t.Fatalf("entry = %+v, want %+v", e, accepted)
	}
	if err := Lock(ctx, root, Accept{All: true, Run: sys.Exec{}}); err != nil {
		t.Fatal(err)
	}
	if e := lockOf(t, root).Options["redis"]; e.SHA256 == accepted.SHA256 || e.Commit != lib.Commit {
		t.Fatalf("accepted update entry = %+v", e)
	}
}

func TestSignerWarnings(t *testing.T) {
	ctx := context.Background()
	root, lib := installRedis(t)
	if err := Lock(ctx, root, Accept{All: true, Run: sys.Exec{}}); err != nil {
		t.Fatal(err)
	}
	w, err := Load(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	trust := func() *signing.Trust {
		tr, err := signing.LoadTrust(filepath.Join(lib.Dirs.Config, signing.TrustFile))
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	if got := w.SignerWarnings(trust()); got == nil || len(got) != 0 {
		t.Fatalf("SignerWarnings while trusted = %#v", got)
	}
	lib.Distrust(t)
	want := []SignerWarning{{Ref: "acme:redis@1", Library: acmeID, Fingerprint: lib.Key.Fingerprint}}
	if got := w.SignerWarnings(trust()); !slices.Equal(got, want) {
		t.Fatalf("SignerWarnings after signer rm = %+v, want %+v", got, want)
	}
	// Trusted again, then revoked.
	lib.Trust(t)
	tr := trust()
	if err := tr.Revoke(acmeID, lib.Key.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if got := w.SignerWarnings(tr); !slices.Equal(got, want) {
		t.Fatalf("SignerWarnings after revoke = %+v", got)
	}
}

// A signer warning names the library the lock entry came from, and only
// for the ref that entry pins: acme's redis@1 entry says nothing of
// evil:redis@1.
func TestSignerWarningsOtherLibrary(t *testing.T) {
	ctx := context.Background()
	root, lib := installRedis(t)
	if err := Lock(ctx, root, Accept{All: true, Run: sys.Exec{}}); err != nil {
		t.Fatal(err)
	}
	testfixture.Library(t, lib.Dirs, "evil", "example.org/evil/stack-library", "options/redis")
	setOption(t, root, "cache", "evil:redis@1")
	lib.Distrust(t)
	w, err := Load(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	tr, err := signing.LoadTrust(filepath.Join(lib.Dirs.Config, signing.TrustFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := w.SignerWarnings(tr); len(got) != 0 {
		t.Fatalf("SignerWarnings = %+v, want none", got)
	}
	if err := w.CheckLock(); err == nil {
		t.Fatal("CheckLock accepts acme's entry for evil:redis@1")
	}
}

func fsStat(fsys fs.FS, name string) (fs.FileInfo, error) { return fs.Stat(fsys, name) }
