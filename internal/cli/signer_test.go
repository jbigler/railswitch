package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/signing"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

const testLib = "github.com/acme/stack-library"

type testKey struct{ priv, pub, line, fp string }

// sshKey generates an ed25519 key with no passphrase in a temp dir: pub is
// the key as signers.toml stores it, line the .pub file's line.
func sshKey(t *testing.T) testKey {
	t.Helper()
	priv := filepath.Join(t.TempDir(), "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "pub@example", "-f", priv).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	line := strings.TrimSpace(readFile(t, priv+".pub"))
	pub, err := signing.ParseKey(line)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := signing.Fingerprint(pub)
	if err != nil {
		t.Fatal(err)
	}
	return testKey{priv: priv, pub: pub, line: line, fp: fp}
}

// signersPath points XDG_CONFIG_HOME at a temp dir and returns where
// signers.toml goes.
func signersPath(t *testing.T) string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	return filepath.Join(cfg, "railswitch", signing.TrustFile)
}

// sshFake runs ssh-keygen for real and nothing else.
func sshFake() *systest.Fake { return &systest.Fake{Passthrough: []string{"ssh-keygen"}} }

func runSSH(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	return runStdin(t, sshFake(), stdin, args...)
}

func mustOK(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	return mustRunStdin(t, sshFake(), stdin, args...)
}

func lsSigners(t *testing.T) signerDoc {
	t.Helper()
	var doc signerDoc
	out := mustOK(t, "", "--json", "signer", "ls")
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("signer ls --json: %v\n%s", err, out)
	}
	return doc
}

func TestSignerAddLsRm(t *testing.T) {
	path := signersPath(t)
	if out := mustOK(t, "", "--json", "signer", "ls"); strings.TrimSpace(out) != `{"libraries":[]}` {
		t.Errorf("empty signer ls = %s", out)
	}
	k := sshKey(t)
	out := mustOK(t, "", "signer", "add", testLib, k.priv+".pub")
	if !strings.Contains(out, k.fp) {
		t.Errorf("signer add printed %q, want the fingerprint %s", out, k.fp)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("signers.toml: %v %v", fi, err)
	}
	var raw map[string][]map[string]any
	if err := json.Unmarshal([]byte(mustOK(t, "", "--json", "signer", "ls")), &raw); err != nil {
		t.Fatal(err)
	}
	want := map[string][]map[string]any{"libraries": {{
		"id": testLib, "last_serial": 0.0,
		"keys":    []any{map[string]any{"key": k.pub, "fingerprint": k.fp, "added": time.Now().Format(time.DateOnly)}},
		"revoked": []any{},
	}}}
	if !reflect.DeepEqual(raw, want) {
		t.Errorf("signer ls --json = %v\nwant %v", raw, want)
	}
	if out := mustOK(t, "", "signer", "ls"); !strings.Contains(out, testLib) || !strings.Contains(out, k.fp) {
		t.Errorf("signer ls = %q", out)
	}
	mustOK(t, "", "signer", "rm", testLib, k.fp)
	if doc := lsSigners(t); len(doc.Libraries) != 0 {
		t.Errorf("after rm: %+v", doc)
	}
	if code, _, errs := runSSH(t, "", "signer", "rm", testLib, k.fp); code != 1 || !strings.Contains(errs, "not trusted") {
		t.Errorf("rm of an untrusted key: exit %d, %s", code, errs)
	}
}

// A revoked key, a malformed key and a bad library id are refused and
// leave signers.toml as it was.
func TestSignerAddRevokedRefused(t *testing.T) {
	path := signersPath(t)
	k := sshKey(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "schema = 1\n[[library]]\nid = \"" + testLib + "\"\nlast_serial = 3\n[[library.revoked]]\nfingerprint = \"" + k.fp + "\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"revoked":   {"signer", "add", testLib, k.priv + ".pub"},
		"malformed": {"signer", "add", testLib, k.priv},
		"bad id":    {"signer", "add", "Bad/../id", k.priv + ".pub"},
	} {
		if code, _, errs := runSSH(t, "", args...); code != 1 {
			t.Errorf("%s: exit %d, want 1\n%s", name, code, errs)
		}
		if got := readFile(t, path); got != body {
			t.Errorf("%s: signers.toml changed:\n%s", name, got)
		}
	}
	if code, _, errs := runSSH(t, "", "signer", "add", testLib, k.priv+".pub"); !strings.Contains(errs, "revoked") {
		t.Errorf("revoked: exit %d, %s", code, errs)
	}
}

func TestSignerAddStdin(t *testing.T) {
	signersPath(t)
	k := sshKey(t)
	out := mustOK(t, k.line+"\n", "signer", "add", testLib, "-")
	if !strings.Contains(out, k.fp) {
		t.Errorf("signer add - printed %q", out)
	}
	doc := lsSigners(t)
	if len(doc.Libraries) != 1 || len(doc.Libraries[0].Keys) != 1 || doc.Libraries[0].Keys[0].Key != k.pub {
		t.Errorf("signer ls = %+v", doc)
	}
	if code, _, _ := runSSH(t, "", "signer", "add", testLib, "-"); code != 1 {
		t.Errorf("empty stdin: exit %d, want 1", code)
	}
	if code, _, _ := runSSH(t, k.line+"\n"+k.line+"\n", "signer", "add", "other/lib", "-"); code != 1 {
		t.Errorf("two lines on stdin: exit %d, want 1", code)
	}
}
