package cli

import (
	"errors"
	"io"
	"os"
	"syscall"
	"unsafe"
)

func termios(fd uintptr, req uintptr, t *syscall.Termios) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(t))); errno != 0 {
		return errno
	}
	return nil
}

// isTerminal reports whether f is a terminal.
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	return termios(f.Fd(), syscall.TCGETS, &t) == nil
}

// readNoEcho reads one line from the terminal f with echo off, without its
// newline. The saved terminal state is restored on every path.
func readNoEcho(f *os.File) (line []byte, err error) {
	fd := f.Fd()
	var saved syscall.Termios
	if err := termios(fd, syscall.TCGETS, &saved); err != nil {
		return nil, err
	}
	quiet := saved
	quiet.Lflag &^= syscall.ECHO
	if err := termios(fd, syscall.TCSETS, &quiet); err != nil {
		return nil, err
	}
	defer func() {
		if rerr := termios(fd, syscall.TCSETS, &saved); rerr != nil && err == nil {
			err = rerr
		}
	}()
	var b [1]byte
	for {
		n, rerr := f.Read(b[:])
		if n == 1 {
			if b[0] == '\n' {
				return line, nil
			}
			line = append(line, b[0])
		}
		switch {
		case rerr == nil, errors.Is(rerr, syscall.EINTR):
		case errors.Is(rerr, io.EOF):
			return line, nil
		default:
			return line, rerr
		}
	}
}
