package tailscale

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

var ctx = context.Background()

// status is `tailscale status --json` of a running node (ipnstate.Status,
// trimmed).
const status = `{
  "Version": "1.104.2",
  "BackendState": "Running",
  "TailscaleIPs": ["100.64.0.7", "fd7a:115c:a1e0::7"],
  "Self": {
    "HostName": "box",
    "DNSName": "box.tail1234.ts.net.",
    "TailscaleIPs": ["fd7a:115c:a1e0::7", "100.64.0.7"]
  }
}`

func TestSelf(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"tailscale", "status", "--json"}, sys.Result{Stdout: []byte(status)}, nil)
	host, ip, err := T{R: f}.Self(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if host != "box.tail1234.ts.net" || ip != netip.MustParseAddr("100.64.0.7") {
		t.Errorf("Self = %q, %v", host, ip)
	}
}

func TestSelfNotRunning(t *testing.T) {
	for name, out := range map[string]string{
		"logged out": `{"BackendState": "NeedsLogin", "Self": {"DNSName": "", "TailscaleIPs": null}}`,
		"no v4":      `{"BackendState": "Running", "Self": {"DNSName": "box.tail1234.ts.net.", "TailscaleIPs": ["fd7a:115c:a1e0::7"]}}`,
	} {
		f := &systest.Fake{}
		f.On([]string{"tailscale", "status", "--json"}, sys.Result{Stdout: []byte(out)}, nil)
		if _, _, err := (T{R: f}).Self(ctx); err == nil {
			t.Errorf("%s: Self succeeded", name)
		}
	}
	f := &systest.Fake{}
	f.On([]string{"tailscale"}, sys.Result{ExitCode: 1}, fmt.Errorf("tailscale: %w (exit 1)", sys.ErrExit))
	if _, _, err := (T{R: f}).Self(ctx); !errors.Is(err, sys.ErrExit) {
		t.Errorf("Self error = %v; want the runner's", err)
	}
}

// serveStatus is `tailscale serve status --json` (ipn.ServeConfig): two
// railswitch ports, a handler served to another target, a TCP forward and
// a background session's port.
const serveStatus = `{
  "TCP": {
    "443": {"HTTPS": true},
    "13002": {"HTTPS": true},
    "19002": {"HTTPS": true},
    "5432": {"TCPForward": "127.0.0.1:5432"}
  },
  "Web": {
    "box.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:9999"}}},
    "box.tail1234.ts.net:13002": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:13002"}}},
    "box.tail1234.ts.net:19002": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:19002"}, "/docs": {"Path": "/srv/docs"}}}
  },
  "Foreground": {
    "abc": {"TCP": {"8443": {"HTTPS": true}}, "Web": {"box.tail1234.ts.net:8443": {"Handlers": {"/": {"Text": "hi"}}}}}
  }
}`

func TestServed(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"tailscale", "serve", "status", "--json"}, sys.Result{Stdout: []byte(serveStatus)}, nil)
	got, err := T{R: f}.Served(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]string{
		443:   "http://127.0.0.1:9999",
		13002: "http://127.0.0.1:13002",
		19002: "/: http://127.0.0.1:19002, /docs: path /srv/docs",
		5432:  "tcp 127.0.0.1:5432",
		8443:  "text",
	}
	if !maps.Equal(got, want) {
		t.Errorf("Served = %v; want %v", got, want)
	}
	if got[19002] == Target(19002) {
		t.Error("a port with another handler beside railswitch's reads as railswitch's own")
	}
}

func TestServedEmpty(t *testing.T) {
	for _, out := range []string{"{}\n", "null\n"} {
		f := &systest.Fake{}
		f.On([]string{"tailscale", "serve", "status", "--json"}, sys.Result{Stdout: []byte(out)}, nil)
		got, err := T{R: f}.Served(ctx)
		if err != nil || len(got) != 0 {
			t.Errorf("Served(%q) = %v, %v", out, got, err)
		}
	}
}

func TestServeArgv(t *testing.T) {
	f := &systest.Fake{}
	ts := T{R: f}
	if err := ts.ServeOn(ctx, 13002); err != nil {
		t.Fatal(err)
	}
	if err := ts.ServeOff(ctx, 13002); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"tailscale", "serve", "--bg", "--https=13002", "http://127.0.0.1:13002"},
		{"tailscale", "serve", "--https=13002", "off"},
	}
	if !slices.EqualFunc(f.Calls, want, slices.Equal) {
		t.Errorf("calls = %q; want %q", f.Calls, want)
	}
	for _, c := range f.Calls {
		if slices.Contains(c, "reset") {
			t.Errorf("call %q resets serve", c)
		}
	}
}

func TestServeOnDenied(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"tailscale", "serve"}, sys.Result{ExitCode: 1}, fmt.Errorf("tailscale: %w (exit 1): Access denied: serve config denied", sys.ErrExit))
	err := T{R: f}.ServeOn(ctx, 13002)
	if err == nil || !strings.Contains(err.Error(), "sudo tailscale set --operator=$USER") || !strings.Contains(err.Error(), "13002") {
		t.Errorf("ServeOn error = %v; want the port and the operator fix", err)
	}
}
