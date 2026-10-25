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
