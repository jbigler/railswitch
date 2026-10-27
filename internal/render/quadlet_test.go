package render

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/pack/builtin"
	"github.com/jbigler/railswitch/internal/testfixture"
	"github.com/jbigler/railswitch/internal/unithash"
)

var update = flag.Bool("update", false, "rewrite testdata/golden")

const minimalDir = "../../testdata/fixtures/minimal"

// renderUnits renders the fixture dir's units with mutate applied.
func renderUnits(t *testing.T, dir string, mutate func(*config.Project)) (string, map[string][]byte) {
	t.Helper()
	ws := testfixture.Workspace(t, dir)
	units, err := Quadlet(mustBuild(t, fixtureInputs(t, ws, mutate)))
	if err != nil {
		t.Fatalf("Quadlet: %v", err)
	}
	return ws, units
}

// unitLines returns file's lines, failing when it wasn't rendered.
func unitLines(t *testing.T, units map[string][]byte, file string) []string {
	t.Helper()
	data, ok := units[file]
	if !ok {
		t.Fatalf("%s not rendered (have %v)", file, sortedKeys(units))
	}
	return strings.Split(string(data), "\n")
}

func hasLine(t *testing.T, units map[string][]byte, file, line string) {
	t.Helper()
	if !slices.Contains(unitLines(t, units, file), line) {
		t.Errorf("%s: no line %q:\n%s", file, line, units[file])
	}
}

func noLinePrefix(t *testing.T, units map[string][]byte, file, prefix string) {
	t.Helper()
	for _, l := range unitLines(t, units, file) {
		if strings.HasPrefix(l, prefix) {
			t.Errorf("%s: unexpected line %q", file, l)
		}
	}
}

var unitHashRe = regexp.MustCompile(`(?m)^Label=([a-z0-9-]+\.unit-hash)=[0-9a-f]{12}\n`)

func TestGolden(t *testing.T) {
	for _, dir := range []string{fixtureDir, minimalDir} {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			ws, units := renderUnits(t, dir, nil)
			golden := filepath.Join("testdata", "golden", name)
			// The workspace is a temp dir: goldens hold it as /WORKSPACES/<name>,
			// with each unit-hash label re-computed for that path.
			norm := func(b []byte) []byte {
				m := unitHashRe.FindSubmatch(b)
				if m == nil {
					return bytes.ReplaceAll(b, []byte(filepath.Dir(ws)), []byte("/WORKSPACES"))
				}
				b = bytes.Replace(b, m[0], nil, 1)
				return unithash.Stamp(bytes.ReplaceAll(b, []byte(filepath.Dir(ws)), []byte("/WORKSPACES")), string(m[1]))
			}
			if *update {
				if err := os.RemoveAll(golden); err != nil {
					t.Fatal(err)
				}
				for file, data := range units {
					writeFile(t, filepath.Join(golden, file), string(norm(data)))
				}
				return
			}
			entries, err := os.ReadDir(golden)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, e := range entries {
				want = append(want, e.Name())
			}
			if got := sortedKeys(units); !slices.Equal(got, want) {
				t.Errorf("rendered files:\n got %v\nwant %v", got, want)
			}
			for _, file := range want {
				exp, err := os.ReadFile(filepath.Join(golden, file))
				if err != nil {
					t.Fatal(err)
				}
				if got := norm(units[file]); !bytes.Equal(got, exp) {
					t.Errorf("%s differs from its golden (go test ./internal/render -run TestGolden -update):\n--- got\n%s\n--- want\n%s", file, got, exp)
				}
			}
		})
	}
}

func TestMarkerLine(t *testing.T) {
	ws, units := renderUnits(t, fixtureDir, nil)
	for file, data := range units {
		first, _, _ := strings.Cut(string(data), "\n")
		want := Marker + ws + "/project.toml (prefix filial, worktree " + strings.Split(file, "-")[1] + "). Edit project.toml, not this file."
		if file == "filial-proxy.network" {
			want = Marker + ws + "/project.toml (prefix filial). Edit project.toml, not this file."
		}
		if first != want {
			t.Errorf("%s: line 1 = %q, want %q", file, first, want)
		}
	}
}

