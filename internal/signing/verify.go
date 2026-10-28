package signing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
)

// Status is a library's verdict.
type Status string

const (
	// StatusTrusted: signed by a key trusted for the library, serial not
	// below the last one verified.
	StatusTrusted Status = "trusted"
	// StatusUnknownSigner: validly signed, by a key not trusted for it.
	StatusUnknownSigner Status = "unknown-signer"
	// StatusRotation: signed by a new key a trusted key vouches for, not
	// yet confirmed.
	StatusRotation Status = "rotation"
	// StatusRolledBack: trusted, but older than an index already verified.
	StatusRolledBack Status = "rolled-back"
	// StatusRevoked: validly signed by a key revoked for the library.
	StatusRevoked Status = "revoked"
	// StatusBadSignature: the index or its signature is malformed, or the
	// signature does not cover the index.
	StatusBadSignature Status = "bad-signature"
)

// Result is Verify's verdict on a library folder.
type Result struct {
	Status  Status `json:"status"`
	Reason  string `json:"reason,omitempty"` // why bad-signature
	Library string `json:"library"`
	Serial  int64  `json:"serial"`
	// Fingerprint and Key are the signing key's; for a rotation, the
	// trusted key that vouches for it.
	Fingerprint string `json:"fingerprint,omitempty"`
	Key         string `json:"key,omitempty"`
	// NewFingerprint and NewKey are a rotation's new key, which signed the
	// index.
	NewFingerprint string `json:"new_fingerprint,omitempty"`
	NewKey         string `json:"new_key,omitempty"`
	// KnownLibrary: the trust store already holds keys, revocations or a
	// verified serial for the library, so a signer no trusted key vouches
	// for is not a first trust.
	KnownLibrary bool            `json:"known_library"`
	Packages     []PackageResult `json:"packages"`
}

// PackageResult is one package directory: Signed when the index is
// trusted, lists it, and its content hash matches.
type PackageResult struct {
	Ref    string `json:"ref"` // "" when the directory does not load
	Kind   string `json:"kind"`
	Dir    string `json:"dir"` // "<kind>s/<name>"
	Signed bool   `json:"signed"`
	Reason string `json:"reason,omitempty"`
}

// Verify checks the library folder dir against t. On StatusTrusted it
// updates t in memory: the library's last serial and the index's
// revocations; the caller saves it. An error is a failure to check
// (ssh-keygen missing, no temp dir, an unreadable packs/, options/, templates/ or
// agents/), never a verdict. A package is Signed only when the index is
// trusted.
func Verify(ctx context.Context, run sys.Runner, dir string, t *Trust) (*Result, error) {
	res, err := judge(ctx, run, dir, t)
	if err != nil || res.Status == StatusTrusted {
		return res, err
	}
	for i := range res.Packages {
		if p := &res.Packages[i]; p.Signed {
			p.Signed, p.Reason = false, "the index is not trusted"
		}
	}
	return res, nil
}

// judge is Verify's verdict, Packages judged against the index alone.
func judge(ctx context.Context, run sys.Runner, dir string, t *Trust) (*Result, error) {
	bad := func(format string, args ...any) (*Result, error) {
		return &Result{Status: StatusBadSignature, Reason: fmt.Sprintf(format, args...), Packages: []PackageResult{}}, nil
	}
	data, err := safefile.Read(filepath.Join(dir, IndexFile), safefile.Limit)
	if err != nil {
		return bad("%s: %v", IndexFile, err)
	}
	idx, err := ParseIndex(data)
	if err != nil {
		return bad("%v", err)
	}
	sig, err := safefile.Read(filepath.Join(dir, SigFile), safefile.Limit)
	if err != nil {
		return bad("%s: %v", SigFile, err)
	}
	signer, err := SignerOf(sig)
	if err != nil {
		return bad("%s: %v", SigFile, err)
	}
	fp, _ := Fingerprint(signer)

	res := &Result{Library: idx.Library, Serial: idx.Serial, Fingerprint: fp, Key: signer}
	res.Packages, err = checkPackages(dir, idx)
	if err != nil {
		return nil, err
	}
	l := t.Library(idx.Library)
	res.KnownLibrary = l != nil && (len(l.Keys) > 0 || len(l.Revoked) > 0 || l.LastSerial > 0)
	v := verifier{ctx: ctx, run: run, library: idx.Library}
	// Every branch has ssh-keygen check the signature against only the
	// keys that branch accepts.
	var keys []string
	switch {
	case l.key(fp) != nil:
		res.Status, keys = StatusTrusted, l.trustedKeys()
	case l.revoked(fp):
		res.Status, keys = StatusRevoked, []string{signer}
	default:
		res.Status, keys = StatusUnknownSigner, []string{signer}
		rot, err := v.rotation(l, idx, fp)
		if err != nil {
			return nil, err
		}
		if rot != nil {
			from := l.key(rot.From)
			res.Status = StatusRotation
			res.Fingerprint, res.Key = from.Fingerprint, from.Key
			res.NewFingerprint, res.NewKey = fp, signer
		}
	}
	ok, err := v.check(keys, NamespaceLibrary, sig, data)
	if err != nil {
		return nil, err
	}
	if !ok {
		res.Status, res.Reason = StatusBadSignature, IndexFile+": the signature does not verify"
		res.NewFingerprint, res.NewKey = "", ""
		return res, nil
	}
	sum := sha256Hex(data)
	if res.Status == StatusTrusted || res.Status == StatusRotation {
		switch {
		case idx.Serial < l.LastSerial:
			res.Status = StatusRolledBack
			res.Reason = fmt.Sprintf("serial %d is below %d, already verified", idx.Serial, l.LastSerial)
			return res, nil
		case idx.Serial == l.LastSerial && l.LastIndex != "" && l.LastIndex != sum:
			res.Status = StatusRolledBack
			res.Reason = fmt.Sprintf("serial %d was already used by another index", idx.Serial)
			return res, nil
		}
	}
	if res.Status == StatusTrusted {
		l.LastSerial, l.LastIndex = idx.Serial, sum
		for _, r := range idx.Revoke {
			l.revoke(r.Fingerprint)
		}
	}
	return res, nil
}

