package lifecycle

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

// TestPanic (D181): every running container whose name starts exactly with
// a registered worktree's "<prefix>-<slug>-" (units' and transients' alike)
// is cut off by a drop-all table loaded from outside it; then every one is
// paused. It needs no workspace. A stopped one is left alone, another
// project's is never touched, and a failed step is reported while the
// rest go on.
func TestPanic(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"podman", "ps", "-a", "--format", "{{.Names}}"}, sys.Result{Stdout: []byte(
		"filial-feat-rails\nfilial-feat-agent-probe\nfilial-feat-db\nfilial-feat-deps-run-abc\nfilial-feat-x-rails\nfilial-other-rails\nshop-feat-rails\nrailswitch-traefik\n")}, nil)
	inspect := func(name, out string) {
		f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Pid}} {{.State.Status}}", name}, sys.Result{Stdout: []byte(out)}, nil)
	}
	inspect("filial-feat-rails", "4242 running\n")
	inspect("filial-feat-agent-probe", "5151 paused\n")
	inspect("filial-feat-db", "0 exited\n")
	inspect("filial-feat-deps-run-abc", "6161 running\n")
	inspect("filial-feat-x-rails", "7171 running\n")
	f.On([]string{"podman", "unshare", "nsenter", "-t", "4242"}, sys.Result{Stderr: []byte("nope")}, errors.Join(sys.ErrExit, errors.New("exit 1")))

	// filial's project.toml may not even load: the registry is enough.
	rows, err := Panic(ctx, f, []machine.WorktreeEntry{{Prefix: "filial", Slug: "feat"}, {Prefix: "filial", Slug: "feat-x"}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]PanicRow{}
	for _, r := range rows {
		got[r.Container] = r
	}
	if len(got) != 4 {
		t.Errorf("rows %+v; want rails, agent, deps transient, feat-x rails", rows)
	}
	if r := got["filial-feat-agent-probe"]; !r.Quarantined || !r.Paused || r.Error != "" {
		t.Errorf("agent row %+v; want quarantined, already paused", r)
	}
	if r := got["filial-feat-deps-run-abc"]; !r.Quarantined || !r.Paused || r.Worktree != "feat" {
		t.Errorf("deps transient row %+v; want quarantined and paused", r)
	}
	if r := got["filial-feat-x-rails"]; r.Worktree != "feat-x" || !r.Paused {
		t.Errorf("feat-x row %+v; want worktree feat-x, the longest match", r)
	}
	if r := got["filial-feat-rails"]; r.Quarantined || !r.Paused || !strings.HasPrefix(r.Error, "quarantine:") {
		t.Errorf("rails row %+v; want its quarantine error, paused all the same", r)
	}
	// The drop-all table went in on stdin, and every quarantine came before the first pause.
	var lastQuarantine, firstPause = -1, -1
	for i, c := range f.Calls {
		switch {
		case len(c) > 3 && slices.Equal(c[:3], []string{"podman", "unshare", "nsenter"}):
			lastQuarantine = i
			if in := string(f.Stdins[i]); !strings.Contains(in, "policy drop;") || !strings.Contains(in, `oif "lo" accept`) {
				t.Errorf("quarantine rules:\n%s", in)
			}
		case len(c) > 1 && c[1] == "pause":
			if firstPause < 0 {
				firstPause = i
			}
			if c[2] == "filial-feat-agent-probe" {
				t.Errorf("paused an already paused container")
			}
		}
		if j := strings.Join(c, " "); strings.Contains(j, "--filter") || strings.Contains(j, "shop-") || strings.Contains(j, "filial-other") {
			t.Errorf("ran %q", c)
		}
	}
	if lastQuarantine < 0 || firstPause < lastQuarantine {
		t.Errorf("last quarantine at %d, first pause at %d; want every quarantine first", lastQuarantine, firstPause)
	}
}
