// Package signing verifies a library's signed index (D99) against the keys
// the user trusts for that library (D100), with vouched rotation, revocation
// and rollback protection (D101).
package signing

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// KeyTypes are the public key types a signer may use.
var KeyTypes = []string{
	"ssh-ed25519",
	"ecdsa-sha2-nistp256",
	"ecdsa-sha2-nistp384",
	"ecdsa-sha2-nistp521",
	"ssh-rsa",
	"sk-ssh-ed25519@openssh.com",
}

var fingerprintRe = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)

// ParseKey reads an authorized-key line "<type> <base64> [comment]" and
// returns it as "<type> <base64>", the comment dropped.
func ParseKey(authorizedKey string) (string, error) {
	f := strings.Fields(authorizedKey)
	if len(f) < 2 {
		return "", errors.New("not a public key: want \"<type> <base64> [comment]\"")
	}
	blob, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil {
		return "", fmt.Errorf("public key: %w", err)
	}
	if err := checkBlob(f[0], blob); err != nil {
		return "", err
	}
	return f[0] + " " + base64.StdEncoding.EncodeToString(blob), nil
}

// checkBlob parses a key blob field by field: its type is typ and in
// KeyTypes, each field has the length and form OpenSSH accepts in the one
// encoding OpenSSH writes, and nothing follows the last. Fingerprints hash
// the blob as given, so a second encoding of one key (an RSA mpint with a
// leading zero, a string OpenSSH strips a trailing NUL from) would give it
// a second fingerprint: a revoked key trusted again under another name.
func checkBlob(typ string, blob []byte) error {
	if !slices.Contains(KeyTypes, typ) {
		return fmt.Errorf("public key type %q is not one of %s", typ, strings.Join(KeyTypes, ", "))
	}
	var f [][]byte
	for rest := blob; len(rest) > 0; {
		s, r, err := readString(rest)
		if err != nil {
			return fmt.Errorf("public key: %w", err)
		}
		f, rest = append(f, s), r
	}
	if len(f) == 0 || string(f[0]) != typ {
		var inner []byte
		if len(f) > 0 {
			inner = f[0]
		}
		return fmt.Errorf("public key: the blob is a %q key, the line says %q", inner, typ)
	}
	if err := checkFields(typ, f[1:]); err != nil {
		return fmt.Errorf("public key: %s: %w", typ, err)
	}
	return nil
}

// checkFields checks the fields after a key blob's type.
func checkFields(typ string, f [][]byte) error {
	want := 2
	if typ == "ssh-ed25519" {
		want = 1
	}
	if len(f) != want {
		return fmt.Errorf("%d fields after the type, want %d", len(f), want)
	}
	switch typ {
	case "ssh-ed25519":
		return checkLen(f[0], ed25519.PublicKeySize)
	case "sk-ssh-ed25519@openssh.com":
		if bytes.IndexByte(f[1], 0) >= 0 {
			return errors.New("the application holds a NUL byte")
		}
		return checkLen(f[0], ed25519.PublicKeySize)
	case "ssh-rsa":
		if err := checkMpint(f[0]); err != nil {
			return fmt.Errorf("e: %w", err)
		}
		if err := checkMpint(f[1]); err != nil {
			return fmt.Errorf("n: %w", err)
		}
		return nil
	}
	curve := strings.TrimPrefix(typ, "ecdsa-sha2-")
	if string(f[0]) != curve {
		return fmt.Errorf("curve %q, want %q", f[0], curve)
	}
	c := map[string]ecdh.Curve{"nistp256": ecdh.P256(), "nistp384": ecdh.P384(), "nistp521": ecdh.P521()}[curve]
	// NewPublicKey takes only an uncompressed point on the curve, each
	// coordinate below the field prime.
	if _, err := c.NewPublicKey(f[1]); err != nil {
		return errors.New("the point is not an uncompressed point on the curve")
	}
	return nil
}

func checkLen(b []byte, n int) error {
	if len(b) != n {
		return fmt.Errorf("the key is %d bytes, want %d", len(b), n)
	}
	return nil
}

// checkMpint refuses an SSH mpint that is not positive or not minimal (a
// leading zero byte the next byte's top bit doesn't need).
func checkMpint(b []byte) error {
	switch {
	case len(b) == 0:
		return errors.New("zero")
	case b[0]&0x80 != 0:
		return errors.New("negative")
	case b[0] == 0 && (len(b) == 1 || b[1]&0x80 == 0):
		return errors.New("a superfluous leading zero byte")
	}
	return nil
}

// Fingerprint is the key's SHA256 fingerprint as ssh-keygen -l prints it:
// "SHA256:" and the unpadded base64 of sha256 over the key blob.
func Fingerprint(authorizedKey string) (string, error) {
	k, err := ParseKey(authorizedKey)
	if err != nil {
		return "", err
	}
	blob, _ := base64.StdEncoding.DecodeString(strings.Fields(k)[1])
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:]), nil
}

const (
	sigBegin = "-----BEGIN SSH SIGNATURE-----"
	sigEnd   = "-----END SSH SIGNATURE-----"
)

// SignerOf is the public key inside an armored SSH signature (the SSHSIG
// blob's publickey field), as "<type> <base64>". It says who claims to
// have signed; only ssh-keygen -Y verify says the signature holds.
func SignerOf(armored []byte) (string, error) {
	s := strings.TrimSpace(string(armored))
	body, ok := strings.CutPrefix(s, sigBegin)
	if !ok {
		return "", errors.New("signature: no " + sigBegin + " line")
	}
	body, ok = strings.CutSuffix(body, sigEnd)
	if !ok {
		return "", errors.New("signature: no " + sigEnd + " line")
	}
	blob, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(body), ""))
	if err != nil {
		return "", fmt.Errorf("signature: %w", err)
	}
	rest, ok := bytes.CutPrefix(blob, []byte("SSHSIG"))
	if !ok || len(rest) < 4 {
		return "", errors.New("signature: not an SSHSIG blob")
	}
	if v := binary.BigEndian.Uint32(rest); v != 1 {
		return "", fmt.Errorf("signature: SSHSIG version %d, want 1", v)
	}
	key, _, err := readString(rest[4:])
	if err != nil {
		return "", fmt.Errorf("signature: public key: %w", err)
	}
	typ, _, err := readString(key)
	if err != nil {
		return "", fmt.Errorf("signature: public key: %w", err)
	}
	if err := checkBlob(string(typ), key); err != nil {
		return "", fmt.Errorf("signature: %w", err)
	}
	return string(typ) + " " + base64.StdEncoding.EncodeToString(key), nil
}

// readString reads one SSH wire string: a uint32 length and that many bytes.
func readString(b []byte) (s, rest []byte, err error) {
	if len(b) < 4 {
		return nil, nil, errors.New("truncated")
	}
	n := binary.BigEndian.Uint32(b)
	if uint64(n) > uint64(len(b)-4) {
		return nil, nil, errors.New("truncated")
	}
	return b[4 : 4+n], b[4+n:], nil
}
