package systest

import (
	"bytes"
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

func TestFakeRecordsCmdsAndStdin(t *testing.T) {
	f := &Fake{Passthrough: []string{"cat"}}
	ctx := context.Background()
	f.On([]string{"podman"}, sys.Result{Stdout: []byte("out"), Stderr: []byte("err")}, nil)
	var so, se bytes.Buffer
	r, err := f.Run(ctx, sys.Cmd{Name: "podman", Args: []string{"secret", "create"}, Stdin: strings.NewReader("s3cret"), Stdout: &so, Stderr: &se, Timeout: sys.NoTimeout})
	if err != nil || len(r.Stdout) != 0 || len(r.Stderr) != 0 || so.String() != "out" || se.String() != "err" {
		t.Fatalf("got %+v %v so=%q se=%q", r, err, so.String(), se.String())
	}
	r, err = f.Run(ctx, sys.Cmd{Name: "cat", Stdin: strings.NewReader("piped")})
	if err != nil || string(r.Stdout) != "piped" {
		t.Fatalf("passthrough got %q %v", r.Stdout, err)
	}
	if _, err := f.Run(ctx, sys.Cmd{Name: "podman"}); err != nil {
		t.Fatal(err)
	}
	if len(f.Cmds) != 3 || f.Cmds[0].Timeout != sys.NoTimeout || f.Cmds[1].Name != "cat" {
		t.Fatalf("Cmds = %+v", f.Cmds)
	}
	if !reflect.DeepEqual(f.Stdins, [][]byte{[]byte("s3cret"), []byte("piped"), nil}) {
		t.Fatalf("Stdins = %q", f.Stdins)
	}
}

func TestFakeRecordsDetach(t *testing.T) {
	f := &Fake{}
	if _, err := f.Run(context.Background(), sys.Cmd{Name: "foot", Detach: true}); err != nil {
		t.Fatal(err)
	}
	if !f.Cmds[0].Detach {
		t.Error("Detach not recorded")
	}
}
