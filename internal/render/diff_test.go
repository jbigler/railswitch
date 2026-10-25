package render

import "testing"

func TestLineDiff(t *testing.T) {
	for _, tc := range []struct{ a, b, want string }{
		{"a\n", "b\n", "-a\n+b\n"},
		{"a\n", "a\n", ""},
		{"", "", ""},
		{"", "x\ny\n", "+x\n+y\n"},
		{"x\ny\n", "", "-x\n-y\n"},
		{"a\nb\nc\n", "a\nc\nd\n", " a\n-b\n c\n+d\n"},
		{"a", "a\n", "-a\n\\ No newline at end of file\n+a\n"},
	} {
		if got := lineDiff([]byte(tc.a), []byte(tc.b)); got != tc.want {
			t.Errorf("lineDiff(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}
