package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/signing"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/tomlfile"
)

const libraryUsage = `usage: railswitch library verify <dir>
       railswitch library trust <dir> --fingerprint <fp> [--rotation]
       railswitch library index <dir> --library <id> [--serial <n>]
       railswitch library sign <dir> --key <private-key-file>
       railswitch library rotate <dir> --old-key <private-key-file> --new <public-key-file>`

// railswitch library verify|trust|index|sign|rotate: a library folder's
// signed index (D99): checking it against signers.toml, and publishing it.
func cmdLibrary(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", libraryUsage)
	}
	switch args[0] {
	case "verify":
		return libraryVerify(ctx, e, args[1:])
	case "trust":
		return libraryTrust(ctx, e, args[1:])
	case "index":
		return libraryIndex(e, args[1:])
	case "sign":
		return librarySign(ctx, e, args[1:])
	case "rotate":
		return libraryRotate(ctx, e, args[1:])
	}
	return e.usageErr("%s", libraryUsage)
}

func libraryVerify(ctx context.Context, e *env, args []string) int {
	fs := e.flags("library verify")
	dir, code, ok := parseNamed(e, fs, args, libraryUsage)
	if !ok {
		return code
	}
	t, path, err := loadTrust()
	if err != nil {
		return e.fail(err)
	}
	res, err := signing.Verify(ctx, e.run, dir, t)
	if err != nil {
		return e.fail(err)
	}
	if res.Status == signing.StatusTrusted {
		if err := t.Save(path); err != nil {
			return e.fail(err)
		}
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(res); err != nil {
			return e.fail(err)
		}
	} else {
		printVerify(e, dir, res)
	}
	if res.Status != signing.StatusTrusted {
		return 1
	}
	return 0
}

// printVerify prints a Result. Everything but the status comes from the
// library, so it goes through safefile.Printable.
func printVerify(e *env, dir string, res *signing.Result) {
	p := safefile.Printable
	if res.Library != "" {
		fmt.Fprintf(e.stdout, "library %s, serial %d\n", p(res.Library), res.Serial)
	}
	fmt.Fprintf(e.stdout, "status  %s\n", res.Status)
	if res.Reason != "" {
		fmt.Fprintf(e.stdout, "reason  %s\n", p(res.Reason))
	}
	if res.Fingerprint != "" {
		fmt.Fprintf(e.stdout, "signer  %s\n", res.Fingerprint)
	}
	if res.NewFingerprint != "" {
		fmt.Fprintf(e.stdout, "new     %s\n", res.NewFingerprint)
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	for _, pk := range res.Packages {
		state, ref := "signed", pk.Ref
		if !pk.Signed {
			state = "not signed"
		}
		if ref == "" {
			ref = "-"
		}
		line := fmt.Sprintf("  %s\t%s\t%s", state, p(ref), p(pk.Dir))
		if pk.Reason != "" {
			line += "\t" + p(pk.Reason)
		}
		fmt.Fprintln(tw, line)
	}
	tw.Flush()
	d := p(dir)
	switch res.Status {
	case signing.StatusUnknownSigner:
		fmt.Fprintf(e.stdout, "Check the fingerprint %s with the publisher, then trust it:\n  railswitch library trust %s --fingerprint %s\n", res.Fingerprint, d, res.Fingerprint)
	case signing.StatusRotation:
		fmt.Fprintf(e.stdout, "The trusted key %s vouches for the new key %s. Check it with the publisher, then confirm the rotation:\n  railswitch library trust %s --fingerprint %s --rotation\n", res.Fingerprint, res.NewFingerprint, d, res.NewFingerprint)
	}
}

func libraryTrust(ctx context.Context, e *env, args []string) int {
	fs := e.flags("library trust")
	fp := fs.String("fingerprint", "", "the `fingerprint` library verify reported, checked with the publisher")
	rotation := fs.Bool("rotation", false, "confirm a rotation: trust the new key the trusted one vouches for")
	dir, code, ok := parseNamed(e, fs, args, libraryUsage)
	if !ok {
		return code
	}
	if *fp == "" {
		return e.usageErr("library trust: --fingerprint is required (the one library verify %s reports)", dir)
	}
	t, path, err := loadTrust()
	if err != nil {
		return e.fail(err)
	}
	res, err := signing.Verify(ctx, e.run, dir, t)
	if err != nil {
		return e.fail(err)
	}
	var key string
	switch {
	case res.Status == signing.StatusTrusted && res.Fingerprint == *fp:
		if err := t.Save(path); err != nil { // the serial Verify recorded
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "%s is already trusted for %s\n", *fp, res.Library)
		return 0
	case res.Status == signing.StatusUnknownSigner && !*rotation && res.Fingerprint == *fp:
		key = res.Key
	case res.Status == signing.StatusRotation && *rotation && res.NewFingerprint == *fp:
		key = res.NewKey
	default:
		return e.fail(trustRefusal(res, *fp, *rotation))
	}
	added, err := t.Add(res.Library, key)
	if err != nil {
		return e.fail(err)
	}
	// Verify again with the key trusted, so this index's serial and
	// revocations are recorded at once.
	again, err := signing.Verify(ctx, e.run, dir, t)
	if err != nil {
		return e.fail(err)
	}
	if err := t.Save(path); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "trusted %s for %s\n", added, res.Library)
	if again.Status != signing.StatusTrusted {
		fmt.Fprintf(e.stderr, "railswitch: library trust: the key is trusted, but %s verifies as %s: %s\n", safefile.Printable(dir), again.Status, safefile.Printable(again.Reason))
		return 1
	}
	return 0
}

