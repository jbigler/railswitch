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
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/pack/builtin"
)

var update = flag.Bool("update", false, "rewrite testdata/golden")

const minimalDir = "../../testdata/fixtures/minimal"

// renderUnits renders the fixture dir's units with mutate applied.
func renderUnits(t *testing.T, dir string, mutate func(*config.Project)) (string, map[string][]byte) {
	t.Helper()
	ws := fixtureWSOf(t, dir)
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

func TestGolden(t *testing.T) {
	for _, dir := range []string{fixtureDir, minimalDir} {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			ws, units := renderUnits(t, dir, nil)
			golden := filepath.Join("testdata", "golden", name)
			// The workspace is a temp dir: goldens hold it as /WORKSPACES/<name>.
			norm := func(b []byte) []byte {
				return bytes.ReplaceAll(b, []byte(filepath.Dir(ws)), []byte("/WORKSPACES"))
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
}

func TestPercentEscaped(t *testing.T) {
	mfs := fstest.MapFS{}
	err := fs.WalkDir(builtin.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(builtin.FS, p)
		if p == "packs/rails/pack.toml" {
			data = bytes.Replace(data, []byte(`server = "bin/dev"`), []byte(`server = "echo 100%"`), 1)
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
	units, err := Quadlet(mustBuild(t, catalogInputs(t, ws, cat, nil)))
	if err != nil {
		t.Fatal(err)
	}
	hasLine(t, units, "filial-master-rails.container", "Exec=echo 100%%")
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
	hasLine(t, units, rails, "Wants=filial-master-browser.service filial-master-nvim.service railswitch-traefik.service")
	hasLine(t, units, rails, "UserNS=keep-id:uid=1000,gid=1000")
	hasLine(t, units, rails, "User=1000:1000")
	hasLine(t, units, rails, "PodmanArgs=--cpus=6")
	hasLine(t, units, "filial-master-nvim.container", "PodmanArgs=--cpus=2 --tty --interactive")
	hasLine(t, units, "filial-master-nvim.container", "SuccessExitStatus=1")
	hasLine(t, units, "filial-master-nvim.container", "Volume="+ws+"/.railswitch/home/master:/home/appuser:z")
	hasLine(t, units, "filial-master-db.container", "Notify=healthy")
	noLinePrefix(t, units, "filial-master-db.container", "UserNS=")
}
