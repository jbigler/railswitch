package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

// shareSlug records slug of project filial as shared in the registry.
func shareSlug(t *testing.T, dirs machine.Dirs, slug string) {
	t.Helper()
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	if err := reg.SetShare("filial", slug, &machine.ShareEntry{Host: "box.tail1234.ts.net", IP: "100.64.0.7"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
}

// tailscaleServing scripts a running tailscale serving each port to its own
// loopback port, as railswitch share does.
func tailscaleServing(f *systest.Fake, ports ...string) {
	var tcp, web []string
	for _, p := range ports {
		tcp = append(tcp, `"`+p+`": {"HTTPS": true}`)
		web = append(web, `"box.tail1234.ts.net:`+p+`": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:`+p+`"}}}`)
	}
	f.On([]string{"tailscale", "status", "--json"}, sys.Result{Stdout: []byte(`{"BackendState": "Running", "Self": {"DNSName": "box.tail1234.ts.net.", "TailscaleIPs": ["100.64.0.7"]}}`)}, nil)
	f.On([]string{"tailscale", "serve", "status", "--json"}, sys.Result{Stdout: []byte(`{"TCP": {` + strings.Join(tcp, ",") + `}, "Web": {` + strings.Join(web, ",") + `}}`)}, nil)
}

func TestShareUsage(t *testing.T) {
	ws, _ := lockedWS(t)
	for _, args := range [][]string{
		{"-C", ws, "share", "master", "feat"},
		{"-C", ws, "-w", "master", "share", "feat"},
		{"-C", ws, "-w", "master", "unshare", "feat"},
	} {
		if code, _, _ := runIn(t, args...); code != 2 {
			t.Errorf("railswitch %s: exit %d; want 2", strings.Join(args, " "), code)
		}
	}
}

// TestUnshareNotShared: unshare of a worktree that is not shared says so and
// runs nothing.
func TestUnshareNotShared(t *testing.T) {
	ws, _ := lockedWS(t)
	f := fakeHost()
	code, out, errs := runWith(t, f, "-C", ws, "unshare", "feat")
	if code != 0 || out != "worktree feat is not shared\n" {
		t.Fatalf("unshare: exit %d, stdout %q, stderr %q", code, out, errs)
	}
	for _, c := range f.Calls {
		if c[0] == "tailscale" || c[0] == "systemctl" {
			t.Errorf("unexpected call %q", c)
		}
	}
}

func TestWtRmDropsShareWithoutRestart(t *testing.T) {
	ws, dirs := gitWS(t)
	f := fakeHost()
	f.Passthrough = []string{"git"}
	if code, out, errs := runWith(t, f, "-C", ws, "wt", "add", "feat-x"); code != 0 {
		t.Fatalf("wt add: exit %d\n%s%s", code, out, errs)
	}
	shareSlug(t, dirs, "feat-x") // ID 3
	tailscaleServing(f, "13003", "19003", "13002")
	n := len(f.Calls)
	if code, out, errs := runWith(t, f, "-C", ws, "wt", "rm", "feat-x"); code != 0 {
		t.Fatalf("wt rm: exit %d\n%s%s", code, out, errs)
	}
	var calls []string
	for _, c := range f.Calls[n:] {
		calls = append(calls, strings.Join(c, " "))
	}
	var off []string
	for _, c := range calls {
		if strings.HasPrefix(c, "tailscale serve --") {
			off = append(off, c)
		}
		if strings.HasPrefix(c, "systemctl") && strings.Contains(c, " restart") {
			t.Errorf("wt rm restarted a unit: %q", c)
		}
	}
	if want := []string{"tailscale serve --https=13003 off", "tailscale serve --https=19003 off"}; !slices.Equal(off, want) {
		t.Errorf("serve calls = %q; want %q", off, want)
	}
	stop := slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, "systemctl --user stop") })
	last := slices.IndexFunc(calls, func(c string) bool { return c == "tailscale serve --https=19003 off" })
	if stop >= 0 && last > stop {
		t.Errorf("serve turned off after the units stopped:\n%s", strings.Join(calls, "\n"))
	}
}
