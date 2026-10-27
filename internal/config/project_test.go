package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const filial = "../../testdata/fixtures/filial/project.toml"

func loadFilial(t *testing.T) *Project {
	t.Helper()
	p, err := Load(filial)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadFilial(t *testing.T) {
	p := loadFilial(t)
	if p.Schema != 1 {
		t.Errorf("Schema = %d", p.Schema)
	}
	if got := p.Options["cache"]; got != "redis@1" {
		t.Errorf(`Options["cache"] = %q`, got)
	}
	if got := p.Worktrees["postgres-trial"].Versions["postgres"]; got != "17" {
		t.Errorf("postgres-trial postgres = %q", got)
	}
	if got := p.Secrets["twilio_auth_token"].Env; got != "TWILIO_AUTH_TOKEN" {
		t.Errorf("twilio env = %q", got)
	}
	if p.Image.Base != "containerfiles/Containerfile.base" {
		t.Errorf("Image.Base = %q", p.Image.Base)
	}
	if p.Project.Prefix != "filial" || p.Project.BaseBranch != "master" || p.Stack.App != "rails" {
		t.Errorf("project/stack = %+v %+v", p.Project, p.Stack)
	}
	if len(p.Links) != 1 || p.Links[0].URL != "{worktree.host}/admin" {
		t.Errorf("Links = %+v", p.Links)
	}
	if err := p.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project.toml")
	src := "schema = 1\n[optins]\ncache = \"redis@1\"\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "optins") {
		t.Fatalf("Load = %v; want an error naming optins", err)
	}
}

func TestUnknownNestedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project.toml")
	src := "schema = 1\n[services.rails]\nmemroy = \"6g\"\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "services.rails.memroy") {
		t.Fatalf("Load = %v; want an error naming services.rails.memroy", err)
	}
}

func TestLoadNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project.toml")
	src := "schema = 2\n[future]\nkey = 1\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "newer railswitch") {
		t.Fatalf("Load = %v; want a newer-railswitch error", err)
	}
}

func TestLoadNewerSchemaChangedType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project.toml")
	src := "schema = 2\nlinks = \"x\"\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "newer railswitch") {
		t.Fatalf("Load = %v; want a newer-railswitch error", err)
	}
}

