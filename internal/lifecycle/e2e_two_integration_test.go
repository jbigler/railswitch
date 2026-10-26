//go:build integration

package lifecycle_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/tailscale"
)

// projectRmTimeout bounds one `project rm`: it stops every worktree of the
// project. twoProjectsReserve keeps both of them, plus a minute, back from
// the test deadline for cleanup.
const (
	projectRmTimeout   = 5 * time.Minute
	twoProjectsReserve = 2*projectRmTimeout + time.Minute
)

var e2ePrefixRe = regexp.MustCompile(`^[a-z][a-z0-9]{1,15}$`)

// machineJSON is the part of `railswitch machine --json` the test reads
// (docs/json.md).
type machineJSON struct {
	Projects []struct {
		Prefix    string `json:"prefix"`
		Subnet    string `json:"subnet"`
		TraefikIP string `json:"traefik_ip"`
		Worktrees []struct {
			Slug  string         `json:"slug"`
			ID    int            `json:"id"`
			Ports map[string]int `json:"ports"`
		} `json:"worktrees"`
	} `json:"projects"`
	Routes []struct {
		Host    string `json:"host"`
		Project string `json:"project"`
	} `json:"routes"`
}

// e2eProject is one of the two projects under test.
type e2eProject struct {
	prefix, ws, slug string
	host             string // the base worktree's app host
	rails            string // the base worktree's rails container
	traefikIP        string
	subnet           string
	s3Host           string
	removed          bool
}

