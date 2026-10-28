package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/libraries"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

// gitFake runs ssh-keygen and git for real and nothing else.
func gitFake() *systest.Fake { return &systest.Fake{Passthrough: []string{"ssh-keygen", "git"}} }

// libraryRepo is a bare repository holding a library signed with k, as a
// file:// URL; XDG dirs (signersPath) and git's config are the test's.
func libraryRepo(t *testing.T, k testKey) string {
	url, _ := librarySrc(t, k)
	return url
}

// librarySrc is libraryRepo and the working repository that pushes to it.
func librarySrc(t *testing.T, k testKey) (url, src string) {
	t.Helper()
	for k, v := range map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null", "XDG_RUNTIME_DIR": t.TempDir(),
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example",
	} {
		t.Setenv(k, v)
	}
	src = published(t, k)
	bare := filepath.Join(t.TempDir(), "lib.git")
	gitIn(t, src, "init", "-q", "--bare", "-b", "main", bare)
	gitIn(t, bare, "config", "uploadpack.allowFilter", "true")
	gitIn(t, src, "init", "-q", "-b", "main")
	gitIn(t, src, "remote", "add", "origin", bare)
	push(t, src)
	return "file://" + bare, src
}

func push(t *testing.T, src string) {
	t.Helper()
	gitIn(t, src, "add", "-A")
	gitIn(t, src, "commit", "-q", "-m", "publish")
	gitIn(t, src, "push", "-q", "origin", "main")
}

// A rotation waits as pending; trusting it by alias promotes it, and the
// new key is the record's signer.
func TestLibraryTrustRotationAlias(t *testing.T) {
	signersPath(t)
	old, next := sshKey(t), sshKey(t)
	url, src := librarySrc(t, old)
	mustOK(t, "", "signer", "add", testLib, old.priv+".pub")
	if out := mustRunStdin(t, gitFake(), "", "library", "add", url, "--alias", "acme"); !strings.Contains(out, "acme: trusted") {
		t.Fatalf("add: %s", out)
	}
	mustOK(t, "", "library", "rotate", src, "--old-key", old.priv, "--new", next.priv+".pub")
	mustOK(t, "", "library", "sign", src, "--key", next.priv)
	push(t, src)
	out := mustRunStdin(t, gitFake(), "", "library", "fetch", "acme")
	if !strings.Contains(out, "library trust acme --fingerprint "+next.fp+" --rotation") {
		t.Errorf("fetch, rotation: %s", out)
	}
	doc, raw := lsLibraries(t)
	if p := doc.Libraries[0].Pending; p == nil || p.Status != "rotation" || p.Fingerprint != old.fp || p.NewFingerprint != next.fp || doc.Libraries[0].Serial != 1 {
		t.Errorf("ls, rotation pending: %s", raw)
	}
	mustRunStdin(t, gitFake(), "", "library", "trust", "acme", "--fingerprint", next.fp, "--rotation")
	dirs, _ := machine.DefaultDirs()
	rec, err := libraries.ReadRecord(dirs, "acme")
	if err != nil || rec.Signer != next.fp || rec.Serial != 2 || rec.Status != "trusted" {
		t.Errorf("record after the rotation = %+v %v; want signer %s, serial 2", rec, err, next.fp)
	}
}

func lsLibraries(t *testing.T) (libraryLsDoc, string) {
	t.Helper()
	out := mustRunStdin(t, gitFake(), "", "--json", "library", "ls")
	var doc libraryLsDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("library ls --json: %v\n%s", err, out)
	}
	return doc, out
}

