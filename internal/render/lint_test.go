package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lintFiles reads the named files from testdata/lint, keyed by base name.
func lintFiles(t *testing.T, names ...string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join("testdata", "lint", n))
		if err != nil {
			t.Fatal(err)
		}
		files[n] = data
	}
	return files
}

// wantOnly asserts that findings is non-empty and every finding has rule.
func wantOnly(t *testing.T, findings []Finding, rule string) {
	t.Helper()
	if len(findings) == 0 {
		t.Fatalf("no finding, want %s", rule)
	}
	for _, f := range findings {
		if f.Rule != rule {
			t.Errorf("finding %+v, want only rule %s", f, rule)
		}
		if f.File == "" || f.Line < 1 || f.Msg == "" {
			t.Errorf("finding %+v lacks file, line or message", f)
		}
	}
}

func TestLintSingleDollar(t *testing.T) {
	wantOnly(t, Lint(lintFiles(t, "single-dollar.container")), "single-dollar")
}

func TestLintUnquotedSpace(t *testing.T) {
	wantOnly(t, Lint(lintFiles(t, "unquoted-space.container")), "unquoted-space")
}

func TestLintNetworkDependency(t *testing.T) {
	// Network=demo_proxy (demo-proxy.network's NetworkName) with only After=.
	wantOnly(t, Lint(lintFiles(t, "network-dependency.container", "demo-main-dev.network", "demo-proxy.network")), "network-dependency")
	// A bare name that no .network unit creates.
	wantOnly(t, Lint(lintFiles(t, "network-dependency-ok.container", "demo-main-dev.network")), "network-dependency")
	// Requires= and After= both name the creating unit's service.
	if fs := Lint(lintFiles(t, "network-dependency-ok.container", "demo-main-dev.network", "demo-proxy.network")); len(fs) != 0 {
		t.Errorf("network-dependency-ok.container: %+v", fs)
	}
}

func TestLintLoopbackPublish(t *testing.T) {
	wantOnly(t, Lint(lintFiles(t, "loopback-publish.container")), "loopback-publish")
}

func TestLintFirewallNetworks(t *testing.T) {
	// The annotation omits demo_proxy, the NetworkName of demo-proxy.network.
	wantOnly(t, Lint(lintFiles(t, "firewall-networks.container", "demo-main-dev.network", "demo-proxy.network")), "firewall-networks")
	// --hooks-dir without the profile annotation, and vice versa.
	wantOnly(t, Lint(lintFiles(t, "firewall-hooks.container", "demo-main-dev.network")), "firewall-networks")
	wantOnly(t, Lint(lintFiles(t, "firewall-profile.container", "demo-main-dev.network")), "firewall-networks")
}

func TestLintSharedUnderscore(t *testing.T) {
	wantOnly(t, Lint(lintFiles(t, "shared-underscore.container")), "shared-underscore")
}

func TestLintDepsRemain(t *testing.T) {
	wantOnly(t, Lint(lintFiles(t, "deps-remain.container")), "deps-remain")
}

func TestLintMachineMarker(t *testing.T) {
	files := lintFiles(t, "machine-ok.container")
	if fs := Lint(files); len(fs) != 0 {
		t.Fatalf("machine-ok.container: %+v", fs)
	}
	files["machine-ok.container"] = []byte(strings.Replace(string(files["machine-ok.container"]),
		"Volume=%t/podman/podman.sock:/var/run/docker.sock:ro", "Volume=foo:/x", 1))
	wantOnly(t, Lint(files), "shared-underscore")
}

func TestGoldensLintClean(t *testing.T) {
	for _, name := range []string{"filial", "minimal"} {
		dir := filepath.Join("testdata", "golden", name)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{}
		for _, e := range entries {
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			files[e.Name()] = data
		}
		if len(files) == 0 {
			t.Fatalf("%s: no goldens", dir)
		}
		for _, f := range Lint(files) {
			t.Errorf("%s: %s", name, f)
		}
	}
}

func TestLintContinuation(t *testing.T) {
	files := map[string][]byte{"x.container": []byte("# railswitch: rendered from /WS/demo/project.toml (prefix demo, worktree main). Edit project.toml, not this file.\n# role: data\n\n[Container]\nExec=a \\\n  b\nPublishPort=127.0.0.1:1:1 \\\n  x\nPublishPort=\\\n  0.0.0.0:2:2\n")}
	fs := Lint(files)
	wantOnly(t, fs, "loopback-publish")
	if len(fs) != 1 || fs[0].Line != 9 {
		t.Errorf("findings %+v, want one at line 9", fs)
	}
}
