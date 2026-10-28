package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/lockstore"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/workspace"
)

func testStore(t *testing.T) lockstore.Store {
	t.Helper()
	dirs, err := machine.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	return lockstore.Store{Dir: lockstore.Dir(dirs)}
}

// dropStore removes the machine's store of accepted packages.
func dropStore(t *testing.T) {
	t.Helper()
	if err := os.RemoveAll(testStore(t).Dir); err != nil {
		t.Fatal(err)
	}
}

func readLock(t *testing.T, ws string) *config.Lock {
	t.Helper()
	l, err := config.ReadLock(filepath.Join(ws, "stack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// storeHolds fails the test unless the store holds every package ws's
// stack.lock pins.
func storeHolds(t *testing.T, ws string) {
	t.Helper()
	l := readLock(t, ws)
	for _, tbl := range []map[string]config.LockEntry{l.Packs, l.Options, l.Agents} {
		for k, e := range tbl {
			if _, err := testStore(t).Get(e.SHA256); err != nil {
				t.Errorf("%s locked at %s is not stored: %v", k, e.SHA256, err)
			}
		}
	}
}

// pendingOption makes ws's stack.lock pin, for the built-in option name@1, a
// stored copy of it with edit applied: as if the lock accepted that copy
// and a railswitch upgrade changed the option since. It returns the
// copy's sha.
func pendingOption(t *testing.T, ws, name string, edit func(fstest.MapFS)) string {
	t.Helper()
	cat, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	files := cat.Options[pack.Ref{Name: name, Version: 1}].Files
	mem := fstest.MapFS{}
	if err := fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(files, p)
		mem[p] = &fstest.MapFile{Data: b}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	edit(mem)
	sha, err := testStore(t).Put(mem)
	if err != nil {
		t.Fatal(err)
	}
	l := readLock(t, ws)
	e := l.Options[name]
	e.SHA256 = sha
	l.Options[name] = e
	if err := l.Write(filepath.Join(ws, "stack.lock")); err != nil {
		t.Fatal(err)
	}
	return sha
}

func addFile(m fstest.MapFS) { m["NOTES.md"] = &fstest.MapFile{Data: []byte("accepted\n")} }

// Review Focus: a Settings save while a built-in update waits leaves that
// package's entry as it was; the project keeps running the accepted copy.
func TestSettingsApplyKeepsPending(t *testing.T) {
	ws, _ := lockedWS(t)
	sha := pendingOption(t, ws, "postgres", addFile)
	before := readLock(t, ws).Options["postgres"]
	hash := hashOf(t, filepath.Join(ws, "project.toml"))
	if code, _, errs := runStdin(t, unitsFake(), cacheNone, "-C", ws, "settings", "apply", "--if-hash", hash); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	l := readLock(t, ws)
	if got := l.Options["postgres"]; got != before || got.SHA256 != sha {
		t.Errorf("postgres entry %+v, want it kept: %+v", got, before)
	}
	if _, ok := l.Options["redis"]; ok {
		t.Error("the save itself was not locked")
	}
	w, err := workspace.Load(t.Context(), ws, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Pending) != 1 || w.Pending[0].Name != "postgres" || w.CheckLock() != nil {
		t.Errorf("Pending %v, CheckLock %v", w.Pending, w.CheckLock())
	}
}

// Review Focus: pack file write of local A while built-in B waits: A's
// entry advances, B's doesn't.
func TestPackFileWriteAcceptsOnlyItsPackage(t *testing.T) {
	ws, dir := localRails(t)
	sha := pendingOption(t, ws, "postgres", addFile)
	file := filepath.Join(dir, "deps.allow")
	before := readFile(t, file)
	if code, _, errs := runStdin(t, unitsFake(), before+"more.example\n", "-C", ws, "pack", "file", "write", "local:rails@1", "deps.allow", "--if-hash", hashOf(t, file)); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	l := readLock(t, ws)
	if want, _ := pack.ContentHash(os.DirFS(dir)); l.Packs["rails"].SHA256 != want {
		t.Errorf("rails entry %+v, want %s", l.Packs["rails"], want)
	}
	if l.Options["postgres"].SHA256 != sha {
		t.Errorf("postgres entry advanced: %+v", l.Options["postgres"])
	}
	storeHolds(t, ws)
}

func TestLockStores(t *testing.T) {
	ws, _ := lockedWS(t)
	storeHolds(t, ws)
	dropStore(t)
	if code, _, errs := runStdin(t, unitsFake(), cacheNone, "-C", ws, "settings", "apply", "--if-hash", hashOf(t, filepath.Join(ws, "project.toml"))); code != 0 {
		t.Fatalf("settings apply: exit %d: %s", code, errs)
	}
	storeHolds(t, ws)

	newEnv(t)
	repo := bareRepo(t, "main")
	shop := filepath.Join(t.TempDir(), "shop")
	mustNew(t, shop, "--repo", repo, "--prefix", "shop", "--branch", "main")
	storeHolds(t, shop)
}

// While a built-in's update waits, everything that copies its text out
// (containerfile eject, its default in containerfile show, pack eject)
// reads the accepted copy, not the unreviewed update.
func TestEjectReadsAccepted(t *testing.T) {
	ws, _ := lockedWS(t)
	var accepted []byte
	pendingOption(t, ws, "postgres", func(m fstest.MapFS) {
		accepted = append(m["Containerfile.pg-client"].Data, "# accepted\n"...)
		m["Containerfile.pg-client"].Data = accepted
	})
	mustRun(t, "-C", ws, "containerfile", "eject", "pg-client", "--force")
	if got, want := readFile(t, filepath.Join(ws, "containerfiles", "Containerfile.pg-client")), ejected("postgres@1", "Containerfile.pg-client", accepted); got != want {
		t.Errorf("containerfile eject wrote:\n%s\nwant:\n%s", got, want)
	}
	var doc cfShowDoc
	if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "--json", "containerfile", "show", "pg-client")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Default == nil || doc.Default.Text != string(accepted) {
		t.Errorf("containerfile show default: %+v", doc.Default)
	}
	mustRun(t, "-C", ws, "pack", "eject", "postgres@1", "--as", "pg")
	if got := readFile(t, filepath.Join(ws, "options", "pg", "Containerfile.pg-client")); got != string(accepted) {
		t.Errorf("pack eject copied:\n%s", got)
	}
}

// D107: the agent base image is locked; while an update of it waits, an
// eject (and containerfile show's default) reads the accepted copy.
func TestEjectAgentBaseAccepted(t *testing.T) {
	ws, _ := lockedWS(t)
	cat, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	def, err := fs.ReadFile(cat.AgentBase, pack.AgentBaseFile)
	if err != nil {
		t.Fatal(err)
	}
	accepted := append(def, "# accepted\n"...)
	sha, err := testStore(t).Put(fstest.MapFS{pack.AgentBaseFile: {Data: accepted}})
	if err != nil {
		t.Fatal(err)
	}
	l := readLock(t, ws)
	e, ok := l.Base["agent"]
	if !ok {
		t.Fatalf("stack.lock has no [base.agent]: %+v", l.Base)
	}
	e.SHA256 = sha
	l.Base["agent"] = e
	if err := l.Write(filepath.Join(ws, "stack.lock")); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "-C", ws, "containerfile", "eject", "agent-base", "--force")
	if got, want := readFile(t, filepath.Join(ws, "containerfiles", "Containerfile.agent-base")), ejected("agent-base", pack.AgentBaseFile, accepted); got != want {
		t.Errorf("containerfile eject wrote:\n%s\nwant:\n%s", got, want)
	}
	var doc cfShowDoc
	if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "--json", "containerfile", "show", "agent-base")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Default == nil || doc.Default.Text != string(accepted) {
		t.Errorf("containerfile show default: %+v", doc.Default)
	}
}

// While a built-in's update waits and its accepted copy is gone from the
// store, an eject refuses: copying the resolved text would hand the
// unreviewed update to a build.
func TestEjectRefusesWithoutAcceptedCopy(t *testing.T) {
	ws, _ := lockedWS(t)
	pendingOption(t, ws, "postgres", func(m fstest.MapFS) {
		m["Containerfile.pg-client"].Data = append(m["Containerfile.pg-client"].Data, "# accepted\n"...)
	})
	dropStore(t)
	file := filepath.Join(ws, "containerfiles", "Containerfile.pg-client")
	before := readFile(t, file)
	if code, _, errs := run(t, "-C", ws, "containerfile", "eject", "pg-client", "--force"); code == 0 || !strings.Contains(errs, "accepted copy is missing") {
		t.Errorf("eject without the accepted copy: exit %d: %s", code, errs)
	}
	if readFile(t, file) != before {
		t.Error("the eject wrote the unreviewed text")
	}
}
