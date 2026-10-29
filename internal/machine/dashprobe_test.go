package machine

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve answers every request with code and body; its address.
func serve(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func answer(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

// TestProbeDashboard: only "ok <version> [<this fingerprint>]" is ours; a
// railswitch dashboard with another token is Foreign; anything else,
// hostile text included, is Other, quoted; a refused connection is nothing.
func TestProbeDashboard(t *testing.T) {
	fp := TokenFingerprint("mine")
	ctx := context.Background()
	for _, c := range []struct {
		name, body string
		code       int
		want       string // Probe.String()
	}{
		{"ours", "ok v0.1.1 " + fp + "\n", 200, "railswitch dashboard v0.1.1"},
		{"ours before fingerprints", "ok v0.1.0\n", 200, "railswitch dashboard v0.1.0"},
		{"another user's", "ok v0.1.1 " + TokenFingerprint("theirs") + "\n", 200, "a railswitch dashboard with another login answers"},
		{"older dashboard", "forbidden host\n", 403, `another program answers on 127.0.0.1:7700 (403 "forbidden host")`},
		{"200 not ok", "hello\n", 200, `(200 "hello")`},
		{"hostile version", "ok \x1b]0;pwned\x07\n", 200, `(200 "ok \x1b]0;pwned\a")`},
		{"bad fingerprint", "ok v1 XYZ\n", 200, `(200 "ok v1 XYZ")`},
		{"redirect", "", 302, `(302 "")`},
		{"long body", "ok " + strings.Repeat("x", 4096), 200, `(200 "ok xxx`},
	} {
		p := ProbeDashboard(ctx, serve(t, answer(c.code, c.body)), fp)
		if got := p.String(); !strings.Contains(got, c.want) || strings.ContainsAny(got, "\x1b\x07") {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		if (c.name == "ours" || c.name == "ours before fingerprints") != p.Ours {
			t.Errorf("%s: Ours = %v", c.name, p.Ours)
		}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	if p := ProbeDashboard(ctx, addr, fp); p.Err == nil || !strings.Contains(p.String(), "nothing answers") || !strings.Contains(p.Fix(), "journalctl") {
		t.Errorf("refused: %+v %s", p, p)
	}

	// A listener that takes the connection and hangs up is something, not
	// nothing: its fix is to find it.
	ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	if p := ProbeDashboard(ctx, ln.Addr().String(), fp); p.Other == "" || !strings.Contains(p.Fix(), "ss -ltnp") {
		t.Errorf("hang-up: %+v", p)
	}
}

// TestTokenFingerprint: stable, 12 hex, not the token.
func TestTokenFingerprint(t *testing.T) {
	a, b := TokenFingerprint("tok"), TokenFingerprint("tok")
	if a != b || len(a) != 12 || strings.Contains(a, "tok") || a == TokenFingerprint("tok2") {
		t.Errorf("%q %q", a, b)
	}
}
