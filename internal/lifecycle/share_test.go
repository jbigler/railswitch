package lifecycle

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys"
)

const tsStatus = `{"BackendState": "Running", "Self": {"DNSName": "box.tail1234.ts.net.", "TailscaleIPs": ["100.64.0.7", "fd7a:115c:a1e0::7"]}}`

// tailscale scripts a running tailscale whose serve config proxies each
// HTTPS port of served to its target.
func (e *tenv) tailscale(t *testing.T, served map[int]string) {
	t.Helper()
	type handler struct{ Proxy string }
	sc := struct {
		TCP map[string]map[string]bool               `json:",omitempty"`
		Web map[string]map[string]map[string]handler `json:",omitempty"`
	}{TCP: map[string]map[string]bool{}, Web: map[string]map[string]map[string]handler{}}
	for port, target := range served {
		p := strconv.Itoa(port)
		sc.TCP[p] = map[string]bool{"HTTPS": true}
		sc.Web["box.tail1234.ts.net:"+p] = map[string]map[string]handler{"Handlers": {"/": {Proxy: target}}}
	}
	data, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	e.f.On([]string{"tailscale", "status", "--json"}, sys.Result{Stdout: []byte(tsStatus)}, nil)
	e.f.On([]string{"tailscale", "serve", "status", "--json"}, sys.Result{Stdout: data}, nil)
}

