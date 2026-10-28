package testfixture

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/jbigler/railswitch/internal/libraries"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack/builtin"
	"github.com/jbigler/railswitch/internal/signing"
)

// Lib is a library's verified checkout on a test machine, as a trusted
// fetch leaves it: current/ signed with Key, which signers.toml trusts for
// ID, and fetch.toml beside it.
type Lib struct {
	Alias, ID string
	Dirs      machine.Dirs
	Key       Key
	Serial    int64
	Commit    string
}

// Key is an ed25519 key pair made with ssh-keygen.
type Key struct {
	Priv, Pub, Fingerprint string
}

// NewKey makes a key pair in a temp dir.
func NewKey(t testing.TB) Key {
	t.Helper()
	priv := filepath.Join(t.TempDir(), "k")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-q", "-C", "", "-f", priv).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, err := os.ReadFile(priv + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	k, err := signing.ParseKey(string(pub))
	if err != nil {
		t.Fatal(err)
	}
	fp, err := signing.Fingerprint(k)
	if err != nil {
		t.Fatal(err)
	}
	return Key{Priv: priv, Pub: k, Fingerprint: fp}
}

// Library writes alias's checkout into d's data dir holding the built-in
// package dirs rels ("options/redis"), publishes it as library id at serial
// 1 signed with a new key, and trusts that key for id.
func Library(t testing.TB, d machine.Dirs, alias, id string, rels ...string) *Lib {
	t.Helper()
	l := &Lib{Alias: alias, ID: id, Dirs: d, Key: NewKey(t)}
	for _, rel := range rels {
		CopyBuiltin(t, l.Dir(), rel)
	}
	l.Publish(t)
	l.Trust(t)
	return l
}

// Dir is the checkout, current/.
func (l *Lib) Dir() string { return libraries.CurrentDir(l.Dirs, l.Alias) }

// Publish indexes and signs the checkout at the next serial, under a new
// commit, and records it as fetched: what a fetch of a new version leaves.
func (l *Lib) Publish(t testing.TB) {
	t.Helper()
	l.Serial++
	l.Commit = string(bytes.Repeat([]byte{byte('a' + l.Serial%6)}, 40))
	idx, err := signing.BuildIndex(l.Dir(), l.ID, l.Serial, nil)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(l.Dir(), signing.IndexFile)
	if err := os.WriteFile(file, idx.Encode(), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Remove(file + ".sig")
	if out, err := exec.Command("ssh-keygen", "-Y", "sign", "-f", l.Key.Priv, "-n", signing.NamespaceLibrary, file).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen -Y sign: %v\n%s", err, out)
	}
	rec := libraries.Record{Schema: libraries.RecordSchema, ID: l.ID, Commit: l.Commit, Serial: l.Serial, Status: signing.StatusTrusted, Fetched: time.Now().UTC().Truncate(time.Second), Signer: l.Key.Fingerprint}
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(rec); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libraries.Dir(l.Dirs, l.Alias), "fetch.toml"), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Trust trusts l's key for its library in signers.toml.
func (l *Lib) Trust(t testing.TB) {
	t.Helper()
	l.updateTrust(t, func(tr *signing.Trust) error { _, err := tr.Add(l.ID, l.Key.Pub); return err })
}

// Distrust removes l's key from signers.toml (signer rm).
func (l *Lib) Distrust(t testing.TB) {
	t.Helper()
	l.updateTrust(t, func(tr *signing.Trust) error { return tr.Remove(l.ID, l.Key.Fingerprint) })
}

func (l *Lib) updateTrust(t testing.TB, fn func(*signing.Trust) error) {
	t.Helper()
	err := signing.UpdateTrust(filepath.Join(l.Dirs.Config, signing.TrustFile), func(tr *signing.Trust) (bool, error) {
		return true, fn(tr)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// CopyBuiltin copies the built-in package directory rel ("options/redis")
// to dir/rel.
func CopyBuiltin(t testing.TB, dir, rel string) {
	t.Helper()
	err := fs.WalkDir(builtin.FS, rel, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if e.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := fs.ReadFile(builtin.FS, p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
