// Package signing verifies a library's signed index (D99) against the keys
// the user trusts for that library (D100), with vouched rotation, revocation
// and rollback protection (D101).
package signing

import (
	"bytes"
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
	return f[0] + " " + f[1], nil
}

// checkBlob checks a key blob's type: in KeyTypes and the blob's first
// string.
func checkBlob(typ string, blob []byte) error {
	if !slices.Contains(KeyTypes, typ) {
		return fmt.Errorf("public key type %q is not one of %s", typ, strings.Join(KeyTypes, ", "))
	}
	inner, _, err := readString(blob)
	if err != nil {
		return fmt.Errorf("public key: %w", err)
	}
	if string(inner) != typ {
		return fmt.Errorf("public key: the blob is a %q key, the line says %q", inner, typ)
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