// setShare records slug as shared in the registry.
func (e *tenv) setShare(t *testing.T, slug string) {
	t.Helper()
	reg, err := machine.Open(e.l.WS.Dirs.Data)
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

// share is slug's registry Share.
func (e *tenv) share(t *testing.T, slug string) *machine.ShareEntry {
	t.Helper()
	reg, err := machine.Read(e.l.WS.Dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range reg.WorktreesOf("filial") {
		if w.Slug == slug {
			return w.Share
		}
	}
	t.Fatalf("worktree %s not registered", slug)
	return nil
}

// tsCalls are the recorded tailscale argvs, space-joined.
func (e *tenv) tsCalls() []string {
	var out []string
	for _, c := range e.f.Calls {
		if c[0] == "tailscale" {
			out = append(out, joined(c))
		}
	}
	return out
}

// serveCalls are the recorded `tailscale serve` argvs that change the serve
// config (status reads excluded).
func (e *tenv) serveCalls() []string {
	var out []string
	for _, c := range e.tsCalls() {
		if strings.HasPrefix(c, "tailscale serve ") && c != "tailscale serve status --json" {
			out = append(out, c)
		}
	}
	return out
}

func noReset(t *testing.T, calls []string) {
	t.Helper()
	for _, c := range calls {
		if slices.Contains(strings.Fields(c), "reset") {
			t.Errorf("call %q resets serve", c)
		}
	}
}

func TestShareArgv(t *testing.T) {
	e := setup(t)
	e.tailscale(t, nil)
	if err := e.l.Share(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tailscale serve --bg --https=13002 http://127.0.0.1:13002",
		"tailscale serve --bg --https=19002 http://127.0.0.1:19002",
	}
	if got := e.serveCalls(); !slices.Equal(got, want) {
		t.Errorf("serve calls = %q; want %q", got, want)
	}
	noReset(t, e.tsCalls())
	wantInOrder(t, e.calls(), "systemctl --user start filial-feat-rails.service", "tailscale serve --bg --https=13002")
	if s := e.share(t, "feat"); s == nil || *s != (machine.ShareEntry{Host: "box.tail1234.ts.net", IP: "100.64.0.7"}) {
		t.Errorf("registry share = %+v", s)
	}
	if s := e.share(t, "master"); s != nil {
		t.Errorf("master share = %+v", s)
	}
	if !strings.Contains(readUnit(t, e.l.WS.Dirs.Quadlet, "filial-feat-rails.container"), "\nAddHost=box.tail1234.ts.net:100.64.0.7\n") {
		t.Error("the rails unit has no AddHost for the tailnet host")
	}
	const msg = "shared: https://box.tail1234.ts.net:13002/ (storage https://box.tail1234.ts.net:19002/)\n"
	if !strings.Contains(e.out.String(), msg) {
		t.Errorf("output %q lacks %q", e.out.String(), msg)
	}
}

// TestShareIdempotent: ports already served to their own loopback port are
// railswitch's: share again is fine.
func TestShareIdempotent(t *testing.T) {
	e := setup(t)
	e.tailscale(t, map[int]string{13002: "http://127.0.0.1:13002", 19002: "http://127.0.0.1:19002"})
	if err := e.l.Share(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
}

func TestShareRefusesForeignPort(t *testing.T) {
	e := setup(t)
	e.tailscale(t, map[int]string{13002: "http://127.0.0.1:3000"})
	err := e.l.Share(ctx, "feat")
	if err == nil || !strings.Contains(err.Error(), "13002") || !strings.Contains(err.Error(), "http://127.0.0.1:3000") {
		t.Fatalf("Share error = %v; want it to name port 13002 and its target", err)
	}
	if got := e.serveCalls(); len(got) != 0 {
		t.Errorf("serve calls = %q; want none", got)
	}
	if s := e.share(t, "feat"); s != nil {
		t.Errorf("registry share = %+v", s)
	}
}

func TestShareNoTailscale(t *testing.T) {
	e := setup(t)
	e.f.On([]string{"tailscale"}, sys.Result{ExitCode: 1}, fmt.Errorf("tailscale: %w (exit 1)", sys.ErrExit))
	err := e.l.Share(ctx, "feat")
	if err == nil || !strings.Contains(err.Error(), "tailscale up") {
		t.Fatalf("Share error = %v; want it to name `tailscale up`", err)
	}
	if got := e.serveCalls(); len(got) != 0 {
		t.Errorf("serve calls = %q; want none", got)
	}
}

// TestUnshareOnlyOwn (Review Focus 3): another project's 13005 and a port
// served to something else stay on.
func TestUnshareOnlyOwn(t *testing.T) {
	e := setup(t)
	e.setShare(t, "feat")
	e.tailscale(t, map[int]string{
		13002: "http://127.0.0.1:13002",
		19002: "http://127.0.0.1:19002",
		13005: "http://127.0.0.1:13005",
		443:   "http://127.0.0.1:9999",
	})
	if err := e.l.Unshare(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	want := []string{"tailscale serve --https=13002 off", "tailscale serve --https=19002 off"}
	if got := e.serveCalls(); !slices.Equal(got, want) {
		t.Errorf("serve calls = %q; want %q", got, want)
	}
	noReset(t, e.tsCalls())
	if s := e.share(t, "feat"); s != nil {
		t.Errorf("registry share = %+v; want none", s)
	}
	if strings.Contains(readUnit(t, e.l.WS.Dirs.Quadlet, "filial-feat-rails.container"), "AddHost=box.") {
		t.Error("the unshared rails unit keeps the tailnet AddHost")
	}
}

// TestUnshareKeepsRetargetedPort: a share port served to another target
// (someone re-pointed it) is not railswitch's to turn off.
func TestUnshareKeepsRetargetedPort(t *testing.T) {
	e := setup(t)
	e.setShare(t, "feat")
	e.tailscale(t, map[int]string{13002: "http://127.0.0.1:13002", 19002: "http://127.0.0.1:4000"})
	if err := e.l.Unshare(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	if got, want := e.serveCalls(), []string{"tailscale serve --https=13002 off"}; !slices.Equal(got, want) {
		t.Errorf("serve calls = %q; want %q", got, want)
	}
}

func TestShareRollsBack(t *testing.T) {
	e := setup(t)
	e.tailscale(t, nil)
	e.f.On([]string{"tailscale", "serve", "--bg", "--https=19002"}, sys.Result{ExitCode: 1}, fmt.Errorf("tailscale: %w (exit 1)", sys.ErrExit))
	err := e.l.Share(ctx, "feat")
	if err == nil || !strings.Contains(err.Error(), "19002") {
		t.Fatalf("Share error = %v; want it to name 19002", err)
	}
	want := []string{
		"tailscale serve --bg --https=13002 http://127.0.0.1:13002",
		"tailscale serve --bg --https=19002 http://127.0.0.1:19002",
		"tailscale serve --https=13002 off",
	}
	if got := e.serveCalls(); !slices.Equal(got, want) {
		t.Errorf("serve calls = %q; want %q", got, want)
	}
	if s := e.share(t, "feat"); s != nil {
		t.Errorf("registry share = %+v; want none", s)
	}
	if strings.Contains(readUnit(t, e.l.WS.Dirs.Quadlet, "filial-feat-rails.container"), "AddHost=box.") {
		t.Error("the rolled-back rails unit keeps the tailnet AddHost")
	}
}

func TestDownUnsharesFirst(t *testing.T) {
	e := setup(t)
	e.setShare(t, "feat")
	e.tailscale(t, map[int]string{13002: "http://127.0.0.1:13002", 19002: "http://127.0.0.1:19002"})
	if err := e.l.Down(ctx, "feat", true); err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, e.f.Calls,
		"tailscale serve --https=13002 off",
		"tailscale serve --https=19002 off",
		"systemctl --user stop",
		"podman rm")
	if s := e.share(t, "feat"); s != nil {
		t.Errorf("registry share = %+v; want none", s)
	}
}

// TestDownUnsharedNoTailscale: down of a worktree that is not shared never
// runs tailscale (it may not be installed).
func TestDownUnsharedNoTailscale(t *testing.T) {
	e := setup(t)
	if err := e.l.Down(ctx, "feat", true); err != nil {
		t.Fatal(err)
	}
	if got := e.tsCalls(); len(got) != 0 {
		t.Errorf("tailscale calls = %q; want none", got)
	}
}