func TestRoleLine(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, nil)
	for file, role := range map[string]string{
		"filial-master-deps.container":  "deps",
		"filial-master-rails.container": "app",
		"filial-master-db.container":    "data",
		"filial-master-nvim.container":  "tool",
	} {
		if l := unitLines(t, units, file); len(l) < 2 || l[1] != "# role: "+role {
			t.Errorf("%s: line 2 = %q, want %q", file, l[1], "# role: "+role)
		}
	}
}

func TestAssetMounts(t *testing.T) {
	ws, units := renderUnits(t, fixtureDir, nil)
	hasLine(t, units, "filial-master-db.container",
		"Volume="+ws+"/.railswitch/assets/postgres/initdb/restore-dump.sh:/docker-entrypoint-initdb.d/restore-dump.sh:ro,z")
	hasLine(t, units, "filial-master-rails.container", "Entrypoint=/usr/local/bin/entrypoint-app.sh")
	hasLine(t, units, "filial-master-rails.container",
		"Volume="+ws+"/.railswitch/assets/rails/entrypoints/entrypoint-app.sh:/usr/local/bin/entrypoint-app.sh:ro,z")
	re := regexp.MustCompile(`(?m)^Image=localhost/filial/playwright:1\.54\.0-[0-9a-f]{8}$`)
	if !re.Match(units["filial-master-browser.container"]) {
		t.Errorf("browser image is not the standalone build:\n%s", units["filial-master-browser.container"])
	}
}

func TestEntrypointOverride(t *testing.T) {
	ws := fixtureWS(t)
	writeFile(t, filepath.Join(ws, "entrypoints", "entrypoint-app.sh"), "#!/bin/sh\n")
	units, err := Quadlet(mustBuild(t, fixtureInputs(t, ws, nil)))
	if err != nil {
		t.Fatal(err)
	}
	hasLine(t, units, "filial-master-rails.container",
		"Volume="+ws+"/entrypoints/entrypoint-app.sh:/usr/local/bin/entrypoint-app.sh:ro,z")
}

// git-guard (D60): a writable base .git is followed by its hooks, read-only;
// in the base worktree /app is the base checkout, so every service mounting
// the worktree gets /app/.git/hooks read-only.
func TestHooksMountReadOnly(t *testing.T) {
	ws, units := renderUnits(t, fixtureDir, nil)
	git := filepath.Join(ws, "master", ".git")
	hooks := filepath.Join(git, "hooks")
	for _, file := range []string{"filial-feat-nvim.container", "filial-feat-agent-probe.container"} {
		lines := unitLines(t, units, file)
		i := slices.Index(lines, "Volume="+git+":"+git+":z")
		if i < 0 || i+1 >= len(lines) || lines[i+1] != "Volume="+hooks+":"+hooks+":ro,z" {
			t.Errorf("%s: no read-only hooks line after the .git line:\n%s", file, units[file])
		}
		noLinePrefix(t, units, file, "Volume="+hooks+":/app/.git/hooks")
	}
	noLinePrefix(t, units, "filial-feat-rails.container", "Volume="+hooks)
	for _, svc := range []string{"rails", "deps", "nvim", "agent-probe"} {
		hasLine(t, units, "filial-master-"+svc+".container", "Volume="+hooks+":/app/.git/hooks:ro,z")
	}
}

func TestDepsNotRemainAfterExit(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, nil)
	for _, slug := range []string{"master", "feat"} {
		file := "filial-" + slug + "-deps.container"
		hasLine(t, units, file, "Type=oneshot")
		hasLine(t, units, file, "TimeoutStartSec=900")
		hasLine(t, units, file, "LogDriver=passthrough")
		noLinePrefix(t, units, file, "RemainAfterExit")
		noLinePrefix(t, units, file, "Restart=")
	}
}

