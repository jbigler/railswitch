package machine

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// proving answers as a dashboard holding tok does.
func proving(tok string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ok v0.1.1 %s\n", ProbeProof(tok, r.URL.Query().Get("nonce")))
	}
}

func answer(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

// replay answers with the proof it saw for its first probe, to every probe.
func replay(tok string) http.HandlerFunc {
	first := ""
	return func(w http.ResponseWriter, r *http.Request) {
		if first == "" {
			first = ProbeProof(tok, r.URL.Query().Get("nonce"))
		}
		fmt.Fprintf(w, "ok v0.1.1 %s\n", first)
	}
}

// TestProbeDashboard: only the proof made with this token is Ours; another
// token's proof, a replayed one or none is Unproved; anything else, hostile
// text included, is Other, quoted; a refused connection is nothing.
func TestProbeDashboard(t *testing.T) {
	ctx := context.Background()
	rp := serve(t, replay("mine"))
	ProbeDashboard(ctx, rp, "mine") // the replayer records a proof
	for _, c := range []struct {
		name, addr, want string
		ours             bool
	}{
		{"ours", serve(t, proving("mine")), "railswitch dashboard v0.1.1", true},
		{"another user's", serve(t, proving("theirs")), "a railswitch dashboard (v0.1.1) answers on 127.0.0.1:7700 but not with your login", false},
		{"replayed proof", rp, "but not with your login", false},
		{"no proof (older)", serve(t, answer(200, "ok v0.1.0\n")), "a railswitch dashboard (v0.1.0) answers", false},
		{"older dashboard", serve(t, answer(403, "forbidden host\n")), `another program answers on 127.0.0.1:7700 (403 "forbidden host")`, false},
		{"200 not ok", serve(t, answer(200, "hello\n")), `(200 "hello")`, false},
		{"hostile version", serve(t, answer(200, "ok \x1b]0;pwned\x07\n")), `(200 "ok \x1b]0;pwned\a")`, false},
		{"bad proof", serve(t, answer(200, "ok v1 XYZ\n")), `(200 "ok v1 XYZ")`, false},
		{"redirect", serve(t, answer(302, "")), `(302 "")`, false},
		{"long body", serve(t, answer(200, "ok "+strings.Repeat("x", 4096))), `(200 "ok xxx`, false},
	} {
		p := ProbeDashboard(ctx, c.addr, "mine")
		if got := p.String(); !strings.Contains(got, c.want) || strings.ContainsAny(got, "\x1b\x07") || p.Ours != c.ours {
			t.Errorf("%s: %q (ours %v), want %q", c.name, got, p.Ours, c.want)
		}
	}
	if p := ProbeDashboard(ctx, serve(t, proving("")), ""); p.Ours {
		t.Error("no token of our own: proved anyway")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
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
	if p := ProbeDashboard(ctx, ln.Addr().String(), "mine"); p.Other == "" || !strings.Contains(p.Fix(), "ss -ltnp") {
		t.Errorf("hang-up: %+v", p)
	}
}

// TestProbeRefused: nothing listening is nothing, with the journal fix.
func TestProbeRefused(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()
	p := ProbeDashboard(context.Background(), addr, "mine")
	if p.Err == nil || !strings.Contains(p.String(), "nothing answers") || !strings.Contains(p.Fix(), "journalctl") {
		t.Errorf("refused: %+v %s", p, p)
	}
}

// TestProbeProof: depends on the token and the nonce; never the token.
func TestProbeProof(t *testing.T) {
	a := ProbeProof("tok", "n1")
	if a != ProbeProof("tok", "n1") || a == ProbeProof("tok", "n2") || a == ProbeProof("tok2", "n1") || len(a) != 32 || strings.Contains(a, "tok") {
		t.Errorf("%q", a)
	}
	if !NonceOK(strings.Repeat("a", 32)) || NonceOK("x") || NonceOK(strings.Repeat("A", 32)) {
		t.Error("NonceOK")
	}
}
