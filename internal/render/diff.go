package render

import (
	"bytes"
	"strings"
)

// lineDiff is the line diff of a and b with every line shown once: " " for a
// line in both, "-" for a line only in a, "+" for a line only in b, the
// unchanged lines kept so the change reads in place. Equal inputs give "". A
// last line without its newline is followed by `\ No newline at end of file`.
func lineDiff(a, b []byte) string {
	if bytes.Equal(a, b) {
		return ""
	}
	x, y := splitLines(a), splitLines(b)
	// The common head and tail stay out of the quadratic table.
	head := 0
	for head < len(x) && head < len(y) && x[head] == y[head] {
		head++
	}
	tail := 0
	for tail < len(x)-head && tail < len(y)-head && x[len(x)-1-tail] == y[len(y)-1-tail] {
		tail++
	}
	var out strings.Builder
	emit := func(sign byte, line string) {
		out.WriteByte(sign)
		out.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			out.WriteString("\n\\ No newline at end of file\n")
		}
	}
	for _, l := range x[:head] {
		emit(' ', l)
	}
	xs, ys := x[head:len(x)-tail], y[head:len(y)-tail]
	// lcs[i*w+j] is the length of the longest common subsequence of xs[i:]
	// and ys[j:].
	n, m, w := len(xs), len(ys), len(ys)+1
	lcs := make([]int, (n+1)*w)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if xs[i] == ys[j] {
				lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
			} else {
				lcs[i*w+j] = max(lcs[(i+1)*w+j], lcs[i*w+j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && xs[i] == ys[j]:
			emit(' ', xs[i])
			i++
			j++
		case j == m || (i < n && lcs[(i+1)*w+j] >= lcs[i*w+j+1]):
			emit('-', xs[i])
			i++
		default:
			emit('+', ys[j])
			j++
		}
	}
	for _, l := range x[len(x)-tail:] {
		emit(' ', l)
	}
	return out.String()
}

// splitLines splits s into lines, each keeping its "\n"; only the last may
// lack one.
func splitLines(s []byte) []string {
	lines := strings.SplitAfter(string(s), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
