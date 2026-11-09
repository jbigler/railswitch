package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestIssue: /issue redirects to GitHub's form with the version and the
// Referer's route pattern, never its path.
func TestIssue(t *testing.T) {
	f := newFixture(t)
	for _, c := range []struct{ referer, page string }{
		{"http://" + testHost + "/p/ftest/settings", "/p/{prefix}/settings"},
		{"http://" + testHost + "/p/ftest/b/pr", "/p/{prefix}/{slug}/pr"},
		{"http://" + testHost + "/p/ftest/virtual-office/cyberpunk-2/", "/p/{prefix}/virtual-office/{set}/{$}"},
		{"http://" + testHost + "/p/ftest/virtual-office/cyberpunk-2", "/p/{prefix}/virtual-office/{set}/{$}"}, // redirected: still the pattern
		{"http://" + testHost + "/p/ftest//settings", "/p/{prefix}/settings"},
		{"http://" + testHost + "/nowhere", "unknown"},
		{"", "unknown"},
	} {
		r := req("GET", "/issue", nil)
		r.Header.Set("Referer", c.referer)
		w := f.do(r)
		loc := w.Header().Get("Location")
		if w.Code != http.StatusSeeOther || !strings.HasPrefix(loc, issueNew+"?body=") {
			t.Fatalf("%q: %d %q", c.referer, w.Code, loc)
		}
		u, _ := url.Parse(loc)
		body := u.Query().Get("body")
		if !strings.HasSuffix(body, "railswitch v9 · page `"+c.page+"`\n") || strings.Contains(body, "ftest") || strings.Contains(body, "cyberpunk") {
			t.Errorf("%q: body %q", c.referer, body)
		}
	}
	w := f.do(req("GET", "/p/ftest/settings", nil))
	if !strings.Contains(w.Body.String(), `href="/issue"`) {
		t.Errorf("header lacks the Report issue link")
	}
}
