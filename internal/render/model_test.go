package render

import (
	"bytes"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/pack/builtin"
	"github.com/jbigler/railswitch/internal/testfixture"
)

const fixtureDir = "../../testdata/fixtures/filial"

// fixtureWS copies the Filial fixture to a temp workspace (see testfixture.Workspace).
func fixtureWS(t *testing.T) string {
	t.Helper()
	return testfixture.Workspace(t, fixtureDir)
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixtureInputs loads the workspace's project.toml, applies mutate (may be
// nil), and resolves it against the workspace's catalog (the built-ins plus
// its agents/), with worktrees master (ID 1) and feat (ID 2), each when the
// workspace has its directory.
func fixtureInputs(t *testing.T, ws string, mutate func(*config.Project)) Inputs {
	t.Helper()
	cat, err := pack.LoadCatalog(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	return catalogInputs(t, ws, cat, mutate)
}

// catalogInputs is fixtureInputs resolved against cat.
func catalogInputs(t *testing.T, ws string, cat *pack.Catalog, mutate func(*config.Project)) Inputs {
	t.Helper()
	p, err := config.Load(filepath.Join(ws, "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(p)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	st, err := cat.Resolve(p)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := config.ReadGenerated(filepath.Join(ws, "mise.local.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var wts []Worktree
	for i, slug := range []string{"master", "feat"} {
		dir := filepath.Join(ws, slug)
		if _, err := os.Stat(dir); err == nil {
			wts = append(wts, Worktree{Slug: slug, ID: i + 1, Dir: dir, Branch: slug})
		}
	}
	return Inputs{
		Workspace: ws,
		Project:   p,
		Stack:     st,
		Lock:      &config.Lock{Schema: config.LockSchemaVersion},
		Machine:   machine.ProjectEntry{Prefix: p.Project.Prefix, Workspace: ws, Subnet: 3},
		Worktrees: wts,
		Generated: gen,
		Host:      Host{CPUs: 8, Home: "/home/dev", GitName: "Dev Eloper", GitEmail: "dev@example.com"},
	}
}

func mustBuild(t *testing.T, in Inputs) *Model {
	t.Helper()
	m, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return m
}

func mustInstance(t *testing.T, m *Model, slug string) *Instance {
	t.Helper()
	for _, wt := range m.Inputs.Worktrees {
		if wt.Slug == slug {
			in, err := m.Instance(wt)
			if err != nil {
				t.Fatalf("Instance(%s): %v", slug, err)
			}
			return in
		}
	}
	t.Fatalf("no worktree %s", slug)
	return nil
}

// buildErr builds the model and every worktree's instance, returning the
// first error.
func buildErr(in Inputs) error {
	m, err := Build(in)
	if err != nil {
		return err
	}
	for _, wt := range in.Worktrees {
		if _, err := m.Instance(wt); err != nil {
			return err
		}
	}
	return nil
}

func filial(t *testing.T, mutate func(*config.Project)) (*Model, *Instance) {
	t.Helper()
	m := mustBuild(t, fixtureInputs(t, fixtureWS(t), mutate))
	return m, mustInstance(t, m, "master")
}

func envVar(in *Instance, svc, name string) (EnvVar, bool) {
	for _, e := range in.Env[svc] {
		if e.Name == name {
			return e, true
		}
	}
	return EnvVar{}, false
}

func service(t *testing.T, m *Model, id string) *Service {
	t.Helper()
	for i := range m.Services {
		if m.Services[i].ID == id {
			return &m.Services[i]
		}
	}
	t.Fatalf("no service %s", id)
	return nil
}

func TestFixedServiceIDs(t *testing.T) {
	m, in := filial(t, nil)
	var ids []string
	for _, s := range m.Services {
		ids = append(ids, s.ID)
	}
	want := []string{"rails", "deps", "db", "cache", "storage", "browser", "nvim", "agent-claude", "agent-probe"}
	if !slices.Equal(ids, want) {
		t.Errorf("services = %v; want %v", ids, want)
	}
	if e, _ := envVar(in, "rails", "REDIS_URL"); e.Value != "redis://cache:6379/1" {
		t.Errorf("rails REDIS_URL = %q", e.Value)
	}
}

// TestPortBases: every [[ports]] base of the pack, options and tools, by
// port name.
func TestPortBases(t *testing.T) {
	m, _ := filial(t, nil)
	want := map[string]int{"app": 13000, "debug": 33000, "nvim": 17000, "playwright": 18888, "s3": 19000, "db": 55432}
	if got := m.PortBases(); !maps.Equal(got, want) {
		t.Errorf("PortBases = %v; want %v", got, want)
	}
}

func TestLayerPrecedence(t *testing.T) {
	_, in := filial(t, func(p *config.Project) {
		p.Services["rails"].Env["TZ"] = "UTC"
	})
	if e, _ := envVar(in, "rails", "TZ"); e.Value != "UTC" || e.Layer != LayerService {
		t.Errorf("rails TZ = %+v; want UTC at LayerService", e)
	}
	if e, _ := envVar(in, "db", "TZ"); e.Value != "Asia/Bangkok" || e.Layer != LayerProject {
		t.Errorf("db TZ = %+v; want Asia/Bangkok at LayerProject", e)
	}
}

func TestDatabaseURL(t *testing.T) {
	_, in := filial(t, nil)
	e, _ := envVar(in, "rails", "DATABASE_URL")
	if e.Value != "postgres://postgres:s3cret@db/filial_dev" {
		t.Errorf("rails DATABASE_URL = %q", e.Value)
	}
	if e.Layer != LayerOption {
		t.Errorf("DATABASE_URL layer = %v", e.Layer)
	}
	if got := in.Values["db"]; got != "filial_dev" {
		t.Errorf(`Values["db"] = %q`, got)
	}
}

func TestEnvScope(t *testing.T) {
	m, in := filial(t, nil)
	if e, _ := envVar(in, "db", "PGTZ"); e.Value != "Asia/Bangkok" {
		t.Errorf("db PGTZ = %q", e.Value)
	}
	for _, svc := range []string{"rails", "nvim"} {
		if e, _ := envVar(in, svc, "PLAYWRIGHT_HOST"); e.Value != "ws://browser:8888/connect" {
			t.Errorf("%s PLAYWRIGHT_HOST = %q", svc, e.Value)
		}
	}
	if _, ok := envVar(in, "browser", "PLAYWRIGHT_HOST"); ok {
		t.Errorf("browser got its own [gives]")
	}
	// D29: the libpq variables never reach the db service.
	if _, ok := envVar(in, "db", "PGHOST"); ok {
		t.Errorf("db got PGHOST")
	}
	// {dumps} is the workspace's db-dumps/ (db dump, db restore, D122).
	if want := filepath.Join(m.Inputs.Workspace, "db-dumps"); in.Values["dumps"] != want {
		t.Errorf("{dumps} = %q; want %q", in.Values["dumps"], want)
	}

	// No TZ anywhere: PGTZ = "{env.TZ}" is dropped, not an error.
	_, in = filial(t, func(p *config.Project) { delete(p.Env, "TZ") })
	if e, ok := envVar(in, "db", "PGTZ"); ok {
		t.Errorf("db PGTZ = %q with no TZ set", e.Value)
	}
	if _, ok := in.Values["env.TZ"]; ok {
		t.Errorf("env.TZ is set")
	}
}

// A project env value may name an env layer that holds a generated value:
// the service env gets it expanded, Values (names, labels, links) never.
func TestEnvRefToGenerated(t *testing.T) {
	_, in := filial(t, func(p *config.Project) { p.Env["APP_DB_URI"] = "{env.DATABASE_URL}" })
	db, _ := envVar(in, "rails", "DATABASE_URL")
	got, ok := envVar(in, "rails", "APP_DB_URI")
	if !ok || got.Value != db.Value || !strings.Contains(got.Value, "s3cret") {
		t.Errorf("rails APP_DB_URI = %q, %v; want DATABASE_URL's %q", got.Value, ok, db.Value)
	}
	for k, v := range in.Values {
		if strings.Contains(v, "s3cret") {
			t.Errorf("Values[%s] carries a generated value", k)
		}
	}
	if _, ok := envVar(in, "deps", "APP_DB_URI"); ok {
		t.Errorf("deps got APP_DB_URI")
	}
}

// An [options] key that is not a slot runs its option as a service of that
// name, with the role its option's slot gives (D3 amended).
func TestExtraOptionService(t *testing.T) {
	m, in := filial(t, func(p *config.Project) {
		delete(p.Options, "cache")
		p.Options["sessions"] = "redis@1"
	})
	if s := service(t, m, "sessions"); s.Role != RoleData {
		t.Errorf("sessions role = %s; want data", s.Role)
	}
	if e, _ := envVar(in, "rails", "REDIS_URL"); e.Value != "redis://sessions:6379/1" {
		t.Errorf("rails REDIS_URL = %q", e.Value)
	}
}

// postgres@1 has the image create {db} at first start; a project turns it
// off with an empty value (the image then falls back to "postgres").
func TestPostgresCreatesDB(t *testing.T) {
	_, in := filial(t, nil)
	if e, ok := envVar(in, "db", "POSTGRES_DB"); !ok || e.Value != "filial_dev" {
		t.Errorf("db POSTGRES_DB = %q, %v; want filial_dev", e.Value, ok)
	}
	_, in = filial(t, func(p *config.Project) {
		p.Services["db"] = config.ServiceOverride{Env: map[string]string{"POSTGRES_DB": ""}}
	})
	if e, _ := envVar(in, "db", "POSTGRES_DB"); e.Value != "" {
		t.Errorf("db POSTGRES_DB = %q with the opt-out; want empty", e.Value)
	}
}

func TestDepsGetsNoSecrets(t *testing.T) {
	_, in := filial(t, nil)
	var names []string
	for _, e := range in.Env["deps"] {
		names = append(names, e.Name)
		if strings.Contains(e.Value, "s3cret") {
			t.Errorf("deps %s carries a generated value", e.Name)
		}
		if e.Name == "DATABASE_URL" {
			t.Errorf("deps has DATABASE_URL")
		}
	}
	// D11, D44: the pack layer, options' [deps_env] and [services.deps.env] only.
	want := []string{"BINDING", "BUNDLE_FROZEN", "COREPACK_HOME", "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD", "RAILS_DEVELOPMENT_HOSTS", "RAILS_ENV", "RUBY_DEBUG_HOST"}
	if !slices.Equal(names, want) {
		t.Errorf("deps env = %v; want %v", names, want)
	}
}

// D44: an option's [deps_env] reaches the deps service, so the playwright
// option keeps npm installs from downloading a browser it already has; its
// [gives] does the same for installs run in the app container.
func TestOptionDepsEnv(t *testing.T) {
	_, in := filial(t, nil)
	if e, _ := envVar(in, "deps", "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD"); e.Value != "1" || e.Layer != LayerOption || e.Source != "option playwright@1 [deps_env]" {
		t.Errorf("deps PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD = %+v", e)
	}
	if e, _ := envVar(in, "rails", "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD"); e.Value != "1" || e.Source != "option playwright@1 [gives]" {
		t.Errorf("rails PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD = %+v", e)
	}
	_, in = filial(t, func(p *config.Project) { p.Options["browser"] = "none" })
	for _, svc := range []string{"deps", "rails"} {
		if e, ok := envVar(in, svc, "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD"); ok {
			t.Errorf("browser = none: %s has %+v", svc, e)
		}
	}
}

// The debug gem listens on RUBY_DEBUG_HOST:RUBY_DEBUG_PORT inside the
// container; the rails pack publishes that port, so it gives the bind address.
func TestDebugHost(t *testing.T) {
	_, in := filial(t, nil)
	for _, svc := range []string{"rails", "nvim"} {
		if e, _ := envVar(in, svc, "RUBY_DEBUG_HOST"); e.Value != "0.0.0.0" || e.Layer != LayerPack {
			t.Errorf("%s RUBY_DEBUG_HOST = %+v; want 0.0.0.0 at LayerPack", svc, e)
		}
	}
}

// Rails' development host check allows `.localhost` with ONE subdomain
// label (ActionDispatch::HostAuthorization SUBDOMAIN_REGEX), so it blocks
// the D5 hostname <slug>.<prefix>.localhost with a 403 unless the pack adds
// it through RAILS_DEVELOPMENT_HOSTS (read by Rails >= 7.0 in development).
func TestRailsDevelopmentHosts(t *testing.T) {
	_, in := filial(t, nil)
	if e, _ := envVar(in, "rails", "RAILS_DEVELOPMENT_HOSTS"); e.Value != "master.filial.localhost" || e.Layer != LayerPack {
		t.Errorf("rails RAILS_DEVELOPMENT_HOSTS = %+v; want master.filial.localhost at LayerPack", e)
	}
}

func TestMissingSecret(t *testing.T) {
	ws := fixtureWS(t)
	writeFile(t, filepath.Join(ws, "mise.local.toml"), "[env]\nS3_SECRET_KEY = \"s3cret-s3\"\n")
	err := buildErr(fixtureInputs(t, ws, nil))
	if err == nil {
		t.Fatal("no error with DB_PASSWORD missing")
	}
	for _, w := range []string{"DB_PASSWORD", "mise.local.toml"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not name %s", err, w)
		}
	}

	// Present but empty (a hand-cleared line): missing too, never
	// DATABASE_URL=postgres://postgres:@db/… (Review Focus 3).
	for _, name := range []string{"DB_PASSWORD", "S3_SECRET_KEY"} {
		ws = fixtureWS(t)
		body := "[env]\nDB_PASSWORD = \"s3cret\"\nS3_SECRET_KEY = \"s3cret-s3\"\n"
		body = strings.Replace(body, name+` = "`, name+` = "" # `, 1)
		writeFile(t, filepath.Join(ws, "mise.local.toml"), body)
		err := buildErr(fixtureInputs(t, ws, nil))
		if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "mise.local.toml") {
			t.Errorf("%s = \"\": error = %v; want one naming %s and mise.local.toml", name, err, name)
		}
	}

	// An empty value an option doesn't declare in generated: missing at use.
	sc := scope{gen: map[string]string{"X": ""}}
	if _, ok := sc.Lookup("generated.X"); ok {
		t.Errorf("an empty generated value resolves")
	}

	// No mise.local.toml at all.
	ws = fixtureWS(t)
	if err := os.Remove(filepath.Join(ws, "mise.local.toml")); err != nil {
		t.Fatal(err)
	}
	if err := buildErr(fixtureInputs(t, ws, nil)); err == nil || !strings.Contains(err.Error(), "mise.local.toml") {
		t.Errorf("no mise.local.toml: error = %v", err)
	}
}

func TestValuesHaveNoSecrets(t *testing.T) {
	m, _ := filial(t, nil)
	for _, wt := range m.Inputs.Worktrees {
		in := mustInstance(t, m, wt.Slug)
		for k, v := range in.Values {
			if strings.Contains(v, "s3cret") {
				t.Errorf("%s: Values[%q] carries a generated value", wt.Slug, k)
			}
			if strings.HasPrefix(k, "generated.") {
				t.Errorf("%s: Values has %s", wt.Slug, k)
			}
		}
	}
}

func TestWorktreeVersionOverride(t *testing.T) {
	m, master := filial(t, nil)
	feat := mustInstance(t, m, "feat")
	if got := feat.Values["image.db"]; got != "docker.io/library/postgres:17" {
		t.Errorf(`feat image.db = %q`, got)
	}
	if got := feat.Values["postgres.major"]; got != "17" {
		t.Errorf(`feat postgres.major = %q`, got)
	}
	if got := master.Values["image.db"]; got != "docker.io/library/postgres:16" {
		t.Errorf(`master image.db = %q`, got)
	}
	if got := master.Values["postgres.major"]; got != "16" {
		t.Errorf(`master postgres.major = %q`, got)
	}
}

func TestDigestPin(t *testing.T) {
	ws := fixtureWS(t)
	in := fixtureInputs(t, ws, nil)
	digest := "sha256:" + strings.Repeat("ab", 32)
	in.Lock.SetDigest("docker.io/library/postgres:16", digest)
	m := mustBuild(t, in)
	if got := mustInstance(t, m, "master").Values["image.db"]; got != "docker.io/library/postgres:16@"+digest {
		t.Errorf("master image.db = %q", got)
	}
	if got := mustInstance(t, m, "feat").Values["image.db"]; got != "docker.io/library/postgres:17" {
		t.Errorf("feat image.db = %q", got)
	}

	// No stack.lock yet.
	in.Lock = nil
	if got := mustInstance(t, mustBuild(t, in), "master").Values["image.db"]; got != "docker.io/library/postgres:16" {
		t.Errorf("no lock: image.db = %q", got)
	}
}

// gitConfig returns the GIT_CONFIG_KEY_n → GIT_CONFIG_VALUE_n pairs of svc.
func gitConfig(t *testing.T, in *Instance, svc string) map[string]string {
	t.Helper()
	count, ok := envVar(in, svc, "GIT_CONFIG_COUNT")
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(count.Value)
	if err != nil {
		t.Fatalf("GIT_CONFIG_COUNT = %q", count.Value)
	}
	out := map[string]string{}
	for i := range n {
		k, _ := envVar(in, svc, "GIT_CONFIG_KEY_"+strconv.Itoa(i))
		v, _ := envVar(in, svc, "GIT_CONFIG_VALUE_"+strconv.Itoa(i))
		out[k.Value] = v.Value
	}
	return out
}

func TestSecretGitWiring(t *testing.T) {
	_, in := filial(t, func(p *config.Project) {
		p.Secrets["github_token"] = config.Secret{Services: []string{"nvim"}, GitHost: "github.com"}
	})
	if e, _ := envVar(in, "nvim", "GIT_CONFIG_COUNT"); e.Value != "2" || e.Layer != LayerSecret {
		t.Errorf("nvim GIT_CONFIG_COUNT = %+v", e)
	}
	cfg := gitConfig(t, in, "nvim")
	helper := cfg["credential.https://github.com.helper"]
	want := `!f() { test "$1" = get && printf 'username=x-access-token\npassword=%s\n' "$(cat /run/secrets/github_token)"; }; f`
	if helper != want {
		t.Errorf("credential helper = %q; want %q", helper, want)
	}
	if got := cfg["url.https://github.com/.insteadOf"]; got != "git@github.com:" {
		t.Errorf("insteadOf = %q", got)
	}
	for name, want := range map[string]string{
		"GIT_AUTHOR_NAME": "Dev Eloper", "GIT_AUTHOR_EMAIL": "dev@example.com",
		"GIT_COMMITTER_NAME": "Dev Eloper", "GIT_COMMITTER_EMAIL": "dev@example.com",
	} {
		if e, _ := envVar(in, "nvim", name); e.Value != want {
			t.Errorf("nvim %s = %q; want %q", name, e.Value, want)
		}
	}
	for _, svc := range []string{"rails", "deps", "db"} {
		for _, e := range in.Env[svc] {
			if strings.HasPrefix(e.Name, "GIT_") {
				t.Errorf("%s has %s", svc, e.Name)
			}
		}
	}

	// env delivery: the helper prints the variable instead of reading the file.
	_, in = filial(t, func(p *config.Project) {
		p.Secrets["github_token"] = config.Secret{Services: []string{"nvim"}, GitHost: "github.com", Env: "GIT_TOKEN", GitUser: "me"}
	})
	helper = gitConfig(t, in, "nvim")["credential.https://github.com.helper"]
	want = `!f() { test "$1" = get && printf 'username=me\npassword=%s\n' "$GIT_TOKEN"; }; f`
	if helper != want {
		t.Errorf("env helper = %q; want %q", helper, want)
	}
}

func TestNoGitTokenEnv(t *testing.T) {
	_, in := filial(t, func(p *config.Project) {
		p.Secrets["github_token"] = config.Secret{Services: []string{"nvim"}, GitHost: "github.com"}
	})
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if _, ok := envVar(in, "nvim", name); ok {
			t.Errorf("nvim has %s", name)
		}
	}
	if h := gitConfig(t, in, "nvim")["credential.https://github.com.helper"]; !strings.Contains(h, "/run/secrets/github_token") {
		t.Errorf("helper %q does not read /run/secrets/github_token", h)
	}
}

func TestSecretEnvCollision(t *testing.T) {
	ws := fixtureWS(t)
	err := buildErr(fixtureInputs(t, ws, func(p *config.Project) {
		p.Secrets["tz"] = config.Secret{Services: []string{"rails"}, Env: "TZ"}
	}))
	if err == nil {
		t.Fatal("no error for a secret env colliding with [env] TZ")
	}
	for _, w := range []string{"secrets.tz", "rails", "TZ"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not name %s", err, w)
		}
	}
}

