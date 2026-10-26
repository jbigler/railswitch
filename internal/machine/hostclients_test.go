package machine

import (
	"strings"
	"testing"
)

func TestHostClientClause(t *testing.T) {
	got := HostClientClause()
	terms := strings.Split(got, " || ")
	if len(terms) != 257 {
		t.Fatalf("HostClientClause has %d terms; want 257 (the negated pool and one .2 per n in 0..255):\n%s", len(terms), got)
	}
	if want := "!ClientIP(`10.214.0.0/16`)"; terms[0] != want {
		t.Errorf("term 0 = %q; want %q", terms[0], want)
	}
	if want := "ClientIP(`10.214.0.2/32`)"; terms[1] != want {
		t.Errorf("term 1 = %q; want %q", terms[1], want)
	}
	if want := "ClientIP(`10.214.255.2/32`)"; terms[256] != want {
		t.Errorf("last term = %q; want %q", terms[256], want)
	}
	for n, term := range terms[1:] {
		if want := "ClientIP(`" + TraefikIP(n) + "/32`)"; term != want {
			t.Errorf("term %d = %q; want %q", n+1, term, want)
		}
	}

	// Static: registering a project never changes app labels.
	r := newRegistry(t, "a")
	if _, err := r.AddProject("b", "/ws/b", nil); err != nil {
		t.Fatal(err)
	}
	if again := HostClientClause(); again != got {
		t.Errorf("HostClientClause changed after registering projects:\n%s\nwant\n%s", again, got)
	}
}