func TestNoNonLoopbackPublish(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, nil)
	n := 0
	for file := range units {
		for _, l := range unitLines(t, units, file) {
			if strings.HasPrefix(l, "PublishPort=") {
				n++
				if !strings.HasPrefix(l, "PublishPort=127.0.0.1:") {
					t.Errorf("%s: %s binds a non-loopback address", file, l)
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("no PublishPort rendered")
	}
	hasLine(t, units, "filial-feat-db.container", "PublishPort=127.0.0.1:55434:5432")
	// The debug port is the same on both sides, so RUBY_DEBUG_PORT is right in the container too.
	hasLine(t, units, "filial-feat-rails.container", "PublishPort=127.0.0.1:33002:33002")
}

// serverUnits renders the filial fixture with the rails pack's server
// replaced by server.
func serverUnits(t *testing.T, server string) map[string][]byte {
	t.Helper()
	mfs := fstest.MapFS{}
	err := fs.WalkDir(builtin.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(builtin.FS, p)
		if p == "packs/rails/pack.toml" {
			data = bytes.Replace(data, []byte(`server = "bin/dev"`), []byte(`server = "`+server+`"`), 1)
		}
		mfs[p] = &fstest.MapFile{Data: data, Mode: 0o644}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	cat, err := pack.Load(mfs)
	if err != nil {
		t.Fatal(err)
	}
	ws := fixtureWS(t)
	if err := pack.LoadLocalAgents(cat, ws); err != nil {
		t.Fatal(err)
	}
	units, err := Quadlet(mustBuild(t, catalogInputs(t, ws, cat, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return units
}

func TestPercentEscaped(t *testing.T) {
	hasLine(t, serverUnits(t, "echo 100%"), "filial-master-rails.container", "Exec=echo 100%%")
}

func TestDollarEscaped(t *testing.T) {
	// "{{" is a literal "{" in the placeholder grammar.
	hasLine(t, serverUnits(t, "echo $HOME ${{HOME}"), "filial-master-rails.container", "Exec=echo $$HOME $${HOME}")
}

func TestLineBreakRefused(t *testing.T) {
	ws := fixtureWS(t)
	m := mustBuild(t, fixtureInputs(t, ws, nil))
	m.service("rails").Command = "bin/dev\nrm -rf ~"
	_, err := Quadlet(m)
	if err == nil || !strings.Contains(err.Error(), "Exec=") || !strings.Contains(err.Error(), "line break") {
		t.Fatalf("Quadlet = %v, want a line-break error naming Exec=", err)
	}
}

func TestQuadletLintGate(t *testing.T) {
	m := mustBuild(t, fixtureInputs(t, fixtureWS(t), nil))
	rails := m.service("rails")
	rails.Mounts = append(rails.Mounts, Mount{Source: "foo", Target: "/x"})
	units, err := Quadlet(m)
	if err == nil || units != nil || !strings.Contains(err.Error(), "shared-underscore") || !strings.Contains(err.Error(), "filial-master-rails.container") {
		t.Fatalf("Quadlet = %d units, %v; want no units and a shared-underscore error naming filial-master-rails.container", len(units), err)
	}
}

// A container's env file is part of what it was created from: changing
// [env] must change the unit-hash label, or `up` keeps the old container
// (its unit file is unchanged; only the env file it names is).
func TestUnitHashCoversEnv(t *testing.T) {
	ws := fixtureWS(t)
	label := func(tz string) string {
		m := mustBuild(t, fixtureInputs(t, ws, func(p *config.Project) { p.Env["TZ"] = tz }))
		units, err := Quadlet(m)
		if err != nil {
			t.Fatal(err)
		}
		return unithash.Read(units["filial-master-rails.container"], unithash.Key("filial"))
	}
	a, b := label("Asia/Bangkok"), label("Europe/Berlin")
	if a == "" || a == b {
		t.Fatalf("rails unit-hash %q for TZ=Asia/Bangkok and %q for TZ=Europe/Berlin; want different", a, b)
	}
}

// TestWorkspaceWithSpace: keys Quadlet splits into words are quoted; keys it
// reads whole are not.
func TestWorkspaceWithSpace(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "My Projects", "filial")
	if err := os.MkdirAll(filepath.Dir(ws), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(fixtureWS(t), ws); err != nil {
		t.Fatal(err)
	}
	units, err := Quadlet(mustBuild(t, fixtureInputs(t, ws, nil)))
	if err != nil {
		t.Fatal(err)
	}
	const rails = "filial-master-rails.container"
	hasLine(t, units, rails, `EnvironmentFile="`+ws+`/.railswitch/env/master/rails.env"`)
	hasLine(t, units, rails, `GlobalArgs="--hooks-dir=`+ws+`/.railswitch/firewall/hooks.d"`)
	hasLine(t, units, rails, "Volume="+ws+"/master:/app:z")
	hasLine(t, units, rails, "Volume="+ws+"/.railswitch/assets/rails/entrypoints/entrypoint-app.sh:/usr/local/bin/entrypoint-app.sh:ro,z")
	hasLine(t, units, "filial-master-nvim.container", "Volume="+ws+"/.railswitch/home/master:/home/appuser:z")
}

func TestWorker(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, func(p *config.Project) { p.Options["jobs"] = "sidekiq@1" })
	const w = "filial-master-worker.container"
	if l := unitLines(t, units, w); l[1] != "# role: worker" {
		t.Errorf("line 2 = %q", l[1])
	}
	for _, l := range []string{
		"Requires=filial-master-deps.service filial-master-db.service filial-master-cache.service filial-master-storage.service",
		"PodmanArgs=--cpus=6", "DropCapability=all", "NoNewPrivileges=true", "Memory=2g", "PidsLimit=4096",
		"UserNS=keep-id:uid=1000,gid=1000", "Exec=bundle exec sidekiq",
	} {
		hasLine(t, units, w, l)
	}
}

func TestQuoting(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, nil)
	// redis's `--save ""`: an empty argument survives as a quoted word.
	hasLine(t, units, "filial-master-cache.container", `Exec=redis-server --loglevel warning --save "" --appendonly no`)
	// The redirect regex's $ is escaped for systemd.
	hasLine(t, units, "filial-master-storage.container",
		"Label=traefik.http.middlewares.filial-master-s3-ui-root.redirectregex.replacement=http://$${1}/rustfs/console/index.html")
	if got := quoteWord("a b"); got != `"a b"` {
		t.Errorf(`quoteWord("a b") = %s`, got)
	}
	if got := quoteWord(`say "hi"`); got != `"say \"hi\""` {
		t.Errorf(`quoteWord(say "hi") = %s`, got)
	}
}

func TestPlaywrightNoHook(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, nil)
	noLinePrefix(t, units, "filial-master-browser.container", "GlobalArgs=--hooks-dir")
	noLinePrefix(t, units, "filial-master-browser.container", "Annotation=filial.firewall")
	hasLine(t, units, "filial-master-rails.container", "Annotation=filial.firewall.networks=filial-master-dev,filial_proxy")
	hasLine(t, units, "filial-master-deps.container", "Annotation=filial.firewall=deps")
	hasLine(t, units, "filial-master-deps.container", "Annotation=filial.firewall.networks=filial-master-dev")
	hasLine(t, units, "filial-master-nvim.container", "Annotation=filial.firewall=agent,deps")
	hasLine(t, units, "filial-master-nvim.container", "Annotation=filial.firewall.mode=log")
}

func TestHardening(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, nil)
	for _, svc := range []string{"rails", "deps", "nvim"} {
		file := "filial-master-" + svc + ".container"
		hasLine(t, units, file, "DropCapability=all")
		hasLine(t, units, file, "NoNewPrivileges=true")
	}
	hasLine(t, units, "filial-master-db.container", "NoNewPrivileges=true")
	noLinePrefix(t, units, "filial-master-db.container", "DropCapability")
	hasLine(t, units, "filial-master-browser.container", "AddCapability=NET_ADMIN")
	hasLine(t, units, "filial-master-browser.container", "AddCapability=NET_RAW")
	hasLine(t, units, "filial-master-browser.container", "NoNewPrivileges=true")
	noLinePrefix(t, units, "filial-master-browser.container", "DropCapability")
}

func TestLimits(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, nil)
	hasLine(t, units, "filial-master-rails.container", "Memory=4g")
	hasLine(t, units, "filial-master-rails.container", "PidsLimit=4096")
	hasLine(t, units, "filial-master-db.container", "Memory=2g")
	hasLine(t, units, "filial-master-db.container", "PidsLimit=1024")

	_, units = renderUnits(t, fixtureDir, func(p *config.Project) {
		ov := p.Services["rails"]
		ov.Memory = "6g"
		p.Services["rails"] = ov
	})
	hasLine(t, units, "filial-master-rails.container", "Memory=6g")
}

func TestAppHealth(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, nil)
	const rails = "filial-master-rails.container"
	for _, l := range []string{
		"HealthCmd=curl -fsS http://localhost:3000/up", "HealthInterval=10s", "HealthRetries=3",
		"HealthStartPeriod=120s", "Notify=healthy", "TimeoutStartSec=180",
	} {
		hasLine(t, units, rails, l)
	}

	_, units = renderUnits(t, fixtureDir, func(p *config.Project) {
		ov := p.Services["rails"]
		ov.Health = "none"
		p.Services["rails"] = ov
	})
	noLinePrefix(t, units, rails, "Health")
	noLinePrefix(t, units, rails, "Notify=")
}

