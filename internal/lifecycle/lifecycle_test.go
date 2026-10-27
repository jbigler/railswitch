package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/firewall"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
	"github.com/jbigler/railswitch/internal/testfixture"
	"github.com/jbigler/railswitch/internal/unithash"
	"github.com/jbigler/railswitch/internal/workspace"
)

var ctx = context.Background()

const (
	ruby       = "docker.io/library/ruby:3.3.6-slim"
	node       = "docker.io/library/node:22.11.0-slim"
	debian     = "docker.io/library/debian:trixie-slim"
	playwright = "mcr.microsoft.com/playwright:v1.54.0-noble"
)

type tenv struct {
	ws  string
	f   *systest.Fake
	l   *L
	out *bytes.Buffer
}

// units makes every unit-state query report stdout; unit, if given,
// narrows it to that unit.
func (e *tenv) units(stdout string, unit ...string) {
	e.f.On(append([]string{"systemctl", "--user", "show", "-p", "LoadState,NeedDaemonReload,ActiveState"}, unit...), sys.Result{Stdout: []byte(stdout)}, nil)
}

// missing makes calls with argv prefix exit 1 ("does not exist").
func (e *tenv) missing(prefix ...string) {
	e.f.On(prefix, sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit))
}

// setup is the Filial fixture workspace, registered with worktrees slugs
// (IDs from 1), locked and loaded, with a fake host: podman's graph root,
// every image digest sha256:aa.
func setup(t *testing.T, slugs ...string) *tenv {
	t.Helper()
	if len(slugs) == 0 {
		slugs = []string{"master", "feat"}
	}
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	ws := testfixture.Workspace(t, "../../testdata/fixtures/filial")
	for _, d := range append([]string{"master/.git"}, slugs...) {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.AddProject("filial", ws, nil); err != nil {
		t.Fatal(err)
	}
	for _, s := range slugs {
		if _, err := reg.AddWorktree("filial", s, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	reg.Close()
	if err := workspace.Lock(ctx, ws); err != nil {
		t.Fatal(err)
	}
	w, err := workspace.Load(ctx, ws, "")
	if err != nil {
		t.Fatal(err)
	}
	f := &systest.Fake{}
	f.On([]string{"podman", "info", "--format", "{{.Store.GraphRoot}}"}, sys.Result{Stdout: []byte("/g\n")}, nil)
	f.On([]string{"podman", "image", "inspect", "--format", "{{.Digest}}"}, sys.Result{Stdout: []byte("sha256:aa\n")}, nil)
	out := &bytes.Buffer{}
	var routers []string
	for _, s := range slugs {
		routers = append(routers, "filial-"+s+"-app@docker")
	}
	_, api := newFakeTraefik(t, routers...)
	return &tenv{ws: ws, f: f, out: out, l: &L{R: f, WS: w, Out: out, Err: &bytes.Buffer{}, In: strings.NewReader(""), TraefikAPI: api}}
}

func (e *tenv) render(t *testing.T) {
	t.Helper()
	if _, err := e.l.WS.Render(ctx, e.f, false); err != nil {
		t.Fatal(err)
	}
}

// reset forgets the recorded calls.
func (e *tenv) reset() { e.f.Calls, e.f.Cmds, e.f.Stdins = nil, nil, nil }

// calls are the recorded podman, systemctl and journalctl argvs.
func (e *tenv) calls() [][]string {
	var out [][]string
	for _, c := range e.f.Calls {
		if c[0] != "git" {
			out = append(out, c)
		}
	}
	return out
}

func joined(c []string) string { return strings.Join(c, " ") }

// wantInOrder fails unless each want (a space-joined argv prefix) matches a
// call, in order.
func wantInOrder(t *testing.T, calls [][]string, want ...string) {
	t.Helper()
	i := 0
	for _, w := range want {
		for i < len(calls) && !strings.HasPrefix(joined(calls[i]), w) {
			i++
		}
		if i == len(calls) {
			t.Fatalf("no call %q in order; calls:\n%s", w, dump(calls))
		}
		i++
	}
}

func dump(calls [][]string) string {
	var b strings.Builder
	for _, c := range calls {
		b.WriteString(joined(c) + "\n")
	}
	return b.String()
}

func count(calls [][]string, prefix string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(joined(c), prefix) {
			n++
		}
	}
	return n
}

func find(t *testing.T, calls [][]string, prefix string) []string {
	t.Helper()
	for _, c := range calls {
		if strings.HasPrefix(joined(c), prefix) {
			return c
		}
	}
	t.Fatalf("no call %q; calls:\n%s", prefix, dump(calls))
	return nil
}

func builds(calls [][]string) [][]string {
	var b [][]string
	for _, c := range calls {
		if len(c) > 1 && c[0] == "podman" && c[1] == "build" {
			b = append(b, c)
		}
	}
	return b
}

func argAfter(c []string, flag string) string {
	if i := slices.Index(c, flag); i >= 0 && i+1 < len(c) {
		return c[i+1]
	}
	return ""
}

func TestUpOrder(t *testing.T) {
	e := setup(t)
	e.missing("podman", "image", "exists")
	e.missing("podman", "volume", "exists")
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	calls := e.calls()
	ws := e.ws
	wantInOrder(t, calls,
		"podman info",
		"podman pull -q docker.io/library/postgres:17", "podman image inspect --format {{.Digest}} docker.io/library/postgres:17",
		"podman info", // render again
		"podman pull -q "+node, "podman image inspect --format {{.Digest}} "+node,
		"podman pull -q "+ruby, "podman image inspect --format {{.Digest}} "+ruby,
		"podman pull -q "+debian, "podman image inspect --format {{.Digest}} "+debian,
		"podman pull -q "+playwright, "podman image inspect --format {{.Digest}} "+playwright,
		"podman image exists localhost/filial/base:", "podman build --pull=never -f "+ws+"/containerfiles/Containerfile.base",
		"podman image exists localhost/filial/base:", "podman build --pull=never -f "+ws+"/containerfiles/Containerfile.pg-client",
		"podman image exists localhost/filial/nvim:", "podman build --pull=never -f "+ws+"/containerfiles/Containerfile.nvim",
		"podman image exists localhost/filial/agent-base:", "podman build --pull=never -f "+ws+"/containerfiles/Containerfile.agent-base",
		"podman image exists localhost/filial/agent-claude:", "podman build --pull=never -f "+ws+"/containerfiles/Containerfile.claude",
		"podman image exists localhost/filial/agent-probe:", "podman build --pull=never -f "+ws+"/containerfiles/Containerfile.probe",
		"podman image exists localhost/filial/playwright:1.54.0-", "podman build --pull=never -f "+ws+"/containerfiles/Containerfile.playwright -t localhost/filial/playwright:1.54.0-",
		"podman volume exists filial-feat-rustfs-data", "podman volume create filial-feat-rustfs-data",
		"podman volume import filial-feat-rustfs-data "+ws+"/db-dumps/rustfs_data.tar.gz",
		"systemctl --user daemon-reload",
		"systemctl --user start filial-proxy-network.service",
		"systemctl --user start filial-feat-rails.service",
	)
	if n := count(calls, "systemctl --user restart"); n != 0 {
		t.Errorf("restarted:\n%s", dump(calls))
	}
	b := builds(calls)
	if len(b) != 7 {
		t.Fatalf("builds:\n%s", dump(b))
	}
	baseTag := argAfter(b[0], "-t")
	if strings.Contains(joined(b[0]), "BASE_IMAGE=") {
		t.Errorf("base has BASE_IMAGE: %q", b[0])
	}
	if !slices.Contains(b[1], "BASE_IMAGE="+baseTag) {
		t.Errorf("pg-client argv lacks BASE_IMAGE=%s: %q", baseTag, b[1])
	}
	if !slices.Contains(b[2], "BASE_IMAGE="+argAfter(b[1], "-t")) {
		t.Errorf("nvim argv lacks the pg-client tag as BASE_IMAGE: %q", b[2])
	}
	if pw := b[len(b)-1]; !slices.Contains(pw, "PLAYWRIGHT_VERSION=1.54.0") || strings.Contains(joined(pw), "BASE_IMAGE") {
		t.Errorf("playwright argv: %q", pw)
	}
	if app := calls[len(calls)-2]; joined(app) != "systemctl --user start filial-feat-rails.service" {
		t.Errorf("app start %q", app)
	}
	if last := calls[len(calls)-1]; !strings.HasPrefix(joined(last), "systemctl --user start filial-feat-db.service ") {
		t.Errorf("last call %q", last)
	}
}

func TestUpRecordsDigest(t *testing.T) {
	e := setup(t)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	l, err := config.ReadLock(filepath.Join(e.ws, "stack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if l.Digest("docker.io/library/postgres:17") != "sha256:aa" {
		t.Fatalf("lock images = %+v", l.Images)
	}
	unit, err := os.ReadFile(filepath.Join(e.l.WS.Dirs.Quadlet, "filial-feat-db.container"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "Image=docker.io/library/postgres:17@sha256:aa\n") {
		t.Fatalf("unit:\n%s", unit)
	}
}

// git-guard (D60): a finding is a warning; up still runs.
func TestUpWarnsGitGuard(t *testing.T) {
	e := setup(t)
	hook := filepath.Join(e.ws, "master", ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	errs := e.l.Err.(*bytes.Buffer).String()
	if !strings.Contains(errs, "WARNING: git-guard: 1 finding(s): hook-missing-backup "+hook) || !strings.Contains(errs, "; railswitch hooks check\n") {
		t.Errorf("stderr lacks the git-guard warning:\n%s", errs)
	}
}

func TestUpPinsBaseImages(t *testing.T) {
	e := setup(t)
	e.missing("podman", "image", "exists")
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	find(t, e.calls(), "podman pull -q "+ruby)
	l, err := config.ReadLock(filepath.Join(e.ws, "stack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if l.BaseDigest(ruby) != "sha256:aa" {
		t.Fatalf("base images = %+v", l.BaseImages)
	}
	for _, b := range builds(e.calls()) {
		if b[2] != "--pull=never" {
			t.Errorf("build argv %q", b)
		}
	}

	e.reset()
	r := sys.Result{ExitCode: 125, Stderr: []byte("Error: " + ruby + ": image not known")}
	e.f.On([]string{"podman", "image", "inspect", "--format", "{{.Digest}}", ruby}, r, fmt.Errorf("podman: %w (exit 125)", sys.ErrExit))
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	pinned := "docker.io/library/ruby@sha256:aa"
	wantInOrder(t, e.calls(), "podman pull -q "+pinned, "podman tag "+pinned+" "+ruby)
	if n := count(e.calls(), "podman pull -q "+ruby); n != 0 {
		t.Errorf("pulled by tag again")
	}
	for _, b := range builds(e.calls()) {
		if b[2] != "--pull=never" {
			t.Errorf("build argv %q", b)
		}
	}
}

func TestUpLocalDigestDiffers(t *testing.T) {
	e := setup(t)
	e.l.WS.Lock.SetBaseDigest(ruby, "sha256:aa")
	e.f.On([]string{"podman", "image", "inspect", "--format", "{{.Digest}}", ruby}, sys.Result{Stdout: []byte("sha256:zz\n")}, nil)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	pinned := "docker.io/library/ruby@sha256:aa"
	wantInOrder(t, e.calls(), "podman pull -q "+pinned, "podman tag "+pinned+" "+ruby)
}

func TestUpSecondRunNoPull(t *testing.T) {
	e := setup(t)
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=active\n")
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	e.reset()
	e.labels(t)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	calls := e.calls()
	if n := count(calls, "podman pull"); n != 0 {
		t.Errorf("second Up pulled:\n%s", dump(calls))
	}
	if n := count(calls, "systemctl --user daemon-reload") + count(calls, "systemctl --user restart"); n != 0 {
		t.Errorf("second Up reloaded:\n%s", dump(calls))
	}
	find(t, calls, "systemctl --user start filial-feat-rails.service")
}

func TestUpBuildFailsNoStart(t *testing.T) {
	e := setup(t)
	e.missing("podman", "image", "exists")
	e.f.On([]string{"podman", "build"}, sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit))
	if err := e.l.Up(ctx, "feat"); err == nil {
		t.Fatal("want error")
	}
	if n := count(e.calls(), "systemctl --user start"); n != 0 {
		t.Fatalf("started after a failed build:\n%s", dump(e.calls()))
	}
	e.reset()
	e.f.On([]string{"podman", "build"}, sys.Result{}, nil)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	if n := len(builds(e.calls())); n != 7 {
		t.Fatalf("re-run builds = %d", n)
	}
	find(t, e.calls(), "systemctl --user start filial-feat-rails.service")
}

func TestBuildPull(t *testing.T) {
	e := setup(t)
	for _, r := range []string{ruby, node, debian, playwright} {
		e.l.WS.Lock.SetBaseDigest(r, "sha256:aaa")
	}
	e.f.On([]string{"podman", "image", "inspect", "--format", "{{.Digest}}"}, sys.Result{Stdout: []byte("sha256:aaa\n")}, nil)
	e.f.On([]string{"podman", "image", "inspect", "--format", "{{.Digest}}", ruby}, sys.Result{Stdout: []byte("sha256:bbb\n")}, nil)
	if err := e.l.Build(ctx, "feat", false, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.out.String(), ruby+": sha256:aaa → sha256:bbb\n") {
		t.Fatalf("out = %q", e.out.String())
	}
	if strings.Contains(e.out.String(), node+":") {
		t.Errorf("unchanged ref reported: %q", e.out.String())
	}
	if b := builds(e.calls()); len(b) != 7 {
		t.Fatalf("builds (image exists for all):\n%s", dump(b))
	}
	l, err := config.ReadLock(filepath.Join(e.ws, "stack.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if l.BaseDigest(ruby) != "sha256:bbb" {
		t.Fatalf("lock = %+v", l.BaseImages)
	}

	e.reset()
	if err := e.l.Build(ctx, "feat", false, false); err != nil {
		t.Fatal(err)
	}
	if b := builds(e.calls()); len(b) != 0 {
		t.Fatalf("build without changes rebuilt:\n%s", dump(b))
	}
	if err := e.l.Build(ctx, "feat", true, false); err != nil {
		t.Fatal(err)
	}
	if b := builds(e.calls()); len(b) != 7 {
		t.Fatalf("force:\n%s", dump(b))
	}
}

func TestTimeouts(t *testing.T) {
	e := setup(t)
	e.render(t)
	cmdOf := func(prefix string) sys.Cmd {
		t.Helper()
		for i, c := range e.f.Calls {
			if strings.HasPrefix(joined(c), prefix) {
				return e.f.Cmds[i]
			}
		}
		t.Fatalf("no call %q", prefix)
		return sys.Cmd{}
	}
	check := func(what, prefix string, want time.Duration) {
		t.Helper()
		if got := cmdOf(prefix).Timeout; got != want {
			t.Errorf("%s: Timeout = %v, want %v", what, got, want)
		}
		e.reset()
	}
	if err := e.l.Logs(ctx, "feat", "", true); err != nil {
		t.Fatal(err)
	}
	check("logs -f", "podman logs -f", sys.NoTimeout)
	if err := e.l.Logs(ctx, "feat", "", false); err != nil {
		t.Fatal(err)
	}
	check("logs", "podman logs", 0)
	e.f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}"}, sys.Result{Stdout: []byte("true\n")}, nil)
	if err := e.l.Exec(ctx, "feat", false, []string{"bin/rails", "console"}); err != nil {
		t.Fatal(err)
	}
	check("exec", "podman exec", sys.NoTimeout)
	if err := e.l.Exec(ctx, "feat", true, []string{"bin/rails", "console"}); err != nil {
		t.Fatal(err)
	}
	check("exec --fresh", "podman --hooks-dir", sys.NoTimeout)
	if err := e.l.Deps(ctx, "feat", []string{"bundle", "add", "x"}); err != nil {
		t.Fatal(err)
	}
	check("deps", "podman --hooks-dir", sys.NoTimeout)
}

// envRunner records the --env-file of a podman run while the file exists.
type envRunner struct {
	*systest.Fake
	path, data string
}

func (r *envRunner) Run(ctx context.Context, c sys.Cmd) (sys.Result, error) {
	if p := argAfter(c.Args, "--env-file"); p != "" && slices.Contains(c.Args, "run") {
		r.path = p
		b, err := os.ReadFile(p)
		if err != nil {
			return sys.Result{}, err
		}
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != 0o600 {
			return sys.Result{}, fmt.Errorf("env file mode %v %v", fi.Mode(), err)
		}
		r.data = string(b)
	}
	return r.Fake.Run(ctx, c)
}

func TestDepsUnfrozen(t *testing.T) {
	e := setup(t)
	e.render(t)
	r := &envRunner{Fake: e.f}
	e.l.R = r
	if err := e.l.Deps(ctx, "feat", []string{"bundle", "add", "x"}); err != nil {
		t.Fatal(err)
	}
	c := find(t, e.calls(), "podman --hooks-dir")
	if !slices.Contains(c, "filial.firewall=deps") || argAfter(c, "--annotation") != "filial.firewall=deps" {
		t.Errorf("argv lacks --annotation filial.firewall=deps: %q", c)
	}
	if argAfter(c, "-w") != "/app" || !slices.Equal(c[len(c)-3:], []string{"bundle", "add", "x"}) {
		t.Errorf("argv %q", c)
	}
	if r.data == "" || strings.Contains(r.data, "BUNDLE_FROZEN") || !strings.Contains(r.data, "RAILS_ENV=development\n") || !strings.Contains(r.data, "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1\n") {
		t.Errorf("deps env file:\n%s", r.data)
	}
	if _, err := os.Stat(r.path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temp env file %s left behind: %v", r.path, err)
	}
	rendered, err := os.ReadFile(filepath.Join(e.ws, ".railswitch/env/master/deps.env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "BUNDLE_FROZEN=true\n") {
		t.Errorf("rendered deps env:\n%s", rendered)
	}
}

func TestStatusJSON(t *testing.T) {
	e := setup(t)
	e.f.On([]string{"systemctl", "--user", "show"}, sys.Result{Stdout: []byte("ActiveState=active\nSubState=running\n")}, nil)
	e.f.On([]string{"podman", "container", "inspect"}, sys.Result{Stdout: []byte("healthy\n")}, nil)
	if err := e.l.Status(ctx, "feat", true); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]string
	if err := json.Unmarshal(e.out.Bytes(), &rows); err != nil {
		t.Fatalf("%v: %s", err, e.out.String())
	}
	want := map[string]string{"service": "rails", "role": "app", "state": "active/running", "health": "healthy"}
	if len(rows) == 0 || fmt.Sprint(rows[0]) != fmt.Sprint(want) {
		t.Fatalf("rows = %v", rows)
	}
	for _, r := range rows {
		if len(r) != 4 {
			t.Errorf("row fields %v", r)
		}
	}
	find(t, e.calls(), "podman container inspect --format {{if .State.Health}}{{.State.Health.Status}}{{end}} filial-feat-rails")
	find(t, e.calls(), "systemctl --user show -p ActiveState,SubState filial-feat-rails.service")
}

func TestPreflightMissingBind(t *testing.T) {
	e := setup(t)
	e.render(t)
	git := filepath.Join(e.ws, "master", ".git")
	if err := os.Remove(git); err != nil {
		t.Fatal(err)
	}
	m, err := e.l.model(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.l.Preflight(ctx, m, "feat"); err == nil || !strings.Contains(err.Error(), git) {
		t.Fatalf("got %v", err)
	}
}

func TestPreflightFreshWorkspace(t *testing.T) {
	e := setup(t)
	e.render(t)
	m, err := e.l.model(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.l.Preflight(ctx, m, "feat"); err != nil {
		t.Fatal(err)
	}
}

func TestPreflightNoDescriptor(t *testing.T) {
	e := setup(t)
	e.render(t)
	if err := os.Remove(firewall.DescriptorPath(e.ws, "filial")); err != nil {
		t.Fatal(err)
	}
	m, err := e.l.model(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.l.Preflight(ctx, m, "feat"); err == nil || !strings.Contains(err.Error(), "unfirewalled") {
		t.Fatalf("got %v", err)
	}
	if err := e.l.Exec(ctx, "feat", true, []string{"true"}); err == nil || !strings.Contains(err.Error(), "unfirewalled") {
		t.Fatalf("exec: got %v", err)
	}
}

func removals(calls [][]string) [][]string {
	var out [][]string
	for _, c := range calls {
		if j := joined(c); strings.HasPrefix(j, "podman rm") || strings.HasPrefix(j, "podman volume rm") {
			out = append(out, c)
		}
	}
	return out
}

func TestDownDefaultsNo(t *testing.T) {
	e := setup(t)
	e.l.TTY, e.l.In = true, strings.NewReader("\n")
	if err := e.l.Down(ctx, "feat", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.out.String(), "Delete 3 volumes of feat? [y/N]") {
		t.Errorf("prompt %q", e.out.String())
	}
	if r := removals(e.calls()); len(r) != 0 {
		t.Fatalf("removed:\n%s", dump(r))
	}
	e.l.TTY = false
	if err := e.l.Down(ctx, "feat", false); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("non-TTY: got %v", err)
	}
}

func TestDownExactNames(t *testing.T) {
	e := setup(t, "master", "api", "api-v2")
	e.l.TTY, e.l.In = true, strings.NewReader("y\n")
	if err := e.l.Down(ctx, "api", false); err != nil {
		t.Fatal(err)
	}
	r := removals(e.calls())
	if len(r) == 0 {
		t.Fatal("nothing removed")
	}
	for _, c := range e.calls() {
		if slices.ContainsFunc(c, func(a string) bool { return strings.Contains(a, "api-v2") || strings.HasPrefix(a, "--filter") }) {
			t.Errorf("touches api-v2 or filters: %q", c)
		}
	}
	for _, c := range r {
		if last := c[len(c)-1]; !strings.HasPrefix(last, "filial-api-") {
			t.Errorf("removes %q", c)
		}
	}
	find(t, r, "podman rm -f --ignore filial-api-rails")
	find(t, r, "podman volume rm -f filial-api-node-modules")
	find(t, e.calls(), "systemctl --user stop filial-api-rails.service")
}

func TestDownKeepsShared(t *testing.T) {
	e := setup(t)
	if err := e.l.Down(ctx, "feat", true); err != nil {
		t.Fatal(err)
	}
	for _, c := range e.calls() {
		if strings.Contains(joined(c), "filial_") {
			t.Errorf("touches a shared object: %q", c)
		}
		if strings.HasPrefix(joined(c), "podman rmi") || strings.HasPrefix(joined(c), "podman image rm") {
			t.Errorf("removes an image: %q", c)
		}
	}
	if len(removals(e.calls())) == 0 {
		t.Fatal("nothing removed")
	}
}

func TestLogsDepsUsesJournal(t *testing.T) {
	e := setup(t)
	if err := e.l.Logs(ctx, "feat", "deps", true); err != nil {
		t.Fatal(err)
	}
	c := find(t, e.calls(), "journalctl")
	if joined(c) != "journalctl --user -u filial-feat-deps.service -f --no-pager" {
		t.Fatalf("argv %q", c)
	}
	e.reset()
	if err := e.l.Logs(ctx, "feat", "db", false); err != nil {
		t.Fatal(err)
	}
	find(t, e.calls(), "podman logs filial-feat-db")
	if err := e.l.Logs(ctx, "feat", "nope", false); err == nil {
		t.Fatal("unknown service: want error")
	}
}

func TestExecRunningUsesExec(t *testing.T) {
	e := setup(t)
	e.render(t)
	e.l.TTY = true
	e.f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}"}, sys.Result{Stdout: []byte("true\n")}, nil)
	if err := e.l.Exec(ctx, "feat", false, []string{"bin/rails", "console"}); err != nil {
		t.Fatal(err)
	}
	c := find(t, e.calls(), "podman exec")
	if joined(c) != "podman exec -t -i -w /app filial-feat-rails bin/rails console" {
		t.Fatalf("argv %q", c)
	}
	if count(e.calls(), "podman --hooks-dir") != 0 {
		t.Fatal("ran a transient container")
	}
}

func TestExecTransientLabels(t *testing.T) {
	e := setup(t)
	e.render(t)
	e.f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}"}, sys.Result{Stdout: []byte("false\n")}, nil)
	if err := e.l.Exec(ctx, "feat", false, []string{"bin/rails", "console"}); err != nil {
		t.Fatal(err)
	}
	c := find(t, e.calls(), "podman --hooks-dir")
	j := joined(c)
	for _, want := range []string{
		"--hooks-dir " + e.ws + "/.railswitch/firewall/hooks.d run --rm -i",
		"--label traefik.enable=false",
		"--annotation filial.firewall=none",
		"--annotation filial.firewall.networks=filial-feat-dev",
		"--network filial-feat-dev",
		"--env-file " + e.ws + "/.railswitch/env/feat/rails.env",
		"--secret filial_twilio_auth_token,type=env,target=TWILIO_AUTH_TOKEN",
		"-v filial-feat-node-modules:/app/node_modules:U",
		"-w /app localhost/filial/base:",
	} {
		if !strings.Contains(j, want) {
			t.Errorf("argv lacks %q:\n%s", want, j)
		}
	}
	if !strings.HasSuffix(j, " bin/rails console") {
		t.Errorf("argv %s", j)
	}
}

func TestExecNoTTYNoT(t *testing.T) {
	e := setup(t)
	e.render(t)
	e.f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}"}, sys.Result{Stdout: []byte("true\n")}, nil)
	if err := e.l.Exec(ctx, "feat", false, []string{"ls"}); err != nil {
		t.Fatal(err)
	}
	if err := e.l.Exec(ctx, "feat", true, []string{"ls"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range e.calls() {
		if slices.Contains(c, "-t") {
			t.Errorf("-t without a TTY: %q", c)
		}
	}
}

func TestUpReloadsUnloadedUnits(t *testing.T) {
	e := setup(t)
	// Units and digests on disk, as after wt add or an earlier failed up.
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	e.reset()
	e.units("LoadState=not-found\nNeedDaemonReload=no\nActiveState=inactive\n")
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, e.calls(),
		"systemctl --user show -p LoadState,NeedDaemonReload,ActiveState filial-feat-rails.service",
		"systemctl --user daemon-reload",
		"systemctl --user start filial-feat-rails.service")
	if n := count(e.calls(), "podman pull"); n != 0 {
		t.Fatalf("render was expected to report no change:\n%s", dump(e.calls()))
	}
}

func TestUpImportFailRemovesVolume(t *testing.T) {
	e := setup(t)
	e.missing("podman", "volume", "exists")
	e.f.On([]string{"podman", "volume", "import"}, sys.Result{ExitCode: 125}, fmt.Errorf("podman: %w (exit 125)", sys.ErrExit))
	if err := e.l.Up(ctx, "feat"); err == nil {
		t.Fatal("want error")
	}
	wantInOrder(t, e.calls(),
		"podman volume create filial-feat-rustfs-data",
		"podman volume import filial-feat-rustfs-data",
		"podman volume rm -f filial-feat-rustfs-data")
	if n := count(e.calls(), "systemctl --user start"); n != 0 {
		t.Fatal("started after a failed import")
	}
}

// A failed start names the units that failed and shows their journal, so
// the user isn't sent to `journalctl -xe` (the system journal).
func TestUpStartFailureNamesFailedUnits(t *testing.T) {
	e := setup(t)
	failStart(e)
	e.f.On([]string{"systemctl", "--user", "show", "-p", "ActiveState,Result"}, sys.Result{Stdout: []byte("ActiveState=inactive\nResult=success\n")}, nil)
	e.f.On([]string{"systemctl", "--user", "show", "-p", "ActiveState,Result", "filial-feat-db.service"}, sys.Result{Stdout: []byte("ActiveState=failed\nResult=exit-code\n")}, nil)
	// The unit journal mixes podman's own event lines (image pull, container
	// create with every label) with the container's output (journald log
	// driver). Only the container's output is worth showing.
	journal := `{"SYSLOG_IDENTIFIER":"podman","MESSAGE":"container create 3e13c99a (image=docker.io/library/postgres:17, traefik.enable=true)"}
{"SYSLOG_IDENTIFIER":"filial-feat-db","MESSAGE":"initdb: error: could not create directory"}
{"SYSLOG_IDENTIFIER":"filial-feat-db","MESSAGE":[98,121,116,101,115]}
{"SYSLOG_IDENTIFIER":"filial-feat-db","MESSAGE":null}
`
	e.f.On([]string{"journalctl", "--user", "-u", "filial-feat-db.service"}, sys.Result{Stdout: []byte(journal)}, nil)
	err := e.l.Up(ctx, "feat")
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "filial-feat-db.service (exit-code)") || !strings.Contains(err.Error(), "journalctl --user -u filial-feat-db.service") {
		t.Fatalf("error does not name the failed unit and its journal: %v", err)
	}
	if strings.Contains(err.Error(), "filial-feat-cache.service") {
		t.Fatalf("error names a unit that did not fail: %v", err)
	}
	stderr := e.l.Err.(*bytes.Buffer).String()
	if !strings.Contains(stderr, "initdb: error: could not create directory\nbytes\n") {
		t.Fatalf("journal of the failed unit not shown; stderr:\n%s", stderr)
	}
	if strings.Contains(stderr, "container create") {
		t.Fatalf("podman event lines shown; stderr:\n%s", stderr)
	}
	c := find(t, e.calls(), "journalctl")
	if joined(c) != "journalctl --user -u filial-feat-db.service -b -n 400 --no-pager -o json --all" {
		t.Fatalf("journalctl argv %q", c)
	}
}

// failStart makes the app's start fail, as when a unit it requires fails.
func failStart(e *tenv) {
	e.f.On([]string{"systemctl", "--user", "start", "filial-feat-rails.service"}, sys.Result{ExitCode: 1, Stderr: []byte("A dependency job for filial-feat-rails.service failed.\n")}, fmt.Errorf("systemctl: %w (exit 1)", sys.ErrExit))
}

// A tool that fails to start (the app only Wants it, D23) is a warning:
// up goes on and the app is served.
func TestUpToolStartFailureWarns(t *testing.T) {
	e := setup(t)
	e.f.On([]string{"systemctl", "--user", "start", "filial-feat-db.service"}, sys.Result{ExitCode: 1}, fmt.Errorf("systemctl: %w (exit 1)", sys.ErrExit))
	e.f.On([]string{"systemctl", "--user", "show", "-p", "ActiveState,Result"}, sys.Result{Stdout: []byte("ActiveState=active\nResult=success\n")}, nil)
	// Restart=on-failure: a failed start leaves the unit activating.
	e.f.On([]string{"systemctl", "--user", "show", "-p", "ActiveState,Result", "filial-feat-browser.service"}, sys.Result{Stdout: []byte("ActiveState=activating\nResult=exit-code\n")}, nil)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatalf("up failed for a tool: %v", err)
	}
	stderr := e.l.Err.(*bytes.Buffer).String()
	if !strings.Contains(stderr, "warning: filial-feat-browser.service did not start: ") || !strings.Contains(stderr, "; journalctl --user -u filial-feat-browser.service\n") {
		t.Fatalf("no warning naming the failed tool; stderr:\n%s", stderr)
	}
	if strings.Contains(stderr, "filial-feat-nvim.service") || strings.Contains(stderr, "filial-feat-db.service") {
		t.Fatalf("warning names a tool that started; stderr:\n%s", stderr)
	}
}

// Long-running containers log to journald, so a removed container's output
// stays in its unit's journal; oneshots pass their output straight through.
func TestUnitsLogToJournald(t *testing.T) {
	e := setup(t)
	e.render(t)
	for _, f := range []string{"filial-feat-storage.container", "filial-feat-rails.container", "filial-feat-agent-probe.container"} {
		u := readUnit(t, e.l.WS.Dirs.Quadlet, f)
		if got := unitValues(u, "LogDriver"); !slices.Equal(got, []string{"journald"}) || len(unitValues(u, "LogOpt")) != 0 {
			t.Errorf("%s: LogDriver %q LogOpt %q; want journald and no LogOpt", f, got, unitValues(u, "LogOpt"))
		}
	}
	if got := unitValues(readUnit(t, e.l.WS.Dirs.Quadlet, "filial-feat-deps.container"), "LogDriver"); !slices.Equal(got, []string{"passthrough"}) {
		t.Errorf("deps LogDriver %q; want passthrough", got)
	}
}

// unitValues are the values of key in the [Container] section of a unit,
// with Quadlet's word quoting stripped.
func unitValues(unit, key string) []string {
	var out []string
	for _, line := range strings.Split(unit, "\n") {
		if v, ok := strings.CutPrefix(line, key+"="); ok {
			out = append(out, strings.Trim(v, `"`))
		}
	}
	return out
}

// flagValues are the values of every flag in argv.
func flagValues(argv []string, flag string) []string {
	var out []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag {
			out = append(out, argv[i+1])
		}
	}
	return out
}

func TestTransientMatchesUnit(t *testing.T) {
	e := setup(t)
	e.render(t)
	m, in, err := e.l.load(ctx, "feat")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"rails", "deps"} {
		svc, err := service(m, s)
		if err != nil {
			t.Fatal(err)
		}
		argv, err := e.l.transient(m, in, svc, "/env", runOpts{}, []string{"true"})
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(e.l.WS.Dirs.Quadlet, "filial-feat-"+s+".container"))
		if err != nil {
			t.Fatal(err)
		}
		unit := string(b)
		nets := flagValues(argv, "--network")
		if !slices.Equal(nets, []string{"filial-feat-dev"}) {
			t.Errorf("%s: networks %q", s, nets)
		}
		var wantAnn []string
		for _, a := range unitValues(unit, "Annotation") {
			if strings.HasPrefix(a, "filial.firewall.networks=") {
				a = "filial.firewall.networks=" + strings.Join(nets, ",") // what the transient one joins
			}
			wantAnn = append(wantAnn, a)
		}
		if got := flagValues(argv, "--annotation"); !slices.Equal(got, wantAnn) {
			t.Errorf("%s: annotations %q, unit %q", s, got, wantAnn)
		}
		if got, want := flagValues(argv, "-v"), unitValues(unit, "Volume"); !slices.Equal(got, want) {
			t.Errorf("%s: mounts %q, unit %q", s, got, want)
		}
		if got, want := flagValues(argv, "--cpus"), unitCPUs(t, unit); len(got) != 1 || got[0] != want {
			t.Errorf("%s: cpus %q, unit %s", s, got, want)
		}
		if got, want := flagValues(argv, "--userns"), unitValues(unit, "UserNS"); !slices.Equal(got, want) {
			t.Errorf("%s: userns %q, unit %q", s, got, want)
		}
		if got, want := flagValues(argv, "--user"), unitValues(unit, "User"); !slices.Equal(got, want) {
			t.Errorf("%s: user %q, unit %q", s, got, want)
		}
		if got, want := flagValues(argv, "--cap-drop"), unitValues(unit, "DropCapability"); !slices.Equal(got, want) {
			t.Errorf("%s: cap-drop %q, unit %q", s, got, want)
		}
		if got, want := flagValues(argv, "--cap-add"), unitValues(unit, "AddCapability"); !slices.Equal(got, want) {
			t.Errorf("%s: cap-add %q, unit %q", s, got, want)
		}
		if argAfter(argv, "--hooks-dir") != strings.TrimPrefix(unitValues(unit, "GlobalArgs")[0], "--hooks-dir=") {
			t.Errorf("%s: hooks dir differs", s)
		}
	}
}

