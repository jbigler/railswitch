package web

import (
	"slices"
	"testing"
)

// TestSplitArgs: quotes and backslash as a shell has them, no expansion.
func TestSplitArgs(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{`a 'b c' "d\"e" f\ g`, []string{"a", "b c", `d"e`, "f g"}},
		{"", nil},
		{"   \t ", nil},
		{`one`, []string{"one"}},
		{`  x   y  `, []string{"x", "y"}},
		{`''`, []string{""}},
		{`a''b`, []string{"ab"}},
		{`'it''s'`, []string{"its"}},
		{`"a\\b" "c\d" '\n'`, []string{`a\b`, `c\d`, `\n`}},
		{`$HOME *.rb ~ $(id)`, []string{"$HOME", "*.rb", "~", "$(id)"}},
		{`"$HOME"`, []string{"$HOME"}},
		{`--name=x -- y`, []string{"--name=x", "--", "y"}},
		{`é "ü ö"`, []string{"é", "ü ö"}},
		{`a\'b`, []string{"a'b"}},
	} {
		got, err := SplitArgs(c.in)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("SplitArgs(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{`"x`, `'x`, `a "b c`, `x\`, `"a\"`} {
		if got, err := SplitArgs(in); err == nil {
			t.Errorf("SplitArgs(%q) = %q, want an error", in, got)
		}
	}
}