// trustRefusal says why library trust refuses: the index's state is not
// the one --rotation names, or fp is not the fingerprint it reports.
func trustRefusal(res *signing.Result, fp string, rotation bool) error {
	switch {
	case res.Status != signing.StatusUnknownSigner && res.Status != signing.StatusRotation:
		reason := ""
		if res.Reason != "" {
			reason = ": " + safefile.Printable(res.Reason)
		}
		return fmt.Errorf("library trust: the index verifies as %s%s; there is no key to trust", res.Status, reason)
	case rotation && res.Status != signing.StatusRotation:
		return fmt.Errorf("library trust: the index is signed by %s, which no trusted key vouches for; to trust a new signer, drop --rotation", res.Fingerprint)
	case !rotation && res.Status == signing.StatusRotation:
		return fmt.Errorf("library trust: the index is signed by %s, a rotation from the trusted %s; confirm it with --rotation", res.NewFingerprint, res.Fingerprint)
	case rotation:
		return fmt.Errorf("library trust: the rotation's new key is %s, not %s; nothing trusted", res.NewFingerprint, fp)
	}
	return fmt.Errorf("library trust: the index is signed by %s, not %s; nothing trusted", res.Fingerprint, fp)
}

// readIndex is dir's library.toml, its bytes, or nil and fs.ErrNotExist.
func readIndex(dir string) (*signing.Index, []byte, error) {
	data, err := safefile.Read(filepath.Join(dir, signing.IndexFile), safefile.Limit)
	if err != nil {
		return nil, nil, err
	}
	idx, err := signing.ParseIndex(data)
	if err != nil {
		return nil, nil, err
	}
	return idx, data, nil
}

func libraryIndex(e *env, args []string) int {
	fset := e.flags("library index")
	lib := fset.String("library", "", "the library's `id`, as signers.toml names it")
	serial := fset.Int64("serial", 0, "the index's serial `n` (default: the current one + 1, else 1)")
	dir, code, ok := parseNamed(e, fset, args, libraryUsage)
	if !ok {
		return code
	}
	if *lib == "" {
		return e.usageErr("library index: --library is required")
	}
	keep, _, err := readIndex(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return e.fail(err)
	case keep.Library != *lib:
		return e.fail(fmt.Errorf("library index: %s is the index of %s, not %s", signing.IndexFile, keep.Library, *lib))
	}
	n := *serial
	switch {
	case n == 0 && keep != nil:
		n = keep.Serial + 1
	case n == 0:
		n = 1
	case keep != nil && n <= keep.Serial:
		return e.fail(fmt.Errorf("library index: serial %d is not above the current %d; each index gets a new, higher serial", n, keep.Serial))
	}
	idx, err := signing.BuildIndex(dir, *lib, n, keep)
	if err != nil {
		return e.fail(err)
	}
	if err := tomlfile.WriteAtomic(filepath.Join(dir, signing.IndexFile), idx.Encode(), 0o644); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "wrote %s: %s serial %d, %d packages; sign it: railswitch library sign %s --key <private-key-file>\n",
		signing.IndexFile, idx.Library, idx.Serial, len(idx.Packages), safefile.Printable(dir))
	return 0
}