// cancelRunner cancels the run's ctx when podman volume import starts (as
// Ctrl-C would) and, like exec.CommandContext, refuses to start a command
// with a cancelled ctx.
type cancelRunner struct {
	*systest.Fake
	cancel  context.CancelFunc
	removed bool
}

func (r *cancelRunner) Run(ctx context.Context, c sys.Cmd) (sys.Result, error) {
	if err := ctx.Err(); err != nil {
		return sys.Result{}, err
	}
	if len(c.Args) > 1 && c.Args[0] == "volume" && c.Args[1] == "import" {
		r.cancel()
		return sys.Result{}, context.Canceled
	}
	if len(c.Args) > 1 && c.Args[0] == "volume" && c.Args[1] == "rm" {
		r.removed = true
	}
	return r.Fake.Run(ctx, c)
}

func TestUpImportCancelledRemovesVolume(t *testing.T) {
	e := setup(t)
	e.missing("podman", "volume", "exists")
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := &cancelRunner{Fake: e.f, cancel: cancel}
	e.l.R = r
	if err := e.l.Up(cctx, "feat"); err == nil {
		t.Fatal("want error")
	}
	if !r.removed {
		t.Fatalf("unseeded volume not removed after cancellation:\n%s", dump(e.calls()))
	}
}

