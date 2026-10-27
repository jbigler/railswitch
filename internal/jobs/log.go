package jobs

import (
	"bytes"
	"io"
	"os"
	"syscall"
	"unicode/utf8"
)

// droppedLine is appended to a log the tail truncated.
const droppedLine = "… output dropped (log over 8 MiB)\n"

// backstop is the size at which a log the tail is behind on is truncated
// unread. The child writes the file directly, so it can outrun the tail
// between two size checks; the headroom keeps the file within 2*LogLimit.
const backstop = 2*LogLimit - 2<<20

// logTail follows a job's log file: the child writes it (Stdout and
// Stderr), the tail reads it back as cleaned, capped lines and keeps the
// file under 2*LogLimit by truncating it.
type logTail struct {
	w     *os.File // the child's side; used to truncate and mark drops
	r     *os.File
	wake  <-chan struct{} // written to; nil when inotify is unavailable
	watch *os.File        // the inotify descriptor behind wake
	off   int64
	buf   []byte
	line  []byte // the current raw line, at most MaxLine bytes
	cut   bool   // the current line passed MaxLine; the rest is discarded
}

// openLog creates path 0600 for the child and opens it for reading; a
// symlink at path is refused.
func openLog(path string) (*logTail, error) {
	w, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	r, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		w.Close()
		return nil, err
	}
	t := &logTail{w: w, r: r, buf: make([]byte, 64<<10)}
	t.wake, t.watch = watch(path)
	return t, nil
}

// watch wakes the tail as soon as the child writes, so a fast writer
// can't run far ahead of it. Without inotify the tail only polls.
func watch(path string) (<-chan struct{}, *os.File) {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return nil, nil
	}
	if _, err := syscall.InotifyAddWatch(fd, path, syscall.IN_MODIFY|syscall.IN_DONT_FOLLOW); err != nil {
		syscall.Close(fd)
		return nil, nil
	}
	f := os.NewFile(uintptr(fd), "inotify") // non-blocking: Close ends Read
	wake := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := f.Read(buf); err != nil {
				return
			}
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}()
	return wake, f
}

func (t *logTail) Close() {
	if t.watch != nil {
		t.watch.Close()
	}
	t.w.Close()
	t.r.Close()
}

// poll reads the next chunk of the log and returns the complete lines in
// it, whether output was dropped (a cut line or a truncated log), and
// whether anything was read.
func (t *logTail) poll() (lines []string, dropped, read bool, err error) {
	n, err := t.r.ReadAt(t.buf, t.off)
	if err != nil && err != io.EOF {
		return nil, false, false, err
	}
	t.off += int64(n)
	lines, dropped = t.feed(t.buf[:n], lines)
	fi, err := t.r.Stat()
	if err != nil {
		return lines, dropped, n > 0, err
	}
	if size := fi.Size(); size > backstop || (t.off >= LogLimit && t.off >= size) {
		lines = t.flush(lines)
		if err := t.w.Truncate(0); err != nil {
			return lines, true, n > 0, err
		}
		if _, err := t.w.WriteString(droppedLine); err != nil {
			return lines, true, n > 0, err
		}
		t.off = 0
		dropped = true
	}
	return lines, dropped, n > 0, nil
}

// feed splits p into lines, appending the complete ones to lines.
func (t *logTail) feed(p []byte, lines []string) ([]string, bool) {
	dropped := false
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		seg := p
		if i >= 0 {
			seg = p[:i]
		}
		if !t.cut {
			if room := MaxLine - len(t.line); len(seg) > room {
				t.line = append(t.line, seg[:room]...)
				t.cut = true
				dropped = true
			} else {
				t.line = append(t.line, seg...)
			}
		}
		if i < 0 {
			break
		}
		lines = t.flush(lines)
		p = p[i+1:]
	}
	return lines, dropped
}

// flush ends the current line (a final line without a newline, or the
// partial line before a truncation).
func (t *logTail) flush(lines []string) []string {
	if len(t.line) == 0 && !t.cut {
		return lines
	}
	raw := t.line
	if t.cut {
		raw = trimPartialRune(raw)
	}
	lines = append(lines, clean(raw))
	t.line, t.cut = t.line[:0], false
	return lines
}

// trimPartialRune drops an incomplete UTF-8 sequence that a cut left at
// the end of b.
func trimPartialRune(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i]
			}
			break
		}
	}
	return b
}

// clean strips ANSI escape sequences (CSI, OSC and the short ESC forms)
// and a trailing carriage return, and replaces each byte of invalid UTF-8
// with '?'.
func clean(b []byte) string {
	b = bytes.TrimSuffix(b, []byte{'\r'})
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0x1b:
			i = skipEscape(b, i)
		case c < utf8.RuneSelf:
			out = append(out, c)
			i++
		default:
			r, size := utf8.DecodeRune(b[i:])
			if r == utf8.RuneError && size == 1 {
				out = append(out, '?')
			} else {
				out = append(out, b[i:i+size]...)
			}
			i += size
		}
	}
	return string(out)
}

// skipEscape returns the index after the escape sequence starting at
// b[i] == ESC. An unterminated sequence runs to the end of the line.
func skipEscape(b []byte, i int) int {
	j := i + 1
	if j >= len(b) {
		return j
	}
	switch b[j] {
	case '[': // CSI: parameters, intermediates, final byte
		j++
		for j < len(b) && b[j] >= 0x20 && b[j] <= 0x3f {
			j++
		}
		if j < len(b) && b[j] >= 0x40 && b[j] <= 0x7e {
			j++
		}
		return j
	case ']': // OSC: ends with BEL or ST (ESC \)
		for j++; j < len(b); j++ {
			if b[j] == 0x07 {
				return j + 1
			}
			if b[j] == 0x1b && j+1 < len(b) && b[j+1] == '\\' {
				return j + 2
			}
		}
		return j
	}
	// nF (intermediates then a final byte) or a two-byte escape.
	for j < len(b) && b[j] >= 0x20 && b[j] <= 0x2f {
		j++
	}
	if j < len(b) {
		j++
	}
	return j
}