// checkPackages judges every package the index lists and every package
// directory it doesn't.
func checkPackages(dir string, idx *Index) ([]PackageResult, error) {
	found, err := packageDirs(dir)
	if err != nil {
		return nil, err
	}
	listed := map[string]bool{}
	out := []PackageResult{}
	for _, p := range idx.Packages {
		r, _ := pack.ParseRef(p.Ref) // checked by ParseIndex
		d := pkgDir{p.Kind, r.Name}
		listed[d.rel()] = true
		pr := PackageResult{Ref: p.Ref, Kind: p.Kind, Dir: d.rel()}
		ref, sum, err := loadPackage(dir, d.kind, d.name)
		switch {
		case err != nil:
			pr.Reason = err.Error()
		case ref != r:
			pr.Reason = fmt.Sprintf("%s declares %s", d.rel(), ref)
		case sum != p.SHA256:
			pr.Reason = "content differs from " + IndexFile
		default:
			pr.Signed = true
		}
		out = append(out, pr)
	}
	for _, d := range found {
		if listed[d.rel()] {
			continue
		}
		pr := PackageResult{Kind: d.kind, Dir: d.rel(), Reason: "not in " + IndexFile}
		if ref, _, err := loadPackage(dir, d.kind, d.name); err == nil {
			pr.Ref = ref.String()
		}
		out = append(out, pr)
	}
	return out, nil
}

// verifier runs ssh-keygen -Y verify for one library.
type verifier struct {
	ctx     context.Context
	run     sys.Runner
	library string
}

// rotation is the index's rotation to the key fp that a key trusted for l
// vouches for with a valid signature, or nil.
func (v verifier) rotation(l *Library, idx *Index, fp string) (*Rotation, error) {
	for i, r := range idx.Rotate {
		from := l.key(r.From)
		if from == nil {
			continue
		}
		if to, _ := Fingerprint(r.To); to != fp {
			continue
		}
		ok, err := v.check([]string{from.Key}, NamespaceRotate, []byte(r.Signature), RotationMessage(v.library, r.To))
		if err != nil {
			return nil, err
		}
		if ok {
			return &idx.Rotate[i], nil
		}
	}
	return nil, nil
}

// check reports whether sig is a valid signature over msg, in namespace,
// by one of keys for the library. The allowed-signers and signature files
// live in a 0700 temp dir that is always removed.
func (v verifier) check(keys []string, namespace string, sig, msg []byte) (bool, error) {
	if len(keys) == 0 {
		return false, nil
	}
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	tmp, err := os.MkdirTemp(base, "railswitch-verify-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)
	var allowed strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&allowed, "%s namespaces=%q %s\n", v.library, namespace, k)
	}
	allowedPath, sigPath := filepath.Join(tmp, "allowed_signers"), filepath.Join(tmp, "sig")
	if err := os.WriteFile(allowedPath, []byte(allowed.String()), 0o600); err != nil {
		return false, err
	}
	if err := os.WriteFile(sigPath, sig, 0o600); err != nil {
		return false, err
	}
	_, err = v.run.Run(v.ctx, sys.Cmd{
		Name:  "ssh-keygen",
		Args:  []string{"-Y", "verify", "-f", allowedPath, "-I", v.library, "-n", namespace, "-s", sigPath},
		Stdin: bytes.NewReader(msg),
	})
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sys.ErrExit):
		return false, nil
	default:
		return false, fmt.Errorf("ssh-keygen -Y verify: %w", err)
	}
}