func TestValidate(t *testing.T) {
	setSecret := func(p *Project, name string, s Secret) {
		p.Secrets = map[string]Secret{name: s}
	}
	ok := Secret{Services: []string{"rails"}}
	cases := []struct {
		name   string
		mutate func(p *Project)
		want   []string
	}{
		{"prefix upper", func(p *Project) { p.Project.Prefix = "Filial" }, []string{"project.prefix", "Filial"}},
		{"prefix dash", func(p *Project) { p.Project.Prefix = "fi-l" }, []string{"project.prefix", "fi-l"}},
		{"prefix short", func(p *Project) { p.Project.Prefix = "f" }, []string{"project.prefix"}},
		{"unknown slot", func(p *Project) { p.Options["worker"] = "sidekiq@1" }, []string{"options.worker"}},
		{"ref without major", func(p *Project) { p.Options["database"] = "postgres" }, []string{"options.database", `"postgres"`}},
		{"ref version zero", func(p *Project) { p.Options["database"] = "postgres@0" }, []string{"options.database", `"postgres@0"`}},
		{"stack app hsts", func(p *Project) { p.Stack.App = "app" }, []string{"stack.app", "HSTS"}},
		{"secret dash", func(p *Project) { setSecret(p, "db-pass", ok) }, []string{"secrets.db-pass"}},
		{"env newline", func(p *Project) { p.Env["TZ"] = "Asia\nBangkok" }, []string{"env.TZ", "newline"}},
		{"service env newline", func(p *Project) {
			p.Services["rails"].Env["HEADLESS_SYSTEM_TESTS"] = "0\n1"
		}, []string{"services.rails.env.HEADLESS_SYSTEM_TESTS", "newline"}},
		{"link placeholder", func(p *Project) { p.Links[0].URL = "{worktree.hots}/admin" }, []string{"links[0].url", "worktree.hots"}},
		{"worktree slug", func(p *Project) {
			p.Worktrees["Bad Slug"] = WorktreeOverride{}
		}, []string{`worktrees."Bad Slug"`}},
		{"firewall mode", func(p *Project) { p.Firewall.Mode = "strict" }, []string{"firewall.mode", "strict"}},
		{"addon dockerfile", func(p *Project) { p.Image.Addons = []string{"Dockerfile"} }, []string{"image.addons[0]", "Dockerfile"}},
		{"addon outside", func(p *Project) {
			p.Image.Addons = []string{"containerfiles/Containerfile.pg-client", "../x/Containerfile.y"}
		}, []string{"image.addons[1]", "../x/Containerfile.y"}},
		{"no base", func(p *Project) { p.Image.Base = "" }, []string{"image.base", "railswitch containerfile eject base"}},
		{"bad base", func(p *Project) { p.Image.Base = "Containerfile" }, []string{"image.base", "Containerfile"}},
		{"secret name case", func(p *Project) { setSecret(p, "Twilio-Token", ok) }, []string{"secrets.Twilio-Token"}},
		{"secret scope", func(p *Project) {
			setSecret(p, "tok", Secret{Services: []string{"rails"}, Scope: "global"})
		}, []string{"secrets.tok.scope", "global"}},
		{"secret env", func(p *Project) {
			setSecret(p, "tok", Secret{Services: []string{"rails"}, Env: "1X"})
		}, []string{"secrets.tok.env", "1X"}},
		{"secret no services", func(p *Project) {
			setSecret(p, "tok", Secret{Services: []string{}})
		}, []string{"secrets.tok.services"}},
		{"no schema", func(p *Project) { p.Schema = 0 }, []string{"schema", "schema = 1"}},
		{"newer schema", func(p *Project) { p.Schema = 2 }, []string{"schema", "newer railswitch"}},
		{"memory", func(p *Project) {
			s := p.Services["rails"]
			s.Memory = "lots"
			p.Services["rails"] = s
		}, []string{"services.rails.memory", "lots"}},
		{"pids_limit", func(p *Project) {
			s := p.Services["rails"]
			s.PidsLimit = -5
			p.Services["rails"] = s
		}, []string{"services.rails.pids_limit", "-5"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := loadFilial(t)
			c.mutate(p)
			err := p.Validate()
			if err == nil {
				t.Fatal("Validate = nil; want error")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("Validate = %q; want it to contain %q", err, w)
				}
			}
		})
	}
}

func TestValidateJoinsAll(t *testing.T) {
	p := loadFilial(t)
	p.Project.Prefix = "F"
	p.Firewall.Mode = "strict"
	err := p.Validate()
	if err == nil || !strings.Contains(err.Error(), "project.prefix") || !strings.Contains(err.Error(), "firewall.mode") {
		t.Fatalf("Validate = %v; want both problems", err)
	}
}

func TestAgentConfigValidate(t *testing.T) {
	good := []Agent{
		{Use: "claude@1"},
		{Use: "local:probe@1"},
		{Use: "claude@1", Run: "on-demand"},
		{Use: "claude@1", Run: "service", Env: map[string]string{"X": "1"}, Allow: []string{"api.example.com"}},
	}
	for i, a := range good {
		p := loadFilial(t)
		p.Agents = map[string]Agent{"probe": a}
		if err := p.Validate(); err != nil {
			t.Errorf("good[%d] %+v: %v", i, a, err)
		}
	}
	ok := Agent{Use: "claude@1"}
	cases := []struct {
		name  string
		agent string
		a     Agent
		want  []string
	}{
		{"upper", "Bad", ok, []string{"agents.Bad"}},
		{"ls", "ls", ok, []string{"agents.ls", "reserved"}},
		{"base", "base", ok, []string{"agents.base", "reserved"}},
		{"stop", "stop", ok, []string{"agents.stop", "reserved"}},
		{"underscore", "a_b", ok, []string{"agents.a_b"}},
		{"long", strings.Repeat("a", 26), ok, []string{"agents." + strings.Repeat("a", 26)}},
		{"run", "x", Agent{Use: "claude@1", Run: "always"}, []string{"agents.x.run", "always"}},
		{"env", "x", Agent{Use: "claude@1", Env: map[string]string{"1X": "v"}}, []string{"agents.x.env.1X"}},
		{"allow", "x", Agent{Use: "claude@1", Allow: []string{"a b"}}, []string{"agents.x.allow[0]", "a b"}},
		{"local zero", "x", Agent{Use: "local:x@0"}, []string{"agents.x.use", "local:x@0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := loadFilial(t)
			p.Agents = map[string]Agent{c.agent: c.a}
			err := p.Validate()
			if err == nil {
				t.Fatal("Validate = nil; want error")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("Validate = %q; want it to contain %q", err, w)
				}
			}
		})
	}
}

