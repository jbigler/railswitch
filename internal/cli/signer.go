package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/signing"
)

const signerUsage = `usage: railswitch signer ls
       railswitch signer add <library> <public-key-file|->
       railswitch signer rm <library> <fingerprint>
       railswitch signer revoke <library> <fingerprint>`

// railswitch signer ls|add|rm|revoke: the keys trusted per library (D100),
// in signers.toml. rm distrusts a key, which signer add can trust again;
// revoke never lets it be trusted again from the CLI.
func cmdSigner(_ context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", signerUsage)
	}
	switch args[0] {
	case "ls":
		return signerLs(e, args[1:])
	case "add":
		return signerAdd(e, args[1:])
	case "rm":
		return signerRm(e, args[1:])
	case "revoke":
		return signerRevoke(e, args[1:])
	}
	return e.usageErr("%s", signerUsage)
}

// trustPath is signers.toml in the machine config directory.
func trustPath() (string, error) {
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return "", err
	}
	return filepath.Join(dirs.Config, signing.TrustFile), nil
}

// updateTrust is signing.UpdateTrust on signers.toml: fn runs under its
// lock, and t is saved when fn returns save.
func updateTrust(fn func(t *signing.Trust) (save bool, err error)) error {
	path, err := trustPath()
	if err != nil {
		return err
	}
	return signing.UpdateTrust(path, fn)
}

// signerDoc is `signer ls --json` (docs/json.md).
type signerDoc struct {
	Libraries []signerLib `json:"libraries"`
}

type signerLib struct {
	ID         string          `json:"id"`
	LastSerial int64           `json:"last_serial"`
	Keys       []signerKey     `json:"keys"`
	Revoked    []signerRevoked `json:"revoked"`
}

type signerKey struct {
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
	Added       string `json:"added"` // YYYY-MM-DD
}

type signerRevoked struct {
	Fingerprint string `json:"fingerprint"`
}

func newSignerDoc(t *signing.Trust) signerDoc {
	doc := signerDoc{Libraries: []signerLib{}}
	for _, l := range t.Libraries {
		sl := signerLib{ID: l.ID, LastSerial: l.LastSerial, Keys: []signerKey{}, Revoked: []signerRevoked{}}
		for _, k := range l.Keys {
			sl.Keys = append(sl.Keys, signerKey{Key: k.Key, Fingerprint: k.Fingerprint, Added: k.Added.Format(time.DateOnly)})
		}
		for _, r := range l.Revoked {
			sl.Revoked = append(sl.Revoked, signerRevoked{Fingerprint: r.Fingerprint})
		}
		doc.Libraries = append(doc.Libraries, sl)
	}
	return doc
}

func signerLs(e *env, args []string) int {
	fs := e.flags("signer ls")
	if _, code, ok := parseArgs(e, fs, args, 0, signerUsage); !ok {
		return code
	}
	path, err := trustPath()
	if err != nil {
		return e.fail(err)
	}
	t, err := signing.LoadTrust(path) // a read: writes replace the file atomically
	if err != nil {
		return e.fail(err)
	}
	doc := newSignerDoc(t)
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	if len(doc.Libraries) == 0 {
		fmt.Fprintln(e.stdout, "no trusted signers; add one with railswitch signer add <library> <public-key-file>")
		return 0
	}
	for _, l := range doc.Libraries {
		fmt.Fprintf(e.stdout, "%s (last serial %d)\n", l.ID, l.LastSerial)
		tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
		for _, k := range l.Keys {
			fmt.Fprintf(tw, "  key\t%s\tadded %s\n", k.Fingerprint, k.Added)
		}
		for _, r := range l.Revoked {
			fmt.Fprintf(tw, "  revoked\t%s\n", r.Fingerprint)
		}
		tw.Flush()
	}
	return 0
}

// maxKeyLine bounds a public key read from stdin; an RSA key of 16384 bits
// is under 3 KiB.
const maxKeyLine = 16 << 10

func signerAdd(e *env, args []string) int {
	fs := e.flags("signer add")
	pos, code, ok := parseArgs(e, fs, args, 2, signerUsage)
	if !ok {
		return code
	}
	lib, src := pos[0], pos[1]
	key, err := e.readPublicKey(src)
	if err != nil {
		return e.fail(err)
	}
	var fp string
	err = updateTrust(func(t *signing.Trust) (bool, error) {
		fp, err = t.Add(lib, key)
		return true, err
	})
	if err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "trusted %s for %s\n", fp, lib)
	return 0
}

// readPublicKey reads one authorized-key line from the file src, or from
// stdin for "-". The file is the user's own (~/.ssh/id_ed25519.pub, which
// may be a symlink), so plain os.Open.
func (e *env) readPublicKey(src string) (string, error) {
	r := e.stdin
	if src == "-" {
		src = "stdin"
	} else {
		f, err := os.Open(src)
		if err != nil {
			return "", err
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, maxKeyLine+1))
	if err != nil {
		return "", fmt.Errorf("%s: %w", src, err)
	}
	line := bytes.TrimSpace(data)
	switch {
	case len(data) > maxKeyLine:
		return "", fmt.Errorf("%s: over %d bytes, not a public key", src, maxKeyLine)
	case len(line) == 0:
		return "", fmt.Errorf("%s: no public key", src)
	case bytes.ContainsAny(line, "\r\n"):
		return "", fmt.Errorf("%s: want one public key line", src)
	}
	key, err := signing.ParseKey(string(line))
	if err != nil {
		return "", fmt.Errorf("%s: %w", src, err)
	}
	return key, nil
}

func signerRm(e *env, args []string) int {
	fs := e.flags("signer rm")
	pos, code, ok := parseArgs(e, fs, args, 2, signerUsage)
	if !ok {
		return code
	}
	err := updateTrust(func(t *signing.Trust) (bool, error) {
		if err := t.Remove(pos[0], pos[1]); err != nil {
			return false, errors.New(safefile.Printable(err.Error()))
		}
		return true, nil
	})
	if err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "distrusted %s for %s\n", safefile.Printable(pos[1]), safefile.Printable(pos[0]))
	return 0
}

func signerRevoke(e *env, args []string) int {
	fs := e.flags("signer revoke")
	pos, code, ok := parseArgs(e, fs, args, 2, signerUsage)
	if !ok {
		return code
	}
	err := updateTrust(func(t *signing.Trust) (bool, error) {
		if err := t.Revoke(pos[0], pos[1]); err != nil {
			return false, errors.New(safefile.Printable(err.Error()))
		}
		return true, nil
	})
	if err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "revoked %s for %s: it is never trusted again for this library (this can't be undone from the CLI)\n", pos[1], pos[0])
	return 0
}
