package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

// exit1 is podman's answer for `secret exists` of a missing secret.
func exit1() (sys.Result, error) {
	return sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit)
}

// unset scripts `podman secret exists <name>` as missing.
func unset(f *systest.Fake, name string) {
	r, err := exit1()
	f.On([]string{"podman", "secret", "exists", name}, r, err)
}

// lockedWS is filialWS after railswitch lock.
func lockedWS(t *testing.T) (string, machine.Dirs) {
	t.Helper()
	ws, dirs := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	return ws, dirs
}

// created scripts every `podman secret inspect --format {{.CreatedAt}}`.
func created(f *systest.Fake) {
	f.On([]string{"podman", "secret", "inspect", "--format", "{{.CreatedAt.Unix}}"}, sys.Result{Stdout: []byte("1792919287\n")}, nil)
}

func runStdin(t *testing.T, f *systest.Fake, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := dispatch(context.Background(), f, args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

// secretCalls are f's `podman secret …` calls and their stdins.
func secretCalls(f *systest.Fake) (calls [][]string, stdins []string) {
	for i, c := range f.Calls {
		if len(c) > 1 && c[0] == "podman" && c[1] == "secret" {
			calls = append(calls, c)
			stdins = append(stdins, string(f.Stdins[i]))
		}
	}
	return calls, stdins
}

// noValue fails when value is in any argv, stdout or stderr.
func noValue(t *testing.T, f *systest.Fake, value string, outs ...string) {
	t.Helper()
	for _, c := range f.Calls {
		if slices.ContainsFunc(c, func(a string) bool { return strings.Contains(a, value) }) {
			t.Errorf("argv holds the value: %q", c)
		}
	}
	for _, o := range outs {
		if strings.Contains(o, value) {
			t.Errorf("output holds the value: %q", o)
		}
	}
}

func TestSecretSetStdin(t *testing.T) {
	ws, _ := lockedWS(t)
	f := fakeHost()
	unset(f, "filial_github_token")
	code, out, errs := runStdin(t, f, "tok123\n", "-C", ws, "secret", "set", "github_token")
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	calls, stdins := secretCalls(f)
	want := [][]string{
		{"podman", "secret", "exists", "filial_github_token"},
		{"podman", "secret", "create", "filial_github_token", "-"},
	}
	if !slices.EqualFunc(calls, want, slices.Equal) || stdins[1] != "tok123" {
		t.Fatalf("calls = %q stdins = %q", calls, stdins)
	}
	noValue(t, f, "tok123", out, errs)
}

// TestSecretSetCRLF: a value ending in CRLF (a file saved on Windows) loses
// the \r with the newline.
func TestSecretSetCRLF(t *testing.T) {
	ws, _ := lockedWS(t)
	f := fakeHost()
	unset(f, "filial_github_token")
	if code, out, errs := runStdin(t, f, "tok123\r\n", "-C", ws, "secret", "set", "github_token"); code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	if _, stdins := secretCalls(f); len(stdins) != 2 || stdins[1] != "tok123" {
		t.Fatalf("stdins = %q", stdins)
	}
}

func TestSecretSetReplace(t *testing.T) {
	ws, _ := lockedWS(t)
	f := fakeHost()
	f.On([]string{"podman", "secret", "inspect", "--format", "{{.Spec.Driver.Name}}"}, sys.Result{Stdout: []byte("file\n")}, nil)
	code, out, errs := runStdin(t, f, "tok123\n", "-C", ws, "secret", "set", "github_token")
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	calls, _ := secretCalls(f)
	if len(calls) != 3 || !slices.Equal(calls[2], []string{"podman", "secret", "create", "--replace", "filial_github_token", "-"}) {
		t.Fatalf("calls = %q", calls)
	}
}

func TestSecretSetRejects(t *testing.T) {
	ws, _ := lockedWS(t)
	for _, tc := range []struct {
		name, stdin string
		code        int
	}{
		{"nope", "x\n", 2},
		{"github_token", "", 1},
		{"github_token", "\n", 1},
		{"github_token", "a\nb", 1},
	} {
		f := fakeHost()
		code, out, errs := runStdin(t, f, tc.stdin, "-C", ws, "secret", "set", tc.name)
		if code != tc.code {
			t.Errorf("set %s <<%q: exit %d, want %d\nstdout: %s\nstderr: %s", tc.name, tc.stdin, code, tc.code, out, errs)
		}
		if calls, _ := secretCalls(f); slices.ContainsFunc(calls, func(c []string) bool { return c[2] == "create" }) {
			t.Errorf("set %s <<%q: created a secret: %q", tc.name, tc.stdin, calls)
		}
	}
}

func TestSecretSetRunningNotice(t *testing.T) {
	ws, _ := lockedWS(t)
	running := func() *systest.Fake {
		f := fakeHost()
		f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", "filial-master-nvim"}, sys.Result{Stdout: []byte("true\n")}, nil)
		return f
	}
	f := running()
	code, out, errs := runStdin(t, f, "tok\n", "-C", ws, "secret", "set", "github_token")
	if code != 0 || !strings.Contains(out+errs, "filial-master-nvim") || !strings.Contains(out+errs, "restart") {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	if strings.Contains(out+errs, "filial-feat-nvim") {
		t.Errorf("names a stopped container:\n%s%s", out, errs)
	}
	for _, c := range f.Calls {
		if c[0] == "systemctl" {
			t.Errorf("restarted without --restart: %q", c)
		}
	}
	f = running()
	code, out, errs = runStdin(t, f, "tok\n", "-C", ws, "secret", "set", "github_token", "--restart")
	if code != 0 {
		t.Fatalf("--restart: exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	want := []string{"systemctl", "--user", "restart", "filial-master-nvim.service"}
	if !slices.ContainsFunc(f.Calls, func(c []string) bool { return slices.Equal(c, want) }) {
		t.Fatalf("calls = %q", f.Calls)
	}
}

// An on-demand agent has no unit: a running session of it is neither named
// for a restart nor restarted; its next run gets the new value.
func TestSecretSetOnDemandReceiver(t *testing.T) {
	ws, _ := lockedWS(t)
	editProject(t, ws, "services = [\"nvim\"]\n", "services = [\"nvim\", \"agent-claude\"]\n")
	running := func() *systest.Fake {
		f := fakeHost()
		for _, c := range []string{"filial-master-nvim", "filial-master-agent-claude"} {
			f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", c}, sys.Result{Stdout: []byte("true\n")}, nil)
		}
		return f
	}
	f := running()
	code, out, errs := runStdin(t, f, "tok\n", "-C", ws, "secret", "set", "github_token")
	if code != 0 || !strings.Contains(out, "filial-master-nvim") || strings.Contains(out+errs, "agent-claude") {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	f = running()
	code, out, errs = runStdin(t, f, "tok\n", "-C", ws, "secret", "set", "github_token", "--restart")
	if code != 0 {
		t.Fatalf("--restart: exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	var restarts [][]string
	for _, c := range f.Calls {
		if len(c) > 2 && c[0] == "systemctl" && c[2] == "restart" {
			restarts = append(restarts, c)
		}
	}
	want := []string{"systemctl", "--user", "restart", "filial-master-nvim.service"}
	if len(restarts) != 1 || !slices.Equal(restarts[0], want) {
		t.Fatalf("restarts = %q; want only %q", restarts, want)
	}
}

func TestSecretLsNoValues(t *testing.T) {
	ws, _ := lockedWS(t)
	f := fakeHost()
	unset(f, "filial_github_token")
	created(f)
	code, out, errs := runStdin(t, f, "", "-C", ws, "secret", "ls")
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	var line string
	for l := range strings.Lines(out) {
		if strings.HasPrefix(l, "github_token") {
			line = l
		}
	}
	for _, s := range []string{"github_token", "project", "filial_github_token", "not set", "nvim"} {
		if !strings.Contains(line, s) {
			t.Errorf("line %q lacks %q\n%s", line, s, out)
		}
	}
	if !strings.Contains(out, "env TWILIO_AUTH_TOKEN") {
		t.Errorf("no env delivery:\n%s", out)
	}
	code, out, _ = runStdin(t, f, "", "-C", ws, "--json", "secret", "ls")
	var rows []secretRow
	if code != 0 || json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 3 {
		t.Fatalf("--json: exit %d\n%s", code, out)
	}
	gh := rows[0]
	if gh.Name != "github_token" || gh.Scope != "project" || gh.PodmanSecret != "filial_github_token" ||
		gh.Delivery != "file" || gh.Set || gh.Created != nil || !slices.Equal(gh.Services, []string{"nvim"}) {
		t.Errorf("github_token row = %+v", gh)
	}
	if p := rows[1]; p.Name != "probe_key" || !slices.Equal(p.Services, []string{"agent-probe"}) {
		t.Errorf("probe_key row = %+v", p)
	}
	if rows[2].Delivery != "env TWILIO_AUTH_TOKEN" || !rows[2].Set || rows[2].Created == nil {
		t.Errorf("twilio row = %+v", rows[2])
	}
}

func TestMachineScope(t *testing.T) {
	ws, _ := lockedWS(t)
	editProject(t, ws, "services = [\"nvim\"]\n", "services = [\"nvim\"]\nscope = \"machine\"\n")
	f := fakeHost()
	unset(f, "railswitch_github_token")
	created(f)
	code, out, errs := runStdin(t, f, "", "-C", ws, "secret", "ls")
	if code != 0 || !strings.Contains(out, "railswitch_github_token") || !strings.Contains(out, "machine") || strings.Contains(out, "filial_github_token") {
		t.Fatalf("ls: exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	f = fakeHost()
	unset(f, "railswitch_github_token")
	if code, out, errs := runStdin(t, f, "tok\n", "-C", ws, "secret", "set", "github_token"); code != 0 {
		t.Fatalf("set: exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	calls, _ := secretCalls(f)
	if len(calls) != 2 || !slices.Equal(calls[1], []string{"podman", "secret", "create", "railswitch_github_token", "-"}) {
		t.Fatalf("calls = %q", calls)
	}
	f = fakeHost()
	if code, _, errs := runStdin(t, f, "", "-C", ws, "secret", "rm", "github_token"); code != 0 {
		t.Fatalf("rm: exit %d: %s", code, errs)
	}
	if calls, _ := secretCalls(f); len(calls) != 1 || !slices.Equal(calls[0], []string{"podman", "secret", "rm", "railswitch_github_token"}) {
		t.Fatalf("rm calls = %q", calls)
	}
}

func writeConfig(t *testing.T, dir, data string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

const passConfig = "schema = 1\n[secrets]\ndriver = \"pass\"\ndriver_opts = { root = \"/x\", key = \"ABC\" }\n"

func TestSecretDriverPass(t *testing.T) {
	ws, dirs := lockedWS(t)
	f := fakeHost()
	unset(f, "filial_github_token")
	if code, out, errs := runStdin(t, f, "tok\n", "-C", ws, "secret", "set", "github_token"); code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	calls, _ := secretCalls(f)
	if slices.ContainsFunc(calls[1], func(a string) bool { return strings.HasPrefix(a, "--driver") }) {
		t.Fatalf("no config: create = %q", calls[1])
	}
	writeConfig(t, dirs.Config, passConfig)
	f = fakeHost()
	unset(f, "filial_github_token")
	if code, out, errs := runStdin(t, f, "tok\n", "-C", ws, "secret", "set", "github_token"); code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	calls, _ = secretCalls(f)
	want := []string{"podman", "secret", "create", "--driver=pass", "--driver-opts=key=ABC,root=/x", "filial_github_token", "-"}
	if !slices.Equal(calls[1], want) {
		t.Fatalf("create = %q\nwant     %q", calls[1], want)
	}
}

func TestSecretLsDriverDiffers(t *testing.T) {
	ws, dirs := lockedWS(t)
	writeConfig(t, dirs.Config, passConfig)
	f := fakeHost()
	f.On([]string{"podman", "secret", "inspect", "--format", "{{.Spec.Driver.Name}}"}, sys.Result{Stdout: []byte("file\n")}, nil)
	created(f)
	code, out, errs := runStdin(t, f, "", "-C", ws, "secret", "ls")
	if code != 0 || !strings.Contains(out, "driver differs: railswitch secret set github_token to move it") {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
}

func TestSecretAdd(t *testing.T) {
	ws, _ := lockedWS(t)
	editProject(t, ws, "[secrets.twilio_auth_token]                # values live in podman secrets\nservices = [\"rails\"]\nenv      = \"TWILIO_AUTH_TOKEN\"\n\n", "")
	path := filepath.Join(ws, "project.toml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f := fakeHost()
	unset(f, "filial_twilio_auth_token")
	args := []string{"-C", ws, "secret", "add", "twilio_auth_token", "--service", "rails", "--env", "TWILIO_AUTH_TOKEN"}
	code, out, errs := runStdin(t, f, "AC123\n", args...)
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := string(before) + "\n[secrets.twilio_auth_token]\nservices = [\"rails\"]\nenv = \"TWILIO_AUTH_TOKEN\"\n"
	if string(after) != want {
		t.Fatalf("project.toml:\n%s\nwant:\n%s", after, want)
	}
	calls, stdins := secretCalls(f)
	if len(calls) != 2 || !slices.Equal(calls[1], []string{"podman", "secret", "create", "filial_twilio_auth_token", "-"}) || stdins[1] != "AC123" {
		t.Fatalf("calls = %q stdins = %q", calls, stdins)
	}
	noValue(t, f, "AC123", out, errs)

	if code, out, errs := runStdin(t, fakeHost(), "AC123\n", args...); code != 1 {
		t.Fatalf("second add: exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	code, out, errs = runStdin(t, fakeHost(), "x\n", "-C", ws, "secret", "add", "other", "--service", "nope")
	if code != 2 || !strings.Contains(errs, "nope") || !strings.Contains(errs, "rails, deps, db") {
		t.Fatalf("--service nope: exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
}

func TestSecretAddEnvCollision(t *testing.T) {
	ws, _ := lockedWS(t)
	path := filepath.Join(ws, "project.toml")
	before := readFile(t, path)
	f := fakeHost()
	code, out, errs := runStdin(t, f, "v\n", "-C", ws, "secret", "add", "dburl", "--service", "rails", "--env", "DATABASE_URL")
	if code != 1 || !strings.Contains(errs, "dburl") || !strings.Contains(errs, "rails") || !strings.Contains(errs, "DATABASE_URL") {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	if readFile(t, path) != before {
		t.Error("project.toml changed")
	}
	if calls, _ := secretCalls(f); len(calls) != 0 {
		t.Errorf("podman secret calls: %q", calls)
	}
}

func TestSecretSetDriverMove(t *testing.T) {
	ws, dirs := lockedWS(t)
	writeConfig(t, dirs.Config, passConfig)
	f := fakeHost()
	f.On([]string{"podman", "secret", "inspect", "--format", "{{.Spec.Driver.Name}}"}, sys.Result{Stdout: []byte("file\n")}, nil)
	if code, out, errs := runStdin(t, f, "tok\n", "-C", ws, "secret", "set", "github_token"); code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	calls, _ := secretCalls(f)
	want := [][]string{
		{"podman", "secret", "exists", "filial_github_token"},
		{"podman", "secret", "inspect", "--format", "{{.Spec.Driver.Name}}", "filial_github_token"},
		{"podman", "secret", "rm", "filial_github_token"},
		{"podman", "secret", "create", "--driver=pass", "--driver-opts=key=ABC,root=/x", "filial_github_token", "-"},
	}
	if !slices.EqualFunc(calls, want, slices.Equal) {
		t.Fatalf("calls = %q", calls)
	}
	// Same driver: --replace.
	f = fakeHost()
	f.On([]string{"podman", "secret", "inspect", "--format", "{{.Spec.Driver.Name}}"}, sys.Result{Stdout: []byte("pass\n")}, nil)
	if code, _, errs := runStdin(t, f, "tok\n", "-C", ws, "secret", "set", "github_token"); code != 0 {
		t.Fatal(errs)
	}
	calls, _ = secretCalls(f)
	if last := calls[len(calls)-1]; !slices.Contains(last, "--replace") || slices.ContainsFunc(calls, func(c []string) bool { return c[2] == "rm" }) {
		t.Fatalf("calls = %q", calls)
	}
}

// TestSecretSetDriverMoveCreateFails: when the create after removing the
// old driver's copy fails, the error says the secret is now unset and how
// to set it.
func TestSecretSetDriverMoveCreateFails(t *testing.T) {
	ws, dirs := lockedWS(t)
	writeConfig(t, dirs.Config, passConfig)
	f := fakeHost()
	f.On([]string{"podman", "secret", "inspect", "--format", "{{.Spec.Driver.Name}}"}, sys.Result{Stdout: []byte("file\n")}, nil)
	f.On([]string{"podman", "secret", "create"}, sys.Result{ExitCode: 125}, fmt.Errorf("podman: %w (exit 125): pass: no key", sys.ErrExit))
	code, _, errs := runStdin(t, f, "tok\n", "-C", ws, "secret", "set", "github_token")
	if code == 0 || !strings.Contains(errs, "filial_github_token is now unset") || !strings.Contains(errs, "railswitch secret set github_token") {
		t.Fatalf("exit %d, stderr %q", code, errs)
	}
}

func TestSecretAddGitSigning(t *testing.T) {
	ws, _ := lockedWS(t)
	path := filepath.Join(ws, "project.toml")
	before := readFile(t, path)
	f := fakeHost()
	unset(f, "filial_sign")
	const key = "-----BEGIN OPENSSH PRIVATE KEY----- AAAAsigning"
	code, out, errs := runStdin(t, f, key+"\n", "-C", ws, "secret", "add", "sign", "--service", "rails", "--git-signing")
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	want := before + "\n[secrets.sign]\nservices = [\"rails\"]\ngit_signing = true\n"
	if got := readFile(t, path); got != want {
		t.Fatalf("project.toml:\n%s\nwant:\n%s", got, want)
	}
	calls, stdins := secretCalls(f)
	if len(calls) != 2 || !slices.Equal(calls[1], []string{"podman", "secret", "create", "filial_sign", "-"}) || stdins[1] != key {
		t.Fatalf("calls = %q stdins = %q", calls, stdins)
	}
	noValue(t, f, key, out, errs)

	// env delivery is refused before anything is written.
	code, out, errs = runStdin(t, fakeHost(), key+"\n", "-C", ws, "secret", "add", "sign2", "--service", "rails", "--git-signing", "--env", "SIGN_KEY")
	if code != 1 || !strings.Contains(errs, "git_signing") {
		t.Fatalf("--env: exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
}
