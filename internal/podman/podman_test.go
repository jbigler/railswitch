package podman

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

var ctx = context.Background()

func exitErr(code int, stderr string) (sys.Result, error) {
	return sys.Result{Stderr: []byte(stderr), ExitCode: code},
		fmt.Errorf("podman: %w (exit %d): %s", sys.ErrExit, code, stderr)
}

func wantCalls(t *testing.T, f *systest.Fake, want ...[]string) {
	t.Helper()
	if !reflect.DeepEqual(f.Calls, want) {
		t.Fatalf("Calls = %q\nwant    %q", f.Calls, want)
	}
}

func TestSimpleArgv(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(Podman) error
		want []string
	}{
		{"Tag", func(p Podman) error { return p.Tag(ctx, "a:1", "b:2") }, []string{"podman", "tag", "a:1", "b:2"}},
		{"VolumeCreate", func(p Podman) error { return p.VolumeCreate(ctx, "api-db") }, []string{"podman", "volume", "create", "api-db"}},
		{"VolumeImport", func(p Podman) error { return p.VolumeImport(ctx, "api-db", "/t/x.tar") }, []string{"podman", "volume", "import", "api-db", "/t/x.tar"}},
		{"RemoveContainer", func(p Podman) error { return p.RemoveContainer(ctx, "api") }, []string{"podman", "rm", "-f", "--ignore", "api"}},
		{"RemoveVolume", func(p Podman) error { return p.RemoveVolume(ctx, "api") }, []string{"podman", "volume", "rm", "-f", "api"}},
		{"RemoveNetwork", func(p Podman) error { return p.RemoveNetwork(ctx, "api") }, []string{"podman", "network", "rm", "-f", "api"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &systest.Fake{}
			if err := tc.call(Podman{R: f}); err != nil {
				t.Fatal(err)
			}
			wantCalls(t, f, tc.want)
			if tc.name == "VolumeImport" && f.Cmds[0].Timeout != 10*time.Minute {
				t.Fatalf("import Timeout = %v", f.Cmds[0].Timeout)
			}
		})
	}
}

func TestImageExists(t *testing.T) {
	f := &systest.Fake{}
	ok, err := Podman{R: f}.ImageExists(ctx, "img:1")
	if !ok || err != nil {
		t.Fatalf("got %v %v", ok, err)
	}
	wantCalls(t, f, []string{"podman", "image", "exists", "img:1"})
	f.On([]string{"podman", "image", "exists"}, sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit))
	ok, err = Podman{R: f}.ImageExists(ctx, "img:1")
	if ok || err != nil {
		t.Fatalf("exit 1: got %v %v", ok, err)
	}
	f.On([]string{"podman", "image", "exists"}, sys.Result{ExitCode: 125}, fmt.Errorf("podman: %w (exit 125)", sys.ErrExit))
	if _, err = (Podman{R: f}).ImageExists(ctx, "img:1"); err == nil {
		t.Fatal("exit 125: want error")
	}
}

func TestVolumeExists(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"podman", "volume", "exists"}, sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit))
	ok, err := Podman{R: f}.VolumeExists(ctx, "api-db")
	if ok || err != nil {
		t.Fatalf("got %v %v", ok, err)
	}
	wantCalls(t, f, []string{"podman", "volume", "exists", "api-db"})
}

func TestPull(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"podman", "image", "inspect"}, sys.Result{Stdout: []byte("sha256:aa\n")}, nil)
	d, err := Podman{R: f}.Pull(ctx, "docker.io/x:1")
	if err != nil || d != "sha256:aa" {
		t.Fatalf("got %q %v", d, err)
	}
	wantCalls(t, f,
		[]string{"podman", "pull", "-q", "docker.io/x:1"},
		[]string{"podman", "image", "inspect", "--format", "{{.Digest}}", "docker.io/x:1"})
	if f.Cmds[0].Timeout != 30*time.Minute {
		t.Fatalf("pull Timeout = %v", f.Cmds[0].Timeout)
	}
}