// TestE2ETwoProjects is the Stage 3 acceptance run (docs/stage3-acceptance.md)
// against real podman and systemd: two projects from the same repo, prefixes
// $RAILSWITCH_E2E_PREFIX and ${RAILSWITCH_E2E_PREFIX}b, are up at once; the
// host reaches both, neither reaches the other except through its own
// Traefik (and there not the other's hosts), and `project rm` of both leaves
// nothing of either.
//
//	RAILSWITCH_E2E_REPO=/tmp/rs-demo/demo RAILSWITCH_E2E_PREFIX=s3a \
//	  go test -tags integration ./internal/lifecycle/ -run E2ETwoProjects -timeout 60m
func TestE2ETwoProjects(t *testing.T) {
	repo := os.Getenv("RAILSWITCH_E2E_REPO")
	if repo == "" {
		t.Skip("RAILSWITCH_E2E_REPO is not set")
	}
	base := os.Getenv("RAILSWITCH_E2E_PREFIX")
	if base == "" {
		base = "s3a"
	}
	for _, p := range []string{base, base + "b"} {
		if !e2ePrefixRe.MatchString(p) {
			t.Fatalf("prefix %q must match %s (RAILSWITCH_E2E_PREFIX is at most 14 characters)", p, e2ePrefixRe)
		}
	}
	ctx := context.Background()
	if d, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, d.Add(-twoProjectsReserve))
		t.Cleanup(cancel)
	}
	bin := buildBinary(t)
	parent, err := os.MkdirTemp("", "rs-e2e2-")
	if err != nil {
		t.Fatal(err)
	}
	quadletDir := filepath.Join(os.Getenv("HOME"), ".config", "containers", "systemd")
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		quadletDir = filepath.Join(x, "containers", "systemd")
	}

	rs := func(t *testing.T, timeout time.Duration, args ...string) string {
		t.Helper()
		out, errOut, err := railswitch(ctx, bin, timeout, args...)
		if err != nil {
			t.Fatalf("railswitch %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, out, errOut)
		}
		return out
	}

	// new + up of both base worktrees.
	a := &e2eProject{prefix: base}
	b := &e2eProject{prefix: base + "b"}
	projects := []*e2eProject{a, b}
	for _, p := range projects {
		p.ws = filepath.Join(parent, "rs-"+p.prefix)
		t.Logf("workspace %s", p.ws)
		// Storage is "none" by default in the rails pack; the s3. checks
		// (D49) need the rustfs option.
		rs(t, 5*time.Minute, "project", "new", p.ws, "--repo", repo, "--prefix", p.prefix, "--option", "storage=rustfs@1")
		t.Cleanup(func() {
			if p.removed {
				return
			}
			if out, errOut, err := railswitch(context.Background(), bin, projectRmTimeout, "project", "rm", p.prefix, "--yes", "--purge"); err != nil {
				t.Logf("cleanup: railswitch project rm %s: %v\n%s%s", p.prefix, err, out, errOut)
			}
		})
		wts := worktrees(t, rs(t, time.Minute, "-C", p.ws, "--json", "wt", "ls"))
		if len(wts) != 1 {
			t.Fatalf("%s after new: want 1 worktree, got %+v", p.prefix, wts)
		}
		p.slug = wts[0].Slug
		p.host = p.slug + "." + p.prefix + ".localhost"
		p.rails = naming.Container(p.prefix, p.slug, "rails")
		url := "http://" + p.host + "/"
		if out := rs(t, 10*time.Minute, "-C", p.ws, "-w", p.slug, "up"); lastLine(out) != url+" ready" {
			t.Fatalf("%s: up did not report the route ready; output:\n%s", p.prefix, out)
		}
	}

	// machine --json: disjoint IDs, ports, subnets, Traefik IPs and hosts.
	var m machineJSON
	if out := rs(t, time.Minute, "--json", "machine"); json.Unmarshal([]byte(out), &m) != nil {
		t.Fatalf("machine --json does not parse:\n%s", out)
	}
	ids, ports, subnets, traefiks, hosts := map[int]string{}, map[int]string{}, map[string]string{}, map[string]string{}, map[string]string{}
	disjoint := func(what string, seen map[string]string, key, owner string) {
		if prev, ok := seen[key]; ok && prev != owner {
			t.Errorf("machine --json: %s %s belongs to both %s and %s", what, key, prev, owner)
		}
		seen[key] = owner
	}
	for _, mp := range m.Projects {
		disjoint("subnet", subnets, mp.Subnet, mp.Prefix)
		disjoint("Traefik IP", traefiks, mp.TraefikIP, mp.Prefix)
		for _, w := range mp.Worktrees {
			owner := mp.Prefix + "/" + w.Slug
			if prev, ok := ids[w.ID]; ok {
				t.Errorf("machine --json: ID %d belongs to both %s and %s", w.ID, prev, owner)
			}
			ids[w.ID] = owner
			for name, port := range w.Ports {
				if prev, ok := ports[port]; ok {
					t.Errorf("machine --json: port %d belongs to both %s and %s (%s)", port, prev, owner, name)
				}
				ports[port] = owner
			}
		}
		for _, p := range projects {
			if p.prefix == mp.Prefix {
				p.subnet, p.traefikIP = mp.Subnet, mp.TraefikIP
			}
		}
	}
	for _, r := range m.Routes {
		if prev, ok := hosts[r.Host]; ok {
			t.Errorf("machine --json: host %s routed by both %s and %s", r.Host, prev, r.Project)
		}
		hosts[r.Host] = r.Project
		for _, p := range projects {
			if r.Project == p.prefix && strings.HasPrefix(r.Host, "s3.") && strings.HasSuffix(r.Host, "."+p.host) {
				p.s3Host = r.Host
			}
		}
	}
	for _, p := range projects {
		if p.traefikIP == "" || p.subnet == "" {
			t.Fatalf("machine --json lists no subnet/Traefik IP for %s", p.prefix)
		}
		if p.s3Host == "" {
			t.Fatalf("machine --json lists no s3. route for %s", p.host)
		}
	}

	// The host reaches both apps, both s3. hosts, logs and the Traefik API.
	for _, p := range projects {
		if code := httpStatus(ctx, t, "http://"+p.host+"/"); code != "200" {
			t.Errorf("host GET http://%s/: status %s, want 200", p.host, code)
		}
		if code := httpStatus(ctx, t, "http://"+p.s3Host+"/"); code == "000" || code == "404" {
			t.Errorf("host GET http://%s/: status %s, want an answer from storage", p.s3Host, code)
		}
		// Each project's own rails container reaches its own app.
		if code, exit := ctrStatus(ctx, t, p.rails, p.host, "http://"+p.traefikIP+"/"); code != "200" {
			t.Errorf("%s GET %s via %s: status %q (curl exit %d), want 200", p.rails, p.host, p.traefikIP, code, exit)
		}
		// D49: its rails container reaches its own s3. host by name, through
		// Traefik (the AddHost of the s3. host), as the app's presigned URLs do.
		if code, exit := ctrStatus(ctx, t, p.rails, "", "http://"+p.s3Host+"/"); code == "000" || code == "404" {
			t.Errorf("%s GET http://%s/: status %q (curl exit %d), want an answer from storage", p.rails, p.s3Host, code, exit)
		}
	}
	// logs.localhost and traefik.localhost: the host only (host clause).
	for _, c := range []struct{ host, path string }{{"traefik.localhost", "/api/rawdata"}, {"logs.localhost", "/"}} {
		if code := hostStatus(ctx, t, c.host, "http://127.0.0.1"+c.path); code != "200" {
			t.Errorf("host GET %s%s: status %s, want 200", c.host, c.path, code)
		}
		if code, exit := ctrStatus(ctx, t, a.rails, c.host, "http://"+a.traefikIP+c.path); code == "200" {
			t.Errorf("%s GET %s%s via its own Traefik %s: status 200 (curl exit %d), want refused", a.rails, c.host, c.path, a.traefikIP, exit)
		}
	}

	// A's container through A's Traefik: B's host is refused.
	if code, exit := ctrStatus(ctx, t, a.rails, b.host, "http://"+a.traefikIP+"/"); code != "404" {
		t.Errorf("%s GET %s via %s: status %q (curl exit %d), want 404", a.rails, b.host, a.traefikIP, code, exit)
	}
	// isolate=strict: no direct path to B's rails, B's Traefik or Dozzle.
	bRailsIP := strings.TrimSpace(podman(ctx, t, "inspect", b.rails, "--format",
		`{{(index .NetworkSettings.Networks "`+naming.ProxyNetwork(b.prefix)+`").IPAddress}}`))
	if bRailsIP == "" {
		t.Fatalf("%s has no address on %s", b.rails, naming.ProxyNetwork(b.prefix))
	}
	for _, target := range []string{"http://" + bRailsIP + ":3000/", "http://" + b.traefikIP + "/", "http://10.214.255.3:8080/"} {
		if code, exit := ctrStatus(ctx, t, a.rails, "", target); exit != 7 && exit != 28 {
			t.Errorf("%s GET %s: status %q, curl exit %d; want a connection failure (exit 7 or 28)", a.rails, target, code, exit)
		}
	}

	// share A's base worktree, when tailscale is up.
	if _, _, err := run(ctx, 30*time.Second, "tailscale", "status"); err != nil {
		t.Logf("tailscale status fails (%v): skipping the share check", err)
	} else {
		shareCheck(ctx, t, rs, a, m)
	}

	// project rm A: B's objects and Traefik stay; then project rm B.
	before := map[string][]string{}
	for _, p := range projects {
		before[p.prefix] = ownedObjects(ctx, t, quadletDir, p.prefix)
		if len(before[p.prefix]) == 0 {
			t.Fatalf("no object of %s exists before project rm; the teardown check would prove nothing", p.prefix)
		}
	}
	for i, p := range projects {
		rs(t, projectRmTimeout, "project", "rm", p.prefix, "--yes", "--purge")
		p.removed = true
		if left := ownedObjects(ctx, t, quadletDir, p.prefix); len(left) > 0 {
			t.Errorf("after project rm %s: still there: %v", p.prefix, left)
		}
		if i == 0 {
			now := ownedObjects(ctx, t, quadletDir, b.prefix)
			for _, o := range before[b.prefix] {
				if !slices.Contains(now, o) {
					t.Errorf("project rm %s removed %s of %s", a.prefix, o, b.prefix)
				}
			}
			if running := lines(podman(ctx, t, "ps", "--format", "{{.Names}}")); !slices.Contains(running, "railswitch-traefik") {
				t.Errorf("railswitch-traefik is not running after project rm %s; running: %v", a.prefix, running)
			}
			if code := httpStatus(ctx, t, "http://"+b.host+"/"); code != "200" {
				t.Errorf("after project rm %s: GET http://%s/: status %s, want 200", a.prefix, b.host, code)
			}
		}
	}
	for _, o := range append(before[a.prefix], before[b.prefix]...) {
		if slices.Contains(allObjects(ctx, t, quadletDir), o) {
			t.Errorf("after project rm of both: %s still exists", o)
		}
	}
}