func TestStackLocalRefused(t *testing.T) {
	p := loadFilial(t)
	p.Options["database"] = "local:pg@1"
	err := p.Validate()
	if err == nil || !strings.Contains(err.Error(), "options.database") || !strings.Contains(err.Error(), "agents only until Stage 5") {
		t.Fatalf("Validate = %v", err)
	}
	p = loadFilial(t)
	p.Stack.Pack = "local:rails@1"
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "stack.pack") || !strings.Contains(err.Error(), "agents only until Stage 5") {
		t.Fatalf("Validate = %v", err)
	}
}

// C1, D54: [tools.nvim] is a table; the old bool is refused, and unknown
// keys under it are errors (the custom decoder checks them itself).
func TestToolsNvimTable(t *testing.T) {
	load := func(src string) (*Project, error) {
		path := filepath.Join(t.TempDir(), "project.toml")
		if err := os.WriteFile(path, []byte("schema = 1\n"+src), 0o644); err != nil {
			t.Fatal(err)
		}
		return Load(path)
	}
	p, err := load("[tools.nvim]\n")
	if err != nil || p.Tools.Nvim == nil {
		t.Fatalf("empty [tools.nvim]: %+v, %v; want non-nil", p, err)
	}
	p, err = load("[tools.nvim]\nconfig_repo = \"https://github.com/u/cfg\"\nconfig_ref = \"v2\"\n")
	if err != nil || p.Tools.Nvim == nil || p.Tools.Nvim.ConfigRepo != "https://github.com/u/cfg" || p.Tools.Nvim.ConfigRef != "v2" {
		t.Fatalf("config_repo: %+v, %v", p.Tools.Nvim, err)
	}
	if _, err := load("[tools]\nnvim = true\n"); err == nil || !strings.Contains(err.Error(), "[tools.nvim]") {
		t.Errorf("nvim = true: %v; want an error naming [tools.nvim]", err)
	}
	if _, err := load("[tools.nvim]\nconfig_rpo = \"x\"\n"); err == nil || !strings.Contains(err.Error(), "config_rpo") {
		t.Errorf("config_rpo: %v; want an error naming config_rpo", err)
	}
	if p, err := load(""); err != nil || p.Tools.Nvim != nil {
		t.Errorf("no [tools]: %+v, %v; want nil", p.Tools.Nvim, err)
	}
}

func TestGitSigningValidate(t *testing.T) {
	for _, c := range []struct {
		name string
		sec  Secret
		want []string
	}{
		{"env", Secret{Services: []string{"rails"}, GitSigning: true, Env: "X"}, []string{"secrets.sign.git_signing", "file"}},
		{"git_host", Secret{Services: []string{"rails"}, GitSigning: true, GitHost: "github.com"}, []string{"secrets.sign.git_signing", "git_host"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := loadFilial(t)
			p.Secrets = map[string]Secret{"sign": c.sec}
			err := p.Validate()
			if err == nil {
				t.Fatal("Validate = nil; want error")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("Validate = %q; want it to contain %q", err, w)
				}
			}
		})
	}
	p := loadFilial(t)
	p.Secrets = map[string]Secret{"sign": {Services: []string{"rails"}, GitSigning: true}}
	if err := p.Validate(); err != nil {
		t.Errorf("file-delivered signing key: %v", err)
	}
}
