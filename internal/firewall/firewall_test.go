package firewall

import (
	"bytes"
	"encoding/json"
	"flag"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/render"
)

var update = flag.Bool("update", false, "rewrite testdata/golden")

const (
	ws         = "/WORKSPACES/filial"
	networkDir = "/home/u/.local/share/containers/storage/networks"
)

// filial is the Filial fixture's resolved stack and [firewall] table.
func filial(t *testing.T) (*pack.Stack, config.Firewall) {
	t.Helper()
	p, err := config.Load("../../testdata/fixtures/filial/project.toml")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := pack.LoadCatalog("../../testdata/fixtures/filial", nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := cat.Resolve(p)
	if err != nil {
		t.Fatal(err)
	}
	return st, p.Firewall
}

func files(t *testing.T, fw config.Firewall) map[string]render.File {
	t.Helper()
	st, _ := filial(t)
	out, err := Files("filial", ws, networkDir, st, fw)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDescriptorGolden(t *testing.T) {
	_, fw := filial(t)
	f, ok := files(t, fw)[".railswitch/firewall/hooks.d/filial-firewall.json"]
	if !ok {
		t.Fatal("no hooks.d/filial-firewall.json")
	}
	if f.Mode != 0o644 {
		t.Errorf("mode %04o", f.Mode)
	}
	golden := filepath.Join("testdata", "golden", "filial-firewall.json")
	if *update {
		if err := os.WriteFile(golden, f.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.Data, want) {
		t.Errorf("descriptor differs from %s (go test ./internal/firewall -run TestDescriptorGolden -update):\n--- got\n%s\n--- want\n%s", golden, f.Data, want)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, f.Data); err != nil {
		t.Fatal(err)
	}
	const brief = `{"version":"1.0.0","hook":{"path":"/WORKSPACES/filial/.railswitch/firewall/hook.sh","args":["hook.sh","filial","/WORKSPACES/filial/.railswitch/firewall"],"env":["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin","NETWORK_DIR=/home/u/.local/share/containers/storage/networks"],"timeout":60},"when":{"annotations":{"^filial\\.firewall$":".*"}},"stages":["createRuntime"]}`
	if compact.String() != brief {
		t.Errorf("descriptor\n got %s\nwant %s", compact.String(), brief)
	}
}

func TestScriptFiles(t *testing.T) {
	_, fw := filial(t)
	out := files(t, fw)
	for _, name := range []string{"hook.sh", "firewall.sh"} {
		f, ok := out[".railswitch/firewall/"+name]
		if !ok {
			t.Fatalf("no %s", name)
		}
		want, err := fs.ReadFile(assets, "assets/"+name)
		if err != nil {
			t.Fatal(err)
		}
		if f.Mode != 0o755 || !bytes.Equal(f.Data, want) {
			t.Errorf("%s: mode %04o, same bytes %v", name, f.Mode, bytes.Equal(f.Data, want))
		}
	}
	var keys []string
	for k := range out {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := []string{
		".railswitch/firewall/agent-claude.allow",
		".railswitch/firewall/agent-probe.allow",
		".railswitch/firewall/agent.allow",
		".railswitch/firewall/app.allow",
		".railswitch/firewall/deps.allow",
		".railswitch/firewall/firewall.sh",
		".railswitch/firewall/hook.sh",
		".railswitch/firewall/hooks.d/filial-firewall.json",
	}
	if !slices.Equal(keys, want) {
		t.Errorf("files %v, want %v", keys, want)
	}
}

// listed is the allowlist's entries: comments and blank lines dropped.
func listed(data []byte) []string {
	var out []string
	for l := range strings.SplitSeq(string(data), "\n") {
		l, _, _ = strings.Cut(l, "#")
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestDepsAllow(t *testing.T) {
	// The fixture's allow.deps = ["github.com"] is already in the pack's list.
	_, fw := filial(t)
	fw.Allow["deps"] = append(fw.Allow["deps"], "example.org", "rubygems.org", "example.org")
	f := files(t, fw)[".railswitch/firewall/deps.allow"]
	want := []string{
		"rubygems.org",
		"index.rubygems.org",
		"meta:github",
		"github.com",
		"codeload.github.com",
		"objects.githubusercontent.com",
		"release-assets.githubusercontent.com",
		"registry.npmjs.org",
		"repo.yarnpkg.com",
		"registry.yarnpkg.com",
		"example.org",
	}
	if got := listed(f.Data); !slices.Equal(got, want) || f.Mode != 0o644 {
		t.Errorf("deps.allow mode %04o entries\n%v\nwant\n%v", f.Mode, got, want)
	}
}

// The app profile's list (the app and its workers, D119) is [firewall]
// allow.app: the rails pack ships no app.allow.
func TestAppAllow(t *testing.T) {
	_, fw := filial(t)
	if got := listed(files(t, fw)[".railswitch/firewall/app.allow"].Data); len(got) != 0 {
		t.Errorf("app.allow without allow.app: %v", got)
	}
	fw.Allow = map[string][]string{"app": {"api.example.com", "api.example.com", "mail.example.com"}}
	if got := listed(files(t, fw)[".railswitch/firewall/app.allow"].Data); !slices.Equal(got, []string{"api.example.com", "mail.example.com"}) {
		t.Errorf("app.allow = %v", got)
	}
	fw.Allow = map[string][]string{"app": {"bad host"}}
	st, _ := filial(t)
	if _, err := Files("filial", ws, networkDir, st, fw); err == nil || !strings.Contains(err.Error(), "firewall.allow.app") {
		t.Errorf("a bad allow.app entry: %v", err)
	}
}

func TestAgentAllow(t *testing.T) {
	_, fw := filial(t)
	if got := listed(files(t, fw)[".railswitch/firewall/agent.allow"].Data); len(got) != 0 {
		t.Errorf("agent.allow without allow.agent: %v", got)
	}
	fw.Allow["agent"] = []string{"api.example.com", "10.0.0.0/8"}
	if got := listed(files(t, fw)[".railswitch/firewall/agent.allow"].Data); !slices.Equal(got, []string{"api.example.com", "10.0.0.0/8"}) {
		t.Errorf("agent.allow %v", got)
	}
}

func TestAgentAllowFiles(t *testing.T) {
	st, fw := filial(t)
	fw.Allow["agent"] = []string{"shared.example"}
	out := files(t, fw)
	var claude []string
	for _, a := range st.Agents {
		if a.Name == "claude" {
			claude = a.Agent.Allow
		}
	}
	if len(claude) == 0 {
		t.Fatal("fixture agent claude has no package allow")
	}
	for name, want := range map[string][]string{
		"claude": append(slices.Clone(claude), "shared.example"),
		"probe":  {"probe.example", "shared.example"},
	} {
		f, ok := out[".railswitch/firewall/agent-"+name+".allow"]
		if !ok {
			t.Errorf("no agent-%s.allow", name)
			continue
		}
		if got := listed(f.Data); !slices.Equal(got, want) || f.Mode != 0o644 {
			t.Errorf("agent-%s.allow mode %04o entries\n%v\nwant\n%v", name, f.Mode, got, want)
		}
	}
	if got := listed(out[".railswitch/firewall/agent-claude.allow"].Data); slices.Contains(got, "probe.example") {
		t.Errorf("agent-claude.allow lists probe's host: %v", got)
	}
	if got := listed(out[".railswitch/firewall/agent-probe.allow"].Data); slices.Contains(got, claude[0]) {
		t.Errorf("agent-probe.allow lists claude's host %s: %v", claude[0], got)
	}
}

func TestAllowRejectsBadEntry(t *testing.T) {
	st, fw := filial(t)
	for _, bad := range []string{"a.example b.example", "x\ny", "x#y", ""} {
		fw.Allow["deps"] = []string{bad}
		if _, err := Files("filial", ws, networkDir, st, fw); err == nil || !strings.Contains(err.Error(), "firewall.allow.deps") {
			t.Errorf("%q: err %v", bad, err)
		}
	}
}

func TestScriptsParse(t *testing.T) {
	names, err := fs.Glob(assets, "assets/*.sh")
	if err != nil || len(names) != 2 {
		t.Fatalf("scripts %v %v", names, err)
	}
	for _, n := range names {
		if out, err := exec.Command("bash", "-n", filepath.FromSlash(n)).CombinedOutput(); err != nil {
			t.Errorf("bash -n %s: %v\n%s", n, err, out)
		}
	}
}

// hookEnv puts shims on PATH: nsenter runs its args after -n, nft copies
// stdin to $OUT.
func hookEnv(t *testing.T, out string) []string {
	t.Helper()
	bin := t.TempDir()
	shims := map[string]string{
		"nsenter": "#!/bin/bash\nwhile (( $# )) && [[ $1 != -n ]]; do shift; done\nshift\nexec \"$@\"\n",
		"nft":     "#!/bin/bash\ncat >\"$OUT\"\n",
	}
	for name, body := range shims {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	nd, err := filepath.Abs(filepath.Join("testdata", "netavark"))
	if err != nil {
		t.Fatal(err)
	}
	return append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "OUT="+out, "NETWORK_DIR="+nd)
}

// runHook runs assets/hook.sh filial <root> with an OCI state carrying annotations.
func runHook(t *testing.T, root string, env []string, annotations map[string]string) ([]byte, error) {
	t.Helper()
	return runHookAt(t, filepath.Join("assets", "hook.sh"), root, env, annotations)
}

// runHookAt runs the hook script at script, which reads its allowlists next
// to it.
func runHookAt(t *testing.T, script, root string, env []string, annotations map[string]string) ([]byte, error) {
	t.Helper()
	state, err := json.Marshal(map[string]any{
		"ociVersion":  "1.0.2",
		"id":          "0123456789abcdef0123456789abcdef",
		"status":      "creating",
		"pid":         4242,
		"bundle":      "/run/bundle",
		"annotations": annotations,
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script, "filial", root)
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(state)
	return cmd.CombinedOutput()
}

func TestHookReadsNetavarkSubnet(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "nft.out")
	if msg, err := runHook(t, root, hookEnv(t, out), map[string]string{
		"filial.firewall":          "none",
		"filial.firewall.mode":     "enforce",
		"filial.firewall.networks": "filial-feat-dev",
	}); err != nil {
		t.Fatalf("hook: %v\n%s", err, msg)
	}
	rules, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rules), "10.89.7.0/24") {
		t.Errorf("ruleset has no fixture subnet 10.89.7.0/24:\n%s", rules)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "0123456789ab")); err != nil {
		t.Errorf("state file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "hook.log")); err != nil {
		t.Errorf("log: %v", err)
	}
}

// The hook keeps its state and log to this user, and narrows files an
// older hook left world-writable.
func TestHookFilesPrivate(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o777); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(root, "state", "fedcba987654")
	if err := os.WriteFile(old, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "state"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(old, 0o666); err != nil {
		t.Fatal(err)
	}
	if msg, err := runHook(t, root, hookEnv(t, filepath.Join(t.TempDir(), "nft.out")), map[string]string{
		"filial.firewall":          "none",
		"filial.firewall.mode":     "enforce",
		"filial.firewall.networks": "filial-feat-dev",
	}); err != nil {
		t.Fatalf("hook: %v\n%s", err, msg)
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(root, "state"):                 0o700,
		filepath.Join(root, "state", "0123456789ab"): 0o600,
		old:                             0o600,
		filepath.Join(root, "hook.log"): 0o600,
	} {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: %v %v, want %v", path, fi.Mode().Perm(), err, want)
		}
	}
}

// The app profile (D119) reads app.allow: a container with it starts.
func TestHookAppProfile(t *testing.T) {
	root, dir := t.TempDir(), t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	data, err := os.ReadFile(filepath.Join("assets", "hook.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.allow"), []byte("# list\n203.0.113.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if msg, err := runHookAt(t, script, root, hookEnv(t, filepath.Join(t.TempDir(), "nft.out")), map[string]string{
		"filial.firewall":          "app",
		"filial.firewall.mode":     "enforce",
		"filial.firewall.networks": "filial-feat-dev",
	}); err != nil {
		t.Fatalf("hook with profile app: %v\n%s", err, msg)
	}
}

func TestHookFailsClosed(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "nft.out")
	msg, err := runHook(t, root, hookEnv(t, out), map[string]string{
		"filial.firewall":      "none",
		"filial.firewall.mode": "enforce",
	})
	if err == nil {
		t.Fatalf("hook without a networks annotation succeeded:\n%s", msg)
	}
	if !strings.Contains(string(msg), "filial.firewall.networks") {
		t.Errorf("error does not name the annotation: %s", msg)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("nft ran")
	}
}

// TestHookAgentProfile: an agent-<name> profile reads agent-<name>.allow; a
// missing file or a name outside the agent pattern fails the start.
func TestHookAgentProfile(t *testing.T) {
	dir := t.TempDir()
	hook, err := assets.ReadFile("assets/hook.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, hook, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-probe.allow"), []byte("# probe\n203.0.113.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ann := func(profile string) map[string]string {
		return map[string]string{
			"filial.firewall":          profile,
			"filial.firewall.mode":     "enforce",
			"filial.firewall.networks": "filial-feat-dev",
		}
	}

	out := filepath.Join(t.TempDir(), "nft.out")
	if msg, err := runHookAt(t, script, t.TempDir(), hookEnv(t, out), ann("agent-probe")); err != nil {
		t.Fatalf("agent-probe: %v\n%s", err, msg)
	}
	rules, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rules), "203.0.113.7") {
		t.Errorf("ruleset lacks agent-probe.allow's 203.0.113.7:\n%s", rules)
	}

	for _, p := range []string{"agent-missing", "agentx", "agent-a/../x"} {
		out := filepath.Join(t.TempDir(), "nft.out")
		if msg, err := runHookAt(t, script, t.TempDir(), hookEnv(t, out), ann(p)); err == nil {
			t.Errorf("profile %q: hook succeeded:\n%s", p, msg)
		}
		if _, err := os.Stat(out); err == nil {
			t.Errorf("profile %q: nft ran", p)
		}
	}
}