// shareCheck shares p's base worktree and checks that the serve config
// gained only p's own ports, each proxied to its loopback port, and that
// unshare restores it.
func shareCheck(ctx context.Context, t *testing.T, rs func(*testing.T, time.Duration, ...string) string, p *e2eProject, m machineJSON) {
	t.Helper()
	ts := tailscale.T{R: sys.Exec{}}
	before, err := ts.Served(ctx)
	if err != nil {
		t.Fatalf("tailscale serve status: %v", err)
	}
	var own []int
	for _, mp := range m.Projects {
		if mp.Prefix != p.prefix {
			continue
		}
		for _, w := range mp.Worktrees {
			if w.Slug == p.slug {
				own = slices.Collect(maps.Values(w.Ports))
			}
		}
	}
	rs(t, 2*time.Minute, "-C", p.ws, "-w", p.slug, "share")
	after, err := ts.Served(ctx)
	if err != nil {
		t.Fatalf("tailscale serve status: %v", err)
	}
	added := 0
	for port, target := range after {
		if prev, ok := before[port]; ok {
			if prev != target {
				t.Errorf("share changed tailnet port %d: %s → %s", port, prev, target)
			}
			continue
		}
		added++
		if !slices.Contains(own, port) || target != tailscale.Target(port) {
			t.Errorf("share added tailnet port %d → %s; want only %s's ports %v, each to its own loopback port", port, target, p.slug, own)
		}
	}
	for port := range before {
		if _, ok := after[port]; !ok {
			t.Errorf("share removed tailnet port %d", port)
		}
	}
	if added == 0 {
		t.Errorf("share added no tailnet port; serve config %v", after)
	}
	rs(t, 2*time.Minute, "-C", p.ws, "-w", p.slug, "unshare")
	if again, err := ts.Served(ctx); err != nil {
		t.Errorf("tailscale serve status: %v", err)
	} else if !maps.Equal(before, again) {
		t.Errorf("after unshare the serve config is %v, want %v as before share", again, before)
	}
}

