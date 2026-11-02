// Package pty opens Linux pseudo-terminals for the dashboard's terminals
// (D149): /dev/ptmx and the ioctls that unlock it, name its peer and set
// its size.
package pty

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

// winsize is the kernel's struct winsize.
type winsize struct{ Row, Col, X, Y uint16 }

// Open returns a new pseudo-terminal: the master (pollable, so read
// deadlines work) and its slave, neither the caller's controlling terminal.
func Open() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("pty: %w", err)
	}
	defer func() {
		if err != nil {
			master.Close()
		}
	}()
	var unlock int32
	if err = ioctl(master, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		return nil, nil, fmt.Errorf("pty: unlock: %w", err)
	}
	var n uint32
	if err = ioctl(master, syscall.TIOCGPTN, unsafe.Pointer(&n)); err != nil {
		return nil, nil, fmt.Errorf("pty: number: %w", err)
	}
	slave, err = os.OpenFile("/dev/pts/"+strconv.FormatUint(uint64(n), 10), os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("pty: %w", err)
	}
	return master, slave, nil
}

// Resize sets the terminal's size; the kernel signals SIGWINCH to its
// foreground process group.
func Resize(f *os.File, rows, cols int) error {
	ws := winsize{Row: uint16(rows), Col: uint16(cols)}
	if err := ioctl(f, syscall.TIOCSWINSZ, unsafe.Pointer(&ws)); err != nil {
		return fmt.Errorf("pty: resize: %w", err)
	}
	return nil
}

// Size is the terminal's size.
func Size(f *os.File) (rows, cols int, err error) {
	var ws winsize
	if err := ioctl(f, syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil {
		return 0, 0, fmt.Errorf("pty: size: %w", err)
	}
	return int(ws.Row), int(ws.Col), nil
}

func ioctl(f *os.File, req uint, arg unsafe.Pointer) error {
	c, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if err := c.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(req), uintptr(arg))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}
