package libraries

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	builtinpacks "github.com/jbigler/railswitch/internal/pack/builtin"
	"github.com/jbigler/railswitch/internal/signing"
	"github.com/jbigler/railswitch/internal/sys"
)

const testLib = "github.com/acme/stack-library"

type key struct{ priv, pub string }

func newKey(t *testing.T) key {
	t.Helper()
	priv := filepath.Join(t.TempDir(), "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "pub@example", "-f", priv).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	data, err := os.ReadFile(priv + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	pub, err := signing.ParseKey(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return key{priv, pub}
}

func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture is a machine (XDG dirs in temp dirs), a library's working
// repository src and the bare repository it is fetched from.
type fixture struct {
	d       machine.Dirs
	runtime string
	src     string
	url     string
	lib     *Library
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example")
	runtime := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	root := t.TempDir()
	f := &fixture{
		d:       machine.Dirs{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data")},
		runtime: runtime,
		src:     filepath.Join(root, "src"),
	}
	err := fs.WalkDir(builtinpacks.FS, "packs/rails", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(builtinpacks.FS, p)
		if err != nil {
			return err
		}
		write(t, filepath.Join(f.src, filepath.FromSlash(p)), string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.src, "README.md"), "not a library path; ignored\n")
	run(t, f.src, "git", "init", "-q", "-b", "main")
	bare := filepath.Join(root, "lib.git")
	run(t, root, "git", "init", "-q", "--bare", "-b", "main", bare)
	run(t, bare, "git", "config", "uploadpack.allowFilter", "true")
	run(t, f.src, "git", "remote", "add", "origin", bare)
	f.url = "file://" + bare
	f.lib = &Library{Alias: "acme", URL: f.url}
	if err := Update(f.d, func(c *Config) (bool, error) { return true, c.Add(*f.lib) }); err != nil {
		t.Fatal(err)
	}
	return f
}

// publish indexes src as serial, signs it with k, commits and force-pushes.
func (f *fixture) publish(t *testing.T, k key, serial int64) {
	t.Helper()
	f.sign(t, k, serial)
	f.commit(t)
}

// sign indexes src as serial and signs it with k.
func (f *fixture) sign(t *testing.T, k key, serial int64) {
	t.Helper()
	os.Remove(filepath.Join(f.src, signing.IndexFile))
	idx, err := signing.BuildIndex(f.src, testLib, serial, nil)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.src, signing.IndexFile), string(idx.Encode()))
	os.Remove(filepath.Join(f.src, signing.SigFile))
	run(t, f.src, "ssh-keygen", "-q", "-Y", "sign", "-f", k.priv, "-n", signing.NamespaceLibrary, signing.IndexFile)
}

func (f *fixture) commit(t *testing.T) {
	t.Helper()
	run(t, f.src, "git", "add", "-A")
	run(t, f.src, "git", "commit", "-q", "--allow-empty", "-m", "publish")
	run(t, f.src, "git", "push", "-q", "-f", "origin", "main")
}

func (f *fixture) update(fn func(*signing.Trust) (bool, error)) error {
	return signing.UpdateTrust(filepath.Join(f.d.Config, signing.TrustFile), fn)
}

func (f *fixture) trust(t *testing.T, k key) {
	t.Helper()
	err := f.update(func(tr *signing.Trust) (bool, error) {
		_, err := tr.Add(testLib, k.pub)
		return true, err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// fetch runs Fetch with the library as libraries.toml has it now, and
// checks that the temp clone is gone.
func (f *fixture) fetch(t *testing.T) (*FetchResult, error) {
	t.Helper()
	c, err := Load(Path(f.d))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Fetch(context.Background(), sys.Exec{}, f.d, c.Library(f.lib.Alias), f.update)
	if ents, _ := os.ReadDir(f.runtime); len(ents) != 0 {
		t.Errorf("$XDG_RUNTIME_DIR after Fetch: %v; want empty", ents)
	}
	if ents, _ := os.ReadDir(Dir(f.d, f.lib.Alias)); ents != nil {
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), "staging-") || strings.HasPrefix(e.Name(), "old-") {
				t.Errorf("%s left in the library dir", e.Name())
			}
		}
	}
	return res, err
}

