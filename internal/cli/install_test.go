package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/libraries"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys/systest"
	"github.com/jbigler/railswitch/internal/testfixture"
	"github.com/jbigler/railswitch/internal/workspace"
)

const (
	acmeID    = "example.org/acme/stack-library"
	cacheAcme = `[{"op":"option","key":"cache","ref":"acme:redis@1"}]`
)

// installFake is unitsFake running ssh-keygen for real (the re-verify).
func installFake() *systest.Fake {
	f := unitsFake()
	f.Passthrough = append(f.Passthrough, "ssh-keygen")
	return f
}

// acmeWS is a locked filial workspace and a trusted library acme holding
// a copy of the built-in redis option.
func acmeWS(t *testing.T) (string, machine.Dirs, *testfixture.Lib) {
	t.Helper()
	ws, dirs := lockedWS(t)
	return ws, dirs, testfixture.Library(t, dirs, "acme", acmeID, "options/redis")
}

type installPreview struct {
	Hash    string
	Install *reviewDoc
}

func previewAcme(t *testing.T, ws string) installPreview {
	t.Helper()
	code, out, errs := runStdin(t, installFake(), cacheAcme, "-C", ws, "--json", "settings", "preview")
	if code != 0 {
		t.Fatalf("preview: exit %d: %s", code, errs)
	}
	var p installPreview
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return p
}

// unchanged fails unless project.toml and stack.lock are what they were.
func unchanged(t *testing.T, ws, toml, lock string) {
	t.Helper()
	if readFile(t, filepath.Join(ws, "project.toml")) != toml || readFile(t, filepath.Join(ws, "stack.lock")) != lock {
		t.Error("project.toml or stack.lock changed")
	}
}

func TestSettingsPreviewInstall(t *testing.T) {
	ws, _, _ := acmeWS(t)
	p := previewAcme(t, ws)
	in := p.Install
	if in == nil || len(in.Packages) != 1 || in.Packages[0].Ref != "acme:redis@1" || in.Packages[0].Status != "added" || in.Flags == 0 || !strings.HasPrefix(in.Hash, "sha256:") {
		t.Fatalf("install = %+v", in)
	}
	// An edit that installs nothing has a null install.
	code, out, _ := runStdin(t, installFake(), cacheNone, "-C", ws, "--json", "settings", "preview")
	if code != 0 || !strings.Contains(out, `"install":null`) {
		t.Fatalf("exit %d: %s", code, out)
	}
}

// Adding a library option without --accept-install, or with a stale
// hash, writes nothing.
func TestSettingsApplyInstallRefused(t *testing.T) {
	ws, _, _ := acmeWS(t)
	toml, lock := readFile(t, filepath.Join(ws, "project.toml")), readFile(t, filepath.Join(ws, "stack.lock"))
	p := previewAcme(t, ws)
	code, _, errs := runStdin(t, installFake(), cacheAcme, "-C", ws, "settings", "apply", "--if-hash", p.Hash)
	if code != 1 || !strings.Contains(errs, "installing acme:redis@1 needs review") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	unchanged(t, ws, toml, lock)
	code, _, errs = runStdin(t, installFake(), cacheAcme, "-C", ws, "settings", "apply", "--if-hash", p.Hash, "--accept-install", "sha256:0000")
	if code != 1 || !strings.Contains(errs, "the install changed since it was reviewed") {
		t.Fatalf("stale hash: exit %d: %s", code, errs)
	}
	unchanged(t, ws, toml, lock)
}