func TestAppHealthLongStartPeriod(t *testing.T) {
	ws := fixtureWS(t)
	in := fixtureInputs(t, ws, nil)
	in.Stack.Pack.App.HealthStartPeriod = "3m"
	units, err := Quadlet(mustBuild(t, in))
	if err != nil {
		t.Fatal(err)
	}
	hasLine(t, units, "filial-master-rails.container", "TimeoutStartSec=240")
}

func TestQuadletWorktreeVersionOverride(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, nil)
	hasLine(t, units, "filial-feat-db.container", "Image=docker.io/library/postgres:17")
	hasLine(t, units, "filial-feat-db.container", "Volume=filial-feat-db-data-pg17:/var/lib/postgresql/data")
	hasLine(t, units, "filial-master-db.container", "Image=docker.io/library/postgres:16")
	hasLine(t, units, "filial-master-db.container", "Volume=filial-master-db-data-pg16:/var/lib/postgresql/data")
}

func TestUnitsHaveNoSecrets(t *testing.T) {
	ws, units := renderUnits(t, fixtureDir, nil)
	gen, err := config.ReadGenerated(filepath.Join(ws, "mise.local.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(gen) == 0 {
		t.Fatal("fixture has no secret values")
	}
	for file, data := range units {
		for name, v := range gen {
			if bytes.Contains(data, []byte(v)) {
				t.Errorf("%s contains the value of %s", file, name)
			}
		}
	}
}

func TestNoUnitVars(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, nil)
	for file, data := range units {
		s := strings.ReplaceAll(string(data), "$${", "")
		if strings.Contains(s, "${") {
			t.Errorf("%s holds a ${VAR} for systemd to expand:\n%s", file, data)
		}
	}
}

