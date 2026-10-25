package cli

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// openPty returns a pty master and its slave.
func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Skipf("unlockpt: %v", e)
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		t.Skipf("ptsname: %v", e)
	}
	s, err := os.OpenFile("/dev/pts/"+itoa(n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open slave: %v", err)
	}
	t.Cleanup(func() { s.Close(); m.Close() })
	return m, s
}

func itoa(n uint32) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func echoOn(t *testing.T, f *os.File) bool {
	t.Helper()
	var tm syscall.Termios
	if err := termios(f.Fd(), syscall.TCGETS, &tm); err != nil {
		t.Fatal(err)
	}
	return tm.Lflag&syscall.ECHO != 0
}

func TestReadNoEchoLine(t *testing.T) {
	m, s := openPty(t)
	if !isTerminal(s) || !echoOn(t, s) {
		t.Fatal("slave: not a terminal with echo")
	}
	go m.Write([]byte("tok\n"))
	line, err := readNoEcho(context.Background(), s)
	if err != nil || string(line) != "tok" {
		t.Fatalf("got %q %v", line, err)
	}
	if !echoOn(t, s) {
		t.Error("echo not restored")
	}
}

func TestReadNoEchoCancel(t *testing.T) {
	_, s := openPty(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := readNoEcho(ctx, s); done <- err }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prompt ignored cancellation")
	}
	if !echoOn(t, s) {
		t.Error("echo not restored after cancel")
	}
}
