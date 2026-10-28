package cli

import (
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/signing"
)

// Review L6: signer rm distrusts; signer revoke revokes, and a revoked key
// is never trusted again from the CLI.
func TestSignerRevoke(t *testing.T) {
	path := signersPath(t)
	k, other := sshKey(t), sshKey(t)
	mustOK(t, "", "signer", "add", testLib, k.priv+".pub")
	mustOK(t, "", "signer", "add", testLib, other.priv+".pub")
	out := mustOK(t, "", "signer", "revoke", testLib, k.fp)
	if !strings.Contains(out, "revoked "+k.fp) {
		t.Errorf("signer revoke printed %q", out)
	}
	doc := lsSigners(t)
	if l := doc.Libraries[0]; len(l.Keys) != 1 || l.Keys[0].Fingerprint != other.fp || len(l.Revoked) != 1 || l.Revoked[0].Fingerprint != k.fp {
		t.Fatalf("after revoke: %+v", l)
	}

	before := readFile(t, path)
	if code, _, errs := runSSH(t, "", "signer", "add", testLib, k.priv+".pub"); code != 1 || !strings.Contains(errs, "revoked") {
		t.Errorf("signer add of a revoked key: exit %d %s", code, errs)
	}
	dir := published(t, k)
	if code, res := verifyJSON(t, dir); code != 1 || res.Status != signing.StatusRevoked {
		t.Errorf("verify, signed by the revoked key: exit %d %+v", code, res)
	}
	for _, flag := range [][]string{nil, {"--new-signer"}} {
		args := append([]string{"library", "trust", dir, "--fingerprint", k.fp}, flag...)
		if code, _, _ := runSSH(t, "", args...); code != 1 {
			t.Errorf("%q: exit %d, want 1", args, code)
		}
	}
	if code, _, _ := runSSH(t, "", "signer", "rm", testLib, k.fp); code != 1 {
		t.Errorf("signer rm of a revoked key: exit %d, want 1", code)
	}
	if readFile(t, path) != before {
		t.Error("signers.toml changed")
	}

	// Revoking again is a no-op; a key never trusted can be revoked ahead.
	mustOK(t, "", "signer", "revoke", testLib, k.fp)
	stranger := sshKey(t)
	mustOK(t, "", "signer", "revoke", "example.org/other", stranger.fp)
	if code, _, _ := runSSH(t, "", "signer", "add", "example.org/other", stranger.priv+".pub"); code != 1 {
		t.Errorf("add after a revoke ahead: exit %d, want 1", code)
	}
	for _, args := range [][]string{
		{"signer", "revoke", testLib, "SHA256:short"},
		{"signer", "revoke", "Bad/../id", k.fp},
	} {
		if code, _, _ := runSSH(t, "", args...); code != 1 {
			t.Errorf("%q: exit %d, want 1", args, code)
		}
	}
	if code, _, _ := runSSH(t, "", "signer", "revoke", testLib); code != 2 {
		t.Errorf("revoke with no fingerprint: exit %d, want 2", code)
	}
}
