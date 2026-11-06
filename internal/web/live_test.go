package web

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestLive serves the dashboard on test fixtures for a look in a browser:
// `make live` (RS_LIVE=1; RS_LIVE_ADDR, default 127.0.0.1:7799). It runs
// until stopped. Every request is given the guard's Host, cookie and
// Origin, so a plain browser on 127.0.0.1 gets through. The pages:
// /p/ftest/settings (Firewall: a log row, an enforce row with "Allow
// for", a data container), /p/ftest/b, and the office at
// /p/ftest/virtual-office/cyberpunk/#floor/b, whose camera holds still
// with ?cam=x,y,z&look=x,y,z. The binary embeds static files when it is
// built: rerun it after an edit.
func TestLive(t *testing.T) {
	if os.Getenv("RS_LIVE") == "" {
		t.Skip("RS_LIVE unset: run `make live`")
	}
	addr := os.Getenv("RS_LIVE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:7799"
	}
	f := settingsFixture(t)
	f.cli.set("--json -C /ws/ftest project show", strings.Replace(projectShowJSON, `"options":[`, `"firewall":{"mode":"","default":"enforce","effective":"enforce","allow":{"app":["api.example.com"],"deps":[],"agent":[]}},"options":[`, 1))
	f.cli.set("--json -C /ws/ftest fw show", `{"containers":[
	 {"name":"ftest-main-web","firewall":"firewalled","profiles":["app"],"mode":"log","rejected":[{"addr":"1.2.3.4","proto":"tcp","port":"443","host":"pay.example.com"}]},
	 {"name":"ftest-b-web","firewall":"firewalled","profiles":["app"],"mode":"enforce","rejected":[{"addr":"1.2.3.5","proto":"tcp","port":"443","host":"api.stripe.com"}]},
	 {"name":"ftest-b-db","firewall":"firewalled","profiles":["none"],"mode":"enforce","rejected":[{"addr":"9.9.9.9","proto":"udp","port":"53","host":"dns.example"}]}]}`)
	h := f.s.Handler()
	t.Logf("serving http://%s/p/ftest (stop with Ctrl-C)", addr)
	err := http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = testHost
		r.AddCookie(&http.Cookie{Name: "railswitch_dashboard", Value: testToken})
		if r.Header.Get("Origin") != "" {
			r.Header.Set("Origin", "http://"+testHost)
		}
		h.ServeHTTP(w, r)
	}))
	t.Fatal(err)
}
