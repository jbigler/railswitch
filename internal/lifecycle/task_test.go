package lifecycle

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

func TestTaskArgv(t *testing.T) {
	e := setup(t)
	e.f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", "filial-feat-rails"}, sys.Result{Stdout: []byte("true\n")}, nil)
	if err := e.l.Task(ctx, "feat", "test", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	want := append([]string{"podman", "exec", "-i", "--preserve-fds", "1", "-w", "/app", "filial-feat-rails"}, hangup([]string{"sh", "-c", `bin/rails test "$@"`, "test", "a", "b"})...)
	calls := e.calls()
	if got := calls[len(calls)-1]; !slices.Equal(got, want) {
		t.Errorf("argv %q; want %q", got, want)
	}
	if to := e.f.Cmds[len(e.f.Cmds)-1].Timeout; to != sys.NoTimeout {
		t.Errorf("timeout %v; want NoTimeout", to)
	}
}

func TestTaskUnknownUsage(t *testing.T) {
	e := setup(t)
	err := e.l.Task(ctx, "feat", "tset", nil)
	var u *UsageError
	if !errors.As(err, &u) || u.Msg != "unknown task tset; railswitch task ls" {
		t.Fatalf("err %v; want UsageError \"unknown task tset; railswitch task ls\"", err)
	}
	for _, c := range e.calls() {
		if strings.HasPrefix(joined(c), "podman exec") || strings.HasPrefix(joined(c), "podman --hooks-dir") {
			t.Errorf("ran %q", c)
		}
	}
}