func librarySign(ctx context.Context, e *env, args []string) int {
	fs := e.flags("library sign")
	key := fs.String("key", "", "the signing key's private `file` (or its .pub with the key in ssh-agent)")
	dir, code, ok := parseNamed(e, fs, args, libraryUsage)
	if !ok {
		return code
	}
	if *key == "" {
		return e.usageErr("library sign: --key is required")
	}
	_, data, err := readIndex(dir)
	if err != nil {
		return e.fail(err)
	}
	sig, err := e.sshSign(ctx, *key, signing.NamespaceLibrary, data)
	if err != nil {
		return e.fail(err)
	}
	if err := tomlfile.WriteAtomic(filepath.Join(dir, signing.SigFile), sig, 0o644); err != nil {
		return e.fail(err)
	}
	signer, _ := signing.SignerOf(sig) // sshSign checked it
	fp, _ := signing.Fingerprint(signer)
	fmt.Fprintf(e.stdout, "wrote %s, signed by %s\n", signing.SigFile, fp)
	return 0
}

func libraryRotate(ctx context.Context, e *env, args []string) int {
	fs := e.flags("library rotate")
	oldKey := fs.String("old-key", "", "the trusted key's private `file`, which vouches for the new one")
	newKey := fs.String("new", "", "the new key's public `file`")
	dir, code, ok := parseNamed(e, fs, args, libraryUsage)
	if !ok {
		return code
	}
	if *oldKey == "" || *newKey == "" {
		return e.usageErr("library rotate: --old-key and --new are required")
	}
	to, err := e.readPublicKey(*newKey)
	if err != nil {
		return e.fail(err)
	}
	toFP, _ := signing.Fingerprint(to)
	idx, _, err := readIndex(dir)
	if err != nil {
		return e.fail(err)
	}
	sig, err := e.sshSign(ctx, *oldKey, signing.NamespaceRotate, signing.RotationMessage(idx.Library, to))
	if err != nil {
		return e.fail(err)
	}
	signer, _ := signing.SignerOf(sig)
	from, _ := signing.Fingerprint(signer)
	if from == toFP {
		return e.fail(fmt.Errorf("library rotate: --old-key and --new are the same key %s", from))
	}
	rot := signing.Rotation{From: from, To: to, Signature: string(sig)}
	replaced := false
	for i, r := range idx.Rotate {
		if r.From == from && r.To == to {
			idx.Rotate[i], replaced = rot, true
		}
	}
	if !replaced {
		idx.Rotate = append(idx.Rotate, rot)
	}
	idx.Serial++ // a new index: a serial names one
	out := idx.Encode()
	if _, err := signing.ParseIndex(out); err != nil {
		return e.fail(err)
	}
	if err := tomlfile.WriteAtomic(filepath.Join(dir, signing.IndexFile), out, 0o644); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "added a rotation from %s to %s to %s, serial %d; sign it with the new key: railswitch library sign %s --key <new-private-key-file>\n",
		from, toFP, signing.IndexFile, idx.Serial, safefile.Printable(dir))
	return 0
}

// sshSign is ssh-keygen's signature over msg in namespace with key. It
// signs a copy in a temp dir: ssh-keygen asks before overwriting a .sig
// and, with no answer, keeps the old one. No timeout and the terminal
// attached: ssh-keygen may ask for the key's passphrase or a touch.
func (e *env) sshSign(ctx context.Context, key, namespace string, msg []byte) ([]byte, error) {
	abs, err := filepath.Abs(key)
	if err != nil {
		return nil, err
	}
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	tmp, err := os.MkdirTemp(base, "railswitch-sign-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	file := filepath.Join(tmp, "message")
	if err := os.WriteFile(file, msg, 0o600); err != nil {
		return nil, err
	}
	_, err = e.run.Run(ctx, sys.Cmd{
		Name:    "ssh-keygen",
		Args:    []string{"-Y", "sign", "-f", abs, "-n", namespace, file},
		Stdin:   e.stdin,
		Stdout:  e.stderr,
		Stderr:  e.stderr,
		Timeout: sys.NoTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("ssh-keygen -Y sign: %w", err)
	}
	sig, err := os.ReadFile(file + ".sig")
	if err != nil {
		return nil, err
	}
	if _, err := signing.SignerOf(sig); err != nil {
		return nil, fmt.Errorf("ssh-keygen -Y sign wrote an unreadable signature: %w", err)
	}
	return sig, nil
}