const loadedClean = "LoadState=loaded\nNeedDaemonReload=no\nActiveState=active\n"

// upTwice runs Up once (units, digests on disk), then resets the calls.
func upTwice(t *testing.T, e *tenv) {
	t.Helper()
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	e.reset()
}

// labels makes every container's unit-hash label inspect answer the hash
// of its rendered unit, except those in stale, which answer "old".
func (e *tenv) labels(t *testing.T, stale ...string) {
	t.Helper()
	dir := e.l.WS.Dirs.Quadlet
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		ctr, ok := strings.CutSuffix(en.Name(), ".container")
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, en.Name()))
		if err != nil {
			t.Fatal(err)
		}
		key := unithash.Key("filial")
		if strings.HasPrefix(ctr, "railswitch-") {
			key = unithash.Key("railswitch")
		}
		h := unithash.Read(data, key)
		if slices.Contains(stale, ctr) {
			h = "old"
		}
		e.f.On([]string{"podman", "container", "inspect", "--format", `{{index .Config.Labels "` + key + `"}}`, ctr}, sys.Result{Stdout: []byte(h + "\n")}, nil)
	}
}

// TestUpStaleLabelRestarted: an active unit whose container was created
// from an older unit (its unit-hash label differs) is restarted, even with
// NeedDaemonReload cleared by another daemon-reload; an inactive one is not.
func TestUpStaleLabelRestarted(t *testing.T) {
	e := setup(t)
	upTwice(t, e)
	e.units(loadedClean)
	e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState=inactive\n", "filial-feat-cache.service")
	e.labels(t, "filial-feat-db", "filial-feat-cache")
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, e.calls(),
		"systemctl --user restart filial-feat-db.service",
		"systemctl --user start filial-feat-rails.service")
	calls := e.calls()
	if n := count(calls, "systemctl --user restart filial-feat-cache") + count(calls, "systemctl --user restart railswitch-traefik") + count(calls, "systemctl --user daemon-reload"); n != 0 {
		t.Fatalf("restarted an inactive unit or traefik, or reloaded:\n%s", dump(calls))
	}
	if r := find(t, calls, "systemctl --user restart"); len(r) != 4 {
		t.Errorf("restart = %q; want only the db", r)
	}
}

