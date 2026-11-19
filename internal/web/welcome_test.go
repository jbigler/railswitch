package web

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

// TestWelcome (D220): / with no project is the welcome page: the checks
// box, a repository and a template folder start into /new, the templates
// already here as one-click starts, and the terms.
func TestWelcome(t *testing.T) {
	f := newProjectFixture(t)
	f.cli.set("--json project ls", "[]")
	f.cli.set("--json template ls", templateLsJSON)
	w := f.do(req("GET", "/", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	contains(t, "welcome", body, `data-view="welcome"`, `id="checks" data-checks`,
		`<form method="get" action="/new"`, `name="repo"`,
		`<form method="get" action="/machine/templates/import"`, `<input type="hidden" name="next" value="new">`, `name="dir"`,
		`href="/new?template=rails-pg%402"`, "<dt>Prefix</dt>", "<dt>Secrets</dt>",
		`<a href="/">No projects yet: start here</a>`, `href="/machine#checks"`)
	noCSPViolation(t, "welcome", body)

	// A project registered: / goes to it, as before.
	g := newProjectFixture(t)
	if w := g.do(req("GET", "/", nil)); w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), "/p/") {
		t.Errorf("root with a project: %d %s", w.Code, w.Header().Get("Location"))
	}
}

// TestChecks: the fragment lists doctor's host checks (-C /), failures
// first and then warnings, each with its fix to copy; the passing ones
// folded and without a fix. A doctor that fails to run says so.
func TestChecks(t *testing.T) {
	f := newFixture(t)
	f.cli.set("--json -C / doctor", `[
 {"name":"podman >= 5.3","ok":true,"warn":false,"detail":"podman 5.4.2","fix":""},
 {"name":"user services outlive logout","ok":false,"warn":true,"detail":"","fix":"loginctl enable-linger"},
 {"name":"unprivileged port 80","ok":false,"warn":false,"detail":"net.ipv4.ip_unprivileged_port_start = 1024","fix":"railswitch allow-ports"}]`)
	w := f.do(req("GET", "/machine/checks", nil))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	body := w.Body.String()
	contains(t, "checks", body, "1 to fix before a project can start",
		`<li class="check-fail">`, `<code>railswitch allow-ports</code> <button type="button" class="ln" data-copy="railswitch allow-ports"`,
		`<li class="check-warn">`, `data-copy="loginctl enable-linger"`,
		`1 checks pass`, `<li class="check-ok">`, "podman 5.4.2", "data-checks-again")
	if strings.Index(body, "check-fail") > strings.Index(body, "check-warn") {
		t.Error("a warning is listed before a failure")
	}
	noCSPViolation(t, "checks", body)

	f.cli.set("--json -C / doctor", `[{"name":"podman >= 5.3","ok":true,"warn":false,"detail":"podman 5.4.2","fix":""},
 {"name":"gh logged in","ok":false,"warn":true,"detail":"gh is not logged in","fix":"gh auth login"}]`)
	contains(t, "only a warning", f.do(req("GET", "/machine/checks", nil)).Body.String(), "Ready: nothing blocks a project.", "worth a look")

	f.cli.fail("--json -C / doctor", errors.New("podman: not found"))
	contains(t, "doctor failed", f.do(req("GET", "/machine/checks", nil)).Body.String(), "The checks did not run: podman: not found")

	// The Machine page has the box, filled by the page's script.
	g := signersFixture(t)
	contains(t, "machine page", g.do(req("GET", "/machine", nil)).Body.String(), `id="checks" data-checks`)
}

// TestTemplateImportNext: from the welcome page (?next=new) the review
// goes back to / and, once imported, on to /new with the template picked;
// any other next is ignored.
func TestTemplateImportNext(t *testing.T) {
	f := importFixture(t)
	page := func(next string) string {
		return f.do(req("GET", "/machine/templates/import?next="+url.QueryEscape(next)+"&dir="+url.QueryEscape(importDirPath), nil)).Body.String()
	}
	contains(t, "next=new", page("new"), `data-next="/new?template=rails-stack%402"`, `<a href="/">← Welcome</a>`)
	for _, next := range []string{"", "https://evil.example/", "/p/x"} {
		if body := page(next); !strings.Contains(body, `data-next="/machine"`) {
			t.Errorf("next %q: data-next is not /machine", next)
		}
	}
	f.cli.set("--json template import "+importDirPath, strings.Replace(importJSON, `"status":"update"`, `"status":"same"`, 1))
	contains(t, "same", page("new"), `<a href="/new?template=rails-stack%402">Start a project from it</a>`)
}

// TestNewPrefill: /new?repo=…&template=… fills the form, for the page's
// script to read or pick at once; a template not here selects nothing.
func TestNewPrefill(t *testing.T) {
	f := newProjectFixture(t)
	f.cli.set("--json template ls", templateLsJSON)
	body := f.do(req("GET", "/new?repo="+url.QueryEscape(`git@github.com:o/"a".git`)+"&template=rails-pg@2", nil)).Body.String()
	contains(t, "prefill", body, `value="git@github.com:o/&#34;a&#34;.git"`, `<option value="rails-pg@2" selected>`)
	if body := f.do(req("GET", "/new?template=other@1", nil)).Body.String(); strings.Contains(body, " selected>") {
		t.Error("an unknown template is selected")
	}
}

// TestCheckCmd: Copy takes the command a fix starts with, never its words.
func TestCheckCmd(t *testing.T) {
	for fix, want := range map[string]string{
		"railswitch allow-ports": "railswitch allow-ports",
		"loginctl enable-linger (only needed to keep projects running)":                     "loginctl enable-linger",
		"systemctl --user restart railswitch-dashboard.service; if it still fails, stop it": "systemctl --user restart railswitch-dashboard.service",
		"gh auth login (install the GitHub CLI first if it is missing)":                     "gh auth login",
		"install jq (the egress firewall hook runs it on the host)":                         "",
		"": "",
	} {
		if got := (doctorCheck{Fix: fix}).Cmd(); got != want {
			t.Errorf("%q: %q, want %q", fix, got, want)
		}
	}
}
