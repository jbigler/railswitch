package pack

import (
	"errors"
	"testing"
)

func TestExpand(t *testing.T) {
	sc := MapScope{"worktree.host": "feat.filial.localhost"}
	cases := []struct{ in, want string }{
		{"{worktree.host}/admin", "feat.filial.localhost/admin"},
		{"/data/rustfs{0..3}", "/data/rustfs{0..3}"},
		{"http://$${1}/x", "http://$${1}/x"},
		{"{{{worktree.host}", "{feat.filial.localhost"},
		{"{worktree.host}:{worktree.host}", "feat.filial.localhost:feat.filial.localhost"},
		{"{{literal}", "{literal}"},
	}
	for _, c := range cases {
		got, err := Expand(c.in, sc)
		if err != nil || got != c.want {
			t.Errorf("Expand(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestExpandUnknown(t *testing.T) {
	_, err := Expand("{worktree.hots}", MapScope{"worktree.host": "x"})
	var u *UnknownPlaceholderError
	if !errors.As(err, &u) || u.Name != "worktree.hots" {
		t.Fatalf("err = %v; want UnknownPlaceholderError{worktree.hots}", err)
	}
}

func TestExpandUnset(t *testing.T) {
	_, err := Expand("TZ={env.TZ}", MapScope{})
	if !errors.Is(err, ErrUnset) {
		t.Fatalf("err = %v; want ErrUnset", err)
	}
}

func TestMajor(t *testing.T) {
	for in, want := range map[string]string{"16.4": "16", "1.0.0-alpha.83": "1", "8": "8"} {
		if got := Major(in); got != want {
			t.Errorf("Major(%q) = %q; want %q", in, got, want)
		}
	}
}