// TestUpTraefikStaleLabelRestarted: Traefik running an older unit is
// restarted, with no NeedDaemonReload to tell.
func TestUpTraefikStaleLabelRestarted(t *testing.T) {
	e := setup(t)
	upTwice(t, e)
	e.units(loadedClean)
	e.labels(t, "railswitch-traefik")
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, e.calls(),
		"systemctl --user restart railswitch-traefik.service",
		"systemctl --user start filial-feat-rails.service")
	if n := count(e.calls(), "systemctl --user restart filial-"); n != 0 {
		t.Fatalf("restarted a current unit:\n%s", dump(e.calls()))
	}
}

// TestUpStaleReloadedAndRestarted: a unit loaded stale is reloaded, and
// restarted because its container's label differs.
func TestUpStaleReloadedAndRestarted(t *testing.T) {
	e := setup(t)
	upTwice(t, e)
	e.units(loadedClean)
	e.units("LoadState=loaded\nNeedDaemonReload=yes\nActiveState=active\n", "filial-feat-db.service")
	e.labels(t, "filial-feat-db")
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, e.calls(),
		"systemctl --user daemon-reload",
		"systemctl --user restart filial-feat-db.service",
		"systemctl --user start filial-feat-rails.service")
}

// TestPinBasesDigestRef: a FROM pinned by digest is pulled as is when
// missing, never re-tagged, and nothing is recorded for it.
func TestPinBasesDigestRef(t *testing.T) {
	e := setup(t)
	ref := "docker.io/library/ruby@sha256:bb"
	in := &render.Instance{Builds: []render.BuildStep{{Name: "base", From: []string{ref}}}}
	e.missing("podman", "image", "exists", ref)
	for _, repull := range []bool{false, true} {
		e.reset()
		changed, wrote, err := e.l.pinBases(ctx, in, repull)
		if err != nil || len(changed) != 0 || wrote {
			t.Fatalf("pinBases(repull=%v) = %v, %v, %v", repull, changed, wrote, err)
		}
		calls := e.calls()
		if got := count(calls, "podman pull -q "+ref); got != 1 {
			t.Errorf("repull=%v: pulls of %s = %d; calls:\n%s", repull, ref, got, dump(calls))
		}
		if n := count(calls, "podman tag") + count(calls, "podman image inspect"); n != 0 {
			t.Errorf("repull=%v: tagged or inspected:\n%s", repull, dump(calls))
		}
		if dg := e.l.WS.Lock.BaseDigest(ref); dg != "" {
			t.Errorf("recorded %q for %s", dg, ref)
		}
	}
	e.reset()
	e.f.On([]string{"podman", "image", "exists", ref}, sys.Result{}, nil)
	if _, _, err := e.l.pinBases(ctx, in, false); err != nil {
		t.Fatal(err)
	}
	if n := count(e.calls(), "podman pull"); n != 0 {
		t.Errorf("pulled a present digest ref:\n%s", dump(e.calls()))
	}
}