func TestBuild(t *testing.T) {
	f := &systest.Fake{}
	err := Podman{R: f}.Build(ctx, BuildArgs{
		File: "/c/Containerfile", Tag: "rs/api:abc", Context: "/c",
		Args: map[string]string{"RUBY": "3.3.6", "BASE": "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls(t, f, []string{"podman", "build", "--pull=never", "-f", "/c/Containerfile", "-t", "rs/api:abc",
		"--build-arg", "BASE=x", "--build-arg", "RUBY=3.3.6", "/c"})
	if f.Cmds[0].Timeout != 30*time.Minute {
		t.Fatalf("Timeout = %v", f.Cmds[0].Timeout)
	}
}

func TestRemoversTolerateMissing(t *testing.T) {
	f := &systest.Fake{}
	r, e := exitErr(1, "Error: no such volume api")
	f.On([]string{"podman", "volume", "rm"}, r, e)
	r, e = exitErr(1, "Error: unable to find network with name or ID api: network not found")
	f.On([]string{"podman", "network", "rm"}, r, e)
	p := Podman{R: f}
	if err := p.RemoveVolume(ctx, "api"); err != nil {
		t.Fatal(err)
	}
	if err := p.RemoveNetwork(ctx, "api"); err != nil {
		t.Fatal(err)
	}
	r, e = exitErr(2, "Error: volume api is being used")
	f.On([]string{"podman", "volume", "rm"}, r, e)
	if err := p.RemoveVolume(ctx, "api"); err == nil {
		t.Fatal("in-use volume: want error")
	}
}

func TestNoFilters(t *testing.T) {
	f := &systest.Fake{}
	p := Podman{R: f}
	for _, fn := range []func(context.Context, string) error{p.RemoveContainer, p.RemoveVolume, p.RemoveNetwork} {
		if err := fn(ctx, "api"); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.Calls) != 3 {
		t.Fatalf("Calls = %q", f.Calls)
	}
	for _, c := range f.Calls {
		if slices.ContainsFunc(c, func(a string) bool { return a == "--filter" || len(a) > 8 && a[:9] == "--filter=" }) {
			t.Errorf("argv uses --filter: %q", c)
		}
		if c[len(c)-1] != "api" {
			t.Errorf("last arg = %q, want exact name api", c[len(c)-1])
		}
	}
}

func TestRunning(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"podman", "container", "inspect"}, sys.Result{Stdout: []byte("true\n")}, nil)
	ok, err := Podman{R: f}.Running(ctx, "api-app")
	if !ok || err != nil {
		t.Fatalf("got %v %v", ok, err)
	}
	wantCalls(t, f, []string{"podman", "container", "inspect", "--format", "{{.State.Running}}", "api-app"})
	r, e := exitErr(125, "Error: no such container api-app")
	f.On([]string{"podman", "container", "inspect"}, r, e)
	ok, err = Podman{R: f}.Running(ctx, "api-app")
	if ok || err != nil {
		t.Fatalf("missing: got %v %v", ok, err)
	}
}

func TestGraphRoot(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"podman", "info"}, sys.Result{Stdout: []byte("/home/u/.local/share/containers/storage\n")}, nil)
	g, err := Podman{R: f}.GraphRoot(ctx)
	if err != nil || g != "/home/u/.local/share/containers/storage" {
		t.Fatalf("got %q %v", g, err)
	}
	wantCalls(t, f, []string{"podman", "info", "--format", "{{.Store.GraphRoot}}"})
}

func TestImageDigest(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"podman", "image", "inspect"}, sys.Result{Stdout: []byte(" sha256:aa\n")}, nil)
	d, err := Podman{R: f}.ImageDigest(ctx, "x:1")
	if err != nil || d != "sha256:aa" {
		t.Fatalf("got %q %v", d, err)
	}
	wantCalls(t, f, []string{"podman", "image", "inspect", "--format", "{{.Digest}}", "x:1"})
	r, e := exitErr(125, "Error: x:1: image not known: no such image")
	f.On([]string{"podman", "image", "inspect"}, r, e)
	d, err = Podman{R: f}.ImageDigest(ctx, "x:1")
	if err != nil || d != "" {
		t.Fatalf("missing: got %q %v", d, err)
	}
	f.On([]string{"podman", "image", "inspect"}, sys.Result{}, errors.New("boom"))
	if _, err = (Podman{R: f}).ImageDigest(ctx, "x:1"); err == nil {
		t.Fatal("want error")
	}
}

func TestHealth(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"podman", "container", "inspect"}, sys.Result{Stdout: []byte("healthy\n")}, nil)
	h, err := Podman{R: f}.Health(ctx, "api-db")
	if err != nil || h != "healthy" {
		t.Fatalf("got %q %v", h, err)
	}
	wantCalls(t, f, []string{"podman", "container", "inspect", "--format", "{{if .State.Health}}{{.State.Health.Status}}{{end}}", "api-db"})
	r, e := exitErr(125, "Error: no such container api-db")
	f.On([]string{"podman", "container", "inspect"}, r, e)
	h, err = Podman{R: f}.Health(ctx, "api-db")
	if err != nil || h != "" {
		t.Fatalf("missing: got %q %v", h, err)
	}
	f.On([]string{"podman", "container", "inspect"}, sys.Result{Stdout: []byte("\n")}, nil)
	h, err = Podman{R: f}.Health(ctx, "api-db")
	if err != nil || h != "" {
		t.Fatalf("no healthcheck: got %q %v", h, err)
	}
}

