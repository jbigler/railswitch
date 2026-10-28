package textdiff

import (
	"slices"
	"testing"
)

func TestUnified(t *testing.T) {
	a := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n"
	b := "1\nTWO\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n"
	want := "--- project.toml\n+++ project.toml\n" +
		"@@ -1,5 +1,5 @@\n 1\n-2\n+TWO\n 3\n 4\n 5\n" +
		"@@ -10,3 +10,4 @@\n 10\n 11\n 12\n+13\n"
	if got := Unified("project.toml", "project.toml", a, b); got != want {
		t.Errorf("Unified =\n%s\nwant\n%s", got, want)
	}
	if got := Unified("project.toml", "project.toml", a, a); got != "" {
		t.Errorf("same text: %q", got)
	}
	if got := Unified("project.toml", "project.toml", "x", "y"); got != "--- project.toml\n+++ project.toml\n@@ -1 +1 @@\n-x\n\\ No newline at end of file\n+y\n\\ No newline at end of file\n" {
		t.Errorf("no final newline:\n%s", got)
	}
}

func TestAdded(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want []int
	}{
		{"1\n2\n3\n", "1\nTWO\n3\n4\n", []int{2, 4}},
		{"", "a\nb", []int{1, 2}},
		{"a\nb\n", "a\n", nil},
		{"a\n", "a\n", nil},
	} {
		if got := Added(c.a, c.b); !slices.Equal(got, c.want) {
			t.Errorf("Added(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