// TestUpBuildsTagsOfRecordedDigests: the first up records the FROM digests,
// which are part of the build tags (D36): it builds the tags the units run.
func TestUpBuildsTagsOfRecordedDigests(t *testing.T) {
	e := setup(t)
	e.missing("podman", "image", "exists")
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	unit, err := os.ReadFile(filepath.Join(e.l.WS.Dirs.Quadlet, "filial-feat-rails.container"))
	if err != nil {
		t.Fatal(err)
	}
	built := false
	for _, b := range builds(e.calls()) {
		if strings.Contains(string(unit), "Image="+argAfter(b, "-t")+"\n") {
			built = true
		}
	}
	if !built {
		t.Fatalf("no build of the rails unit's image; builds %q\nunit:\n%s", builds(e.calls()), unit)
	}
}

// TestStartTimeoutFromUnits: systemctl start/restart of a worktree's units
// waits up to the sum of their TimeoutStartSec (systemd's 90 s default when
// unset) plus a margin, so a long health start period is covered.
func TestStartTimeoutFromUnits(t *testing.T) {
	e := setup(t)
	upTwice(t, e)
	e.units(loadedClean)
	e.labels(t, "filial-feat-db")
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	total := 0
	entries, err := os.ReadDir(e.l.WS.Dirs.Quadlet)
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		if !strings.HasPrefix(en.Name(), "filial-feat-") || !strings.HasSuffix(en.Name(), ".container") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(e.l.WS.Dirs.Quadlet, en.Name()))
		if err != nil {
			t.Fatal(err)
		}
		n := 90
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(line, "TimeoutStartSec="); ok {
				if n, err = strconv.Atoi(v); err != nil {
					t.Fatal(err)
				}
			}
		}
		total += n
	}
	want := time.Duration(total)*time.Second + 2*time.Minute
	if want < 20*time.Minute {
		t.Fatalf("fixture total %v is too small to tell", want)
	}
	for i, c := range e.f.Calls {
		if j := joined(c); strings.HasPrefix(j, "systemctl --user start") || strings.HasPrefix(j, "systemctl --user restart") {
			if got := e.f.Cmds[i].Timeout; got != want {
				t.Errorf("%s: Timeout = %v; want %v", j, got, want)
			}
		}
	}
}

