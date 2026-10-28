package cli

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// ed25519Line is a fresh ed25519 public key line, made in Go: thirty
// ssh-keygen runs would be slow.
func ed25519Line(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var blob []byte
	for _, s := range [][]byte{[]byte("ssh-ed25519"), pub} {
		blob = binary.BigEndian.AppendUint32(blob, uint32(len(s)))
		blob = append(blob, s...)
	}
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob)
}

// Review M3: concurrent writers of signers.toml each load, change and
// save; without a lock one's save drops another's change.
func TestSignerConcurrentAdds(t *testing.T) {
	signersPath(t)
	const n = 30
	dir := t.TempDir()
	files := make([]string, n)
	for i := range files {
		files[i] = filepath.Join(dir, "k"+string(rune('a'+i))+".pub")
		if err := os.WriteFile(files[i], []byte(ed25519Line(t)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := range files {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], _, _ = runSSH(t, "", "signer", "add", testLib, files[i])
		}()
	}
	wg.Wait()
	for i, c := range codes {
		if c != 0 {
			t.Errorf("signer add %d: exit %d", i, c)
		}
	}
	if doc := lsSigners(t); len(doc.Libraries) != 1 || len(doc.Libraries[0].Keys) != n {
		got := 0
		if len(doc.Libraries) == 1 {
			got = len(doc.Libraries[0].Keys)
		}
		t.Errorf("signers.toml holds %d keys after %d concurrent adds", got, n)
	}
}
