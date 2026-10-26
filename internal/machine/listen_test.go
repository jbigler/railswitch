package machine

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func TestListening(t *testing.T) {
	got, err := Listening("testdata/proc")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[int]bool{13109: true, 19001: true}; !maps.Equal(got, want) {
		t.Errorf("Listening = %v; want %v (13110 is ESTABLISHED, not LISTEN)", got, want)
	}
}

// TestListeningNoTCP6: a host without IPv6 has no tcp6; that is no ports.
// A missing tcp is an error: /proc is unusable.
func TestListeningNoTCP6(t *testing.T) {
	dir := t.TempDir()
	if _, err := Listening(dir); err == nil {
		t.Error("Listening without net/tcp succeeded")
	}
	if err := os.MkdirAll(filepath.Join(dir, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "net", "tcp"), []byte("  sl  local_address rem_address   st\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Listening(dir)
	if err != nil || len(got) != 0 {
		t.Errorf("Listening(header-only tcp, no tcp6) = %v, %v; want none", got, err)
	}
}
