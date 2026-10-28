// Package textdiff is the line diff of two texts: a unified diff to show,
// and which lines a change adds.
package textdiff

import (
	"fmt"
	"strings"
)

const diffContext = 3

type diffOp struct {
	kind byte // ' ', '-' or '+'
	line string
}

// Unified is the unified diff from a (file aName) to b (bName), with 3
// lines of context; "" when they are equal.
func Unified(aName, bName, a, b string) string {
	if a == b {
		return ""
	}
	ops := lineOps(a, b)
	var out strings.Builder
	out.WriteString("--- " + aName + "\n+++ " + bName + "\n")
	aLine, bLine := 1, 1 // line numbers of ops[i]
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			aLine, bLine, i = aLine+1, bLine+1, i+1
			continue
		}
		start := max(0, i-diffContext)
		end, last := i, i // last: the hunk's last change
		for end < len(ops) && end-last <= 2*diffContext {
			if ops[end].kind != ' ' {
				last = end
			}
			end++
		}
		end = min(len(ops), last+1+diffContext)
		aStart, bStart := aLine-(i-start), bLine-(i-start)
		var na, nb int
		var body strings.Builder
		for _, op := range ops[start:end] {
			if op.kind != '+' {
				na++
			}
			if op.kind != '-' {
				nb++
			}
			body.WriteByte(op.kind)
			body.WriteString(op.line)
			if !strings.HasSuffix(op.line, "\n") {
				body.WriteString("\n\\ No newline at end of file\n")
			}
		}
		fmt.Fprintf(&out, "@@ -%s +%s @@\n%s", hunkRange(aStart, na), hunkRange(bStart, nb), body.String())
		for _, op := range ops[i:end] {
			if op.kind != '+' {
				aLine++
			}
			if op.kind != '-' {
				bLine++
			}
		}
		i = end
	}
	return out.String()
}

// Added is the 1-based numbers of b's lines that the diff from a to b adds.
func Added(a, b string) []int {
	var out []int
	n := 0
	for _, op := range lineOps(a, b) {
		if op.kind == '-' {
			continue
		}
		n++
		if op.kind == '+' {
			out = append(out, n)
		}
	}
	return out
}

// lineOps turns a into b line by line. Common leading and trailing lines
// are trimmed before the LCS, so an edit of a few lines costs little
// however long the file.
func lineOps(a, b string) []diffOp {
	x, y := splitLines(a), splitLines(b)
	pre := 0
	for pre < len(x) && pre < len(y) && x[pre] == y[pre] {
		pre++
	}
	suf := 0
	for suf < len(x)-pre && suf < len(y)-pre && x[len(x)-1-suf] == y[len(y)-1-suf] {
		suf++
	}
	var ops []diffOp
	for _, l := range x[:pre] {
		ops = append(ops, diffOp{' ', l})
	}
	ops = append(ops, lcsOps(x[pre:len(x)-suf], y[pre:len(y)-suf])...)
	for _, l := range x[len(x)-suf:] {
		ops = append(ops, diffOp{' ', l})
	}
	return ops
}

func hunkRange(start, n int) string {
	switch n {
	case 0:
		return fmt.Sprintf("%d,0", start-1)
	case 1:
		return fmt.Sprint(start)
	}
	return fmt.Sprintf("%d,%d", start, n)
}

// splitLines splits s after each newline; the last line may lack one.
func splitLines(s string) []string {
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// lcsOps turns x into y through a longest common subsequence of lines.
func lcsOps(x, y []string) []diffOp {
	n, m := len(x), len(y)
	// lcs[i*(m+1)+j] is the LCS length of x[i:] and y[j:].
	lcs := make([]int32, (n+1)*(m+1))
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i*(m+1)+j] = lcs[(i+1)*(m+1)+j+1] + 1
			} else {
				lcs[i*(m+1)+j] = max(lcs[(i+1)*(m+1)+j], lcs[i*(m+1)+j+1])
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case x[i] == y[j]:
			ops = append(ops, diffOp{' ', x[i]})
			i, j = i+1, j+1
		case lcs[(i+1)*(m+1)+j] >= lcs[i*(m+1)+j+1]:
			ops = append(ops, diffOp{'-', x[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', y[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', x[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', y[j]})
	}
	return ops
}