// networks scripts `podman network ls --format json`.
func (e *tenv) networks(json string) {
	e.f.On([]string{"podman", "network", "ls", "--format", "json"}, sys.Result{Stdout: []byte(json)}, nil)
}

func (e *tenv) subnet(t *testing.T) int {
	t.Helper()
	reg, err := machine.Open(e.l.WS.Dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	pe, _ := reg.Project("filial")
	return pe.Subnet
}

// TestUpMovesClashingSubnet: another tool's network on the project's /24,
// and no filial_proxy yet: up moves the project before rendering.
func TestUpMovesClashingSubnet(t *testing.T) {
	e := setup(t)
	e.networks(`[{"name":"podman","subnets":[{"subnet":"10.88.0.0/16"}]},{"name":"old_proxy","subnets":[{"subnet":"10.214.0.0/24","gateway":"10.214.0.1"}]}]`)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	if n := e.subnet(t); n != 1 {
		t.Fatalf("subnet = %d; want 1", n)
	}
	if want := "proxy subnet 10.214.0.0/24 is used by podman network old_proxy; project filial moved to 10.214.1.0/24\n"; !strings.Contains(e.out.String(), want) {
		t.Fatalf("out:\n%s\nwant %q", e.out, want)
	}
	wantInOrder(t, e.calls(), "podman network ls --format json", "podman info")
	net := readUnit(t, e.l.WS.Dirs.Quadlet, "filial-proxy.network")
	for _, w := range []string{"Subnet=10.214.1.0/24", "Gateway=10.214.1.1", "IPRange=10.214.1.128/25"} {
		if !strings.Contains(net, w) {
			t.Errorf("filial-proxy.network lacks %s:\n%s", w, net)
		}
	}
	if tr := readUnit(t, e.l.WS.Dirs.Quadlet, "railswitch-traefik.container"); !strings.Contains(tr, "ip=10.214.1.2\n") || strings.Contains(tr, "ip=10.214.0.2\n") {
		t.Errorf("traefik unit keeps the old IP:\n%s", tr)
	}
}

// TestUpSubnetNoClash: no clash, or filial_proxy already exists on the
// registered subnet: up leaves the registry alone.
func TestUpSubnetNoClash(t *testing.T) {
	for _, nets := range []string{
		``,
		`[{"name":"podman","subnets":[{"subnet":"10.88.0.0/16"}]}]`,
		`[{"name":"filial_proxy","subnets":[{"subnet":"10.214.0.0/24"}]},{"name":"old_proxy","subnets":[{"subnet":"10.214.0.0/24"}]}]`,
	} {
		e := setup(t)
		e.networks(nets)
		if err := e.l.Up(ctx, "feat"); err != nil {
			t.Fatalf("%s: %v", nets, err)
		}
		if n := e.subnet(t); n != 0 {
			t.Errorf("%s: subnet = %d; want 0", nets, n)
		}
		if strings.Contains(e.out.String(), "proxy subnet") {
			t.Errorf("%s: out:\n%s", nets, e.out)
		}
	}
}

// TestUpProxyNetworkOtherSubnet: filial_proxy exists on another subnet
// than the registered one: up refuses before rendering.
func TestUpProxyNetworkOtherSubnet(t *testing.T) {
	e := setup(t)
	e.networks(`[{"name":"filial_proxy","subnets":[{"subnet":"10.214.7.0/24"}]}]`)
	err := e.l.Up(ctx, "feat")
	if err == nil {
		t.Fatal("want error")
	}
	for _, w := range []string{"10.214.7.0/24", "10.214.0.0/24", "railswitch down", "podman network rm filial_proxy"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error lacks %q: %v", w, err)
		}
	}
	if slices.ContainsFunc(e.calls(), func(c []string) bool { return joined(c) == "podman info --format {{.Store.GraphRoot}}" }) {
		t.Errorf("rendered after the refusal:\n%s", dump(e.calls()))
	}
}

