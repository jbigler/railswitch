package machine

import (
	"maps"
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
func TestListeningNoTCP6(t *testing.T) {
	dir := t.TempDir()
	got, err := Listening(dir)
	if err != nil || len(got) != 0 {
		t.Errorf("Listening(empty) = %v, %v; want none", got, err)
	}
}