func TestUnknownServiceRefs(t *testing.T) {
	for name, mutate := range map[string]func(*config.Project){
		"services": func(p *config.Project) { p.Services["railz"] = config.ServiceOverride{} },
		"secrets":  func(p *config.Project) { p.Secrets["x"] = config.Secret{Services: []string{"railz"}} },
	} {
		err := buildErr(fixtureInputs(t, fixtureWS(t), mutate))
		if err == nil || !strings.Contains(err.Error(), "railz") {
			t.Errorf("%s: error = %v; want one naming railz", name, err)
		}
	}
}

func TestPortsFromID(t *testing.T) {
	ws := fixtureWS(t)
	in := fixtureInputs(t, ws, nil)
	m := mustBuild(t, in)
	inst, err := m.Instance(Worktree{Slug: "seven", ID: 7, Dir: filepath.Join(ws, "master"), Branch: "seven"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"DB_PORT": "55439", "RUBY_DEBUG_PORT": "33007", "NVIM_PORT": "17007",
		"PLAYWRIGHT_HOST_PORT": "18895", "APP_PORT": "13007", "S3_PORT": "19007",
	}
	for name, v := range want {
		if got := inst.Values[name]; got != v {
			t.Errorf("Values[%s] = %q; want %s", name, got, v)
		}
	}
	// Each service's env has its own published ports (worktree layer).
	own := map[string][]string{
		"rails": {"RUBY_DEBUG_PORT", "APP_PORT"}, "db": {"DB_PORT"}, "storage": {"S3_PORT"},
		"browser": {"PLAYWRIGHT_HOST_PORT"}, "nvim": {"NVIM_PORT"},
	}
	for svc, names := range own {
		for _, n := range names {
			if e, _ := envVar(inst, svc, n); e.Value != want[n] || e.Layer != LayerWorktree {
				t.Errorf("%s %s = %+v; want %s at LayerWorktree", svc, n, e, want[n])
			}
		}
	}
	if e, _ := envVar(inst, "rails", "WORKTREE_ID"); e.Value != "7" {
		t.Errorf("rails WORKTREE_ID = %q", e.Value)
	}
}

// Controller ruling 1: the worktree layer reaches role-tool option services.
func TestWorktreeLayerReachesTools(t *testing.T) {
	_, in := filial(t, nil)
	for _, svc := range []string{"rails", "browser", "nvim"} {
		if e, _ := envVar(in, svc, "WORKTREE_HOST"); e.Value != "master.filial.localhost" {
			t.Errorf("%s WORKTREE_HOST = %q", svc, e.Value)
		}
		if e, _ := envVar(in, svc, "WORKTREE_SLUG"); e.Value != "master" {
			t.Errorf("%s WORKTREE_SLUG = %q", svc, e.Value)
		}
	}
	for _, svc := range []string{"deps", "db", "cache", "storage"} {
		if _, ok := envVar(in, svc, "WORKTREE_HOST"); ok {
			t.Errorf("%s has WORKTREE_HOST", svc)
		}
	}
	if e, _ := envVar(in, "browser", "CHROME_START_URL"); e.Value != "http://master.filial.localhost" {
		t.Errorf("browser CHROME_START_URL = %q", e.Value)
	}
}

// Controller ruling: pack env and options' [gives] reach tool packages only,
// never a role-tool option such as the browser (least privilege).
func TestBrowserGetsNoCredentials(t *testing.T) {
	_, in := filial(t, func(p *config.Project) {
		p.Services["browser"] = config.ServiceOverride{Env: map[string]string{"BROWSER_ALLOW_HOSTS": "example.com"}}
	})
	for _, name := range []string{"DATABASE_URL", "PGPASSWORD", "S3_SECRET_KEY", "REDIS_URL", "RAILS_ENV"} {
		if e, ok := envVar(in, "browser", name); ok {
			t.Errorf("browser has %s (%s)", name, e.Source)
		}
		if _, ok := envVar(in, "nvim", name); !ok {
			t.Errorf("nvim lacks %s", name)
		}
	}
	for name, want := range map[string]string{
		"WORKTREE_HOST":        "master.filial.localhost",
		"CHROME_START_URL":     "http://master.filial.localhost",
		"TZ":                   "Asia/Bangkok",
		"BROWSER_ALLOW_HOSTS":  "example.com",
		"PLAYWRIGHT_HOST_PORT": "18889",
	} {
		if e, _ := envVar(in, "browser", name); e.Value != want {
			t.Errorf("browser %s = %q; want %q", name, e.Value, want)
		}
	}
	for _, e := range in.Env["browser"] {
		if strings.Contains(e.Value, "s3cret") {
			t.Errorf("browser %s carries a generated value", e.Name)
		}
	}
}

func TestAssets(t *testing.T) {
	m, _ := filial(t, nil)
	cat, err := pack.LoadCatalog(fixtureDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := fs.ReadFile(cat.Packs[pack.Ref{Name: "rails", Version: 1}].Files, "entrypoints/entrypoint-app.sh")
	if err != nil {
		t.Fatal(err)
	}
	assets := map[string]Asset{}
	for _, a := range m.Assets {
		assets[a.Rel] = a
	}
	app, ok := assets[".railswitch/assets/rails/entrypoints/entrypoint-app.sh"]
	if !ok {
		t.Fatalf("no app entrypoint asset in %v", slices.Collect(maps.Keys(assets)))
	}
	if app.Mode != 0o755 || string(app.Data) != string(embedded) {
		t.Errorf("app entrypoint asset mode %v, %d bytes", app.Mode, len(app.Data))
	}
	// postgres mounts nothing: up restores the dumps itself (D122).
	if _, ok := assets[".railswitch/assets/postgres/initdb/restore-dump.sh"]; ok {
		t.Errorf("a restore-dump.sh asset")
	}
	rails := service(t, m, "rails")
	if rails.Entrypoint != "/usr/local/bin/entrypoint-app.sh" {
		t.Errorf("rails Entrypoint = %q", rails.Entrypoint)
	}
	if src := mountSource(t, rails, "/usr/local/bin/entrypoint-app.sh"); src != filepath.Join(m.Inputs.Workspace, ".railswitch/assets/rails/entrypoints/entrypoint-app.sh") {
		t.Errorf("rails entrypoint source = %q", src)
	}

	// D13: the project's own entrypoint wins and no asset is written for it.
	ws := fixtureWS(t)
	own := filepath.Join(ws, "entrypoints", "entrypoint-app.sh")
	writeFile(t, own, "#!/bin/sh\nexec \"$@\"\n")
	m = mustBuild(t, fixtureInputs(t, ws, nil))
	if src := mountSource(t, service(t, m, "rails"), "/usr/local/bin/entrypoint-app.sh"); src != own {
		t.Errorf("rails entrypoint source = %q; want %q", src, own)
	}
	for _, a := range m.Assets {
		if a.Rel == ".railswitch/assets/rails/entrypoints/entrypoint-app.sh" {
			t.Errorf("asset listed for an overridden entrypoint")
		}
	}
}

// mountSource returns the expanded source of s's mount at target.
func mountSource(t *testing.T, s *Service, target string) string {
	t.Helper()
	for _, mt := range s.Mounts {
		if mt.Target == target {
			v, err := pack.Expand(mt.Source, pack.MapScope{})
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
	}
	t.Fatalf("%s has no mount at %s", s.ID, target)
	return ""
}

func TestNamesFor(t *testing.T) {
	m, _ := filial(t, nil)
	n, err := m.NamesFor("feat")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"filial-feat-rails.service", "filial-feat-dev-network.service"} {
		if !slices.Contains(n.Units, u) {
			t.Errorf("Units %v lacks %s", n.Units, u)
		}
	}
	if !slices.Contains(n.Containers, "filial-feat-db") {
		t.Errorf("Containers %v lacks filial-feat-db", n.Containers)
	}
	wantVols := []string{"filial-feat-db-data-pg17", "filial-feat-node-modules", "filial-feat-rustfs-data"}
	if !slices.Equal(n.Volumes, wantVols) {
		t.Errorf("Volumes = %v; want %v", n.Volumes, wantVols)
	}
	if !slices.Equal(n.Networks, []string{"filial-feat-dev"}) {
		t.Errorf("Networks = %v", n.Networks)
	}
	if _, err := m.NamesFor("gone"); err == nil {
		t.Errorf("NamesFor(gone): no error")
	}
}

// NamesFor needs no generated values: wt rm and down work without them.
func TestNamesForWithoutGenerated(t *testing.T) {
	ws := fixtureWS(t)
	in := fixtureInputs(t, ws, nil)
	in.Generated = map[string]string{}
	m := mustBuild(t, in)
	if _, err := m.NamesFor("master"); err != nil {
		t.Errorf("NamesFor: %v", err)
	}
}

func TestOptionAddonWarning(t *testing.T) {
	m, _ := filial(t, func(p *config.Project) { p.Image.Addons = nil })
	if len(m.Warnings) != 1 {
		t.Fatalf("Warnings = %q; want one", m.Warnings)
	}
	for _, w := range []string{"pg-client", "eject"} {
		if !strings.Contains(m.Warnings[0], w) {
			t.Errorf("warning %q lacks %q", m.Warnings[0], w)
		}
	}
	m, _ = filial(t, nil)
	if len(m.Warnings) != 0 {
		t.Errorf("Warnings with pg-client listed = %q", m.Warnings)
	}
}

// Controller ruling 2: every pack/option/tool field has a home in the model.
func TestModelCarriesPackFields(t *testing.T) {
	m, in := filial(t, nil)
	rails, deps, db := service(t, m, "rails"), service(t, m, "deps"), service(t, m, "db")
	storage, browser, nvim := service(t, m, "storage"), service(t, m, "browser"), service(t, m, "nvim")

	if rails.Role != RoleApp || deps.Role != RoleDeps || db.Role != RoleData || browser.Role != RoleTool || nvim.Role != RoleTool {
		t.Errorf("roles: %s %s %s %s %s", rails.Role, deps.Role, db.Role, browser.Role, nvim.Role)
	}
	if rails.Command != "bin/dev" || rails.Image.Kind != ImageBase || deps.Image.Kind != ImageBase {
		t.Errorf("rails command %q, images %v %v", rails.Command, rails.Image.Kind, deps.Image.Kind)
	}
	if !slices.Equal(rails.Requires, []string{"deps", "db", "cache", "storage"}) || !slices.Equal(rails.Wants, []string{"browser", "nvim"}) {
		t.Errorf("rails Requires %v Wants %v", rails.Requires, rails.Wants)
	}
	if !slices.Equal(rails.Networks, []string{"dev", "proxy"}) || !slices.Equal(db.Networks, []string{"dev"}) ||
		!slices.Equal(storage.Networks, []string{"dev", "proxy"}) || !slices.Equal(browser.Networks, []string{"dev", "proxy"}) {
		t.Errorf("networks rails %v db %v storage %v browser %v", rails.Networks, db.Networks, storage.Networks, browser.Networks)
	}
	if len(rails.Routes) != 1 || rails.Routes[0].Host != "{worktree.host}" || rails.Routes[0].Port != 3000 {
		t.Errorf("rails Routes = %+v", rails.Routes)
	}
	if rails.Health == nil || rails.Health.Cmd != "curl -fsS http://localhost:3000/up" || rails.Health.StartPeriod != "120s" {
		t.Errorf("rails Health = %+v", rails.Health)
	}
	if !rails.Worktree || !deps.Worktree || !nvim.Worktree || db.Worktree || browser.Worktree {
		t.Errorf("Worktree flags rails %v deps %v nvim %v db %v browser %v", rails.Worktree, deps.Worktree, nvim.Worktree, db.Worktree, browser.Worktree)
	}
	if !deps.Oneshot || deps.Entrypoint != "/usr/local/bin/entrypoint-deps.sh" || !slices.Equal(deps.Firewall, []string{"deps"}) {
		t.Errorf("deps oneshot %v entrypoint %q firewall %v", deps.Oneshot, deps.Entrypoint, deps.Firewall)
	}
	if rails.Unit.Memory != "4g" || rails.Unit.PidsLimit != 4096 || db.Unit.Memory != "2g" || db.Unit.PidsLimit != 1024 {
		t.Errorf("limits rails %+v db %+v", rails.Unit, db.Unit)
	}
	if db.Unit.ShmSize != "256mb" || len(db.Exec) == 0 || db.Exec[0] != "postgres" || db.Health == nil || db.Health.Cmd != "pg_isready -h 127.0.0.1 -U postgres" {
		t.Errorf("db unit %+v exec %v health %+v", db.Unit, db.Exec, db.Health)
	}
	if db.Image.Kind != ImagePull || db.Image.Ref != "docker.io/library/postgres:{postgres}" {
		t.Errorf("db Image = %+v", db.Image)
	}
	if !browser.Unit.RunInit || browser.Unit.ShmSize != "1g" || !slices.Equal(browser.Unit.AddCapability, []string{"NET_ADMIN", "NET_RAW"}) ||
		!slices.Equal(browser.Firewall, []string{"self"}) || browser.Image.Kind != ImageStandalone {
		t.Errorf("browser unit %+v firewall %v image %+v", browser.Unit, browser.Firewall, browser.Image)
	}
	if !nvim.Unit.TTY || nvim.Unit.SuccessExitStatus != 1 || nvim.Image.Kind != ImageBuild || !slices.Equal(nvim.Firewall, []string{"agent", "deps"}) {
		t.Errorf("nvim unit %+v image %+v firewall %v", nvim.Unit, nvim.Image, nvim.Firewall)
	}
	var redirect string
	for _, r := range storage.Routes {
		if r.Name == "s3-ui" {
			redirect = r.RootRedirect
		}
	}
	if redirect != "/rustfs/console/index.html" {
		t.Errorf("s3-ui root_redirect = %q", redirect)
	}
	if len(storage.Volumes) != 1 || storage.Volumes[0].Seed != "rustfs_data.tar.gz" || storage.Volumes[0].Shared {
		t.Errorf("storage Volumes = %+v", storage.Volumes)
	}

	// nvim: worktree, base .git (writable), home, caches, worktree volumes, shared volumes.
	sc := in.Scope("nvim")
	mounts := map[string]string{}
	for _, mt := range nvim.Mounts {
		src, err := pack.Expand(mt.Source, sc)
		if err != nil {
			t.Fatal(err)
		}
		dst, err := pack.Expand(mt.Target, sc)
		if err != nil {
			t.Fatal(err)
		}
		mounts[dst] = src + " " + mt.Options
	}
	ws := m.Inputs.Workspace
	git := filepath.Join(ws, "master", ".git")
	for target, want := range map[string]string{
		"/app":                              filepath.Join(ws, "master") + " z",
		git:                                 git + " z",
		"/home/appuser":                     filepath.Join(ws, ".railswitch", "home", "master") + " z",
		"/usr/local/bin/entrypoint-nvim.sh": filepath.Join(ws, ".railswitch/assets/nvim/entrypoints/entrypoint-nvim.sh") + " ro,z",
	} {
		if mounts[target] != want {
			t.Errorf("nvim mount %s = %q; want %q", target, mounts[target], want)
		}
	}
	vols := map[string]Volume{}
	for _, v := range nvim.Volumes {
		name, err := in.VolumeName("nvim", v)
		if err != nil {
			t.Fatal(err)
		}
		vols[name] = v
	}
	for _, name := range []string{"filial_gems_ruby3.3.6", "filial_npm_cache", "filial-master-node-modules", "filial_nvim_config", "filial_nvim_share", "filial_npm_global_node22.11.0"} {
		if _, ok := vols[name]; !ok {
			t.Errorf("nvim lacks volume %s (has %v)", name, slices.Collect(maps.Keys(vols)))
		}
	}
	// The app's view of the base .git is read-only.
	for _, mt := range rails.Mounts {
		if mt.Source == "{base.git}" && mt.Options != "ro,z" {
			t.Errorf("rails .git mount options = %q", mt.Options)
		}
	}
}

// The nvim tool's npm global prefix is shared per Node version: a worktree on
// another Node gets its own volume, and the app never mounts it.
func TestNvimNpmGlobalPerNodeVersion(t *testing.T) {
	ws := fixtureWS(t)
	writeFile(t, filepath.Join(ws, "feat", ".nvmrc"), "20.18.1\n")
	m := mustBuild(t, fixtureInputs(t, ws, nil))
	want := map[string]string{"master": "filial_npm_global_node22.11.0", "feat": "filial_npm_global_node20.18.1"}
	for slug, name := range want {
		in := mustInstance(t, m, slug)
		got := map[string]string{}
		for _, svc := range []string{"nvim", "rails"} {
			for _, v := range m.service(svc).Volumes {
				n, err := in.VolumeName(svc, v)
				if err != nil {
					t.Fatal(err)
				}
				if v.Mount == "/home/appuser/.npm-global" {
					got[svc] = n
				}
			}
		}
		if got["nvim"] != name {
			t.Errorf("%s nvim npm global volume = %q; want %q", slug, got["nvim"], name)
		}
		if n, ok := got["rails"]; ok {
			t.Errorf("%s rails mounts npm global volume %s", slug, n)
		}
	}
}

func TestServiceOverrides(t *testing.T) {
	m, _ := filial(t, func(p *config.Project) {
		p.Services["rails"] = config.ServiceOverride{Memory: "6g", PidsLimit: 100, Health: "none"}
		p.Services["db"] = config.ServiceOverride{Memory: "1g"}
	})
	rails, db := service(t, m, "rails"), service(t, m, "db")
	if rails.Unit.Memory != "6g" || rails.Unit.PidsLimit != 100 || rails.Health != nil {
		t.Errorf("rails unit %+v health %+v", rails.Unit, rails.Health)
	}
	if db.Unit.Memory != "1g" || db.Unit.PidsLimit != 1024 {
		t.Errorf("db unit %+v", db.Unit)
	}
	m, _ = filial(t, func(p *config.Project) {
		p.Services["rails"] = config.ServiceOverride{Health: "curl -f localhost:3000/health"}
	})
	if h := service(t, m, "rails").Health; h == nil || h.Cmd != "curl -f localhost:3000/health" {
		t.Errorf("rails Health = %+v", h)
	}
}

func TestEnvNewline(t *testing.T) {
	ws := fixtureWS(t)
	writeFile(t, filepath.Join(ws, "mise.local.toml"), "[env]\nDB_PASSWORD = \"a\\nb\"\nS3_SECRET_KEY = \"x\"\n")
	err := buildErr(fixtureInputs(t, ws, nil))
	if err == nil || !strings.Contains(err.Error(), "newline") {
		t.Fatalf("error = %v; want a newline error", err)
	}
	if strings.Contains(err.Error(), "a\nb") {
		t.Errorf("error echoes the value")
	}
}

func TestUnknownPlaceholder(t *testing.T) {
	err := buildErr(fixtureInputs(t, fixtureWS(t), func(p *config.Project) { p.Env["X"] = "{nope}" }))
	var u *pack.UnknownPlaceholderError
	if !errors.As(err, &u) || u.Name != "nope" {
		t.Errorf("error = %v; want UnknownPlaceholderError{nope}", err)
	}
}

func TestS3EndpointThroughTraefik(t *testing.T) {
	_, in := filial(t, nil)
	if e, ok := envVar(in, "rails", "S3_ENDPOINT"); !ok || e.Value != "http://s3.master.filial.localhost" {
		t.Errorf("rails S3_ENDPOINT = %q (set: %v)", e.Value, ok)
	}
}

// testShare is a tailnet share of the master worktree.
var testShare = &machine.ShareEntry{Host: "box.tail1234.ts.net", IP: "100.64.0.7"}

// sharedModel is the Filial model with master (ID 1) shared.
func sharedModel(t *testing.T, mutate func(*config.Project)) *Model {
	t.Helper()
	in := fixtureInputs(t, fixtureWS(t), mutate)
	in.Worktrees[0].Share = testShare
	return mustBuild(t, in)
}

func TestShareEnvLayer(t *testing.T) {
	m := sharedModel(t, nil)
	in := mustInstance(t, m, "master")
	for name, want := range map[string]string{
		"RAILS_DEVELOPMENT_HOSTS": "master.filial.localhost,box.tail1234.ts.net",
		"S3_ENDPOINT":             "https://box.tail1234.ts.net:19001",
	} {
		if e, ok := envVar(in, "rails", name); !ok || e.Value != want || e.Layer != LayerShare || e.Layer.String() != "share" {
			t.Errorf("shared rails %s = %+v (set: %v); want %q at layer share", name, e, ok, want)
		}
	}
	wt := m.Inputs.Worktrees[0]
	wt.Share = nil
	unshared, err := m.Instance(wt)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := in.Env["deps"], unshared.Env["deps"]; !slices.Equal(got, want) {
		t.Errorf("shared deps env = %+v; want the unshared one %+v (deps gets no layer 7, D11)", got, want)
	}
	if _, ok := envVar(in, "storage", "S3_ENDPOINT"); ok {
		t.Error("the storage service gets its own [share] gives")
	}
	units, err := Quadlet(m)
	if err != nil {
		t.Fatal(err)
	}
	hasLine(t, units, "filial-master-rails.container", "AddHost=box.tail1234.ts.net:100.64.0.7")
	noLinePrefix(t, units, "filial-feat-rails.container", "AddHost=box.")
	plain := m.Inputs
	plain.Worktrees = slices.Clone(plain.Worktrees)
	plain.Worktrees[0].Share = nil
	plainUnits, err := Quadlet(mustBuild(t, plain))
	if err != nil {
		t.Fatal(err)
	}
	if d := "filial-master-deps.container"; string(units[d]) != string(plainUnits[d]) {
		t.Errorf("sharing changes %s (its unit-hash restarts deps):\n%s\nunshared:\n%s", d, units[d], plainUnits[d])
	}
	if e, _ := envVar(mustInstance(t, m, "feat"), "rails", "S3_ENDPOINT"); e.Value != "http://s3.feat.filial.localhost" {
		t.Errorf("unshared feat S3_ENDPOINT = %q", e.Value)
	}

	m, in = filial(t, nil)
	for name, want := range map[string]EnvVar{
		"RAILS_DEVELOPMENT_HOSTS": {Value: "master.filial.localhost", Layer: LayerPack},
		"S3_ENDPOINT":             {Value: "http://s3.master.filial.localhost", Layer: LayerOption},
	} {
		if e, _ := envVar(in, "rails", name); e.Value != want.Value || e.Layer != want.Layer {
			t.Errorf("unshared rails %s = %+v; want %q at layer %s", name, e, want.Value, want.Layer)
		}
	}
	if _, ok := in.Values["share.host"]; ok {
		t.Error("an unshared worktree has share.host")
	}
	units, err = Quadlet(m)
	if err != nil {
		t.Fatal(err)
	}
	noLinePrefix(t, units, "filial-master-rails.container", "AddHost=box.")
}

// TestProjectShareEnv: project.toml [share] env is layer 7 too, above the
// pack's, and reaches every service but deps, like project [env].
func TestProjectShareEnv(t *testing.T) {
	m := sharedModel(t, func(p *config.Project) {
		p.Share.Env = map[string]string{"RAILS_DEVELOPMENT_HOSTS": "{share.host}", "APP_URL": "{share.url.app}"}
	})
	in := mustInstance(t, m, "master")
	if e, _ := envVar(in, "rails", "RAILS_DEVELOPMENT_HOSTS"); e.Value != "box.tail1234.ts.net" || e.Layer != LayerShare {
		t.Errorf("rails RAILS_DEVELOPMENT_HOSTS = %+v; want the project's [share] env", e)
	}
	if e, _ := envVar(in, "storage", "APP_URL"); e.Value != "https://box.tail1234.ts.net:13001" {
		t.Errorf("storage APP_URL = %+v", e)
	}
	if _, ok := envVar(in, "deps", "APP_URL"); ok {
		t.Error("deps gets the project's [share] env")
	}
	if _, ok := envVar(mustInstance(t, m, "feat"), "rails", "APP_URL"); ok {
		t.Error("an unshared worktree gets [share] env")
	}
}

func TestSharePorts(t *testing.T) {
	m, _ := filial(t, nil)
	want := []SharePort{{Service: "rails", Name: "app", Port: 13002}, {Service: "storage", Name: "s3", Port: 19002}}
	if got := m.SharePorts(2); !slices.Equal(got, want) {
		t.Errorf("SharePorts(2) = %+v; want %+v", got, want)
	}
}

func TestGitSigningEnv(t *testing.T) {
	signing := [][2]string{
		{"gpg.format", "ssh"},
		{"user.signingkey", "/run/secrets/sign"},
		{"commit.gpgsign", "true"},
		{"tag.gpgsign", "true"},
	}
	pairs := func(in *Instance, svc string) [][2]string {
		count, _ := envVar(in, svc, "GIT_CONFIG_COUNT")
		n, err := strconv.Atoi(count.Value)
		if err != nil {
			t.Fatalf("%s GIT_CONFIG_COUNT = %q", svc, count.Value)
		}
		var out [][2]string
		for i := range n {
			k, _ := envVar(in, svc, "GIT_CONFIG_KEY_"+strconv.Itoa(i))
			v, _ := envVar(in, svc, "GIT_CONFIG_VALUE_"+strconv.Itoa(i))
			out = append(out, [2]string{k.Value, v.Value})
		}
		return out
	}
	_, in := filial(t, func(p *config.Project) {
		p.Secrets["sign"] = config.Secret{Services: []string{"agent-claude"}, GitSigning: true}
	})
	if got := pairs(in, "agent-claude"); !slices.Equal(got, signing) {
		t.Errorf("agent-claude pairs = %q; want %q", got, signing)
	}
	if e, _ := envVar(in, "agent-claude", "GIT_AUTHOR_NAME"); e.Value != "Dev Eloper" {
		t.Errorf("agent-claude GIT_AUTHOR_NAME = %q", e.Value)
	}
	for _, e := range in.Env["rails"] {
		if strings.HasPrefix(e.Name, "GIT_") {
			t.Errorf("rails has %s", e.Name)
		}
	}

	_, in = filial(t, func(p *config.Project) {
		p.Secrets["sign"] = config.Secret{Services: []string{"agent-claude"}, GitSigning: true}
		p.Secrets["github_token"] = config.Secret{Services: []string{"agent-claude"}, GitHost: "github.com"}
	})
	got := pairs(in, "agent-claude")
	if len(got) != 6 || got[0][0] != "credential.https://github.com.helper" || got[1][0] != "url.https://github.com/.insteadOf" || !slices.Equal(got[2:], signing) {
		t.Errorf("agent-claude pairs with git_host = %q", got)
	}
}

// builtinCatalogWith is the built-in catalog with each embedded file passed
// through edit (path relative to the builtin FS), and ws as its workspace.
func builtinCatalogWith(t *testing.T, ws string, edit func(path string, data []byte) []byte) *pack.Catalog {
	t.Helper()
	mfs := fstest.MapFS{}
	err := fs.WalkDir(builtin.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(builtin.FS, p)
		mfs[p] = &fstest.MapFile{Data: edit(p, data), Mode: 0o644}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	cat, err := pack.Load(mfs)
	if err != nil {
		t.Fatal(err)
	}
	cat.Root = ws
	return cat
}

// D69: a runtime with no fallback and no source is empty, and a build arg
// built from it is "" (the rails Containerfile then skips bun). The rails
// pack's bun runtime has no fallback.
func TestEmptyRuntime(t *testing.T) {
	ws := fixtureWS(t)
	writeFile(t, filepath.Join(ws, "feat", ".bun-version"), "1.2.3\n")
	m := mustBuild(t, fixtureInputs(t, ws, nil))
	for slug, want := range map[string]string{"master": "", "feat": "1.2.3"} {
		in := mustInstance(t, m, slug)
		if got, ok := in.Values["bun"]; !ok || got != want {
			t.Errorf("%s: {bun} = %q (set %v); want %q", slug, got, ok, want)
		}
		if got, ok := in.Builds[0].Args["BUN_VERSION"]; !ok || got != want {
			t.Errorf("%s: base image BUN_VERSION = %q (set %v); want %q", slug, got, ok, want)
		}
	}
}

// withoutRuntime is the built-in catalog with the rails pack's
// [runtimes.<name>] table removed.
func withoutRuntime(t *testing.T, ws, name string) *pack.Catalog {
	t.Helper()
	re := regexp.MustCompile(`(?ms)^\[runtimes\.` + name + `\]\n.*?\n\n`)
	return builtinCatalogWith(t, ws, func(p string, data []byte) []byte {
		if p != "packs/rails/pack.toml" {
			return data
		}
		out := re.ReplaceAll(data, nil)
		if bytes.Equal(out, data) {
			t.Fatalf("no [runtimes.%s] in the rails pack", name)
		}
		return out
	})
}

// D68: the browser option pins its own version, for a pack with no
// playwright runtime.
func TestPlaywrightPinWithoutRuntime(t *testing.T) {
	ws := testfixture.Workspace(t, minimalDir)
	cat := withoutRuntime(t, ws, "playwright")
	m := mustBuild(t, catalogInputs(t, ws, cat, func(p *config.Project) { p.Options["browser"] = "playwright@1" }))
	if got := mustInstance(t, m, "master").Values["playwright"]; got != "1.60.0" {
		t.Errorf("{playwright} = %q; want the pin default 1.60.0", got)
	}
}

// D68: a pack runtime of the pinned name beats the pin default.
func TestPlaywrightRuntimeBeatsPinDefault(t *testing.T) {
	_, in := filial(t, nil)
	if got := in.Values["playwright"]; got != "1.54.0" {
		t.Errorf("{playwright} = %q; want 1.54.0 from Gemfile.lock", got)
	}
}

// D68: [versions] and [worktrees.<slug>.versions] beat the pack runtime.
func TestVersionsBeatRuntime(t *testing.T) {
	m := mustBuild(t, fixtureInputs(t, fixtureWS(t), func(p *config.Project) {
		p.Versions["playwright"] = "1.55.0"
		p.Worktrees["master"] = config.WorktreeOverride{Versions: map[string]string{"playwright": "1.56.0"}}
	}))
	for slug, want := range map[string]string{"master": "1.56.0", "feat": "1.55.0"} {
		if got := mustInstance(t, m, slug).Values["playwright"]; got != want {
			t.Errorf("%s: {playwright} = %q; want %q", slug, got, want)
		}
	}
}

// D71: nvim's npm volume needs no node runtime in the pack.
func TestNvimWithoutNode(t *testing.T) {
	ws := fixtureWS(t)
	cat := builtinCatalogWith(t, ws, func(p string, data []byte) []byte {
		data = regexp.MustCompile(`(?ms)^\[runtimes\.node\]\n.*?\n\n`).ReplaceAll(data, nil)
		if p == "packs/rails/pack.toml" { // the base image no longer has a node arg or tag part
			data = bytes.Replace(data, []byte(`tag = "ruby{ruby}-node{node}"`), []byte(`tag = "ruby{ruby}"`), 1)
			data = bytes.Replace(data, []byte(`, NODE_VERSION = "{node}"`), nil, 1)
		}
		return data
	})
	units, err := Quadlet(mustBuild(t, catalogInputs(t, ws, cat, nil)))
	if err != nil {
		t.Fatalf("Quadlet: %v", err)
	}
	hasLine(t, units, "filial-master-nvim.container", "Volume=filial_npm_global_node:/home/appuser/.npm-global:U")
}

const phoenixDir = "../../testdata/fixtures/phoenix"

// Stage 5: a user-written pack (local:phoenix@1, the fixture's packs/phoenix)
// resolves and renders with no Phoenix knowledge in core.
func TestPhoenixFixtureResolves(t *testing.T) {
	ws := testfixture.Workspace(t, phoenixDir)
	in := fixtureInputs(t, ws, nil)
	if in.Stack.PackRef != (pack.Ref{Name: "phoenix", Version: 1, Local: true}) {
		t.Fatalf("pack ref = %+v", in.Stack.PackRef)
	}
	m := mustBuild(t, in)
	i := mustInstance(t, m, "master")
	if v, ok := envVar(i, "web", "PGDATABASE"); !ok || v.Value != "phx_dev" {
		t.Errorf("PGDATABASE = %+v", v)
	}
	if v, ok := envVar(i, "web", "DATABASE_URL"); !ok || !strings.HasPrefix(v.Value, "ecto://") {
		t.Errorf("DATABASE_URL = %+v", v)
	}
	if args := i.Builds[0].Args; args["ELIXIR_VERSION"] != "1.18.4" || args["OTP_MAJOR"] != "27" {
		t.Errorf("base image args = %v", args)
	}
	units, err := Quadlet(m)
	if err != nil {
		t.Fatal(err)
	}
	hasLine(t, units, "phx-master-nvim.container", "Volume=phx_npm_global_node:/home/appuser/.npm-global:U")
}

// A package volume's data = "cache" reaches the render volume.
func TestVolumeCacheFlag(t *testing.T) {
	in := fixtureInputs(t, fixtureWS(t), nil)
	pk := in.Stack.Pack
	if len(pk.Caches) == 0 || len(pk.WorktreeVolumes) == 0 {
		t.Fatal("the fixture pack needs a cache and a worktree volume")
	}
	pk.Caches[0].Data = pack.DataCache
	cached := map[string]bool{pk.Caches[0].Name: true}
	for _, s := range mustBuild(t, in).Services {
		for _, v := range s.Volumes {
			if v.Cache != cached[v.Name] {
				t.Errorf("%s volume %s: Cache = %v", s.ID, v.Name, v.Cache)
			}
		}
	}
}

// The default firewall mode is enforce once a project has agents (D119);
// a set mode wins, and with no agents it is log.
func TestFirewallModeDefault(t *testing.T) {
	agents := &pack.Stack{Agents: []*pack.ChosenAgent{{Name: "a"}}}
	for _, c := range []struct {
		mode string
		st   *pack.Stack
		want string
	}{
		{"", agents, "enforce"},
		{"", &pack.Stack{}, "log"},
		{"log", agents, "log"},
		{"off", agents, "off"},
		{"enforce", &pack.Stack{}, "enforce"},
	} {
		p := &config.Project{Firewall: config.Firewall{Mode: c.mode}}
		if got := FirewallMode(p, c.st); got != c.want {
			t.Errorf("mode %q agents %d: %s, want %s", c.mode, len(c.st.Agents), got, c.want)
		}
	}
}
