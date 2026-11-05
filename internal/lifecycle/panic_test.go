package lifecycle

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

// TestPanic (D181): every running container, by exact name, is cut off by
// a drop-all table loaded from outside it; then running agents are paused.
// A stopped one is left alone; a failed step is reported, the rest go on.
func TestPanic(t *testing.T) {
	e := setup(t)
	inspect := func(name, out string) {
		e.f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Pid}} {{.State.Status}}", name}, sys.Result{Stdout: []byte(out)}, nil)
	}
	inspect("filial-feat-rails", "4242 running\n")
	inspect("filial-feat-agent-probe", "5151 running\n")
	inspect("filial-feat-db", "0 exited\n")
	e.f.On([]string{"podman", "pause", "filial-feat-agent-probe"}, sys.Result{}, nil)
	e.f.On([]string{"podman", "unshare", "nsenter", "-t", "4242"}, sys.Result{Stderr: []byte("nope")}, errors.Join(sys.ErrExit, errors.New("exit 1")))

	rows, err := e.l.Panic(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]PanicRow{}
	for _, r := range rows {
		got[r.Container] = r
	}
	if r := got["filial-feat-agent-probe"]; !r.Quarantined || !r.Paused || r.Error != "" {
		t.Errorf("agent row %+v; want quarantined and paused", r)
	}
	if r := got["filial-feat-rails"]; r.Quarantined || r.Paused || !strings.HasPrefix(r.Error, "quarantine:") {
		t.Errorf("rails row %+v; want its quarantine error, not paused", r)
	}
	if _, ok := got["filial-feat-db"]; ok {
		t.Errorf("a stopped container has a row")
	}
	// The drop-all table went in on stdin, and every quarantine came before the pause.
	var quarantineAt, pauseAt = -1, -1
	for i, c := range e.f.Calls {
		switch {
		case slices.Equal(c[:3], []string{"podman", "unshare", "nsenter"}):
			quarantineAt = i
			if in := string(e.f.Stdins[i]); !strings.Contains(in, "policy drop;") || !strings.Contains(in, `oif "lo" accept`) {
				t.Errorf("quarantine rules:\n%s", in)
			}
		case slices.Equal(c, []string{"podman", "pause", "filial-feat-agent-probe"}):
			pauseAt = i
		}
		if strings.Contains(strings.Join(c, " "), "--filter") {
			t.Errorf("ran %q", c)
		}
	}
	if quarantineAt < 0 || pauseAt < quarantineAt {
		t.Errorf("quarantine at %d, pause at %d; want every quarantine first", quarantineAt, pauseAt)
	}
}
