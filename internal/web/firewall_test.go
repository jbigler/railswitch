package web

import (
	"strings"
	"testing"
)

// The Settings page's Firewall card shows the mode and each allow list;
// "Would be blocked" loads from fw show --json, with an Allow button for
// each rejected address that has a host and a list.
func TestSettingsFirewall(t *testing.T) {
	f := settingsFixture(t)
	f.cli.set("--json -C /ws/ftest project show", strings.Replace(projectShowJSON, `"options":[`, `"firewall":{"mode":"","default":"enforce","effective":"enforce","allow":{"app":["api.example.com"],"deps":[],"agent":[]}},"options":[`, 1))
	body := f.do(req("GET", "/p/ftest/settings", nil)).Body.String()
	contains(t, "settings", body, `id="firewall" data-mode=""`, "Now: <b>enforce</b>. The default is enforce", `name="fw-mode" value="" checked`,
		`data-prof="app" data-host="api.example.com"`, `data-src="/p/ftest/settings/blocked"`, `<input type="hidden" name="key" value="deps">`)
	noCSPViolation(t, "settings", body)

	f.cli.set("--json -C /ws/ftest fw show", `{"containers":[
	 {"name":"ftest-main-web","firewall":"firewalled","profiles":["app"],"mode":"log","rejected":[{"addr":"1.2.3.4","proto":"tcp","port":"443","host":"pay.example.com"},{"addr":"5.6.7.8","proto":"tcp","port":"443","host":""}]},
	 {"name":"ftest-main-db","firewall":"firewalled","profiles":["none"],"mode":"enforce","rejected":[{"addr":"9.9.9.9","proto":"udp","port":"53","host":"dns.example"}]},
	 {"name":"ftest-b-web","firewall":"firewalled","profiles":["app"],"mode":"enforce","rejected":[{"addr":"1.2.3.5","proto":"tcp","port":"443","host":"api.example.com"}],"refused":[{"name":"proxy.example.com","count":3}]},
	 {"name":"ftest-main-odd","firewall":"none","profiles":[],"mode":"","rejected":[]}]}`)
	body = f.do(req("GET", "/p/ftest/settings/blocked", nil)).Body.String()
	contains(t, "blocked", body, `data-fw-allow data-prof="app" data-host="pay.example.com"`, "Add to app list", "no host name: add the host by hand",
		"a data container: nothing to allow", `<span class="mono">ftest-main-odd</span> has no firewall`,
		// D184: under enforce, a worktree service's container can allow it now.
		`action="/p/ftest/b/fw-allow/web" data-post><input type="hidden" name="host" value="api.example.com">`,
		// D195: a name the DNS filter refused, with the same buttons.
		"name refused (DNS), 3 lookups", `data-fw-allow data-prof="app" data-host="proxy.example.com"`,
		`<input type="hidden" name="host" value="proxy.example.com"><button class="ln" title="Allow it in this running container now, until the time runs out">Allow for</button><select name="for" aria-label="How long"><option value="restart">`)
	// Not in log mode (nothing is blocked), not in a container with no service.
	if n := strings.Count(body, "fw-allow/"); n != 2 {
		t.Errorf("%d live allow forms, want 2 (ftest-b-web's two):\n%s", n, body)
	}
	noCSPViolation(t, "blocked", body)

	body = f.do(jsonPost("/p/ftest/settings/preview", `[{"op":"firewall.mode","value":"log"},{"op":"firewall.allow.add","key":"app","value":"pay.example.com"}]`)).Body.String()
	contains(t, "staged", body, "Firewall mode: the default (enforce) → log", "Firewall app: allow pay.example.com")
}

// A project with agents set to log or off says the app is a way out.
func TestSettingsFirewallOptedOut(t *testing.T) {
	f := settingsFixture(t)
	f.cli.set("--json -C /ws/ftest project show", strings.Replace(projectShowJSON, `"options":[`, `"firewall":{"mode":"log","default":"enforce","effective":"log","allow":{"app":[],"deps":[],"agent":[]}},"options":[`, 1))
	body := f.do(req("GET", "/p/ftest/settings", nil)).Body.String()
	contains(t, "settings", body, "This project has agents and its firewall is log")
}
