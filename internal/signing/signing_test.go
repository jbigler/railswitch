package signing

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/pack/builtin"
	"github.com/jbigler/railswitch/internal/sys"
)

const libA = "github.com/acme/stack-library"

type testKey struct {
	priv, pub, fp string
}

func keygen(t *testing.T, typ string, extra ...string) testKey {
	t.Helper()
	priv := filepath.Join(t.TempDir(), "k")
	args := append([]string{"-t", typ, "-N", "", "-q", "-C", "test comment", "-f", priv}, extra...)
	if out, err := exec.Command("ssh-keygen", args...).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen %v: %v\n%s", args, err, out)
	}
	pub, err := os.ReadFile(priv + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	k, err := ParseKey(string(pub))
	if err != nil {
		t.Fatal(err)
	}
	fp, err := Fingerprint(k)
	if err != nil {
		t.Fatal(err)
	}
	return testKey{priv: priv, pub: k, fp: fp}
}

// signFile writes file.sig, k's signature in namespace over file.
func signFile(t *testing.T, k testKey, namespace, file string) {
	t.Helper()
	os.Remove(file + ".sig")
	if out, err := exec.Command("ssh-keygen", "-Y", "sign", "-f", k.priv, "-n", namespace, file).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen -Y sign: %v\n%s", err, out)
	}
}

