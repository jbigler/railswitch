package signing

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wire is b as an SSH wire string.
func wire(b []byte) []byte {
	o := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(o, uint32(len(b)))
	copy(o[4:], b)
	return o
}

func concat(parts ...[]byte) []byte {
	var o []byte
	for _, p := range parts {
		o = append(o, p...)
	}
	return o
}

// fields splits a key blob into its wire strings.
func fields(t *testing.T, blob []byte) [][]byte {
	t.Helper()
	var out [][]byte
	for len(blob) > 0 {
		s, rest, err := readString(blob)
		if err != nil {
			t.Fatal(err)
		}
		out, blob = append(out, s), rest
	}
	return out
}

func keyBlob(t *testing.T, k testKey) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(strings.Fields(k.pub)[1])
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func line(typ string, blob []byte) string {
	return typ + " " + base64.StdEncoding.EncodeToString(blob)
}

// padSig rewrites the RSA public key inside file's SSHSIG with a leading
// zero byte on e, and returns that key: the same key to OpenSSH, another
// blob to hash.
func padSig(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(string(data)), sigBegin), sigEnd)
	blob, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		t.Fatal(err)
	}
	key, rest, err := readString(blob[10:]) // "SSHSIG" and the version
	if err != nil {
		t.Fatal(err)
	}
	f := fields(t, key)
	padded := concat(wire(f[0]), wire(append([]byte{0}, f[1]...)), wire(f[2]))
	out := concat(blob[:10], wire(padded), rest)
	if err := os.WriteFile(file, []byte(sigBegin+"\n"+base64.StdEncoding.EncodeToString(out)+"\n"+sigEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return line("ssh-rsa", padded)
}

// Review H1: an RSA key whose e carries a leading zero is the same key to
// OpenSSH but hashed to another fingerprint, so a revoked key came back as
// unknown-signer and could be trusted again.
func TestRSAAliasOfRevokedKey(t *testing.T) {
	good, bad := keygen(t, "ed25519"), keygen(t, "rsa", "-b", "3072")
	dir := newLibrary(t)
	tr := trust(t, libA, good, bad)
	publish(t, dir, libA, 2, good, func(idx *Index) { idx.Revoke = []Revocation{{bad.fp}} })
	wantStatus(t, verify(t, dir, tr), StatusTrusted)

	publish(t, dir, libA, 3, bad, nil)
	wantStatus(t, verify(t, dir, tr), StatusRevoked)
	alias := padSig(t, filepath.Join(dir, SigFile))
	wantStatus(t, verify(t, dir, tr), StatusBadSignature)
	if _, err := tr.Add(libA, alias); err == nil {
		t.Error("Add trusted a padded alias of a revoked RSA key")
	}
	if _, err := Fingerprint(alias); err == nil {
		t.Error("Fingerprint accepted a padded RSA key")
	}
}

// Every key type's blob is parsed field by field: nothing after the last
// field, the right lengths, canonical numbers.
func TestKeyBlobStrict(t *testing.T) {
	ed := keyBlob(t, keygen(t, "ed25519"))
	ec := keyBlob(t, keygen(t, "ecdsa", "-b", "256"))
	ec384 := keyBlob(t, keygen(t, "ecdsa", "-b", "384"))
	ec521 := keyBlob(t, keygen(t, "ecdsa", "-b", "521"))
	rsa := keyBlob(t, keygen(t, "rsa", "-b", "3072"))
	edF, ecF, rsaF := fields(t, ed), fields(t, ec), fields(t, rsa)
	const skType = "sk-ssh-ed25519@openssh.com"
	sk := concat(wire([]byte(skType)), wire(edF[1]), wire([]byte("ssh:")))

	for _, good := range []string{line("ssh-ed25519", ed), line("ecdsa-sha2-nistp256", ec), line("ecdsa-sha2-nistp384", ec384), line("ecdsa-sha2-nistp521", ec521), line("ssh-rsa", rsa), line(skType, sk)} {
		if _, err := Fingerprint(good); err != nil {
			t.Errorf("Fingerprint(%.40s…): %v", good, err)
		}
	}
	offCurve := append([]byte{}, ecF[2]...)
	offCurve[len(offCurve)-1] ^= 1
	plus := func(b []byte, x ...byte) []byte { return append(append([]byte{}, b...), x...) }
	for name, l := range map[string]string{
		"ed25519 trailing":     line("ssh-ed25519", plus(ed, 0)),
		"ed25519 short":        line("ssh-ed25519", concat(wire(edF[0]), wire(edF[1][:31]))),
		"ed25519 long":         line("ssh-ed25519", concat(wire(edF[0]), wire(plus(edF[1], 0)))),
		"ecdsa trailing":       line("ecdsa-sha2-nistp256", plus(ec, 0)),
		"ecdsa curve mismatch": line("ecdsa-sha2-nistp256", concat(wire(ecF[0]), wire([]byte("nistp384")), wire(ecF[2]))),
		"ecdsa off curve":      line("ecdsa-sha2-nistp256", concat(wire(ecF[0]), wire(ecF[1]), wire(offCurve))),
		"ecdsa compressed":     line("ecdsa-sha2-nistp256", concat(wire(ecF[0]), wire(ecF[1]), wire(plus([]byte{2}, ecF[2][1:33]...)))),
		"rsa trailing":         line("ssh-rsa", plus(rsa, 0)),
		"rsa padded e":         line("ssh-rsa", concat(wire(rsaF[0]), wire(plus([]byte{0}, rsaF[1]...)), wire(rsaF[2]))),
		"rsa padded n":         line("ssh-rsa", concat(wire(rsaF[0]), wire(rsaF[1]), wire(plus([]byte{0}, rsaF[2]...)))),
		"rsa negative e":       line("ssh-rsa", concat(wire(rsaF[0]), wire([]byte{0x81}), wire(rsaF[2]))),
		"rsa zero e":           line("ssh-rsa", concat(wire(rsaF[0]), wire(nil), wire(rsaF[2]))),
		"rsa no n":             line("ssh-rsa", concat(wire(rsaF[0]), wire(rsaF[1]))),
		"sk trailing":          line(skType, plus(sk, 0)),
		"sk short":             line(skType, concat(wire([]byte(skType)), wire(edF[1][:31]), wire([]byte("ssh:")))),
		"sk no application":    line(skType, concat(wire([]byte(skType)), wire(edF[1]))),
	} {
		if _, err := ParseKey(l); err == nil {
			t.Errorf("%s: ParseKey accepted it", name)
		}
	}
}

// Review L4: a package is signed only when the index is trusted.
func TestPackagesUnsignedUnlessTrusted(t *testing.T) {
	good, stranger, next := keygen(t, "ed25519"), keygen(t, "ed25519"), keygen(t, "ed25519")
	dir := newLibrary(t)
	tr := trust(t, libA, good)
	wantUnsigned := func(res *Result) {
		t.Helper()
		if len(res.Packages) == 0 {
			t.Fatalf("%s: no packages", res.Status)
		}
		for _, p := range res.Packages {
			if p.Signed || p.Reason != "the index is not trusted" {
				t.Errorf("%s: package %+v; want unsigned, the index is not trusted", res.Status, p)
			}
		}
	}

	publish(t, dir, libA, 2, good, nil)
	other := filepath.Join(t.TempDir(), "other")
	os.WriteFile(other, []byte("other bytes"), 0o644)
	signFile(t, good, NamespaceLibrary, other)
	sig, _ := os.ReadFile(other + ".sig")
	os.WriteFile(filepath.Join(dir, SigFile), sig, 0o644)
	res := verify(t, dir, tr)
	wantStatus(t, res, StatusBadSignature)
	wantUnsigned(res)

	publish(t, dir, libA, 3, stranger, nil)
	res = verify(t, dir, tr)
	wantStatus(t, res, StatusUnknownSigner)
	wantUnsigned(res)

	publish(t, dir, libA, 3, next, func(idx *Index) { idx.Rotate = []Rotation{rotation(t, libA, good, next)} })
	res = verify(t, dir, tr)
	wantStatus(t, res, StatusRotation)
	wantUnsigned(res)
}

// Review L5: a serial names one index. Another index at the last verified
// serial is rolled-back; the same index again is trusted.
func TestVerifySameSerialOtherIndex(t *testing.T) {
	k, next := keygen(t, "ed25519"), keygen(t, "ed25519")
	dir := newLibrary(t)
	tr := trust(t, libA, k)
	publish(t, dir, libA, 5, k, nil)
	wantStatus(t, verify(t, dir, tr), StatusTrusted)
	first, _ := os.ReadFile(filepath.Join(dir, IndexFile))
	if got := tr.Library(libA).LastIndex; got != sha256Hex(first) {
		t.Errorf("last_index = %q; want the sha256 of library.toml", got)
	}

	publish(t, dir, libA, 5, k, func(idx *Index) { idx.Revoke = []Revocation{{next.fp}} })
	res := verify(t, dir, tr)
	wantStatus(t, res, StatusRolledBack)
	if res.Reason != "serial 5 was already used by another index" {
		t.Errorf("reason = %q", res.Reason)
	}
	if l := tr.Library(libA); len(l.Revoked) != 0 || l.LastIndex != sha256Hex(first) {
		t.Errorf("a rolled-back index changed trust: %+v", l)
	}

	publish(t, dir, libA, 5, next, func(idx *Index) { idx.Rotate = []Rotation{rotation(t, libA, k, next)} })
	wantStatus(t, verify(t, dir, tr), StatusRolledBack)

	publish(t, dir, libA, 5, k, nil)
	wantStatus(t, verify(t, dir, tr), StatusTrusted)
}

// signers.toml holds each key exactly as Add writes it: no comment, no
// control characters, canonical base64.
func TestLoadTrustKeyCanonical(t *testing.T) {
	k := keygen(t, "ed25519")
	path := filepath.Join(t.TempDir(), TrustFile)
	entry := func(key string) string {
		return "schema = 1\n[[library]]\nid = \"" + libA + "\"\nlast_serial = 0\n[[library.key]]\nkey = \"" + key +
			"\"\nfingerprint = \"" + k.fp + "\"\nadded = 2026-10-28\n"
	}
	os.WriteFile(path, []byte(entry(k.pub)), 0o600)
	if _, err := LoadTrust(path); err != nil {
		t.Fatalf("a good signers.toml: %v", err)
	}
	for name, key := range map[string]string{
		"comment":    k.pub + " a comment",
		"newline":    k.pub + `\n`,
		"control":    strings.Replace(k.pub, " ", `\u0007 `, 1),
		"two spaces": strings.Replace(k.pub, " ", "  ", 1),
	} {
		os.WriteFile(path, []byte(entry(key)), 0o600)
		if _, err := LoadTrust(path); err == nil {
			t.Errorf("%s: LoadTrust accepted %q", name, key)
		}
	}
}
