package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/libraries"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/signing"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/tomlfile"
)

const libraryUsage = `usage: railswitch library add <url> --alias <alias> [--branch <branch>]
       railswitch library fetch [<alias>]
       railswitch library ls
       railswitch library rm <alias>
       railswitch library verify <dir>
       railswitch library trust <alias | dir> --fingerprint <fp> [--rotation | --new-signer]
       railswitch library index <dir> --library <id> [--serial <n>]
       railswitch library sign <dir> --key <private-key-file>
       railswitch library rotate <dir> --old-key <private-key-file> --new <public-key-file>`

// railswitch library add|fetch|ls|rm: the library repositories on the
// machine (D103, D104). verify|trust|index|sign|rotate: a library
// folder's signed index (D99): checking it against signers.toml, and
// publishing it.
func cmdLibrary(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", libraryUsage)
	}
	switch args[0] {
	case "add":
		return libraryAdd(ctx, e, args[1:])
	case "fetch":
		return libraryFetch(ctx, e, args[1:])
	case "ls":
		return libraryLs(e, args[1:])
	case "rm":
		return libraryRm(e, args[1:])
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
	var (
		t   *signing.Trust
		res *signing.Result
	)
	err := updateTrust(func(tr *signing.Trust) (bool, error) {
		r, err := signing.Verify(ctx, e.run, dir, tr)
		if err != nil {
			return false, err
		}
		t, res = tr, r
		return r.Status == signing.StatusTrusted, nil // the serial and revocations Verify recorded
	})
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(res); err != nil {
			return e.fail(err)
		}
	} else {
		printVerify(e, dir, res, t)
	}
	if res.Status != signing.StatusTrusted {
		return 1
	}
	return 0
}

// printVerify prints a Result. Everything but the status comes from the
// library, so it goes through safefile.Printable.
func printVerify(e *env, dir string, res *signing.Result, t *signing.Trust) {
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
	switch {
	case res.Status == signing.StatusUnknownSigner && res.KnownLibrary:
		fmt.Fprintf(e.stdout, "WARNING: %s; this index is signed by a different key and no trusted key vouched for it.\n"+
			"It may be an attacker's. Trust it only if the publisher confirms a new signer through another channel:\n"+
			"  railswitch library trust %s --fingerprint %s --new-signer\n", knownTrust(t, res.Library), d, res.Fingerprint)
	case res.Status == signing.StatusUnknownSigner:
		fmt.Fprintf(e.stdout, "Check the fingerprint %s with the publisher, then trust it:\n  railswitch library trust %s --fingerprint %s\n", res.Fingerprint, d, res.Fingerprint)
	case res.Status == signing.StatusRotation:
		fmt.Fprintf(e.stdout, "The trusted key %s vouches for the new key %s. Check it with the publisher, then confirm the rotation:\n  railswitch library trust %s --fingerprint %s --rotation\n", res.Fingerprint, res.NewFingerprint, d, res.NewFingerprint)
	}
}

// knownTrust says what t holds for lib, which has keys or revocations.
func knownTrust(t *signing.Trust, lib string) string {
	l := t.Library(lib)
	var fps []string
	for _, k := range l.Keys {
		fps = append(fps, k.Fingerprint)
	}
	if len(fps) == 0 {
		return fmt.Sprintf("this library trusts no key now but has %d revoked", len(l.Revoked))
	}
	return "this library already trusts " + strings.Join(fps, ", ")
}

func libraryTrust(ctx context.Context, e *env, args []string) int {
	fs := e.flags("library trust")
	fp := fs.String("fingerprint", "", "the `fingerprint` library verify reported, checked with the publisher")
	rotation := fs.Bool("rotation", false, "confirm a rotation: trust the new key the trusted one vouches for")
	newSigner := fs.Bool("new-signer", false, "trust a key no trusted key vouches for, for a library that already has keys or revocations")
	target, code, ok := parseNamed(e, fs, args, libraryUsage)
	if !ok {
		return code
	}
	if *fp == "" {
		return e.usageErr("library trust: --fingerprint is required (the one library verify or fetch reported)")
	}
	if *rotation && *newSigner {
		return e.usageErr("library trust: --rotation and --new-signer don't go together")
	}
	dir, promote, done, err := trustTarget(target)
	if err != nil {
		return e.fail(err)
	}
	defer done()
	var res, again *signing.Result
	var added string
	trust := func(t *signing.Trust) (bool, error) {
		var err error
		res, err = signing.Verify(ctx, e.run, dir, t)
		if err != nil {
			return false, err
		}
		var key string
		switch {
		case res.Status == signing.StatusTrusted && res.Fingerprint == *fp:
			return true, nil // the serial Verify recorded
		case res.Status == signing.StatusUnknownSigner && !*rotation && res.Fingerprint == *fp:
			if res.KnownLibrary && !*newSigner {
				return false, fmt.Errorf("library trust: %s; %s signed this index and no trusted key vouched for it. "+
					"Ask the publisher through another channel; to trust it anyway, pass --new-signer", knownTrust(t, res.Library), *fp)
			}
			key = res.Key
		case res.Status == signing.StatusRotation && *rotation && res.NewFingerprint == *fp:
			key = res.NewKey
		default:
			return false, trustRefusal(res, *fp, *rotation)
		}
		if added, err = t.Add(res.Library, key); err != nil {
			return false, err
		}
		// Verify again with the key trusted, so this index's serial and
		// revocations are recorded at once. An index that doesn't verify
		// then (rolled back) trusts nothing: the key is not saved.
		if again, err = signing.Verify(ctx, e.run, dir, t); err != nil {
			return false, err
		}
		if again.Status != signing.StatusTrusted {
			return false, fmt.Errorf("library trust: %s verifies as %s with the key trusted: %s; nothing was trusted",
				safefile.Printable(dir), again.Status, safefile.Printable(again.Reason))
		}
		return true, nil
	}
	err = updateTrust(func(t *signing.Trust) (bool, error) {
		save, err := trust(t)
		if err != nil || promote == nil {
			return save, err
		}
		// Under the trust lock: a promote that fails trusts nothing.
		return true, promote()
	})
	if err != nil {
		return e.fail(err)
	}
	then := ""
	if promote != nil {
		then = "; " + target + "'s fetch is now current"
	}
	if again == nil {
		fmt.Fprintf(e.stdout, "%s is already trusted for %s%s\n", *fp, res.Library, then)
		return 0
	}
	fmt.Fprintf(e.stdout, "trusted %s for %s%s\n", added, res.Library, then)
	return 0
}

// trustTarget is what library trust verifies: an alias in libraries.toml
// names its pending/ fetch, which promote makes current, with the alias's
// lock held until done; anything else is a library folder (write ./<dir>
// for a folder named like an alias).
func trustTarget(target string) (dir string, promote func() error, done func(), err error) {
	d, err := machine.DefaultDirs()
	if err != nil {
		return "", nil, nil, err
	}
	c, err := libraries.Load(libraries.Path(d))
	if err != nil {
		return "", nil, nil, err
	}
	if c.Library(target) == nil {
		return target, nil, func() {}, nil
	}
	unlock, err := libraries.Lock(d, target)
	if err != nil {
		return "", nil, nil, err
	}
	if _, err := libraries.ReadPending(d, target); err != nil {
		unlock()
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil, nil, fmt.Errorf("library %s has no fetch waiting for trust (library fetch %s fetches it)", target, target)
		}
		return "", nil, nil, err
	}
	return libraries.PendingDir(d, target), func() error { return libraries.Promote(d, target) }, unlock, nil
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