func TestProxyAndDevNetworks(t *testing.T) {
	ws, units := renderUnits(t, fixtureDir, nil)
	for _, l := range []string{"NetworkName=filial_proxy", "Subnet=10.214.3.0/24", "Gateway=10.214.3.1", "IPRange=10.214.3.128/25"} {
		hasLine(t, units, "filial-proxy.network", l)
	}
	hasLine(t, units, "filial-feat-dev.network", "NetworkName=filial-feat-dev")
	hasLine(t, units, "filial-feat-dev.network", "Options=mtu=1400")
	hasLine(t, units, "filial-feat-rails.container", "Network=filial-feat-dev.network:alias=rails")
	hasLine(t, units, "filial-feat-rails.container", "Network=filial-proxy.network")
	hasLine(t, units, "filial-feat-rails.container", "AddHost=s3-ui.feat.filial.localhost:10.214.3.2")
	hasLine(t, units, "filial-feat-rails.container", "EnvironmentFile="+ws+"/.railswitch/env/feat/rails.env")
}

func TestAppDependencies(t *testing.T) {
	ws, units := renderUnits(t, fixtureDir, nil)
	const rails = "filial-master-rails.container"
	hasLine(t, units, rails, "Requires=filial-master-deps.service filial-master-db.service filial-master-cache.service filial-master-storage.service")
	hasLine(t, units, rails, "Wants=filial-master-browser.service filial-master-nvim.service filial-master-agent-probe.service railswitch-traefik.service railswitch-dozzle.service")
	hasLine(t, units, rails, "UserNS=keep-id:uid=1000,gid=1000")
	hasLine(t, units, rails, "User=1000:1000")
	hasLine(t, units, rails, "PodmanArgs=--cpus=6")
	hasLine(t, units, "filial-master-nvim.container", "PodmanArgs=--cpus=2 --tty --interactive")
	hasLine(t, units, "filial-master-nvim.container", "SuccessExitStatus=1")
	hasLine(t, units, "filial-master-nvim.container", "Volume="+ws+"/.railswitch/home/master:/home/appuser:z")
	hasLine(t, units, "filial-master-db.container", "Notify=healthy")
	noLinePrefix(t, units, "filial-master-db.container", "UserNS=")
}