func (f *fixture) mustFetch(t *testing.T, want signing.Status) *FetchResult {
	t.Helper()
	res, err := f.fetch(t)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != want {
		t.Fatalf("fetch status %s (%s); want %s", res.Status, res.Reason, want)
	}
	return res
}

func currentSerial(t *testing.T, f *fixture) int64 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(CurrentDir(f.d, "acme"), signing.IndexFile))
	if err != nil {
		t.Fatal(err)
	}
	idx, err := signing.ParseIndex(data)
	if err != nil {
		t.Fatal(err)
	}
	return idx.Serial
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestFetchTrusted(t *testing.T) {
	f := newFixture(t)
	k := newKey(t)
	f.trust(t, k)
	f.publish(t, k, 1)
	res := f.mustFetch(t, signing.StatusTrusted)
	head := strings.TrimSpace(run(t, f.src, "git", "rev-parse", "HEAD"))
	if res.Commit != head || res.Serial != 1 || res.Packages != 1 || res.Pending {
		t.Errorf("result = %+v; want commit %s, serial 1, 1 package", res, head)
	}
	cur := CurrentDir(f.d, "acme")
	for _, p := range []string{"packs/rails/pack.toml", signing.IndexFile, signing.SigFile} {
		if fi, err := os.Stat(filepath.Join(cur, p)); err != nil || fi.Mode().Perm() != 0o644 {
			t.Errorf("%s: %v %v", p, fi, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(cur, "packs/rails/entrypoints/entrypoint-app.sh")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("entrypoint-app.sh: %v %v; want 0755", fi, err)
	}
	if exists(filepath.Join(cur, "README.md")) {
		t.Error("README.md at the top, not a library path, was written")
	}
	rec, err := ReadRecord(f.d, "acme")
	if err != nil {
		t.Fatal(err)
	}
	fp, _ := signing.Fingerprint(k.pub)
	if rec.ID != testLib || rec.Commit != head || rec.Serial != 1 || rec.Status != signing.StatusTrusted || rec.Signer != fp || rec.Fetched.IsZero() {
		t.Errorf("record = %+v", rec)
	}
	c, _ := Load(Path(f.d))
	if c.Library("acme").ID != testLib {
		t.Errorf("libraries.toml id = %q; want %s", c.Library("acme").ID, testLib)
	}
	// A verified serial is recorded in the trust store.
	tr, _ := signing.LoadTrust(filepath.Join(f.d.Config, signing.TrustFile))
	if tr.Library(testLib).LastSerial != 1 {
		t.Errorf("last_serial = %d", tr.Library(testLib).LastSerial)
	}
}

func TestFetchUnknownPending(t *testing.T) {
	f := newFixture(t)
	k := newKey(t)
	f.publish(t, k, 1)
	res := f.mustFetch(t, signing.StatusUnknownSigner)
	if !res.Pending || exists(CurrentDir(f.d, "acme")) || !exists(filepath.Join(PendingDir(f.d, "acme"), signing.IndexFile)) {
		t.Fatalf("pending %v, current %v; want pending/ and no current/", res.Pending, exists(CurrentDir(f.d, "acme")))
	}
	p, err := ReadPending(f.d, "acme")
	fp, _ := signing.Fingerprint(k.pub)
	if err != nil || p.Fingerprint != fp || p.Library != testLib || p.Status != signing.StatusUnknownSigner || p.KnownLibrary {
		t.Errorf("pending = %+v %v", p, err)
	}
	rec, _ := ReadRecord(f.d, "acme")
	if rec.Status != signing.StatusUnknownSigner || rec.ID != "" || rec.Signer != "" || rec.Commit != "" {
		t.Errorf("record = %+v; want the verdict and no current", rec)
	}
	if c, _ := Load(Path(f.d)); c.Library("acme").ID != "" {
		t.Error("an unverified index recorded the id")
	}
}

func TestFetchTrustPromotes(t *testing.T) {
	f := newFixture(t)
	k := newKey(t)
	f.publish(t, k, 1)
	f.mustFetch(t, signing.StatusUnknownSigner)
	f.trust(t, k)
	unlock, err := Lock(f.d, "acme")
	if err != nil {
		t.Fatal(err)
	}
	err = Promote(f.d, "acme")
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(CurrentDir(f.d, "acme"), "packs/rails/pack.toml")) || exists(PendingDir(f.d, "acme")) {
		t.Error("promote: want current/ and no pending/")
	}
	if _, err := ReadPending(f.d, "acme"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("pending.toml after promote: %v", err)
	}
	rec, _ := ReadRecord(f.d, "acme")
	fp, _ := signing.Fingerprint(k.pub)
	if rec.Status != signing.StatusTrusted || rec.Signer != fp || rec.ID != testLib || rec.Serial != 1 || rec.Commit == "" {
		t.Errorf("record = %+v", rec)
	}
	if c, _ := Load(Path(f.d)); c.Library("acme").ID != testLib {
		t.Error("promote did not record the id")
	}
	// A trusted fetch now replaces current; a pending/ goes away.
	f.publish(t, k, 2)
	f.mustFetch(t, signing.StatusTrusted)
	if currentSerial(t, f) != 2 {
		t.Error("current is not serial 2")
	}
}

func TestFetchRolledBackKeepsCurrent(t *testing.T) {
	f := newFixture(t)
	k := newKey(t)
	f.trust(t, k)
	f.publish(t, k, 2)
	f.mustFetch(t, signing.StatusTrusted)
	head := strings.TrimSpace(run(t, f.src, "git", "rev-parse", "HEAD"))
	f.publish(t, k, 1)
	res := f.mustFetch(t, signing.StatusRolledBack)
	if res.Pending || currentSerial(t, f) != 2 {
		t.Errorf("rolled back: pending %v, current serial %d; want 2", res.Pending, currentSerial(t, f))
	}
	rec, _ := ReadRecord(f.d, "acme")
	if rec.Status != signing.StatusRolledBack || rec.Serial != 2 || rec.Commit != head || rec.Reason == "" {
		t.Errorf("record = %+v; want rolled-back over current's serial 2, commit %s", rec, head)
	}

	// A revoked signer, too, leaves current.
	err := f.update(func(tr *signing.Trust) (bool, error) {
		fp, _ := signing.Fingerprint(k.pub)
		return true, tr.Revoke(testLib, fp)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.publish(t, k, 3)
	f.mustFetch(t, signing.StatusRevoked)
	if currentSerial(t, f) != 2 {
		t.Error("a revoked fetch replaced current")
	}
}

// refused publishes a trusted serial 1, then a signed serial 2 with change
// applied to src after indexing, and checks the fetch fails with want and leaves current and its
// record as they were.
func refused(t *testing.T, want string, change func(f *fixture)) {
	t.Helper()
	f := newFixture(t)
	k := newKey(t)
	f.trust(t, k)
	f.publish(t, k, 1)
	f.mustFetch(t, signing.StatusTrusted)
	before, _ := os.ReadFile(recordPath(f.d, "acme"))
	f.sign(t, k, 2)
	change(f)
	f.commit(t)
	if _, err := f.fetch(t); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("fetch: %v; want an error naming %q", err, want)
	}
	if currentSerial(t, f) != 1 {
		t.Error("a refused fetch replaced current")
	}
	if after, _ := os.ReadFile(recordPath(f.d, "acme")); string(after) != string(before) {
		t.Errorf("a refused fetch changed fetch.toml:\n%s", after)
	}
}

func TestFetchRefusesSymlink(t *testing.T) {
	refused(t, "symlink", func(f *fixture) {
		p := filepath.Join(f.src, "packs/rails/entrypoints/e.sh")
		if err := os.Symlink("/etc/passwd", p); err != nil {
			t.Fatal(err)
		}
	})
}

func TestFetchRefusesSubmodule(t *testing.T) {
	refused(t, "submodule", func(f *fixture) {
		// git add of a nested repository stages a gitlink.
		sub := filepath.Join(f.src, "packs/rails/initdb/sub")
		write(t, filepath.Join(sub, "x"), "x\n")
		run(t, sub, "git", "init", "-q")
		run(t, sub, "git", "add", "x")
		run(t, sub, "git", "commit", "-q", "-m", "sub")
	})
}

func TestFetchRefusesPath(t *testing.T) {
	for _, p := range []string{"packs/rails/.hidden", "packs/rails/deep/x/y", "packs/Rails/pack.toml", "options/x/entrypoints/a/b", "packs/top-file"} {
		t.Run(p, func(t *testing.T) {
			refused(t, "not a", func(f *fixture) { write(t, filepath.Join(f.src, p), "x\n") })
		})
	}
}

func TestFetchRefusesSize(t *testing.T) {
	refused(t, "over", func(f *fixture) {
		write(t, filepath.Join(f.src, "packs/rails/README.md"), strings.Repeat("x", 1<<20+1))
	})
}

func TestFetchRefusesFileCount(t *testing.T) {
	refused(t, "more than 2000", func(f *fixture) {
		for i := range maxFiles {
			write(t, filepath.Join(f.src, "packs/rails/initdb", strconv.Itoa(i)+".sql"), "\n")
		}
	})
}

// The tree listing stops git once it holds more entries than a library
// may have, or more bytes than that many entries need.
func TestListingCapped(t *testing.T) {
	ent := []byte("100644 blob 0123456789012345678901234567890123456789\tpacks/rails/README.md\x00")
	var l listing
	for i := range maxFiles {
		if _, err := l.Write(ent); err != nil {
			t.Fatalf("entry %d: %v", i+1, err)
		}
	}
	if _, err := l.Write(ent); !errors.Is(err, errTooManyFiles) {
		t.Fatalf("entry %d: %v, want errTooManyFiles", maxFiles+1, err)
	}
	var big listing
	if _, err := big.Write(make([]byte, maxListing+1)); !errors.Is(err, errTooManyFiles) {
		t.Fatalf("an oversized listing: %v", err)
	}
}

func TestFetchTempRemoved(t *testing.T) {
	f := newFixture(t)
	k := newKey(t)
	f.publish(t, k, 1)
	f.mustFetch(t, signing.StatusUnknownSigner) // fetch checks $XDG_RUNTIME_DIR is empty
	f.lib.URL = "file://" + filepath.Join(t.TempDir(), "missing.git")
	if err := Update(f.d, func(c *Config) (bool, error) { c.Library("acme").URL = f.lib.URL; return true, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := f.fetch(t); err == nil {
		t.Error("fetch of a missing repository: no error")
	}
	if !exists(PendingDir(f.d, "acme")) {
		t.Error("a failed fetch dropped pending/")
	}
}

func TestFetchIDFixed(t *testing.T) {
	f := newFixture(t)
	k := newKey(t)
	f.trust(t, k)
	f.publish(t, k, 1)
	f.mustFetch(t, signing.StatusTrusted)
	if err := Update(f.d, func(c *Config) (bool, error) { c.Library("acme").ID = "github.com/acme/other"; return true, nil }); err != nil {
		t.Fatal(err)
	}
	f.publish(t, k, 2)
	if _, err := f.fetch(t); err == nil || !strings.Contains(err.Error(), "github.com/acme/other") {
		t.Errorf("fetch of another library's index: %v", err)
	}
	if currentSerial(t, f) != 1 {
		t.Error("current replaced")
	}

	// A second alias can't take an id another alias has.
	if err := Update(f.d, func(c *Config) (bool, error) {
		c.Library("acme").ID = testLib
		return true, c.Add(Library{Alias: "acme2", URL: f.url})
	}); err != nil {
		t.Fatal(err)
	}
	c, _ := Load(Path(f.d))
	if _, err := Fetch(context.Background(), sys.Exec{}, f.d, c.Library("acme2"), f.update); err == nil || !strings.Contains(err.Error(), "already added as acme") {
		t.Errorf("fetch of acme2: %v", err)
	}
	if exists(CurrentDir(f.d, "acme2")) {
		t.Error("acme2 got a current/")
	}
}

func TestConfigAlias(t *testing.T) {
	var c Config
	for _, a := range []string{"local", "Acme", "1acme", "", "a_b", strings.Repeat("a", 21)} {
		if err := c.Add(Library{Alias: a, URL: "https://x/y"}); err == nil {
			t.Errorf("alias %q accepted", a)
		}
	}
	for _, u := range []string{"-uhttps://x", "ext::sh -c x", "https://x\n"} {
		if err := c.Add(Library{Alias: "acme", URL: u}); err == nil {
			t.Errorf("url %q accepted", u)
		}
	}
	if err := c.Add(Library{Alias: "acme", URL: "https://x/y", Branch: "-b"}); err == nil {
		t.Error("branch -b accepted")
	}
	if err := c.Add(Library{Alias: "acme", URL: "https://x/y"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(Library{Alias: "acme", URL: "https://x/z"}); err == nil {
		t.Error("a second acme accepted")
	}
}
