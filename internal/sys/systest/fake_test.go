package systest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

func TestFakeRecordsAndScripts(t *testing.T) {
	f := &Fake{}
	boom := errors.New("boom")
	f.On([]string{"podman"}, sys.Result{Stdout: []byte("generic")}, nil)
	f.On([]string{"podman", "ps"}, sys.Result{Stdout: []byte("specific")}, boom)
	ctx := context.Background()

	r, err := f.Run(ctx, sys.Cmd{Name: "podman", Args: []string{"ps", "-a"}})
	if string(r.Stdout) != "specific" || !errors.Is(err, boom) {
		t.Fatalf("got %q, %v", r.Stdout, err)
	}
	r, err = f.Run(ctx, sys.Cmd{Name: "podman", Args: []string{"images"}})
	if string(r.Stdout) != "generic" || err != nil {
		t.Fatalf("got %q, %v", r.Stdout, err)
	}
	r, err = f.Run(ctx, sys.Cmd{Name: "systemctl", Args: []string{"--user"}})
	if len(r.Stdout) != 0 || r.ExitCode != 0 || err != nil {
		t.Fatalf("unmatched: got %+v, %v", r, err)
	}
	want := [][]string{{"podman", "ps", "-a"}, {"podman", "images"}, {"systemctl", "--user"}}
	if !reflect.DeepEqual(f.Calls, want) {
		t.Fatalf("Calls = %v", f.Calls)
	}
}

func TestFakePassthrough(t *testing.T) {
	f := &Fake{Passthrough: []string{"git"}}
	r, err := f.Run(context.Background(), sys.Cmd{Name: "git", Args: []string{"--version"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(r.Stdout), "git version") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
	if !reflect.DeepEqual(f.Calls, [][]string{{"git", "--version"}}) {
		t.Fatalf("Calls = %v", f.Calls)
	}
}