const ghMount = "Secret=filial_github_token,type=mount,target=/run/secrets/github_token,uid=1000,gid=1000,mode=0400"

func TestUnitSecretLines(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, nil)
	hasLine(t, units, "filial-master-nvim.container", ghMount)
	for _, l := range unitLines(t, units, "filial-master-rails.container") {
		if strings.HasPrefix(l, "Secret=filial_github_token") {
			t.Errorf("rails: unexpected %q", l)
		}
	}
	golden := readGolden(t, "filial", "filial-master-nvim.container")
	if !strings.Contains(golden, "\n"+ghMount+"\n") {
		t.Errorf("golden filial-master-nvim.container lacks %q", ghMount)
	}
	_, units = renderUnits(t, fixtureDir, func(p *config.Project) {
		s := p.Secrets["github_token"]
		s.Env = "GH_TOKEN"
		p.Secrets["github_token"] = s
	})
	hasLine(t, units, "filial-master-nvim.container", "Secret=filial_github_token,type=env,target=GH_TOKEN")
}

func TestEnvSecretForApp(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, nil)
	const line = "Secret=filial_twilio_auth_token,type=env,target=TWILIO_AUTH_TOKEN"
	hasLine(t, units, "filial-master-rails.container", line)
	for _, f := range []string{"filial-master-deps.container", "filial-master-nvim.container"} {
		noLinePrefix(t, units, f, "Secret=filial_twilio_auth_token")
	}
	_, in := filial(t, nil)
	want := []string{"--secret", "filial_twilio_auth_token,type=env,target=TWILIO_AUTH_TOKEN"}
	if got := in.SecretArgs("rails"); !slices.Equal(got, want) {
		t.Errorf("SecretArgs(rails) = %q, want %q", got, want)
	}
}

func readGolden(t *testing.T, dir, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "golden", dir, file))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestUnitHashLabel: every .container carries <prefix>.unit-hash, the hash
// of the unit with that label line excluded plus the service's env file;
// networks carry none.
func TestUnitHashLabel(t *testing.T) {
	m := mustBuild(t, fixtureInputs(t, fixtureWS(t), nil))
	units, err := Quadlet(m)
	if err != nil {
		t.Fatal(err)
	}
	envOf := map[string][]byte{}
	for _, wt := range m.Inputs.Worktrees {
		in := mustInstance(t, m, wt.Slug)
		for _, s := range m.Services {
			envOf[naming.Container("filial", wt.Slug, s.ID)+".container"] = EnvFile(in.Env[s.ID])
		}
	}
	key := unithash.Key("filial")
	for file, data := range units {
		h := unithash.Read(data, key)
		if !strings.HasSuffix(file, ".container") {
			if h != "" {
				t.Errorf("%s has a unit-hash label", file)
			}
			continue
		}
		rest := bytes.Replace(data, []byte("Label="+key+"="+h+"\n"), nil, 1)
		if want := unithash.Sum(rest, envOf[file]); h == "" || h != want {
			t.Errorf("%s: unit-hash %q; want %q", file, h, want)
		}
	}
}

