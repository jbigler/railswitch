package firewall

import (
	"bytes"
	"encoding/json"
	"flag"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

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
	// D182: the poststop one runs hook.sh … stop, at poststop, for the same containers.
	stop := files(t, fw)[".railswitch/firewall/hooks.d/filial-firewall-stop.json"]
	compact.Reset()
	if err := json.Compact(&compact, stop.Data); err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(strings.Replace(brief, `"/WORKSPACES/filial/.railswitch/firewall"]`, `"/WORKSPACES/filial/.railswitch/firewall","stop"]`, 1), `["createRuntime"]`, `["poststop"]`, 1); compact.String() != want {
		t.Errorf("stop descriptor\n got %s\nwant %s", compact.String(), want)
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
		".railswitch/firewall/hooks.d/filial-firewall-stop.json",
		".railswitch/firewall/hooks.d/filial-firewall.json",
		".railswitch/firewall/resolv.conf",
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
	fw.Allow["agent"] = []string{"api.example.com", "203.0.113.0/24"}
	if got := listed(files(t, fw)[".railswitch/firewall/agent.allow"].Data); !slices.Equal(got, []string{"api.example.com", "203.0.113.0/24"}) {
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

// hookEnv puts shims on PATH: nsenter runs its args after -n, nft -f copies
// stdin to $OUT, and nft list prints it back without its first two lines
// (table, delete table), or $LIST when set: the live table to verify.
func hookEnv(t *testing.T, out string) []string {
	t.Helper()
	bin := t.TempDir()
	shims := map[string]string{
		"nsenter": "#!/bin/bash\nwhile (( $# )) && [[ $1 != -n ]]; do shift; done\nshift\nexec \"$@\"\n",
		"nft":     "#!/bin/bash\nif [[ $1 == list ]]; then if [[ -n ${LIST:-} ]]; then cat \"$LIST\"; else tail -n +3 \"$OUT\"; fi; else cat >\"$OUT\"; fi\n",
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

// hookPid is the container process the OCI state names.
var hookPid = 4242

// runHookAt runs the hook script at script, which reads its allowlists next
// to it.
func runHookAt(t *testing.T, script, root string, env []string, annotations map[string]string) ([]byte, error) {
	t.Helper()
	state, err := json.Marshal(map[string]any{
		"ociVersion":  "1.0.2",
		"id":          "0123456789abcdef0123456789abcdef",
		"status":      "creating",
		"pid":         hookPid,
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

// TestHookMetaGoogle: meta:google is goog.json without every range that
// overlaps cloud.json (Google Cloud's customers); without cloud.json,
// nothing.
func TestHookMetaGoogle(t *testing.T) {
	ranges, err := filepath.Abs(filepath.Join("testdata", "ipranges"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name       string
		cloud      string // "": the fixture; "fail": the fetch fails; else the body served
		want, none []string
		why        string // in hook.log
	}{
		{"cloud subtracted", "", []string{"8.8.8.0/24", "74.125.0.0/16"}, []string{"34.64.0.0/10", "35.192.0.0/14", "2001:4860"}, ""},
		{"no cloud.json", "fail", nil, []string{"8.8.8.0/24", "74.125.0.0/16", "34.64.0.0/10"}, "meta:google (cloud.json fetch failed)"},
		// A 200 that isn't the list: without the check, goog's lines were read as cloud's.
		{"cloud.json not the list", "<html>sign in to the wifi</html>", nil, []string{"8.8.8.0/24", "74.125.0.0/16", "34.64.0.0/10"}, "meta:google (cloud.json has no ranges)"},
	} {
		out := filepath.Join(t.TempDir(), "nft.out")
		bin := t.TempDir()
		// curl -fsS --max-time 15 <url>: the fixture named by the URL's file.
		curl := "#!/bin/bash\nu=\"${@: -1}\"\nf=\"$RANGES/${u##*/}\"\n[[ ${CLOUD:-} == fail && $f == */cloud.json ]] && exit 22\n[[ -n ${CLOUD:-} && $f == */cloud.json ]] && { printf %s \"$CLOUD\"; exit 0; }\ncat \"$f\"\n"
		if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(curl), 0o755); err != nil {
			t.Fatal(err)
		}
		env := append(hookEnv(t, out), "RANGES="+ranges)
		for i, kv := range env {
			if strings.HasPrefix(kv, "PATH=") {
				env[i] = "PATH=" + bin + ":" + strings.TrimPrefix(kv, "PATH=")
			}
		}
		if c.cloud != "" {
			env = append(env, "CLOUD="+c.cloud)
		}
		root := t.TempDir()
		if msg, err := runHook(t, root, env, map[string]string{
			"filial.firewall": "none", "filial.firewall.mode": "log",
			"filial.firewall.networks": "filial-feat-dev", "filial.firewall.extra": "meta:google",
		}); err != nil {
			t.Fatalf("%s: hook: %v\n%s", c.name, err, msg)
		}
		rules, _ := os.ReadFile(out)
		allow := string(rules)[strings.Index(string(rules), "set allow4"):strings.Index(string(rules), "set local4")]
		for _, w := range c.want {
			if !strings.Contains(allow, w) {
				t.Errorf("%s: allow4 lacks %s:\n%s", c.name, w, allow)
			}
		}
		for _, n := range c.none {
			if strings.Contains(allow, n) {
				t.Errorf("%s: allow4 has %s:\n%s", c.name, n, allow)
			}
		}
		if log, _ := os.ReadFile(filepath.Join(root, "hook.log")); c.why != "" && !strings.Contains(string(log), c.why) {
			t.Errorf("%s: log does not say why:\n%s", c.name, log)
		}
	}
}

// TestHookFloor (D179): the hook's FLOOR is config.FirewallFloor, its
// floor4 set holds it, and the floor rule comes before every allow rule,
// after the container's own networks; the output chain ends in the deny
// chain, which ends in the mode's verdict.
func TestHookFloor(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("assets", "hook.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "FLOOR=(" + strings.Join(config.FirewallFloor, " ") + ")"; !strings.Contains(string(src), want) {
		t.Errorf("hook.sh lacks %s", want)
	}
	for mode, verdict := range map[string]string{"enforce": "reject with icmpx admin-prohibited", "log": "accept"} {
		root := t.TempDir()
		out := filepath.Join(t.TempDir(), "nft.out")
		if msg, err := runHook(t, root, hookEnv(t, out), map[string]string{
			"filial.firewall":          "none",
			"filial.firewall.mode":     mode,
			"filial.firewall.networks": "filial-feat-dev",
		}); err != nil {
			t.Fatalf("%s: hook: %v\n%s", mode, err, msg)
		}
		rules, _ := os.ReadFile(out)
		r := string(rules)
		for _, f := range config.FirewallFloor {
			if !strings.Contains(r, f) {
				t.Errorf("%s: ruleset lacks floor %s", mode, f)
			}
		}
		local, floor, allow := strings.Index(r, "@local4 accept"), strings.Index(r, "@floor4 goto deny"), strings.Index(r, "@allow4 accept")
		if !(local >= 0 && local < floor && floor < allow && floor < strings.Index(r, "@allowt4 accept")) {
			t.Errorf("%s: want local4, then floor4, then the allow sets:\n%s", mode, r)
		}
		if !strings.Contains(r, "    goto deny\n  }\n  chain deny {") || !strings.Contains(r, "    "+verdict+"\n  }\n}") {
			t.Errorf("%s: output does not end in goto deny, or deny not in %q:\n%s", mode, verdict, r)
		}
	}
}

// TestHookVerifies (D179): a live table that lacks a floor range, or whose
// floor rule comes after the allow rules, fails the start.
func TestHookVerifies(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"floor range missing": func(r string) string { return strings.Replace(r, "169.254.0.0/16,", "", 1) },
		// 0.0.0.0/8 is also the tail of 10.0.0.0/8: a substring match missed it.
		"0.0.0.0/8 missing": func(r string) string {
			i := strings.Index(r, "{ 0.0.0.0/8,10.0.0.0/8")
			if i < 0 {
				t.Fatalf("floor4 not listed as expected:\n%s", r)
			}
			return r[:i+2] + r[i+len("{ 0.0.0.0/8,"):]
		},
		"floor after allow": func(r string) string {
			r = strings.Replace(r, "    ip daddr @floor4 goto deny\n", "", 1)
			return strings.Replace(r, "    ip daddr @allowt4 accept\n", "    ip daddr @allowt4 accept\n    ip daddr @floor4 goto deny\n", 1)
		},
		"no verdict": func(r string) string { return strings.Replace(r, "    reject with icmpx admin-prohibited\n", "", 1) },
	} {
		// Apply once to learn the table, then hand the hook an edited copy.
		probe := filepath.Join(t.TempDir(), "nft.out")
		ann := map[string]string{"filial.firewall": "none", "filial.firewall.mode": "enforce", "filial.firewall.networks": "filial-feat-dev"}
		if msg, err := runHook(t, t.TempDir(), hookEnv(t, probe), ann); err != nil {
			t.Fatalf("probe: %v\n%s", err, msg)
		}
		applied, _ := os.ReadFile(probe)
		lines := strings.SplitN(string(applied), "\n", 3)
		live := filepath.Join(t.TempDir(), "live")
		if err := os.WriteFile(live, []byte(edit(lines[2])), 0o600); err != nil {
			t.Fatal(err)
		}
		msg, err := runHook(t, t.TempDir(), append(hookEnv(t, filepath.Join(t.TempDir(), "nft.out")), "LIST="+live), ann)
		if err == nil || !strings.Contains(string(msg), "verify:") {
			t.Errorf("%s: hook passed or failed for another reason: %v\n%s", name, err, msg)
		}
	}
}

// TestFirewallAllowFor (D179): allow --for puts each address in allowt4
// with the timeout; without it, in allow4; a range with --for is refused.
func TestFirewallAllowFor(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "nft.args")
	shims := map[string]string{
		// podman unshare nsenter … runs its command; inspect answers id and pid.
		"podman":  "#!/bin/bash\ncase \"$1\" in\n  inspect) [[ \"$*\" == *Pid* ]] && echo 4242 || echo 0123456789abcdef ;;\n  unshare) shift; exec \"$@\" ;;\nesac\n",
		"nsenter": "#!/bin/bash\nwhile (( $# )) && [[ $1 != -n ]]; do shift; done\nshift\nexec \"$@\"\n",
		"nft":     "#!/bin/bash\nprintf '%s\\n' \"$*\" >>\"$LOG\"\n",
		"getent":  "#!/bin/bash\necho \"203.0.113.9 STREAM $2\"\n",
	}
	for name, body := range shims {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state", "0123456789ab"), []byte("profiles=app\nmode=enforce\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		cmd := exec.Command("bash", append([]string{filepath.Join("assets", "firewall.sh"), "allow"}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "LOG="+log,
			"FIREWALL_DIR="+dir, "PREFIX=filial", "WORKTREE=api")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("--for", "1800s", "rails", "example.com", "198.51.100.4"); err != nil {
		t.Fatalf("allow --for: %v\n%s", err, out)
	}
	if out, err := run("rails", "example.com"); err != nil {
		t.Fatalf("allow: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(log)
	want := "add element inet wt_firewall allowt4 { 203.0.113.9 timeout 1800s,198.51.100.4 timeout 1800s }\nadd element inet wt_firewall allow4 { 203.0.113.9 }\n"
	if string(got) != want {
		t.Errorf("nft calls:\n%s\nwant:\n%s", got, want)
	}
	for _, args := range [][]string{{"--for", "1800s", "rails", "198.51.100.0/24"}, {"--for", "30m", "rails", "example.com"}} {
		if out, err := run(args...); err == nil {
			t.Errorf("%v: succeeded\n%s", args, out)
		}
	}
}

// TestHookDNSFilter (D182): with filial.firewall.dns=filter under enforce,
// the hook writes the filter's config (the gateway for container names and
// each allowed name, with its nftset) and starts dnsmasq in the
// container's netns; without dnsmasq it fails the start; under log it
// starts nothing; the poststop run ends it and drops its files.
func TestHookDNSFilter(t *testing.T) {
	ann := map[string]string{
		"filial.firewall": "app", "filial.firewall.mode": "enforce",
		"filial.firewall.networks": "filial-feat-dev,filial-feat-proxy", "filial.firewall.dns": "filter",
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	src, _ := os.ReadFile(filepath.Join("assets", "hook.sh"))
	if err := os.WriteFile(script, src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.allow"), []byte("api.example.com\n203.0.113.0/24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withDNS := func(t *testing.T, root string, dnsmasq bool) []string {
		env := hookEnv(t, filepath.Join(t.TempDir(), "nft.out"))
		bin := t.TempDir()
		shims := map[string]string{"getent": "#!/bin/bash\necho \"198.51.100.20 STREAM $2\"\n"}
		if dnsmasq {
			// The stand-in records its argv and, as dnsmasq does, writes its pid file.
			shims["dnsmasq"] = "#!/bin/bash\nprintf '%s\\n' \"$*\" >>\"$DNSLOG\"\nconf=${1#--conf-file=}\npf=$(sed -n 's/^pid-file=//p' \"$conf\")\nsleep 300 & echo $! >\"$pf\"\n"
		}
		for name, body := range shims {
			if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for i, kv := range env {
			if strings.HasPrefix(kv, "PATH=") {
				env[i] = "PATH=" + bin + ":" + strings.TrimPrefix(kv, "PATH=")
			}
		}
		return append(env, "DNSLOG="+filepath.Join(root, "dnsmasq.argv"))
	}

	root := t.TempDir()
	env := withDNS(t, root, true)
	if msg, err := runHookAt(t, script, root, env, ann); err != nil {
		t.Fatalf("hook: %v\n%s", err, msg)
	}
	for _, kv := range env {
		if out, ok := strings.CutPrefix(kv, "OUT="); ok {
			rules, _ := os.ReadFile(out)
			r := string(rules)
			local := strings.Index(r, "@local4 accept")
			// Every network's gateway: the proxy's resolver forwards upstream too.
			for _, gw := range []string{"10.89.7.1", "10.89.8.1"} {
				udp, tcp := strings.Index(r, "ip daddr "+gw+" udp dport 53 udp sport != 953 goto deny"), strings.Index(r, "ip daddr "+gw+" tcp dport 53 goto deny")
				if udp < 0 || tcp < 0 || udp > local || tcp > local {
					t.Errorf("DNS to %s is not kept to the filter, ahead of local4:\n%s", gw, r)
				}
			}
		}
	}
	conf, err := os.ReadFile(filepath.Join(root, "state", "dns", "0123456789ab.conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# gateway=10.89.7.1\n", "# dnsmasq=/", "listen-address=127.0.0.1\n", "no-resolv\n", "cache-size=0\n",
		"server=//10.89.7.1\n", "server=/dns.podman/10.89.7.1\n",
		"server=/api.example.com/10.89.7.1\n", "nftset=/api.example.com/4#inet#wt_firewall#allowd4\n", "query-port=953\n", "address=/#/\n",
	} {
		if !strings.Contains(string(conf), want) {
			t.Errorf("conf lacks %q:\n%s", want, conf)
		}
	}
	if strings.Contains(string(conf), "203.0.113.0/24") {
		t.Errorf("a CIDR is in the DNS filter:\n%s", conf)
	}
	argv, _ := os.ReadFile(filepath.Join(root, "dnsmasq.argv"))
	if string(argv) != "--conf-file="+filepath.Join(root, "state", "dns", "0123456789ab.conf")+"\n" {
		t.Errorf("dnsmasq argv %q", argv)
	}
	pidFile := filepath.Join(root, "state", "dns", "0123456789ab.pid")
	pid, _ := os.ReadFile(pidFile)

	// poststop: the filter ends, its files go.
	stop := exec.Command("bash", script, "filial", root, "stop")
	stop.Stdin = strings.NewReader(`{"id":"0123456789abcdef0123456789abcdef","status":"stopped"}`)
	if out, err := stop.CombinedOutput(); err != nil {
		t.Fatalf("stop: %v\n%s", err, out)
	}
	if _, err := os.Stat(pidFile); err == nil {
		t.Error("the pid file survived poststop")
	}
	if p := strings.TrimSpace(string(pid)); p != "" {
		if err := exec.Command("kill", "-0", p).Run(); err == nil {
			t.Errorf("the filter (pid %s) still runs after poststop", p)
			_ = exec.Command("kill", p).Run()
		}
	}

	// No dnsmasq: the start fails, naming it.
	root = t.TempDir()
	env = withDNS(t, root, false)
	// Keep the shims and the host's tools, but not a dnsmasq the host has:
	// its directories, linked one by one, without it.
	tools := t.TempDir()
	for _, d := range []string{filepath.Dir(lookTool(t, "nsenter")), "/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			if e.Name() != "dnsmasq" {
				_ = os.Symlink(filepath.Join(d, e.Name()), filepath.Join(tools, e.Name())) // the first directory's wins
			}
		}
	}
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			env[i] = "PATH=" + strings.SplitN(strings.TrimPrefix(kv, "PATH="), ":", 2)[0] + ":" + tools
		}
	}
	if msg, err := runHookAt(t, script, root, env, ann); err == nil || !strings.Contains(string(msg), "no dnsmasq") {
		t.Errorf("without dnsmasq: %v\n%s", err, msg)
	}

	// log mode: no filter.
	root = t.TempDir()
	ann["filial.firewall.mode"] = "log"
	if msg, err := runHookAt(t, script, root, withDNS(t, root, true), ann); err != nil {
		t.Fatalf("log: %v\n%s", err, msg)
	}
	if _, err := os.Stat(filepath.Join(root, "dnsmasq.argv")); err == nil {
		t.Error("log mode started the DNS filter")
	}
	// The filter ends with the container's process even without the
	// poststop hook: a dnsmasq left running kept a oneshot unit (deps) from
	// ever finishing.
	t.Run("ends with the container", func(t *testing.T) {
		ctr := exec.Command("sleep", "60")
		if err := ctr.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ctr.Process.Kill() })
		hookPid = ctr.Process.Pid
		t.Cleanup(func() { hookPid = 4242 })
		root := t.TempDir()
		enforce := maps.Clone(ann)
		enforce["filial.firewall.mode"] = "enforce"
		if msg, err := runHookAt(t, script, root, withDNS(t, root, true), enforce); err != nil {
			t.Fatalf("hook: %v\n%s", err, msg)
		}
		b, _ := os.ReadFile(filepath.Join(root, "state", "dns", "0123456789ab.pid"))
		dns, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			t.Fatalf("pid file %q: %v", b, err)
		}
		time.Sleep(1500 * time.Millisecond)
		if syscall.Kill(dns, 0) != nil {
			t.Fatal("the filter ended while the container runs")
		}
		_ = ctr.Process.Kill()
		_ = ctr.Wait()
		for end := time.Now().Add(5 * time.Second); syscall.Kill(dns, 0) == nil; time.Sleep(100 * time.Millisecond) {
			if time.Now().After(end) {
				t.Fatal("the filter outlived the container by 5 s")
			}
		}
	})
}

// lookTool is the shim directory's tool name (hookEnv's first PATH entry).
func lookTool(t *testing.T, name string) string {
	t.Helper()
	for _, kv := range hookEnv(t, filepath.Join(t.TempDir(), "x")) {
		if p, ok := strings.CutPrefix(kv, "PATH="); ok {
			return filepath.Join(strings.SplitN(p, ":", 2)[0], name)
		}
	}
	t.Fatal("no PATH")
	return ""
}

// TestFirewallAllowDNS (D182): fw allow adds a host name to the container's
// DNS filter and restarts it; allow --for leaves the filter alone (its
// lines have no expiry) and says so; fw show ends the filter of a
// container that is gone and drops its files.
func TestFirewallAllowDNS(t *testing.T) {
	bin := t.TempDir()
	dir := t.TempDir()
	dnsDir := filepath.Join(dir, "state", "dns")
	if err := os.MkdirAll(dnsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	argvLog := filepath.Join(dir, "dnsmasq.argv")
	shims := map[string]string{
		// podman: inspect answers id and pid, unshare runs its command, ps lists nothing.
		"podman":  "#!/bin/bash\ncase \"$1\" in\n  inspect) [[ \"$*\" == *Pid* ]] && echo 4242 || echo 0123456789abcdef ;;\n  unshare) shift; exec \"$@\" ;;\nesac\n",
		"nsenter": "#!/bin/bash\nwhile (( $# )) && [[ $1 != -n ]]; do shift; done\nshift\nexec \"$@\"\n",
		"nft":     "#!/bin/bash\n",
		"getent":  "#!/bin/bash\necho \"203.0.113.9 STREAM $2\"\n",
		// A dnsmasq first on PATH that isn't the hook's: never run.
		"dnsmasq": "#!/bin/bash\necho \"the PATH's dnsmasq ran\" >&2\nexit 1\n",
	}
	// The hook's dnsmasq, named in the config. As dnsmasq does: daemonize
	// (off the output pipe), write the pid file.
	hookBin := t.TempDir()
	dnsmasq := filepath.Join(hookBin, "dnsmasq")
	if err := os.WriteFile(dnsmasq, []byte("#!/bin/bash\nprintf '%s\\n' \"$*\" >>\"$DNSLOG\"\nconf=${1#--conf-file=}\npf=$(sed -n 's/^pid-file=//p' \"$conf\")\nsleep 300 >/dev/null 2>&1 & echo $! >\"$pf\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range shims {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "state", "0123456789ab"), []byte("profiles=agent\nmode=enforce\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dnsDir, "0123456789ab.conf")
	pidFile := filepath.Join(dnsDir, "0123456789ab.pid")
	base := "# gateway=10.89.7.1\n# dnsmasq=" + dnsmasq + "\npid-file=" + pidFile + "\nserver=/api.example.com/10.89.7.1\n"
	if err := os.WriteFile(conf, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	// The running filter.
	old := exec.Command("sleep", "300")
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	oldDone := make(chan struct{})
	go func() { _ = old.Wait(); close(oldDone) }()
	t.Cleanup(func() { _ = old.Process.Kill() })
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(old.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		cmd := exec.Command("bash", append([]string{filepath.Join("assets", "firewall.sh")}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "DNSLOG="+argvLog,
			"FIREWALL_DIR="+dir, "PREFIX=filial", "WORKTREE=api")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// --for: the filter's config stays as it was, and nothing restarts.
	out, err := run("allow", "--for", "600s", "agent", "other.example.com")
	if err != nil {
		t.Fatalf("allow --for: %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(conf); string(got) != base {
		t.Errorf("allow --for changed the filter's config:\n%s", got)
	}
	if _, err := os.Stat(argvLog); err == nil {
		t.Error("allow --for restarted the filter")
	}
	if !strings.Contains(out, "does not answer other.example.com") {
		t.Errorf("allow --for: no note about the filter:\n%s", out)
	}

	// Without --for: the name joins the config, the old filter ends, a new one starts.
	if out, err := run("allow", "agent", "new.example.com"); err != nil {
		t.Fatalf("allow: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(conf)
	for _, want := range []string{"server=/new.example.com/10.89.7.1\n", "nftset=/new.example.com/4#inet#wt_firewall#allowd4\n"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("conf lacks %q:\n%s", want, got)
		}
	}
	select {
	case <-oldDone:
	case <-time.After(5 * time.Second):
		t.Error("the old filter still runs after allow")
	}
	if argv, _ := os.ReadFile(argvLog); string(argv) != "--conf-file="+conf+"\n" {
		t.Errorf("dnsmasq argv %q", argv)
	}
	pid, _ := os.ReadFile(pidFile)
	p := strings.TrimSpace(string(pid))
	if p == "" || p == strconv.Itoa(old.Process.Pid) {
		t.Fatalf("no new filter pid: %q", p)
	}

	// show, with the container gone: the filter ends, its files go.
	if out, err := run("show"); err != nil {
		t.Fatalf("show: %v\n%s", err, out)
	}
	for _, f := range []string{conf, pidFile} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s survived show", f)
		}
	}
	for i := 0; i < 50 && exec.Command("kill", "-0", p).Run() == nil; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if exec.Command("kill", "-0", p).Run() == nil {
		t.Errorf("the filter (pid %s) still runs after show", p)
		_ = exec.Command("kill", p).Run()
	}
}

// TestHookMetaAnthropic (D185): meta:anthropic is Anthropic's fixed inbound
// ranges, with nothing to fetch.
func TestHookMetaAnthropic(t *testing.T) {
	out := filepath.Join(t.TempDir(), "nft.out")
	root := t.TempDir()
	if msg, err := runHook(t, root, hookEnv(t, out), map[string]string{
		"filial.firewall": "none", "filial.firewall.mode": "enforce",
		"filial.firewall.networks": "filial-feat-dev", "filial.firewall.extra": "meta:anthropic",
	}); err != nil {
		t.Fatalf("hook: %v\n%s", err, msg)
	}
	rules, _ := os.ReadFile(out)
	allow := string(rules)[strings.Index(string(rules), "set allow4"):strings.Index(string(rules), "set allowt4")]
	for _, w := range []string{"160.79.104.0/23", "153.61.224.0/22"} {
		if !strings.Contains(allow, w) {
			t.Errorf("allow4 lacks %s:\n%s", w, allow)
		}
	}
}