func TestSettingsApplyInstall(t *testing.T) {
	ws, dirs, lib := acmeWS(t)
	p := previewAcme(t, ws)
	code, _, errs := runStdin(t, installFake(), cacheAcme, "-C", ws, "settings", "apply", "--if-hash", p.Hash, "--accept-install", p.Install.Hash)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	data := readFile(t, filepath.Join(ws, "stack.lock"))
	if workspace.Hash([]byte(data)) != p.Install.Hash {
		t.Error("stack.lock is not the reviewed one")
	}
	l, err := config.ReadLock(filepath.Join(ws, "stack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	e := l.Options["redis"]
	if e.Source != "library "+acmeID || e.Signer != lib.Key.Fingerprint {
		t.Fatalf("option redis = %+v", e)
	}
	if !exists(filepath.Join(dirs.Data, "locked", e.SHA256)) {
		t.Error("the installed package was not stored")
	}
}

// The library changed between the preview and the apply: the review's
// hash no longer matches.
func TestSettingsApplyInstallStale(t *testing.T) {
	ws, _, lib := acmeWS(t)
	toml, lock := readFile(t, filepath.Join(ws, "project.toml")), readFile(t, filepath.Join(ws, "stack.lock"))
	p := previewAcme(t, ws)
	testfixture.WriteFile(t, filepath.Join(lib.Dir(), "options", "redis", "NOTES.md"), "new\n")
	lib.Publish(t)
	code, _, errs := runStdin(t, installFake(), cacheAcme, "-C", ws, "settings", "apply", "--if-hash", p.Hash, "--accept-install", p.Install.Hash)
	if code != 1 || !strings.Contains(errs, "the install changed since it was reviewed") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	unchanged(t, ws, toml, lock)
	// After signer rm, the current review is refused too.
	lib.Distrust(t)
	p = previewAcme(t, ws)
	code, _, errs = runStdin(t, installFake(), cacheAcme, "-C", ws, "settings", "apply", "--if-hash", p.Hash, "--accept-install", p.Install.Hash)
	if code != 1 || !strings.Contains(errs, "no longer trusted") {
		t.Fatalf("distrusted: exit %d: %s", code, errs)
	}
	unchanged(t, ws, toml, lock)
}

// project new installs a library option only with the dry run's install
// review accepted.
func TestProjectNewInstall(t *testing.T) {
	dirs := newEnv(t)
	repo := bareRepo(t, "main")
	testfixture.Library(t, dirs, "acme", acmeID, "options/redis")
	args := []string{"--repo", repo, "--prefix", "shop", "--branch", "main", "--agent", "none", "--option", "cache=acme:redis@1"}
	f := gitHost()
	f.Passthrough = append(f.Passthrough, "ssh-keygen")
	ws := filepath.Join(t.TempDir(), "shop")
	doc := dryRun(t, f, append([]string{ws}, args...)...)
	in := doc.Install
	if in == nil || len(in.Packages) != 1 || in.Packages[0].Ref != "acme:redis@1" || in.Packages[0].Status != "added" {
		t.Fatalf("install = %+v", in)
	}
	code, _, errs := runWith(t, f, append([]string{"project", "new", ws}, args...)...)
	if code != 1 || !strings.Contains(errs, "installing acme:redis@1 needs review") {
		t.Fatalf("without --accept-install: exit %d: %s", code, errs)
	}
	if exists(ws) {
		t.Error("a refused install left the workspace")
	}
	code, _, errs = runWith(t, f, append([]string{"project", "new", ws, "--accept-install", "sha256:0"}, args...)...)
	if code != 1 || !strings.Contains(errs, "the install changed since it was reviewed") {
		t.Fatalf("stale: exit %d: %s", code, errs)
	}
	code, out, errs := runWith(t, f, append([]string{"project", "new", ws, "--accept-install", in.Hash}, args...)...)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	data := readFile(t, filepath.Join(ws, "stack.lock"))
	if workspace.Hash([]byte(data)) != in.Hash || !strings.Contains(data, `source = "library `+acmeID+`"`) {
		t.Fatalf("stack.lock:\n%s", data)
	}
}

// A first lock writes without review, except a library package (D106).
func TestLockFirstRefusesLibrary(t *testing.T) {
	ws, dirs := filialWS(t)
	testfixture.Library(t, dirs, "acme", acmeID, "options/redis")
	path := filepath.Join(ws, "project.toml")
	writeFile(t, path, strings.Replace(readFile(t, path), `cache    = "redis@1"`, `cache    = "acme:redis@1"`, 1))
	code, out, errs := runStdin(t, installFake(), "", "-C", ws, "lock")
	if code != 1 || !strings.Contains(out, "added option acme:redis@1") || !strings.Contains(errs, "lock --accept") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if exists(filepath.Join(ws, "stack.lock")) {
		t.Error("lock wrote stack.lock")
	}
}

// pack ls lists the packages of a library on this machine (libraries.toml)
// whose verified checkout still resolves, with their alias; not those of a
// checkout no libraries.toml entry names.
func TestPackLsLibrary(t *testing.T) {
	ws, dirs, _ := acmeWS(t)
	options := func() map[string]string {
		code, out, errs := run(t, "-C", ws, "--json", "pack", "ls")
		if code != 0 {
			t.Fatalf("pack ls: exit %d: %s", code, errs)
		}
		var doc packLsDoc
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		for _, o := range doc.Options {
			m[o.Ref] = o.Library + "/" + o.Slot
		}
		return m
	}
	if _, ok := options()["acme:redis@1"]; ok {
		t.Error("a checkout libraries.toml doesn't name is listed")
	}
	err := libraries.Update(dirs, func(c *libraries.Config) (bool, error) {
		return true, c.Add(libraries.Library{Alias: "acme", URL: "https://example.org/acme.git", ID: acmeID})
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := options()["acme:redis@1"]; got != "acme/cache" {
		t.Errorf("acme:redis@1 = %q, want library acme, slot cache", got)
	}
	testfixture.WriteFile(t, filepath.Join(libraries.CurrentDir(dirs, "acme"), "options", "redis", "README.md"), "tampered\n")
	if _, ok := options()["acme:redis@1"]; ok {
		t.Error("a package whose files no longer hash to the index is listed")
	}
}
