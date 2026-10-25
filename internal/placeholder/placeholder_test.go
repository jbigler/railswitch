package placeholder

import (
	"slices"
	"testing"
)

func TestNames(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"{worktree.host}/x/{branch}", []string{"worktree.host", "branch"}},
		{"{{literal}", nil},
		{"/data/rustfs{0..3}", nil},
		{"{{{a}", []string{"a"}},
		{"plain", nil},
	}
	for _, c := range cases {
		if got := Names(c.in); !slices.Equal(got, c.want) {
			t.Errorf("Names(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}