func TestLibraryAddLsRm(t *testing.T) {
	signersPath(t)
	if _, out := lsLibraries(t); strings.TrimSpace(out) != `{"libraries":[]}` {
		t.Errorf("empty library ls = %s", out)
	}
	k := sshKey(t)
	url := libraryRepo(t, k)

	out := mustRunStdin(t, gitFake(), "", "library", "add", url, "--alias", "acme")
	if !strings.Contains(out, "railswitch library trust acme --fingerprint "+k.fp) {
		t.Errorf("add, unknown signer:\n%s", out)
	}
	doc, raw := lsLibraries(t)
	if len(doc.Libraries) != 1 {
		t.Fatalf("ls = %s", raw)
	}
	r := doc.Libraries[0]
	if r.Alias != "acme" || r.URL != url || r.Status != "unknown-signer" || r.ID != "" || r.Commit != "" || r.Packages != 0 || r.Fetched == "" ||
		r.Pending == nil || r.Pending.Fingerprint != k.fp || r.Pending.Status != "unknown-signer" || r.Pending.KnownLibrary {
		t.Errorf("ls after add = %s", raw)
	}
	var keys map[string][]map[string]any
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"alias", "url", "branch", "id", "status", "serial", "commit", "fetched", "packages", "pending"} {
		if _, ok := keys["libraries"][0][f]; !ok {
			t.Errorf("ls --json row lacks %q: %s", f, raw)
		}
	}
	for _, f := range []string{"fingerprint", "new_fingerprint", "status", "known_library"} {
		if _, ok := keys["libraries"][0]["pending"].(map[string]any)[f]; !ok {
			t.Errorf("ls --json pending lacks %q: %s", f, raw)
		}
	}

	if code, _, errs := runStdin(t, gitFake(), "", "library", "add", url, "--alias", "acme"); code != 1 || !strings.Contains(errs, "already added") {
		t.Errorf("second add: exit %d %s", code, errs)
	}
	if code, _, errs := runStdin(t, gitFake(), "", "library", "trust", "acme", "--fingerprint", strings.Replace(k.fp, "SHA256:", "SHA256:x", 1)); code != 1 {
		t.Errorf("trust, wrong fingerprint: exit %d %s", code, errs)
	}
	out = mustRunStdin(t, gitFake(), "", "library", "trust", "acme", "--fingerprint", k.fp)
	if !strings.Contains(out, "acme's fetch is now current") {
		t.Errorf("trust acme: %s", out)
	}
	doc, raw = lsLibraries(t)
	if r := doc.Libraries[0]; r.Status != "trusted" || r.ID != testLib || r.Serial != 1 || r.Commit == "" || r.Packages != 1 || r.Pending != nil {
		t.Errorf("ls after trust = %s", raw)
	}
	if code, _, errs := runStdin(t, gitFake(), "", "library", "trust", "acme", "--fingerprint", k.fp); code != 1 || !strings.Contains(errs, "no fetch waiting") {
		t.Errorf("trust with nothing pending: exit %d %s", code, errs)
	}
	out = mustRunStdin(t, gitFake(), "", "library", "fetch", "acme")
	if !strings.Contains(out, "acme: trusted") {
		t.Errorf("fetch: %s", out)
	}
	if out := mustRunStdin(t, gitFake(), "", "library", "ls"); !strings.Contains(out, "acme") || !strings.Contains(out, "trusted") {
		t.Errorf("library ls:\n%s", out)
	}

	dirs, _ := machine.DefaultDirs()
	mustRunStdin(t, gitFake(), "", "library", "rm", "acme")
	if doc, _ := lsLibraries(t); len(doc.Libraries) != 0 {
		t.Errorf("ls after rm = %+v", doc)
	}
	if _, err := os.Stat(filepath.Join(dirs.Data, "libraries", "acme")); !os.IsNotExist(err) {
		t.Errorf("rm left the checkout: %v", err)
	}
}

// An add whose fetch fails before a verdict is not added.
func TestLibraryAddFails(t *testing.T) {
	signersPath(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	missing := "file://" + filepath.Join(t.TempDir(), "missing.git")
	if code, _, errs := runStdin(t, gitFake(), "", "library", "add", missing, "--alias", "acme"); code != 1 || !strings.Contains(errs, "not added") {
		t.Errorf("add of a missing repository: exit %d %s", code, errs)
	}
	if doc, _ := lsLibraries(t); len(doc.Libraries) != 0 {
		t.Errorf("ls = %+v", doc)
	}
	if code, _, _ := runStdin(t, gitFake(), "", "library", "add", "https://x/y", "--alias", "local"); code != 1 {
		t.Errorf("alias local: exit %d", code)
	}
}

func TestLibraryRmInUse(t *testing.T) {
	signersPath(t)
	k := sshKey(t)
	url := libraryRepo(t, k)
	mustRunStdin(t, gitFake(), "", "library", "add", url, "--alias", "acme")
	dirs, _ := machine.DefaultDirs()
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "project.toml"), []byte("schema = 1\n[stack]\npack = \"acme:rails@1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.AddProject("shop", ws, nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	reg.Close()

	code, _, errs := runStdin(t, gitFake(), "", "library", "rm", "acme")
	if code != 1 || !strings.Contains(errs, "used by shop") {
		t.Errorf("rm in use: exit %d %s", code, errs)
	}
	if doc, _ := lsLibraries(t); len(doc.Libraries) != 1 {
		t.Error("a refused rm removed the library")
	}
	// A ref of another alias that ends in acme does not count.
	if err := os.WriteFile(filepath.Join(ws, "project.toml"), []byte("schema = 1\n[stack]\npack = \"xacme:rails@1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunStdin(t, gitFake(), "", "library", "rm", "acme")
}