// TestDepsExecFromInstall: the pack's [app] install is the deps service's
// command, run through its entrypoint.
func TestDepsExecFromInstall(t *testing.T) {
	ws := testfixture.Workspace(t, fixtureDir)
	cat, err := pack.LoadCatalog(ws)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cat.Packs {
		p.App.Install = "mix deps.get --only dev"
	}
	units, err := Quadlet(mustBuild(t, catalogInputs(t, ws, cat, nil)))
	if err != nil {
		t.Fatal(err)
	}
	hasLine(t, units, "filial-master-deps.container", "Entrypoint=/usr/local/bin/entrypoint-deps.sh")
	hasLine(t, units, "filial-master-deps.container", "Exec=mix deps.get --only dev")
}

func TestWorkerOnProxy(t *testing.T) {
	_, units := renderUnits(t, fixtureDir, func(p *config.Project) { p.Options["jobs"] = "sidekiq@1" })
	const w = "filial-master-worker.container"
	for _, l := range []string{
		"Network=filial-master-dev.network:alias=worker",
		"Network=filial-proxy.network",
		"AddHost=s3.master.filial.localhost:10.214.3.2",
	} {
		hasLine(t, units, w, l)
	}
	noLinePrefix(t, units, w, "Label=traefik")
}

func TestShareAddHostWorker(t *testing.T) {
	units, err := Quadlet(sharedModel(t, func(p *config.Project) { p.Options["jobs"] = "sidekiq@1" }))
	if err != nil {
		t.Fatal(err)
	}
	hasLine(t, units, "filial-master-worker.container", "AddHost=box.tail1234.ts.net:100.64.0.7")
	noLinePrefix(t, units, "filial-master-db.container", "AddHost=")
}

func TestUnroutedToolOptionOffProxy(t *testing.T) {
	ws := testfixture.Workspace(t, fixtureDir)
	cat, err := pack.LoadCatalog(ws)
	if err != nil {
		t.Fatal(err)
	}
	cat.Options[pack.Ref{Name: "playwright", Version: 1}].Routes = nil
	units, err := Quadlet(mustBuild(t, catalogInputs(t, ws, cat, nil)))
	if err != nil {
		t.Fatalf("Quadlet: %v", err)
	}
	const b = "filial-master-browser.container"
	noLinePrefix(t, units, b, "Network=filial-proxy.network")
	noLinePrefix(t, units, b, "AddHost=")
}

// goldenFiles returns every file of golden dir whose name ends in suffix,
// keyed by name.
func goldenFiles(t *testing.T, dir, suffix string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("testdata", "golden", dir))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			out[e.Name()] = readGolden(t, dir, e.Name())
		}
	}
	if len(out) == 0 {
		t.Fatalf("no %s files in golden %s", suffix, dir)
	}
	return out
}

// TestRouterClientIP: every router of the filial goldens (subnet 3) matches
// only its host, and only clients in the project's subnet or the host (D50).
func TestRouterClientIP(t *testing.T) {
	ruleLabel := regexp.MustCompile(`^Label="traefik\.http\.routers\.[^.]+\.rule=(Host\(` + "`([^`]+)`" + `\).*)"$`)
	rules := 0
	for file, body := range goldenFiles(t, "filial", ".container") {
		for line := range strings.SplitSeq(body, "\n") {
			if !strings.Contains(line, ".rule=") {
				continue
			}
			m := ruleLabel.FindStringSubmatch(line)
			if m == nil {
				t.Errorf("%s: unexpected rule label %q", file, line)
				continue
			}
			rules++
			want := "Host(`" + m[2] + "`) && (ClientIP(`10.214.3.0/24`) || " + machine.HostClientClause() + ")"
			if m[1] != want {
				t.Errorf("%s: rule\n%s\nwant\n%s", file, m[1], want)
			}
		}
	}
	if rules != 8 {
		t.Errorf("found %d router rules in filial goldens; want 8", rules)
	}
}

// TestNetworkIsolate: every network unit pins isolate=strict, so a container
// reaches another project, or any other bridge network, only through Traefik
// (D50).
func TestNetworkIsolate(t *testing.T) {
	for file, body := range goldenFiles(t, "filial", ".network") {
		if !strings.Contains(body, "\nOptions=isolate=strict\n") {
			t.Errorf("%s lacks Options=isolate=strict:\n%s", file, body)
		}
	}
}