// rotation is from's signature vouching for to in lib.
func rotation(t *testing.T, lib string, from, to testKey) Rotation {
	t.Helper()
	msg := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(msg, RotationMessage(lib, to.pub), 0o600); err != nil {
		t.Fatal(err)
	}
	signFile(t, from, NamespaceRotate, msg)
	sig, err := os.ReadFile(msg + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	return Rotation{From: from.fp, To: to.pub, Signature: string(sig)}
}

// copyBuiltin copies the built-in package directory rel into dir.
func copyBuiltin(t *testing.T, dir, rel string) {
	t.Helper()
	err := fs.WalkDir(builtin.FS, rel, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := fs.ReadFile(builtin.FS, p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// newLibrary is a library folder holding the built-in rails pack.
func newLibrary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	copyBuiltin(t, dir, "packs/rails")
	return dir
}

// publish indexes dir as lib at serial, lets edit change the index, and
// signs it with k.
func publish(t *testing.T, dir, lib string, serial int64, k testKey, edit func(*Index)) {
	t.Helper()
	idx, err := BuildIndex(dir, lib, serial, nil)
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(idx)
	}
	file := filepath.Join(dir, IndexFile)
	if err := os.WriteFile(file, idx.Encode(), 0o644); err != nil {
		t.Fatal(err)
	}
	signFile(t, k, NamespaceLibrary, file)
}

func trust(t *testing.T, lib string, keys ...testKey) *Trust {
	t.Helper()
	tr := &Trust{Schema: Schema}
	for _, k := range keys {
		if _, err := tr.Add(lib, k.pub); err != nil {
			t.Fatal(err)
		}
	}
	return tr
}

func verify(t *testing.T, dir string, tr *Trust) *Result {
	t.Helper()
	res, err := Verify(context.Background(), sys.Exec{}, dir, tr)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func wantStatus(t *testing.T, res *Result, want Status) {
	t.Helper()
	if res.Status != want {
		t.Fatalf("status = %s (%s); want %s", res.Status, res.Reason, want)
	}
}

func TestFingerprintMatchesSSHKeygen(t *testing.T) {
	for _, typ := range [][]string{{"ed25519"}, {"ecdsa", "-b", "384"}, {"rsa", "-b", "3072"}} {
		k := keygen(t, typ[0], typ[1:]...)
		out, err := exec.Command("ssh-keygen", "-lf", k.priv+".pub").Output()
		if err != nil {
			t.Fatal(err)
		}
		if f := strings.Fields(string(out)); len(f) < 2 || f[1] != k.fp {
			t.Errorf("%s: Fingerprint = %s; ssh-keygen -lf says %s", typ[0], k.fp, out)
		}
	}
	if _, err := Fingerprint("ssh-dss AAAAB3NzaC1kc3M="); err == nil {
		t.Error("Fingerprint accepted ssh-dss")
	}
	// The line's type must be the blob's.
	k := keygen(t, "ed25519")
	if _, err := Fingerprint("ssh-rsa " + strings.Fields(k.pub)[1]); err == nil {
		t.Error("Fingerprint accepted an ed25519 blob labelled ssh-rsa")
	}
}

func TestSignerOf(t *testing.T) {
	k := keygen(t, "ed25519")
	file := filepath.Join(t.TempDir(), "f")
	os.WriteFile(file, []byte("hello\n"), 0o600)
	signFile(t, k, NamespaceLibrary, file)
	sig, _ := os.ReadFile(file + ".sig")
	got, err := SignerOf(sig)
	if err != nil || got != k.pub {
		t.Errorf("SignerOf = %q, %v; want %q", got, err, k.pub)
	}
	for _, bad := range []string{"", "-----BEGIN SSH SIGNATURE-----\n!!\n-----END SSH SIGNATURE-----\n", "-----BEGIN SSH SIGNATURE-----\nU1NIU0lH\n-----END SSH SIGNATURE-----\n"} {
		if _, err := SignerOf([]byte(bad)); err == nil {
			t.Errorf("SignerOf(%q) succeeded", bad)
		}
	}
}

func TestVerifyTrusted(t *testing.T) {
	k := keygen(t, "ed25519")
	dir := newLibrary(t)
	publish(t, dir, libA, 3, k, nil)
	tr := trust(t, libA, k)
	res := verify(t, dir, tr)
	wantStatus(t, res, StatusTrusted)
	if res.Library != libA || res.Serial != 3 || res.Fingerprint != k.fp {
		t.Errorf("result = %+v", res)
	}
	if len(res.Packages) != 1 || res.Packages[0] != (PackageResult{Ref: "rails@1", Kind: "pack", Dir: "packs/rails", Signed: true}) {
		t.Errorf("packages = %+v", res.Packages)
	}
	if got := tr.Library(libA).LastSerial; got != 3 {
		t.Errorf("last_serial = %d; want 3", got)
	}
}

func TestVerifyUnknownSigner(t *testing.T) {
	k := keygen(t, "ed25519")
	dir := newLibrary(t)
	publish(t, dir, libA, 1, k, nil)
	tr := &Trust{Schema: Schema}
	res := verify(t, dir, tr)
	wantStatus(t, res, StatusUnknownSigner)
	if res.Fingerprint != k.fp || res.Key != k.pub {
		t.Errorf("fingerprint %s key %s; want %s %s", res.Fingerprint, res.Key, k.fp, k.pub)
	}
	if len(tr.Libraries) != 0 {
		t.Errorf("trust changed: %+v", tr.Libraries)
	}
}

func TestVerifyOtherLibraryKey(t *testing.T) {
	k := keygen(t, "ed25519")
	dir := newLibrary(t)
	publish(t, dir, "github.com/acme/other", 1, k, nil)
	tr := trust(t, libA, k)
	wantStatus(t, verify(t, dir, tr), StatusUnknownSigner)
	if tr.Library(libA).LastSerial != 0 || tr.Library("github.com/acme/other") != nil {
		t.Errorf("trust changed: %+v", tr.Libraries)
	}
}

// The library id in the signed bytes picks the trust entry: A's key never
// verifies an index naming B, and an index signed for A with its id edited
// to B no longer verifies at all.
func TestVerifyLibraryIDBound(t *testing.T) {
	const libB = "github.com/acme/b"
	ka, kb := keygen(t, "ed25519"), keygen(t, "ed25519")
	tr := trust(t, libA, ka)
	if _, err := tr.Add(libB, kb.pub); err != nil {
		t.Fatal(err)
	}

	dir := newLibrary(t)
	publish(t, dir, libB, 1, ka, nil)
	wantStatus(t, verify(t, dir, tr), StatusUnknownSigner)

	publish(t, dir, libA, 2, ka, nil)
	file := filepath.Join(dir, IndexFile)
	data, _ := os.ReadFile(file)
	os.WriteFile(file, []byte(strings.Replace(string(data), libA, libB, 1)), 0o644)
	wantStatus(t, verify(t, dir, tr), StatusBadSignature)

	if tr.Library(libA).LastSerial != 0 || tr.Library(libB).LastSerial != 0 {
		t.Errorf("last_serial changed: %+v %+v", tr.Library(libA), tr.Library(libB))
	}
}

func TestVerifyTampered(t *testing.T) {
	k := keygen(t, "ed25519")
	dir := newLibrary(t)
	publish(t, dir, libA, 1, k, nil)
	file := filepath.Join(dir, IndexFile)
	data, _ := os.ReadFile(file)
	os.WriteFile(file, []byte(strings.Replace(string(data), "serial = 1", "serial = 9", 1)), 0o644)
	tr := trust(t, libA, k)
	wantStatus(t, verify(t, dir, tr), StatusBadSignature)
	if tr.Library(libA).LastSerial != 0 {
		t.Error("a bad signature moved last_serial")
	}
}

func TestVerifyPackageChanged(t *testing.T) {
	k := keygen(t, "ed25519")
	dir := newLibrary(t)
	publish(t, dir, libA, 1, k, nil)
	f, err := os.OpenFile(filepath.Join(dir, "packs/rails/pack.toml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\n")
	f.Close()
	res := verify(t, dir, trust(t, libA, k))
	wantStatus(t, res, StatusTrusted)
	if p := res.Packages[0]; p.Signed || !strings.Contains(p.Reason, "content differs") {
		t.Errorf("package = %+v; want unsigned, content differs", p)
	}
}

func TestVerifyUnlistedPackage(t *testing.T) {
	k := keygen(t, "ed25519")
	dir := newLibrary(t)
	publish(t, dir, libA, 1, k, nil)
	copyBuiltin(t, dir, "options/redis")
	os.MkdirAll(filepath.Join(dir, "agents/broken"), 0o755)
	res := verify(t, dir, trust(t, libA, k))
	wantStatus(t, res, StatusTrusted)
	want := []PackageResult{
		{Ref: "rails@1", Kind: "pack", Dir: "packs/rails", Signed: true},
		{Ref: "redis@1", Kind: "option", Dir: "options/redis", Reason: "not in library.toml"},
		{Kind: "agent", Dir: "agents/broken", Reason: "not in library.toml"},
	}
	if len(res.Packages) != len(want) {
		t.Fatalf("packages = %+v", res.Packages)
	}
	for i := range want {
		if res.Packages[i] != want[i] {
			t.Errorf("packages[%d] = %+v; want %+v", i, res.Packages[i], want[i])
		}
	}
}

func TestVerifyRollback(t *testing.T) {
	k := keygen(t, "ed25519")
	dir := newLibrary(t)
	tr := trust(t, libA, k)
	publish(t, dir, libA, 5, k, nil)
	wantStatus(t, verify(t, dir, tr), StatusTrusted)
	publish(t, dir, libA, 4, k, nil)
	wantStatus(t, verify(t, dir, tr), StatusRolledBack)
	if got := tr.Library(libA).LastSerial; got != 5 {
		t.Errorf("last_serial = %d; want 5", got)
	}
	publish(t, dir, libA, 5, k, nil)
	wantStatus(t, verify(t, dir, tr), StatusTrusted)
}

func TestVerifyRevokedKey(t *testing.T) {
	k1, k2 := keygen(t, "ed25519"), keygen(t, "ed25519")
	dir := newLibrary(t)
	tr := trust(t, libA, k1, k2)
	publish(t, dir, libA, 2, k2, func(idx *Index) { idx.Revoke = []Revocation{{k1.fp}} })
	wantStatus(t, verify(t, dir, tr), StatusTrusted)

	publish(t, dir, libA, 3, k1, nil)
	res := verify(t, dir, tr)
	wantStatus(t, res, StatusRevoked)
	if res.Fingerprint != k1.fp || tr.Library(libA).LastSerial != 2 {
		t.Errorf("fingerprint %s, last_serial %d", res.Fingerprint, tr.Library(libA).LastSerial)
	}
	if _, err := tr.Add(libA, k1.pub); err == nil {
		t.Error("Add re-trusted a revoked key")
	}
}

func TestVerifyAppliesRevoke(t *testing.T) {
	k1, k2, stranger := keygen(t, "ed25519"), keygen(t, "ed25519"), keygen(t, "ed25519")
	dir := newLibrary(t)
	tr := trust(t, libA, k1, k2)
	revoke := func(idx *Index) { idx.Revoke = []Revocation{{k2.fp}, {stranger.fp}} }

	publish(t, dir, libA, 1, stranger, revoke)
	wantStatus(t, verify(t, dir, tr), StatusUnknownSigner)
	if l := tr.Library(libA); len(l.Keys) != 2 || len(l.Revoked) != 0 {
		t.Fatalf("an untrusted index's [[revoke]] applied: %+v", l)
	}

	publish(t, dir, libA, 1, k1, revoke)
	wantStatus(t, verify(t, dir, tr), StatusTrusted)
	l := tr.Library(libA)
	if len(l.Keys) != 1 || l.Keys[0].Fingerprint != k1.fp {
		t.Errorf("keys = %+v; want only k1", l.Keys)
	}
	if len(l.Revoked) != 2 || l.Revoked[0].Fingerprint != k2.fp || l.Revoked[1].Fingerprint != stranger.fp {
		t.Errorf("revoked = %+v", l.Revoked)
	}
}

func TestVerifyRotation(t *testing.T) {
	old, next := keygen(t, "ed25519"), keygen(t, "ecdsa")
	dir := newLibrary(t)
	tr := trust(t, libA, old)
	publish(t, dir, libA, 2, next, func(idx *Index) { idx.Rotate = []Rotation{rotation(t, libA, old, next)} })
	res := verify(t, dir, tr)
	wantStatus(t, res, StatusRotation)
	if res.Fingerprint != old.fp || res.NewFingerprint != next.fp || res.NewKey != next.pub {
		t.Errorf("result = %+v", res)
	}
	if l := tr.Library(libA); len(l.Keys) != 1 || l.LastSerial != 0 {
		t.Errorf("a rotation changed trust before confirmation: %+v", l)
	}

	// Confirming (library trust --rotation) adds the new key.
	if _, err := tr.Add(libA, res.NewKey); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, verify(t, dir, tr), StatusTrusted)
}

func TestVerifyRotationUntrusted(t *testing.T) {
	trusted, untrusted, next := keygen(t, "ed25519"), keygen(t, "ed25519"), keygen(t, "ed25519")
	dir := newLibrary(t)
	tr := trust(t, libA, trusted)

	// Vouched for by a key the library doesn't trust.
	publish(t, dir, libA, 2, next, func(idx *Index) { idx.Rotate = []Rotation{rotation(t, libA, untrusted, next)} })
	res := verify(t, dir, tr)
	wantStatus(t, res, StatusUnknownSigner)
	if res.Fingerprint != next.fp {
		t.Errorf("fingerprint = %s; want the signer's %s", res.Fingerprint, next.fp)
	}

	// Claims the trusted key, signed by another.
	publish(t, dir, libA, 2, next, func(idx *Index) {
		r := rotation(t, libA, untrusted, next)
		r.From = trusted.fp
		idx.Rotate = []Rotation{r}
	})
	wantStatus(t, verify(t, dir, tr), StatusUnknownSigner)

	// A rotation signed for another library.
	publish(t, dir, libA, 2, next, func(idx *Index) { idx.Rotate = []Rotation{rotation(t, "github.com/acme/other", trusted, next)} })
	wantStatus(t, verify(t, dir, tr), StatusUnknownSigner)
}

func TestVerifyMissingSignature(t *testing.T) {
	k := keygen(t, "ed25519")
	dir := newLibrary(t)
	publish(t, dir, libA, 1, k, nil)
	os.Remove(filepath.Join(dir, SigFile))
	res := verify(t, dir, trust(t, libA, k))
	wantStatus(t, res, StatusBadSignature)
	if !strings.Contains(res.Reason, SigFile) {
		t.Errorf("reason = %q", res.Reason)
	}
}

func TestParseIndexStrict(t *testing.T) {
	sum := strings.Repeat("a", 64)
	pkg := func(ref string) string {
		return "[[package]]\nref = \"" + ref + "\"\nkind = \"pack\"\nsha256 = \"" + sum + "\"\n"
	}
	head := func(lib string) string { return "schema = 1\nlibrary = \"" + lib + "\"\nserial = 1\n" }
	if _, err := ParseIndex([]byte(head(libA) + pkg("rails@1"))); err != nil {
		t.Fatalf("a good index: %v", err)
	}
	for name, data := range map[string]string{
		"bad id":        head("Acme/../x") + pkg("rails@1"),
		"dotdot id":     head("acme/../x") + pkg("rails@1"),
		"local ref":     head(libA) + pkg("local:rails@1"),
		"duplicate ref": head(libA) + pkg("rails@1") + pkg("rails@1"),
		"two versions":  head(libA) + pkg("rails@1") + pkg("rails@2"),
		"schema 2":      "schema = 2\nlibrary = \"" + libA + "\"\nserial = 1\n",
		"no schema":     "library = \"" + libA + "\"\nserial = 1\n",
		"serial 0":      "schema = 1\nlibrary = \"" + libA + "\"\nserial = 0\n",
		"unknown key":   head(libA) + "extra = 1\n",
		"bad kind":      head(libA) + "[[package]]\nref = \"rails@1\"\nkind = \"tool\"\nsha256 = \"" + sum + "\"\n",
		"bad sha":       head(libA) + "[[package]]\nref = \"rails@1\"\nkind = \"pack\"\nsha256 = \"abc\"\n",
		"bad revoke":    head(libA) + "[[revoke]]\nfingerprint = \"MD5:aa\"\n",
		"bad rotate":    head(libA) + "[[rotate]]\nfrom = \"x\"\nto = \"ssh-ed25519 AAAA\"\nsignature = \"\"\n",
	} {
		if _, err := ParseIndex([]byte(data)); err == nil {
			t.Errorf("%s: ParseIndex accepted\n%s", name, data)
		}
	}
}

func TestTrustFileMode(t *testing.T) {
	k := keygen(t, "ed25519")
	path := filepath.Join(t.TempDir(), "railswitch", TrustFile)
	tr, err := LoadTrust(path)
	if err != nil || len(tr.Libraries) != 0 {
		t.Fatalf("LoadTrust(missing) = %+v, %v", tr, err)
	}
	if _, err := tr.Add(libA, k.pub+" a comment"); err != nil {
		t.Fatal(err)
	}
	if err := tr.Save(path); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "schema = 1") || !strings.Contains(string(data), "added = "+today().Format("2006-01-02")+"\n") || strings.Contains(string(data), "comment") {
		t.Errorf("signers.toml:\n%s", data)
	}
	back, err := LoadTrust(path)
	if err != nil {
		t.Fatal(err)
	}
	if l := back.Library(libA); l == nil || len(l.Keys) != 1 || l.Keys[0] != tr.Library(libA).Keys[0] {
		t.Errorf("reloaded = %+v", back.Libraries)
	}

	os.WriteFile(path, []byte("schema = 2\n"), 0o600)
	if _, err := LoadTrust(path); err == nil || !strings.Contains(err.Error(), "newer railswitch") {
		t.Errorf("LoadTrust(schema 2) = %v", err)
	}
	os.WriteFile(path, []byte(strings.Replace(string(data), k.fp, "SHA256:"+strings.Repeat("A", 43), 1)), 0o600)
	if _, err := LoadTrust(path); err == nil {
		t.Error("LoadTrust accepted a fingerprint that isn't the key's")
	}
}

func TestTrustRemove(t *testing.T) {
	k1, k2 := keygen(t, "ed25519"), keygen(t, "ed25519")
	tr := trust(t, libA, k1, k2)
	if fp, err := tr.Add(libA, k1.pub); err != nil || fp != k1.fp || len(tr.Library(libA).Keys) != 2 {
		t.Errorf("re-Add = %s, %v; keys %d", fp, err, len(tr.Library(libA).Keys))
	}
	if err := tr.Remove(libA, k1.fp); err != nil {
		t.Fatal(err)
	}
	if err := tr.Remove(libA, k1.fp); err == nil {
		t.Error("Remove of an untrusted key succeeded")
	}
	tr.Library(libA).LastSerial = 4
	if err := tr.Remove(libA, k2.fp); err != nil {
		t.Fatal(err)
	}
	if l := tr.Library(libA); l == nil || l.LastSerial != 4 {
		t.Errorf("an entry with a verified serial was dropped: %+v", l)
	}
	tr2 := trust(t, libA, k1)
	if err := tr2.Remove(libA, k1.fp); err != nil || len(tr2.Libraries) != 0 {
		t.Errorf("Remove left %+v, %v; want the entry gone", tr2.Libraries, err)
	}
}

func TestVerifyTempDirRemoved(t *testing.T) {
	run := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", run)
	k := keygen(t, "ed25519")
	dir := newLibrary(t)
	publish(t, dir, libA, 1, k, nil)
	wantStatus(t, verify(t, dir, trust(t, libA, k)), StatusTrusted)
	if entries, _ := os.ReadDir(run); len(entries) != 0 {
		t.Errorf("left in XDG_RUNTIME_DIR: %v", entries)
	}
}