// TestUpStartFailureNamesProxyNetwork: the project's proxy network unit is
// outside the worktree's units, but its failure is what blocks the start.
func TestUpStartFailureNamesProxyNetwork(t *testing.T) {
	e := setup(t)
	e.f.On([]string{"systemctl", "--user", "start"}, sys.Result{ExitCode: 1}, fmt.Errorf("systemctl: %w (exit 1)", sys.ErrExit))
	e.f.On([]string{"systemctl", "--user", "show", "-p", "ActiveState,Result"}, sys.Result{Stdout: []byte("ActiveState=inactive\nResult=success\n")}, nil)
	e.f.On([]string{"systemctl", "--user", "show", "-p", "ActiveState,Result", "filial-proxy-network.service"}, sys.Result{Stdout: []byte("ActiveState=failed\nResult=exit-code\n")}, nil)
	err := e.l.Up(ctx, "feat")
	if err == nil || !strings.Contains(err.Error(), "filial-proxy-network.service (exit-code)") {
		t.Fatalf("error does not name the failed proxy network unit: %v", err)
	}
	e = setup(t)
	e.f.On([]string{"systemctl", "--user", "start"}, sys.Result{ExitCode: 1}, fmt.Errorf("systemctl: %w (exit 1)", sys.ErrExit))
	e.f.On([]string{"systemctl", "--user", "show", "-p", "ActiveState,Result"}, sys.Result{Stdout: []byte("ActiveState=inactive\nResult=success\n")}, nil)
	e.f.On([]string{"systemctl", "--user", "show", "-p", "ActiveState,Result", "railswitch-traefik.service"}, sys.Result{Stdout: []byte("ActiveState=failed\nResult=exit-code\n")}, nil)
	if err := e.l.Up(ctx, "feat"); err == nil || !strings.Contains(err.Error(), "railswitch-traefik.service (exit-code)") {
		t.Fatalf("error does not name the failed traefik unit: %v", err)
	}
}

