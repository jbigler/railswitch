package cli

import (
	"encoding/json"
	"path/filepath"
	"slices"
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
	code, _, errs := runWith(t, f, append([]string{"project", "new", "--no-detect", ws}, args...)...)
	if code != 1 || !strings.Contains(errs, "installing acme:redis@1 needs review") {
		t.Fatalf("without --accept-install: exit %d: %s", code, errs)
	}
	if exists(ws) {
		t.Error("a refused install left the workspace")
	}
	code, _, errs = runWith(t, f, append([]string{"project", "new", "--no-detect", ws, "--accept-install", "sha256:0"}, args...)...)
	if code != 1 || !strings.Contains(errs, "the install changed since it was reviewed") {
		t.Fatalf("stale: exit %d: %s", code, errs)
	}
	code, out, errs := runWith(t, f, append([]string{"project", "new", "--no-detect", ws, "--accept-install", in.Hash}, args...)...)
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
func TestLibraryPackagesOffered(t *testing.T) {
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
	// Settings offers it too: project show's cache choices.
	code, out, errs := run(t, "-C", ws, "--json", "project", "show")
	if code != 0 {
		t.Fatalf("project show: exit %d: %s", code, errs)
	}
	var show projectDoc
	if err := json.Unmarshal([]byte(out), &show); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(show.Options, func(o optionRow) bool { return o.Key == "cache" })
	if i < 0 || !slices.Contains(show.Options[i].Choices, "acme:redis@1") {
		t.Errorf("project show cache choices lack acme:redis@1: %+v", show.Options)
	}
	testfixture.WriteFile(t, filepath.Join(libraries.CurrentDir(dirs, "acme"), "options", "redis", "README.md"), "tampered\n")
	if _, ok := options()["acme:redis@1"]; ok {
		t.Error("a package whose files no longer hash to the index is listed")
	}
}

const evilID = "example.org/evil/stack-library"

// previewEdits is settings preview of edits in ws.
func previewEdits(t *testing.T, ws, edits string) installPreview {
	t.Helper()
	code, out, errs := runStdin(t, installFake(), edits, "-C", ws, "--json", "settings", "preview")
	if code != 0 {
		t.Fatalf("preview: exit %d: %s", code, errs)
	}
	var p installPreview
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return p
}

// installedAcme is acmeWS with acme:redis@1 installed through its Install
// review.
func installedAcme(t *testing.T) (string, machine.Dirs, *testfixture.Lib) {
	t.Helper()
	ws, dirs, lib := acmeWS(t)
	p := previewAcme(t, ws)
	if code, _, errs := runStdin(t, installFake(), cacheAcme, "-C", ws, "settings", "apply", "--if-hash", p.Hash, "--accept-install", p.Install.Hash); code != 0 {
		t.Fatalf("install acme:redis@1: exit %d: %s", code, errs)
	}
	return ws, dirs, lib
}

// installsOnly fails unless p's Install review adds exactly ref.
func installsOnly(t *testing.T, p installPreview, ref string) {
	t.Helper()
	if in := p.Install; in == nil || len(in.Packages) != 1 || in.Packages[0].Ref != ref || in.Packages[0].Status != "added" {
		t.Fatalf("install = %+v, want %s added", p.Install, ref)
	}
}

// A lock entry of acme's redis@1 doesn't pin another library's redis@1:
// installing it is an Install review of its own, refused unaccepted.
func TestInstallOtherLibrarySameName(t *testing.T) {
	ws, dirs, _ := installedAcme(t)
	evil := testfixture.Library(t, dirs, "evil", evilID, "options/redis")
	testfixture.WriteFile(t, filepath.Join(evil.Dir(), "options", "redis", "NOTES.md"), "not acme's\n")
	evil.Publish(t)
	toml, lock := readFile(t, filepath.Join(ws, "project.toml")), readFile(t, filepath.Join(ws, "stack.lock"))
	const cacheEvil = `[{"op":"option","key":"cache","ref":"evil:redis@1"}]`
	p := previewEdits(t, ws, cacheEvil)
	installsOnly(t, p, "evil:redis@1")
	code, _, errs := runStdin(t, installFake(), cacheEvil, "-C", ws, "settings", "apply", "--if-hash", p.Hash)
	if code != 1 || !strings.Contains(errs, "installing evil:redis@1 needs review") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	unchanged(t, ws, toml, lock)
}

// An alias that names another library now (removed and added again) pins
// nothing its old library's entries locked.
func TestInstallAliasReAdded(t *testing.T) {
	ws, dirs, _ := installedAcme(t)
	if err := libraries.Remove(dirs, "acme"); err != nil {
		t.Fatal(err)
	}
	testfixture.Library(t, dirs, "acme", evilID, "options/redis")
	lock := readFile(t, filepath.Join(ws, "stack.lock"))
	code, out, errs := runStdin(t, installFake(), "", "-C", ws, "lock")
	if code != 1 || !strings.Contains(out, "added option acme:redis@1") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if readFile(t, filepath.Join(ws, "stack.lock")) != lock {
		t.Error("stack.lock changed")
	}
}

// A library ref back from [[removed]] is installed again: reviewed, and
// its library verified against today's trust.
func TestInstallBackFromRemoved(t *testing.T) {
	ws, _, lib := installedAcme(t)
	if code, _, errs := runStdin(t, installFake(), cacheNone, "-C", ws, "settings", "apply", "--if-hash", previewEdits(t, ws, cacheNone).Hash); code != 0 {
		t.Fatalf("cache=none: exit %d: %s", code, errs)
	}
	lib.Distrust(t)
	toml, lock := readFile(t, filepath.Join(ws, "project.toml")), readFile(t, filepath.Join(ws, "stack.lock"))
	p := previewAcme(t, ws)
	installsOnly(t, p, "acme:redis@1")
	code, _, errs := runStdin(t, installFake(), cacheAcme, "-C", ws, "settings", "apply", "--if-hash", p.Hash)
	if code != 1 || !strings.Contains(errs, "installing acme:redis@1 needs review") {
		t.Fatalf("without --accept-install: exit %d: %s", code, errs)
	}
	unchanged(t, ws, toml, lock)
	code, _, errs = runStdin(t, installFake(), cacheAcme, "-C", ws, "settings", "apply", "--if-hash", p.Hash, "--accept-install", p.Install.Hash)
	if code != 1 || !strings.Contains(errs, "no longer trusted") {
		t.Fatalf("distrusted: exit %d: %s", code, errs)
	}
	unchanged(t, ws, toml, lock)
}

// Nothing copies an uninstalled library package's Containerfile out.
func TestContainerfileUninstalledLibrary(t *testing.T) {
	ws, dirs := lockedWS(t)
	testfixture.Library(t, dirs, "acme", acmeID, "options/postgres")
	path := filepath.Join(ws, "project.toml")
	writeFile(t, path, strings.Replace(readFile(t, path), `database = "postgres@1"`, `database = "acme:postgres@1"`, 1))
	cf := filepath.Join(ws, "containerfiles", "Containerfile.pg-client")
	before := readFile(t, cf)
	for _, args := range [][]string{{"containerfile", "eject", "--force", "pg-client"}, {"containerfile", "show", "--default", "pg-client"}, {"--json", "containerfile", "show", "pg-client"}, {"containerfile", "new", "extra"}} {
		code, out, errs := run(t, append([]string{"-C", ws}, args...)...)
		if code != 1 || !strings.Contains(errs, "acme:postgres@1 is not installed") {
			t.Errorf("%v: exit %d\n%s\n%s", args, code, out, errs)
		}
	}
	if readFile(t, cf) != before || exists(filepath.Join(ws, "containerfiles", "Containerfile.extra")) {
		t.Error("containerfile wrote a file")
	}
}
