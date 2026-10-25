package safefile

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadRegularFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".ruby-version")
	if err := os.WriteFile(p, []byte("3.3.6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Read(p, Limit)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got) != "3.3.6\n" {
		t.Fatalf("got %q", got)
	}
}

func TestReadSymlinkRefused(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("3.3.6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(link, Limit); !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
}

func TestReadFIFORefusedQuickly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Read(p, Limit)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrRefused) {
			t.Fatalf("err = %v, want ErrRefused", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Read on FIFO did not return within 100ms")
	}
}

func TestReadDirectoryRefused(t *testing.T) {
	if _, err := Read(t.TempDir(), Limit); !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
}

func TestReadOversizedRefused(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), Limit+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(p, Limit); !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
}

func TestReadMissingFile(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), "missing"), Limit)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}