// TestFirewallShowScope runs firewall.sh show against a podman shim: a
// worktree sees only the containers on its own dev network (api, not
// api-v2), a firewall = "self" service is named as such, and one without a
// state file is flagged as unfirewalled.
func TestFirewallShowScope(t *testing.T) {
	bin := t.TempDir()
	podman := `#!/bin/bash
case "$*" in
  "ps -a --no-trunc -q") ;;
  "ps --format "*) printf '%s\n' \
    "aaa111 filial-api-rails filial-api-dev,filial_proxy" \
    "bbb222 filial-api-browser filial-api-dev" \
    "ccc333 filial-api-v2-rails filial-api-v2-dev,filial_proxy" ;;
  *) echo "unexpected podman $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(podman), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("bash", append([]string{filepath.Join("assets", "firewall.sh")}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"),
			"FIREWALL_DIR="+t.TempDir(), "PREFIX=filial", "WORKTREE=api", "SELF_FIREWALLED=browser")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("firewall.sh %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	out := run("show")
	if !strings.Contains(out, "filial-api-rails  NO FIREWALL") || !strings.Contains(out, "filial-api-browser  own in-container firewall") {
		t.Errorf("show:\n%s", out)
	}
	if strings.Contains(out, "api-v2") {
		t.Errorf("show for worktree api lists api-v2:\n%s", out)
	}
	if out := run("show", "browser"); strings.Contains(out, "filial-api-rails") || !strings.Contains(out, "filial-api-browser") {
		t.Errorf("show browser:\n%s", out)
	}
}