func TestStopLoaded(t *testing.T) {
	f := &systest.Fake{}
	r, err := exitErr(5, "Failed to stop a.service: Unit a.service not loaded.")
	f.On([]string{"systemctl", "--user", "stop", "a.service"}, r, err)
	s := Systemd{R: f}
	if err := s.StopLoaded(ctx, "a.service", "b.service"); err != nil {
		t.Fatalf("not loaded: %v", err)
	}
	wantCalls(t, f,
		[]string{"systemctl", "--user", "stop", "a.service"},
		[]string{"systemctl", "--user", "stop", "b.service"})
	r, err = exitErr(1, "Job failed.")
	f.On([]string{"systemctl", "--user", "stop", "b.service"}, r, err)
	if err := s.StopLoaded(ctx, "b.service"); err == nil {
		t.Fatal("a failed stop was ignored")
	}
}

func TestSystemd(t *testing.T) {
	f := &systest.Fake{}
	s := Systemd{R: f}
	if err := s.DaemonReload(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(ctx, "a.service", "b.service"); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx, "a.service"); err != nil {
		t.Fatal(err)
	}
	if err := s.Restart(ctx, "a.service"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetFailed(ctx, "a.service", "b.service"); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, f,
		[]string{"systemctl", "--user", "daemon-reload"},
		[]string{"systemctl", "--user", "start", "a.service", "b.service"},
		[]string{"systemctl", "--user", "stop", "a.service"},
		[]string{"systemctl", "--user", "restart", "a.service"},
		[]string{"systemctl", "--user", "reset-failed", "a.service", "b.service"})
	if f.Cmds[1].Timeout != 10*time.Minute {
		t.Fatalf("start Timeout = %v", f.Cmds[1].Timeout)
	}
	if f.Cmds[3].Timeout != 10*time.Minute {
		t.Fatalf("restart Timeout = %v", f.Cmds[3].Timeout)
	}
}

func TestSystemdShow(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"systemctl", "--user", "show"}, sys.Result{Stdout: []byte("ActiveState=active\nSubState=running\nExecMainStatus=0\n")}, nil)
	m, err := Systemd{R: f}.Show(ctx, "a.service", "ActiveState", "SubState")
	if err != nil {
		t.Fatal(err)
	}
	wantCalls(t, f, []string{"systemctl", "--user", "show", "-p", "ActiveState,SubState", "a.service"})
	want := map[string]string{"ActiveState": "active", "SubState": "running", "ExecMainStatus": "0"}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("got %v", m)
	}
}

func TestSecrets(t *testing.T) {
	f := &systest.Fake{}
	p := Podman{R: f}
	f.On([]string{"podman", "secret", "exists"}, sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit))
	ok, err := p.SecretExists(ctx, "api_tok")
	if ok || err != nil {
		t.Fatalf("exists: got %v %v", ok, err)
	}
	if err := p.SecretCreate(ctx, "api_tok", strings.NewReader("s3cret"), false, Driver{}); err != nil {
		t.Fatal(err)
	}
	if err := p.SecretCreate(ctx, "api_tok", strings.NewReader("s3cret"), true,
		Driver{Name: "pass", Opts: map[string]string{"root": "/x", "key": "ABC"}}); err != nil {
		t.Fatal(err)
	}
	if err := p.SecretRemove(ctx, "api_tok"); err != nil {
		t.Fatal(err)
	}
	f.On([]string{"podman", "secret", "inspect", "--format", "{{.Spec.Driver.Name}}"}, sys.Result{Stdout: []byte("file\n")}, nil)
	if d, err := p.SecretDriver(ctx, "api_tok"); d != "file" || err != nil {
		t.Fatalf("driver: got %q %v", d, err)
	}
	f.On([]string{"podman", "secret", "inspect", "--format", "{{.CreatedAt.Unix}}"}, sys.Result{Stdout: []byte("1792919287\n")}, nil)
	c, err := p.SecretCreated(ctx, "api_tok")
	if err != nil || !c.Equal(time.Unix(1792919287, 0)) {
		t.Fatalf("created: got %v %v", c, err)
	}
	wantCalls(t, f,
		[]string{"podman", "secret", "exists", "api_tok"},
		[]string{"podman", "secret", "create", "api_tok", "-"},
		[]string{"podman", "secret", "create", "--replace", "--driver=pass", "--driver-opts=key=ABC,root=/x", "api_tok", "-"},
		[]string{"podman", "secret", "rm", "api_tok"},
		[]string{"podman", "secret", "inspect", "--format", "{{.Spec.Driver.Name}}", "api_tok"},
		[]string{"podman", "secret", "inspect", "--format", "{{.CreatedAt.Unix}}", "api_tok"})
	if string(f.Stdins[1]) != "s3cret" || string(f.Stdins[2]) != "s3cret" {
		t.Fatalf("stdins = %q", f.Stdins)
	}
}
