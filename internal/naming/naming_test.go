package naming

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"Feature/Foo Bar": "feature-foo-bar",
		"--x--":           "x",
		"a\nb":            "a-b",
	}
	for in, want := range cases {
		got, err := Sanitize(in)
		if err != nil || got != want {
			t.Errorf("Sanitize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestSanitizeCap(t *testing.T) {
	got, err := Sanitize(strings.Repeat("ab-", 50))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 40 || strings.HasSuffix(got, "-") || got == "" {
		t.Errorf("Sanitize(50×ab-) = %q (len %d)", got, len(got))
	}
}

func TestSanitizeEmpty(t *testing.T) {
	if got, err := Sanitize("!!!"); !errors.Is(err, ErrEmptyName) {
		t.Errorf("Sanitize(!!!) = %q, %v; want ErrEmptyName", got, err)
	}
}

func TestValidService(t *testing.T) {
	for _, id := range []string{"app", "dev", "page", "new"} {
		err := ValidService(id)
		if err == nil || !strings.Contains(err.Error(), "HSTS") {
			t.Errorf("ValidService(%q) = %v; want an HSTS error", id, err)
		}
	}
	for _, id := range []string{"rails", "db"} {
		if err := ValidService(id); err != nil {
			t.Errorf("ValidService(%q) = %v", id, err)
		}
	}
	for _, id := range []string{"", "Rails", "1db", "db_x", strings.Repeat("a", 32)} {
		if err := ValidService(id); err == nil {
			t.Errorf("ValidService(%q) = nil; want error", id)
		}
	}
}

func TestValidPrefix(t *testing.T) {
	for _, p := range []string{"filial", "f1", strings.Repeat("a", 16)} {
		if err := ValidPrefix(p); err != nil {
			t.Errorf("ValidPrefix(%q) = %v", p, err)
		}
	}
	for _, p := range []string{"Filial", "fi-l", "fi_l", "f", "1ab", strings.Repeat("a", 17)} {
		if err := ValidPrefix(p); err == nil {
			t.Errorf("ValidPrefix(%q) = nil; want error", p)
		}
	}
}

func TestObjectNames(t *testing.T) {
	cases := []struct{ got, want string }{
		{Container("filial", "feat", "rails"), "filial-feat-rails"},
		{DevNetworkUnit("filial", "feat"), "filial-feat-dev.network"},
		{WorktreeVolume("filial", "feat", "db-data-pg16"), "filial-feat-db-data-pg16"},
		{Shared("filial", "npm_cache"), "filial_npm_cache"},
		{DevNetwork("filial", "feat"), "filial-feat-dev"},
		{ProxyNetwork("filial"), "filial_proxy"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}
