package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	builtinpacks "github.com/jbigler/railswitch/internal/pack/builtin"
	"github.com/jbigler/railswitch/internal/signing"
	"github.com/jbigler/railswitch/internal/sys"
)

// libraryDir is a library folder holding a copy of the built-in rails pack.
func libraryDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	err := fs.WalkDir(builtinpacks.FS, "packs/rails", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := fs.ReadFile(builtinpacks.FS, p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// published is a library folder indexed as testLib and signed with k.
func published(t *testing.T, k testKey) string {
	t.Helper()
	dir := libraryDir(t)
	mustOK(t, "", "library", "index", dir, "--library", testLib)
	mustOK(t, "", "library", "sign", dir, "--key", k.priv)
	return dir
}

func verifyJSON(t *testing.T, dir string) (int, signing.Result) {
	t.Helper()
	code, out, errs := runSSH(t, "", "--json", "library", "verify", dir)
	var res signing.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("library verify --json: %v\nstdout: %s\nstderr: %s", err, out, errs)
	}
	return code, res
}

func TestLibraryIndexSignVerify(t *testing.T) {
	path := signersPath(t)
	k := sshKey(t)
	dir := libraryDir(t)
	mustOK(t, "", "library", "index", dir, "--library", testLib)
	idx, err := signing.ParseIndex([]byte(readFile(t, filepath.Join(dir, signing.IndexFile))))
	if err != nil {
		t.Fatal(err)
	}
	if idx.Library != testLib || idx.Serial != 1 || len(idx.Packages) != 1 || idx.Packages[0].Ref != "rails@1" {
		t.Errorf("index = %+v", idx)
	}

	f := sshFake()
	mustRunStdin(t, f, "", "library", "sign", dir, "--key", k.priv)
	last := f.Cmds[len(f.Cmds)-1]
	if last.Name != "ssh-keygen" || last.Args[0] != "-Y" || last.Args[1] != "sign" || last.Timeout != sys.NoTimeout || last.Stdin == nil {
		t.Errorf("sign ran %s %q (timeout %v, stdin %v); want ssh-keygen -Y sign, no timeout, the terminal attached", last.Name, last.Args, last.Timeout, last.Stdin)
	}
	if _, err := os.Stat(filepath.Join(dir, signing.SigFile)); err != nil {
		t.Fatal(err)
	}

	code, out, _ := runSSH(t, "", "library", "verify", dir)
	if code != 1 || !strings.Contains(out, "unknown-signer") || !strings.Contains(out, k.fp) ||
		!strings.Contains(out, "not signed") || !strings.Contains(out, "the index is not trusted") {
		t.Errorf("verify, untrusted: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("an untrusted verify wrote signers.toml")
	}
	mustOK(t, "", "signer", "add", testLib, k.priv+".pub")
	out = mustOK(t, "", "library", "verify", dir)
	if !strings.Contains(out, "trusted") || !strings.Contains(out, "rails@1") {
		t.Errorf("verify, trusted:\n%s", out)
	}
	if doc := lsSigners(t); doc.Libraries[0].LastSerial != 1 {
		t.Errorf("last_serial = %d, want 1", doc.Libraries[0].LastSerial)
	}

	// A second index goes one serial up, and signing again replaces the
	// signature.
	mustOK(t, "", "library", "index", dir, "--library", testLib)
	mustOK(t, "", "library", "sign", dir, "--key", k.priv)
	mustOK(t, "", "library", "verify", dir)
	if doc := lsSigners(t); doc.Libraries[0].LastSerial != 2 {
		t.Errorf("last_serial = %d, want 2", doc.Libraries[0].LastSerial)
	}
	if code, _, errs := runSSH(t, "", "library", "index", dir, "--library", testLib, "--serial", "1"); code != 1 || !strings.Contains(errs, "below") {
		t.Errorf("index --serial below the current one: exit %d, %s", code, errs)
	}
	if code, _, errs := runSSH(t, "", "library", "index", dir, "--library", "github.com/other/lib"); code != 1 {
		t.Errorf("index for another library over an existing one: exit %d, %s", code, errs)
	}
}

func TestLibraryVerifyJSON(t *testing.T) {
	signersPath(t)
	k := sshKey(t)
	dir := published(t, k)
	code, res := verifyJSON(t, dir)
	if code != 1 || res.Status != signing.StatusUnknownSigner || res.Fingerprint != k.fp || res.Key != k.pub || res.Library != testLib || res.Serial != 1 {
		t.Errorf("unknown signer: exit %d %+v", code, res)
	}
	if len(res.Packages) != 1 || res.Packages[0].Signed || res.Packages[0].Reason != "the index is not trusted" {
		t.Errorf("unknown signer: packages %+v; want not signed", res.Packages)
	}
	mustOK(t, "", "signer", "add", testLib, k.priv+".pub")
	code, res = verifyJSON(t, dir)
	if code != 0 || res.Status != signing.StatusTrusted || len(res.Packages) != 1 || !res.Packages[0].Signed {
		t.Errorf("trusted: exit %d %+v", code, res)
	}
	// A byte changed in the pack: still trusted, the package not signed.
	cf := filepath.Join(dir, "packs", "rails", "pack.toml")
	if err := os.WriteFile(cf, append([]byte(readFile(t, cf)), '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, res = verifyJSON(t, dir); res.Packages[0].Signed {
		t.Errorf("changed package: %+v", res.Packages)
	}
	// Bad signature: the JSON still prints, exit 1, packages [].
	if err := os.WriteFile(filepath.Join(dir, signing.SigFile), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runSSH(t, "", "--json", "library", "verify", dir)
	if code != 1 || !strings.Contains(out, `"status":"bad-signature"`) || !strings.Contains(out, `"packages":[]`) {
		t.Errorf("bad signature: exit %d %s", code, out)
	}
}

func TestLibraryTrustUnknown(t *testing.T) {
	signersPath(t)
	k := sshKey(t)
	dir := published(t, k)
	out := mustOK(t, "", "library", "trust", dir, "--fingerprint", k.fp)
	if !strings.Contains(out, k.fp) {
		t.Errorf("trust printed %q", out)
	}
	doc := lsSigners(t)
	if len(doc.Libraries) != 1 || doc.Libraries[0].Keys[0].Fingerprint != k.fp || doc.Libraries[0].LastSerial != 1 {
		t.Errorf("after trust: %+v", doc)
	}
	if code, res := verifyJSON(t, dir); code != 0 || res.Status != signing.StatusTrusted {
		t.Errorf("verify after trust: exit %d %+v", code, res)
	}
}

// Review focus: a fingerprint other than the one verify reports is
// refused and signers.toml is left as it was.
func TestLibraryTrustWrongFingerprint(t *testing.T) {
	path := signersPath(t)
	k, other := sshKey(t), sshKey(t)
	dir := published(t, k)
	mustOK(t, "", "signer", "add", "github.com/acme/unrelated", other.priv+".pub")
	before := readFile(t, path)
	for _, args := range [][]string{
		{"library", "trust", dir, "--fingerprint", other.fp},
		{"library", "trust", dir, "--fingerprint", strings.ToLower(k.fp)},
		{"library", "trust", dir, "--fingerprint", k.fp, "--rotation"},
	} {
		if code, _, errs := runSSH(t, "", args...); code != 1 {
			t.Errorf("%q: exit %d, want 1\n%s", args, code, errs)
		}
		if got := readFile(t, path); got != before {
			t.Errorf("%q changed signers.toml:\n%s", args, got)
		}
	}
	if code, _, _ := runSSH(t, "", "library", "trust", dir); code != 2 {
		t.Errorf("no --fingerprint: exit %d, want 2", code)
	}
}

func TestLibraryTrustRotation(t *testing.T) {
	signersPath(t)
	old, next := sshKey(t), sshKey(t)
	dir := published(t, old)
	mustOK(t, "", "library", "trust", dir, "--fingerprint", old.fp)
	mustOK(t, "", "library", "rotate", dir, "--old-key", old.priv, "--new", next.priv+".pub")
	mustOK(t, "", "library", "index", dir, "--library", testLib)
	mustOK(t, "", "library", "sign", dir, "--key", next.priv)

	code, res := verifyJSON(t, dir)
	if code != 1 || res.Status != signing.StatusRotation || res.Fingerprint != old.fp || res.NewFingerprint != next.fp {
		t.Fatalf("rotation: exit %d %+v", code, res)
	}
	code, out, _ := runSSH(t, "", "library", "verify", dir)
	if code != 1 || !strings.Contains(out, "--rotation") || !strings.Contains(out, next.fp) {
		t.Errorf("rotation text: %s", out)
	}
	for _, args := range [][]string{
		{"library", "trust", dir, "--fingerprint", next.fp},
		{"library", "trust", dir, "--fingerprint", old.fp, "--rotation"},
	} {
		if code, _, errs := runSSH(t, "", args...); code != 1 {
			t.Errorf("%q: exit %d, want 1\n%s", args, code, errs)
		}
	}
	mustOK(t, "", "library", "trust", dir, "--fingerprint", next.fp, "--rotation")
	if code, res := verifyJSON(t, dir); code != 0 || res.Status != signing.StatusTrusted {
		t.Errorf("after the rotation is confirmed: exit %d %+v", code, res)
	}
	doc := lsSigners(t)
	if n := len(doc.Libraries[0].Keys); n != 2 || doc.Libraries[0].LastSerial != 2 {
		t.Errorf("keys = %d (want old and new), last_serial %d (want 2)", n, doc.Libraries[0].LastSerial)
	}
}

func TestLibraryRotate(t *testing.T) {
	signersPath(t)
	old, next := sshKey(t), sshKey(t)
	dir := published(t, old)
	mustOK(t, "", "library", "rotate", dir, "--old-key", old.priv, "--new", next.priv+".pub")
	mustOK(t, "", "library", "rotate", dir, "--old-key", old.priv, "--new", next.priv+".pub")
	idx, err := signing.ParseIndex([]byte(readFile(t, filepath.Join(dir, signing.IndexFile))))
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Rotate) != 1 || idx.Rotate[0].From != old.fp || idx.Rotate[0].To != next.pub || !strings.HasPrefix(idx.Rotate[0].Signature, "-----BEGIN SSH SIGNATURE-----") {
		t.Fatalf("rotate entries = %+v, want one from the old key to the new", idx.Rotate)
	}
	// The rotation's signature is the old key's, namespace railswitch-rotate.
	allowed := filepath.Join(t.TempDir(), "allowed")
	sig := filepath.Join(t.TempDir(), "sig")
	if err := os.WriteFile(allowed, []byte(testLib+` namespaces="railswitch-rotate" `+old.pub+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sig, []byte(idx.Rotate[0].Signature), 0o600); err != nil {
		t.Fatal(err)
	}
	f := sshFake()
	if _, err := f.Run(t.Context(), sys.Cmd{Name: "ssh-keygen", Args: []string{"-Y", "verify", "-f", allowed, "-I", testLib, "-n", "railswitch-rotate", "-s", sig},
		Stdin: strings.NewReader(string(signing.RotationMessage(testLib, next.pub)))}); err != nil {
		t.Errorf("rotation signature: %v", err)
	}
	if code, _, errs := runSSH(t, "", "library", "rotate", dir, "--old-key", old.priv, "--new", old.priv+".pub"); code != 1 {
		t.Errorf("rotate to the same key: exit %d %s", code, errs)
	}
	if code, _, errs := runSSH(t, "", "library", "rotate", t.TempDir(), "--old-key", old.priv, "--new", next.priv+".pub"); code != 1 {
		t.Errorf("rotate with no library.toml: exit %d %s", code, errs)
	}
}