func readUnit(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestUpWarnsNotIsolated (final I2): up warns, without refusing, about this
// project's networks that lack isolate=strict and names the procedure;
// another project's network is not this up's business.
func TestUpWarnsNotIsolated(t *testing.T) {
	e := setup(t)
	e.f.On([]string{"podman", "network", "inspect", "filial_proxy"}, sys.Result{Stdout: []byte(`[{"name": "filial_proxy", "options": {"mtu": "1400"}}]`)}, nil)
	e.f.On([]string{"podman", "network", "inspect", "filial-feat-dev"}, sys.Result{Stdout: []byte(`[{"name": "filial-feat-dev", "options": {"isolate": "strict"}}]`)}, nil)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	w := e.errOut()
	if !strings.Contains(w, "WARNING") || !strings.Contains(w, "filial_proxy") || strings.Contains(w, "filial-feat-dev") || !strings.Contains(w, "podman network rm") {
		t.Errorf("stderr %q; want a WARNING naming filial_proxy and the recreate procedure", w)
	}
	for _, c := range e.f.Calls {
		if joined(c) == "podman network inspect railswitch_machine" {
			t.Error("up inspected the machine network; only this project's are warned about")
		}
	}
}

// TestUpIsolatedNoWarning: all isolated (or not created yet): no warning.
func TestUpIsolatedNoWarning(t *testing.T) {
	e := setup(t)
	e.f.On([]string{"podman", "network", "inspect", "filial_proxy"}, sys.Result{Stdout: []byte(`[{"name": "filial_proxy", "options": {"isolate": "strict"}}]`)}, nil)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	if w := e.errOut(); strings.Contains(w, "WARNING") {
		t.Errorf("stderr %q", w)
	}
}

// TestUpDozzleStaleLabelRestarted (final M6): Dozzle running an older unit
// is restarted, like Traefik.
func TestUpDozzleStaleLabelRestarted(t *testing.T) {
	e := setup(t)
	upTwice(t, e)
	e.units(loadedClean)
	e.labels(t, "railswitch-dozzle")
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	wantInOrder(t, e.calls(),
		"systemctl --user restart railswitch-dozzle.service",
		"systemctl --user start filial-feat-rails.service")
	if n := count(e.calls(), "systemctl --user restart filial-") + count(e.calls(), "systemctl --user restart railswitch-traefik"); n != 0 {
		t.Fatalf("restarted a current unit:\n%s", dump(e.calls()))
	}
}

// TestUpRestartsStaleDashboard: an active dashboard whose unit file is
// newer than its start is restarted; one started after the file was
// written, or not active, is not.
func TestUpRestartsStaleDashboard(t *testing.T) {
	for _, c := range []struct {
		name    string
		active  string
		started time.Duration // ActiveEnterTimestamp relative to the file's mtime
		restart bool
	}{{"stale", "active", -time.Hour, true}, {"current", "active", time.Hour, false}, {"inactive", "inactive", -time.Hour, false}} {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t)
			upTwice(t, e)
			e.units(loadedClean)
			e.units("LoadState=loaded\nNeedDaemonReload=no\nActiveState="+c.active+"\n", machine.DashboardService)
			mtime := time.Date(2026, 10, 27, 10, 0, 0, 0, time.Local)
			if err := os.Chtimes(filepath.Join(e.l.WS.Dirs.SystemdUser, machine.DashboardService), mtime, mtime); err != nil {
				t.Fatal(err)
			}
			ts := mtime.Add(c.started).Format("Mon 2006-01-02 15:04:05 MST")
			e.f.On([]string{"systemctl", "--user", "show", "-p", "ActiveEnterTimestamp", machine.DashboardService},
				sys.Result{Stdout: []byte("ActiveEnterTimestamp=" + ts + "\n")}, nil)
			if err := e.l.Up(ctx, "feat"); err != nil {
				t.Fatal(err)
			}
			n := count(e.calls(), "systemctl --user restart railswitch-dashboard.service")
			if c.restart {
				wantInOrder(t, e.calls(),
					"systemctl --user restart railswitch-dashboard.service",
					"systemctl --user start filial-feat-rails.service")
			}
			if want := map[bool]int{true: 1, false: 0}[c.restart]; n != want {
				t.Errorf("dashboard restarts %d; want %d:\n%s", n, want, dump(e.calls()))
			}
		})
	}
}

// TestUpShowsProgress: a build streams podman's own output as it runs, and
// every pull is announced, so a long first `up` never sits silent.
func TestUpShowsProgress(t *testing.T) {
	e := setup(t)
	e.missing("podman", "image", "exists")
	e.missing("podman", "volume", "exists")
	e.f.On([]string{"podman", "build"}, sys.Result{Stdout: []byte("STEP 1/9: FROM docker.io/library/ruby\n")}, nil)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	out := e.out.String()
	for _, want := range []string{
		"pulling docker.io/library/postgres:17\n",
		"pulling " + ruby + "\n",
		"building base (",
		"STEP 1/9: FROM docker.io/library/ruby\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("up output lacks %q:\n%s", want, out)
		}
	}
	for _, c := range e.f.Cmds {
		if len(c.Args) > 0 && c.Args[0] == "build" && (c.Stdout == nil || c.Stderr == nil) {
			t.Errorf("podman build output is captured, not streamed: %q", c.Args)
		}
	}
}
