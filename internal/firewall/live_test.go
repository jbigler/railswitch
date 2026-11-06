//go:build integration

package firewall

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
)

// TestLiveDNSFilterEnds runs a container through podman with the hook
// descriptors render writes, the DNS filter on, under the host's OCI
// runtime: the filter must start, and be gone once the container is.
// Under crun the hook's processes start with SIGTERM blocked, so a filter
// ended with a plain kill outlived its container and hung oneshot units
// (D182); runc blocks nothing, so run this under crun.
func TestLiveDNSFilterEnds(t *testing.T) {
	skip := t.Skipf
	if os.Getenv("RAILSWITCH_LIVE_REQUIRED") != "" { // CI: a missing tool is a broken job, not a pass
		skip = t.Fatalf
	}
	for _, tool := range []string{"podman", "dnsmasq", "nft", "nsenter", "pgrep"} {
		if _, err := exec.LookPath(tool); err != nil {
			skip("needs %s", tool)
		}
	}
	if out, _ := exec.Command("dnsmasq", "--version").Output(); !strings.Contains(string(out), " nftset") {
		skip("needs dnsmasq built with nftset")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	podman := func(args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, "podman", args...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if rt, _ := podman("info", "--format", "{{.Host.OCIRuntime.Name}}"); rt != "crun" {
		skip("OCI runtime %q: the case this tests needs crun", rt)
	}

	prefix := "rsit" + strconv.Itoa(os.Getpid()%100000)
	net := prefix + "-dev"
	if out, err := podman("network", "create", net); err != nil {
		t.Fatalf("network create: %v\n%s", err, out)
	}
	t.Cleanup(func() { _, _ = exec.Command("podman", "network", "rm", "-f", net).CombinedOutput() })
	// netavark's config dir: <graph root>/networks rootless, /etc/containers/networks as root.
	graph, _ := podman("info", "--format", "{{.Store.GraphRoot}}")
	netDir := ""
	for _, d := range []string{filepath.Join(graph, "networks"), "/etc/containers/networks"} {
		if _, err := os.Stat(filepath.Join(d, net+".json")); err == nil {
			netDir = d
		}
	}
	if netDir == "" {
		t.Fatalf("no config for network %s", net)
	}

	ws := t.TempDir()
	files, err := Files(prefix, ws, netDir, &pack.Stack{Pack: &pack.Pack{Name: "live", Files: fstest.MapFS{}}}, config.Firewall{})
	if err != nil {
		t.Fatal(err)
	}
	for rel, f := range files {
		p := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, f.Data, f.Mode); err != nil {
			t.Fatal(err)
		}
	}
	fwDir := filepath.Join(ws, filepath.FromSlash(Dir))

	out, err := podman("run", "--rm", "--hooks-dir", filepath.Join(fwDir, "hooks.d"), "--network", net,
		"--annotation", prefix+".firewall=none", "--annotation", prefix+".firewall.mode=enforce",
		"--annotation", prefix+".firewall.networks="+net, "--annotation", prefix+".firewall.dns=filter",
		"docker.io/library/alpine:3.20", "true")
	if err != nil {
		t.Fatalf("podman run: %v\n%s", err, out)
	}
	if log, _ := os.ReadFile(filepath.Join(fwDir, "hook.log")); !strings.Contains(string(log), "dns=filter") {
		t.Fatalf("the hook did not start a DNS filter:\n%s", log)
	}
	// Within a few seconds of the container, no dnsmasq of this workspace runs.
	for end := time.Now().Add(5 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		left, _ := exec.Command("pgrep", "-f", "dnsmasq --conf-file="+fwDir).Output()
		if len(strings.TrimSpace(string(left))) == 0 {
			break
		}
		if time.Now().After(end) {
			_ = exec.Command("pkill", "-KILL", "-f", "dnsmasq --conf-file="+fwDir).Run()
			t.Fatalf("the DNS filter outlived its container: pids %s", strings.Fields(string(left)))
		}
	}
}
