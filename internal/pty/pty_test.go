package pty

import (
	"os"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	m, s, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(); s.Close() })
	return m, s
}

func TestOpenEcho(t *testing.T) {
	m, s := open(t)
	if _, err := s.WriteString("hi\n"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	buf := make([]byte, 64)
	for !strings.Contains(got.String(), "hi") {
		n, err := m.Read(buf)
		if err != nil {
			t.Fatalf("read: %v (got %q)", err, got.String())
		}
		got.Write(buf[:n])
	}
}

func TestResize(t *testing.T) {
	m, s := open(t)
	if err := Resize(m, 40, 120); err != nil {
		t.Fatal(err)
	}
	rows, cols, err := Size(s)
	if err != nil || rows != 40 || cols != 120 {
		t.Fatalf("Size = %d, %d, %v; want 40, 120", rows, cols, err)
	}
}
