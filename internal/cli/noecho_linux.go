package cli

import (
	"context"
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
// newline. The saved terminal state is restored on every path, including a
// cancelled ctx (Ctrl-C), which returns ctx's error at once; the blocked
// read is then abandoned (the process is about to exit).
func readNoEcho(ctx context.Context, f *os.File) (line []byte, err error) {
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
	type result struct {
		line []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		l, err := readLine(f)
		done <- result{l, err}
	}()
	select {
	case r := <-done:
		return r.line, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// readLine reads f up to a newline or EOF, without the newline.
func readLine(f *os.File) ([]byte, error) {
	var line []byte
	var b [1]byte
	for {
		n, err := f.Read(b[:])
		if n == 1 {
			if b[0] == '\n' {
				return line, nil
			}
			line = append(line, b[0])
		}
		switch {
		case err == nil, errors.Is(err, syscall.EINTR):
		case errors.Is(err, io.EOF):
			return line, nil
		default:
			return line, err
		}
	}
}
