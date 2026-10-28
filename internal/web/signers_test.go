package web

import (
	"bytes"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	fpA  = "SHA256:" + "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"
	fpB  = "SHA256:" + "0123456789012345678901234567890123456789abc"
	keyA = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample jeff@laptop"
)

const signerLsJSON = `{"libraries":[{"id":"github.com/acme/lib","last_serial":7,
 "keys":[{"key":"` + keyA + `","fingerprint":"` + fpA + `","added":"2026-10-28"}],
 "revoked":[{"fingerprint":"` + fpB + `"}]},
 {"id":"example.org/empty","last_serial":0,"keys":[],"revoked":[]}]}`

func signersFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.cli.set("--json signer ls", signerLsJSON)
	return f
}

func TestSignersSection(t *testing.T) {
	f := signersFixture(t)
	w := f.do(req("GET", "/machine", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	contains(t, "machine page", body,
		`<h3 class="mono">github.com/acme/lib</h3>`, fpA, "2026-10-28", "jeff@laptop",
		`Revoked: <span class="mono">`+fpB, `<h3 class="mono">example.org/empty</h3>`, "No trusted keys.",
		`action="/machine/signers/rm"`, `data-confirm="Distrust `, `action="/machine/signers/add"`,
		`<textarea name="key"`,
	)
	if strings.Contains(body, "AAAAC3NzaC1lZDI1NTE5AAAAIExample") {
		t.Error("key material shown")
	}
	noCSPViolation(t, "machine", body)

	f.cli.fail("--json signer ls", errors.New("boom"))
	if body := f.do(req("GET", "/machine", nil)).Body.String(); !strings.Contains(body, "boom") {
		t.Errorf("error not shown: %s", body)
	}
}

func TestSignerAddJob(t *testing.T) {
	f := signersFixture(t)
	w := f.do(req("POST", "/machine/signers/add", form("library", "github.com/acme/lib", "key", "  "+keyA+"\r\n\n")))
	if w.Code != http.StatusAccepted {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got, want := f.run.wait(t, 1)[0], []string{testExe, "signer", "add", "github.com/acme/lib", "-"}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
	if got := f.run.Stdins()[0]; !bytes.Equal(got, []byte(keyA)) {
		t.Errorf("stdin %q, want the key", got)
	}
	for _, a := range f.run.Argvs()[0] {
		if strings.Contains(a, "AAAA") {
			t.Errorf("key in argv: %q", a)
		}
	}
}

func TestSignerRmJob(t *testing.T) {
	f := signersFixture(t)
	w := f.do(req("POST", "/machine/signers/rm", form("library", "github.com/acme/lib", "fingerprint", fpA)))
	if w.Code != http.StatusAccepted {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got, want := f.run.wait(t, 1)[0], []string{testExe, "signer", "rm", "github.com/acme/lib", fpA}; !slices.Equal(got, want) {
		t.Errorf("argv %q, want %q", got, want)
	}
	if len(f.run.Stdins()[0]) != 0 {
		t.Error("stdin on rm")
	}
}

func TestSignersGuard(t *testing.T) {
	f := signersFixture(t)
	for _, c := range []struct {
		name, path string
		v          []string
	}{
		{"add", "/machine/signers/add", []string{"library", "a/b", "key", keyA}},
		{"rm", "/machine/signers/rm", []string{"library", "a/b", "fingerprint", fpA}},
	} {
		noCookie := req("POST", c.path, form(c.v...))
		noCookie.Header.Del("Cookie")
		foreign := req("POST", c.path, form(c.v...))
		foreign.Header.Set("Origin", "http://evil.example")
		noOrigin := req("POST", c.path, form(c.v...))
		noOrigin.Header.Del("Origin")
		for what, r := range map[string]*http.Request{"no cookie": noCookie, "foreign origin": foreign, "no origin": noOrigin} {
			want := 403
			if what == "no cookie" {
				want = 401
			}
			if w := f.do(r); w.Code != want {
				t.Errorf("%s %s: %d, want %d", c.name, what, w.Code, want)
			}
		}
	}
	time.Sleep(20 * time.Millisecond)
	if argvs := f.run.Argvs(); len(argvs) != 0 {
		t.Errorf("jobs on refusal: %q", argvs)
	}
}

func TestSignersHostile(t *testing.T) {
	f := newFixture(t)
	f.cli.set("--json signer ls", `{"libraries":[{"id":"a/<script>alert(1)</script>","last_serial":1,
 "keys":[{"key":"ssh-ed25519 AAAA <script>x</script>","fingerprint":"SHA256:<script>y</script>","added":"<b>"}],
 "revoked":[{"fingerprint":"<img src=x>"}]}]}`)
	body := f.do(req("GET", "/machine", nil)).Body.String()
	for _, bad := range []string{"<script>alert", "<script>x", "<script>y", "<img src=x>", "<b>"} {
		if strings.Contains(body, bad) {
			t.Errorf("page holds unescaped %q", bad)
		}
	}
	contains(t, "page", body, "&lt;script&gt;x&lt;/script&gt;", "&lt;img src=x&gt;")
	noCSPViolation(t, "machine", body)
}

func TestSignerFormRefused(t *testing.T) {
	f := signersFixture(t)
	for _, c := range []struct {
		name, path string
		v          []string
	}{
		{"short fingerprint", "/machine/signers/rm", []string{"library", "a/b", "fingerprint", "SHA256:abc"}},
		{"flag fingerprint", "/machine/signers/rm", []string{"library", "a/b", "fingerprint", "--force"}},
		{"bad library rm", "/machine/signers/rm", []string{"library", "../x", "fingerprint", fpA}},
		{"dotdot library", "/machine/signers/add", []string{"library", "a/../b", "key", keyA}},
		{"flag library", "/machine/signers/add", []string{"library", "-x", "key", keyA}},
		{"upper library", "/machine/signers/add", []string{"library", "Acme", "key", keyA}},
		{"two lines", "/machine/signers/add", []string{"library", "a/b", "key", keyA + "\n" + keyA}},
		{"empty key", "/machine/signers/add", []string{"library", "a/b", "key", " \n "}},
		{"huge key", "/machine/signers/add", []string{"library", "a/b", "key", "ssh-ed25519 " + strings.Repeat("A", 16<<10)}},
	} {
		if w := f.do(req("POST", c.path, form(c.v...))); w.Code != 400 {
			t.Errorf("%s: %d, want 400", c.name, w.Code)
		}
	}
	time.Sleep(20 * time.Millisecond)
	if argvs := f.run.Argvs(); len(argvs) != 0 {
		t.Errorf("jobs on refusal: %q", argvs)
	}
}
