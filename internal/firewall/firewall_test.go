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
		"filial.firewall.networks": "filial-feat-dev", "filial.firewall.dns": "filter",
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
			side, local := strings.Index(r, "ip daddr 10.89.7.1 udp dport 53 udp sport != 1053 goto deny"), strings.Index(r, "@local4 accept")
			if side < 0 || !strings.Contains(r, "ip daddr 10.89.7.1 tcp dport 53 goto deny") || side > local {
				t.Errorf("DNS to the gateway is not kept to the filter, ahead of local4:\n%s", r)
			}
		}
	}
	conf, err := os.ReadFile(filepath.Join(root, "state", "dns", "0123456789ab.conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# gateway=10.89.7.1\n", "listen-address=127.0.0.1\n", "no-resolv\n", "cache-size=0\n",
		"server=//10.89.7.1\n", "server=/dns.podman/10.89.7.1\n",
		"server=/api.example.com/10.89.7.1\n", "nftset=/api.example.com/4#inet#wt_firewall#allowd4\n", "query-port=1053\n",
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
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") { // keep the shims, drop a real dnsmasq the host may have
			env[i] = "PATH=" + strings.SplitN(strings.TrimPrefix(kv, "PATH="), ":", 2)[0] + ":" + filepath.Dir(lookTool(t, "nsenter")) + ":/usr/bin:/bin"
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
