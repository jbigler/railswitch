package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/signing"
)

// Review M2: an unvouched key for a library that already trusts a key is
// not a first trust. verify says so, and trust needs --new-signer.
func TestLibraryTrustKnownLibrary(t *testing.T) {
	path := signersPath(t)
	old, other := sshKey(t), sshKey(t)
	mustOK(t, "", "signer", "add", testLib, old.priv+".pub")
	dir := published(t, other)

	code, res := verifyJSON(t, dir)
	if code != 1 || res.Status != signing.StatusUnknownSigner || !res.KnownLibrary {
		t.Errorf("verify --json: exit %d %+v; want unknown-signer, known_library", code, res)
	}
	code, out, _ := runSSH(t, "", "library", "verify", dir)
	if code != 1 || !strings.Contains(out, "this library already trusts "+old.fp) || !strings.Contains(out, "no trusted key vouched for it") || !strings.Contains(out, "--new-signer") {
		t.Errorf("verify text: exit %d\n%s", code, out)
	}

	before := readFile(t, path)
	code, _, errs := runSSH(t, "", "library", "trust", dir, "--fingerprint", other.fp)
	if code != 1 || !strings.Contains(errs, "--new-signer") {
		t.Errorf("trust without --new-signer: exit %d %s", code, errs)
	}
	if readFile(t, path) != before {
		t.Error("a refused trust changed signers.toml")
	}
	mustOK(t, "", "library", "trust", dir, "--fingerprint", other.fp, "--new-signer")
	if code, res := verifyJSON(t, dir); code != 0 || res.Status != signing.StatusTrusted {
		t.Errorf("after --new-signer: exit %d %+v", code, res)
	}
}

// A library whose entry holds only revocations is known too.
func TestLibraryTrustRevocationsOnly(t *testing.T) {
	path := signersPath(t)
	k := sshKey(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "schema = 1\n[[library]]\nid = \"" + testLib + "\"\nlast_serial = 0\n[[library.revoked]]\nfingerprint = \"SHA256:" + strings.Repeat("A", 43) + "\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := published(t, k)
	if _, res := verifyJSON(t, dir); !res.KnownLibrary {
		t.Errorf("revocations only: %+v; want known_library", res)
	}
	if code, _, errs := runSSH(t, "", "library", "trust", dir, "--fingerprint", k.fp); code != 1 || !strings.Contains(errs, "--new-signer") {
		t.Errorf("trust, revocations only: exit %d %s", code, errs)
	}
	if readFile(t, path) != body {
		t.Error("a refused trust changed signers.toml")
	}
}

// A first trust needs no --new-signer, and known_library is false.
func TestLibraryTrustFirst(t *testing.T) {
	signersPath(t)
	k := sshKey(t)
	dir := published(t, k)
	if _, res := verifyJSON(t, dir); res.KnownLibrary {
		t.Errorf("no entry: %+v; want known_library false", res)
	}
	code, out, _ := runSSH(t, "", "library", "verify", dir)
	if code != 1 || strings.Contains(out, "already trusts") || strings.Contains(out, "--new-signer") {
		t.Errorf("first verify text: %s", out)
	}
	mustOK(t, "", "library", "trust", dir, "--fingerprint", k.fp)
}
