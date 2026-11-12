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
	contains(t, "settings", body, `id="firewall" data-mode=""`, "Now: <b>enforce</b>.", "The default is enforce", `name="fw-mode" value="" checked> Default (enforce)`,
		`data-prof="app" data-host="api.example.com"`, `data-src="/p/ftest/settings/blocked"`, `<option value="deps">deps · dependency installs, nvim</option>`, `<span class="mono">app</span> <span class="muted">· the app and its workers</span>`)
	noCSPViolation(t, "settings", body)

	f.cli.set("--json -C /ws/ftest fw show", `{"containers":[
	 {"name":"ftest-main-web","firewall":"firewalled","profiles":["app"],"mode":"log","rejected":[{"addr":"1.2.3.4","proto":"tcp","port":"443","host":"pay.example.com"},{"addr":"5.6.7.8","proto":"tcp","port":"443","host":""}]},
	 {"name":"ftest-main-db","firewall":"firewalled","profiles":["none"],"mode":"enforce","rejected":[{"addr":"9.9.9.9","proto":"udp","port":"53","host":"dns.example"}]},
	 {"name":"ftest-b-web","firewall":"firewalled","profiles":["app"],"mode":"enforce","rejected":[{"addr":"1.2.3.5","proto":"tcp","port":"443","host":"api.example.com"}],"refused":[{"name":"proxy.example.com","count":3},{"name":"s3.b.ftest.localhost","count":9}],"lan":[{"addr":"192.168.1.20","port":"443","host":"search.home.example.com"}]},
	 {"name":"ftest-main-odd","firewall":"none","profiles":[],"mode":"","rejected":[]}]}`)
	body = f.do(req("GET", "/p/ftest/settings/blocked", nil)).Body.String()
	// The project's own name is no host to allow, and the most refused name comes first.
	if own, proxy := strings.Index(body, "s3.b.ftest.localhost"), strings.Index(body, `class="mono addr">proxy.example.com`); own < 0 || proxy < own || strings.Contains(body, `data-host="s3.b.ftest.localhost"`) {
		t.Errorf("own name at %d, proxy at %d, or the own name can be allowed:\n%s", own, proxy, body)
	}
	contains(t, "blocked", body, `data-fw-allow data-prof="app" data-host="pay.example.com"`, "this project's own service: nothing to allow", ">Allow</button>", "Blocked, or would be under enforce <span class=\"need\">· 3</span>", "goes to the app list", "no host name: add the host by hand",
		"a data container: nothing to allow", `<span class="mono">ftest-main-odd</span> has no firewall`,
		// D184: under enforce, a worktree service's container can allow it now.
		`action="/p/ftest/b/fw-allow/web" data-post><input type="hidden" name="host" value="api.example.com">`,
		// D195: a name the DNS filter refused, with the same buttons.
		`<td class="muted">DNS</td><td class="muted num">3 tries</td>`, "<li>ftest-b-web: 192.168.1.20 tcp/443 (search.home.example.com)</li>", `data-fw-allow data-prof="app" data-host="proxy.example.com"`,
		`<input type="hidden" name="host" value="proxy.example.com"><input type="hidden" name="for" value="restart"><button class="ln quiet" title="Allow it in this running container now, until it restarts">Allow until restart</button>`,
		`<button class="ln quiet" title="Allow it in this running container now, until the time runs out">Allow for</button><select name="for" aria-label="How long"><option value="10m">`)
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