// ctrStatus runs curl in container against url (with Host: host unless
// host is ""), --max-time 5, and returns the status code and curl's exit
// code (0 on success).
func ctrStatus(ctx context.Context, t *testing.T, container, host, url string) (string, int) {
	t.Helper()
	args := []string{"exec", container, "curl", "-s", "--max-time", "5", "-o", "/dev/null", "-w", "%{http_code}"}
	if host != "" {
		args = append(args, "-H", "Host: "+host)
	}
	out, errOut, err := run(ctx, 30*time.Second, "podman", append(args, url)...)
	var ee *exec.ExitError
	switch {
	case err == nil:
		return out, 0
	case errors.As(err, &ee) && ee.ExitCode() != 125 && ee.ExitCode() != 126 && ee.ExitCode() != 127:
		return out, ee.ExitCode()
	}
	t.Fatalf("podman %s: %v\n%s", strings.Join(append(args, url), " "), err, errOut)
	return "", 0
}

// hostStatus GETs url from the host with Host: host.
func hostStatus(ctx context.Context, t *testing.T, host, url string) string {
	t.Helper()
	out, errOut, err := run(ctx, time.Minute, "curl", "-sS", "--max-time", "30", "-o", "/dev/null", "-w", "%{http_code}", "-H", "Host: "+host, url)
	if err != nil {
		t.Fatalf("curl -H 'Host: %s' %s: %v (status %q)\n%s", host, url, err, out, errOut)
	}
	return out
}

// allObjects lists every container, volume and network podman knows, and
// every unit file in quadletDir, each as "<kind> <name>".
func allObjects(ctx context.Context, t *testing.T, quadletDir string) []string {
	t.Helper()
	var objs []string
	for kind, args := range map[string][]string{
		"container": {"ps", "-a", "--format", "{{.Names}}"},
		"volume":    {"volume", "ls", "--format", "{{.Name}}"},
		"network":   {"network", "ls", "--format", "{{.Name}}"},
	} {
		for _, n := range lines(podman(ctx, t, args...)) {
			objs = append(objs, kind+" "+n)
		}
	}
	ents, err := os.ReadDir(quadletDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, e := range ents {
		objs = append(objs, "unit "+e.Name())
	}
	return objs
}

// ownedObjects is the part of allObjects named for prefix: "<prefix>-…" or
// "<prefix>_…". The separator keeps "s3a" from matching "s3ab-…".
func ownedObjects(ctx context.Context, t *testing.T, quadletDir, prefix string) []string {
	t.Helper()
	var own []string
	for _, o := range allObjects(ctx, t, quadletDir) {
		_, name, _ := strings.Cut(o, " ")
		if strings.HasPrefix(name, prefix+"-") || strings.HasPrefix(name, prefix+"_") {
			own = append(own, o)
		}
	}
	return own
}
