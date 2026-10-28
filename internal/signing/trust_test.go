package signing

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Each UpdateTrust's load, change and save is one step: concurrent ones
// never lose another's change.
func TestUpdateTrustSerializes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "railswitch", TrustFile)
	const n = 8
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := UpdateTrust(path, func(tr *Trust) (bool, error) {
				l := tr.Library(libA)
				if l == nil {
					l = &Library{ID: libA}
					tr.Libraries = append(tr.Libraries, l)
				}
				serial := l.LastSerial
				time.Sleep(5 * time.Millisecond)
				l.LastSerial = serial + 1
				return true, nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	tr, err := LoadTrust(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.Library(libA).LastSerial; got != n {
		t.Errorf("last_serial = %d after %d increments", got, n)
	}
}

func TestUpdateTrustNoSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), TrustFile)
	change := func(tr *Trust) { tr.Libraries = append(tr.Libraries, &Library{ID: libA}) }
	if err := UpdateTrust(path, func(tr *Trust) (bool, error) { change(tr); return false, nil }); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := UpdateTrust(path, func(tr *Trust) (bool, error) { change(tr); return true, boom }); !errors.Is(err, boom) {
		t.Errorf("err = %v; want fn's", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("signers.toml written: %v", err)
	}
}
