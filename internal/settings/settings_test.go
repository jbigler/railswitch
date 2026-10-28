package settings

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/testfixture"
	"github.com/jbigler/railswitch/internal/workspace"
)

// filialEnv is the Filial fixture workspace, registered with its master and
// feat worktrees and locked, every unit running. prepare, when set, edits
// the workspace before the lock.
func filialEnv(t *testing.T, prepare func(ws string)) Env {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	ws := testfixture.Workspace(t, "../../testdata/fixtures/filial")
	if prepare != nil {
		prepare(ws)
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.AddProject("filial", ws, nil); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"master", "feat"} {
		if _, err := reg.AddWorktree("filial", slug, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	reg.Close()
	ctx := context.Background()
	if err := workspace.Lock(ctx, ws); err != nil {
		t.Fatal(err)
	}
	w, err := workspace.Load(ctx, ws, "")
	if err != nil {
		t.Fatal(err)
	}
	r, err := machine.Read(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	return Env{WS: w, Reg: r, Host: workspace.LocalHost(), Running: func(string) bool { return true }}
}

func projectText(t *testing.T, e Env) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.WS.Root, "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func preview(t *testing.T, e Env, edits ...Edit) *Preview {
	t.Helper()
	p, err := e.Preview(projectText(t, e), edits)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Edits) != len(edits) {
		t.Fatalf("%d results for %d edits", len(p.Edits), len(edits))
	}
	return p
}

func hasReason(r EditResult, sub string) bool {
	return slices.ContainsFunc(r.Reasons, func(s string) bool { return strings.Contains(s, sub) })
}

func TestPreviewRedisNone(t *testing.T) {
	e := filialEnv(t, nil)
	p := preview(t, e, Edit{Op: "option", Key: "cache", Ref: "none"})
	r := p.Edits[0]
	if r.Impact != ImpactCheck || !hasReason(r, "REDIS_URL is no longer given to rails") || r.Error != "" {
		t.Fatalf("result = %+v", r)
	}
	if !slices.Equal(p.Restart, []string{"feat", "master"}) && !slices.Equal(p.Restart, []string{"master", "feat"}) {
		t.Errorf("Restart = %v", p.Restart)
	}
	if p.Hash != workspace.Hash(projectText(t, e)) {
		t.Errorf("Hash = %s", p.Hash)
	}
	if !strings.Contains(p.Diff, "-cache    = \"redis@1\"\n+cache    = \"none\"\n") || !strings.HasPrefix(p.Diff, "--- project.toml\n+++ project.toml\n@@ ") {
		t.Errorf("Diff:\n%s", p.Diff)
	}
	if !strings.Contains(p.NewText, `cache    = "none"`) {
		t.Errorf("NewText:\n%s", p.NewText)
	}
}

// Edit i is judged with edits before it applied: cache → none is fine on
// its own but not once sidekiq, which needs it, is on.
func TestPreviewSidekiqBlocks(t *testing.T) {
	e := filialEnv(t, nil)
	lock := filepath.Join(e.WS.Root, "stack.lock")
	before, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}
	p := preview(t, e,
		Edit{Op: "option", Key: "jobs", Ref: "sidekiq@1"},
		Edit{Op: "option", Key: "cache", Ref: "none"},
	)
	if after, err := os.ReadFile(lock); err != nil || string(after) != string(before) {
		t.Errorf("preview changed stack.lock: %v", err)
	}
	if r := p.Edits[0]; r.Impact == ImpactBlocked {
		t.Fatalf("jobs = sidekiq: %+v", r)
	}
	r := p.Edits[1]
	if r.Impact != ImpactBlocked || !strings.Contains(r.Error, "sidekiq needs cache, but options.cache is none") {
		t.Fatalf("cache = none after sidekiq: %+v", r)
	}
	if r.Index != 1 {
		t.Errorf("Index = %d", r.Index)
	}
}

func TestPreviewPinChangeData(t *testing.T) {
	e := filialEnv(t, nil)
	data := string(projectText(t, e))
	text := strings.Replace(data, `postgres = "16"`, `postgres = "17"`, 1)
	if text == data {
		t.Fatal("fixture has no postgres pin")
	}
	cur, err := e.render(e.WS)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := e.judge(cur, text)
	if r.Impact != ImpactData || !hasReason(r, "filial-master-db-data-pg16 is left behind; filial-master-db-data-pg17 starts empty") || !hasReason(r, "run railswitch db dump first") {
		t.Fatalf("result = %+v", r)
	}
	// feat already runs 17 ([worktrees.feat.versions]): it loses nothing.
	if hasReason(r, "filial-feat-") {
		t.Errorf("feat named: %v", r.Reasons)
	}
}

func TestPreviewOptionNoneData(t *testing.T) {
	e := filialEnv(t, nil)
	p := preview(t, e,
		Edit{Op: "option", Key: "database", Ref: "none"},
		Edit{Op: "option", Key: "storage", Ref: "none"},
	)
	db, st := p.Edits[0], p.Edits[1]
	if db.Impact != ImpactData || !hasReason(db, "filial-master-db-data-pg16 is left behind") || !hasReason(db, "run railswitch db dump first") || !hasReason(db, "DATABASE_URL is no longer given to rails") {
		t.Errorf("database = none: %+v", db)
	}
	if st.Impact != ImpactData || !hasReason(st, "filial-master-rustfs-data is left behind") || hasReason(st, "db dump") || hasReason(st, "pg16") {
		t.Errorf("storage = none: %+v", st)
	}
}

// localOption writes options/scratch/ with one volume, data being its data
// line, and adds it to [options] as an extra key.
func localOption(data string) func(ws string) {
	return func(ws string) {
		dir := filepath.Join(ws, "options", "scratch")
		manifest := `name = "scratch"
version = 1
slot = "cache"
service = "scratch"
works_with = ["*"]

[image]
pull = "docker.io/library/busybox:1"

[[volumes]]
name = "scratch-files"
mount = "/scratch"
` + data + "\n"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "option.toml"), []byte(manifest), 0o644); err != nil {
			panic(err)
		}
		path := filepath.Join(ws, "project.toml")
		p, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}
		out, err := config.SetOption(string(p), "scratch", "local:scratch@1", false)
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			panic(err)
		}
	}
}

func TestPreviewCacheVolume(t *testing.T) {
	for data, want := range map[string]Impact{`data = "cache"`: ImpactNone, "": ImpactData} {
		e := filialEnv(t, localOption(data))
		r := preview(t, e, Edit{Op: "option", Key: "scratch", Ref: "none"}).Edits[0]
		if r.Impact != want {
			t.Errorf("%q: %+v, want %s", data, r, want)
		}
		if want == ImpactData && (!hasReason(r, "filial-master-scratch-files is left behind") || hasReason(r, "db dump")) {
			t.Errorf("%q: reasons %v", data, r.Reasons)
		}
	}
}

func TestPreviewLinkNone(t *testing.T) {
	e := filialEnv(t, nil)
	p := preview(t, e, Edit{Op: "link.add", Label: "Flipper", URL: "{worktree.host}/flipper"})
	if r := p.Edits[0]; r.Impact != ImpactNone || r.Reasons == nil || len(r.Reasons) != 0 {
		t.Fatalf("result = %+v; want none with an empty, non-null reason list", r)
	}
	if len(p.Restart) != 0 {
		t.Errorf("Restart = %v", p.Restart)
	}
}

func TestPreviewEnvCheckAndRestart(t *testing.T) {
	e := filialEnv(t, nil)
	e.Running = func(unit string) bool { return unit == "filial-master-rails.service" }
	p := preview(t, e, Edit{Op: "env.set", Name: "TZ", Value: "Europe/Paris"})
	if r := p.Edits[0]; r.Impact != ImpactCheck || !slices.Equal(r.Reasons, []string{"TZ changes for rails"}) {
		t.Fatalf("result = %+v", r)
	}
	if !slices.Equal(p.Restart, []string{"master"}) {
		t.Errorf("Restart = %v, want only the running master", p.Restart)
	}
}

func TestPreviewBlocked(t *testing.T) {
	e := filialEnv(t, nil)
	p := preview(t, e,
		Edit{Op: "env.unset", Name: "NOPE"},
		Edit{Op: "env.set", Name: "X", Value: "a\nb"},
		Edit{Op: "link.hide", Label: "Nope"},
		Edit{Op: "option", Key: "cache", Ref: "nope@1"},
		Edit{Op: "link.add", Label: "Ok", URL: "{worktree.host}/ok"},
	)
	for i, sub := range []string{"NOPE", "env.X", `no stack link labelled "Nope"`, "nope"} {
		if r := p.Edits[i]; r.Impact != ImpactBlocked || !strings.Contains(r.Error, sub) {
			t.Errorf("edit %d: %+v, want blocked naming %q", i, r, sub)
		}
	}
	if r := p.Edits[4]; r.Impact != ImpactNone {
		t.Errorf("edit after blocked ones: %+v", r)
	}
}

// A shape the line editor can't edit is blocked naming its line.
func TestPreviewBlockedShape(t *testing.T) {
	e := filialEnv(t, nil)
	data := strings.Replace(string(projectText(t, e)), "[env]\nTZ = \"Asia/Bangkok\"\n", "", 1)
	data = strings.Replace(data, "schema = 1\n", "schema = 1\nenv = { TZ = \"Asia/Bangkok\" }\n", 1)
	p, err := e.Preview([]byte(data), []Edit{{Op: "env.set", Name: "TZ", Value: "UTC"}})
	if err != nil {
		t.Fatal(err)
	}
	r := p.Edits[0]
	var be *config.BlockedError
	if r.Impact != ImpactBlocked || !strings.Contains(r.Error, "project.toml:2:") || !strings.Contains(r.Error, "edit project.toml directly") {
		t.Fatalf("result = %+v", r)
	}
	if _, err := ApplyText(data, Edit{Op: "env.set", Name: "TZ", Value: "UTC"}); !errors.As(err, &be) || be.Line != 2 {
		t.Errorf("ApplyText = %v", err)
	}
	if p.Diff != "" || p.NewText != data {
		t.Errorf("nothing placed, yet Diff %q", p.Diff)
	}
}

func TestParseEditsStrict(t *testing.T) {
	good := `[{"op":"option","key":"cache","ref":"none"},{"op":"env.set","name":"A","value":""},{"op":"link.add","label":"L","url":"u"}]`
	got, err := ParseEdits(strings.NewReader(good))
	if err != nil || len(got) != 3 || got[0] != (Edit{Op: "option", Key: "cache", Ref: "none"}) {
		t.Fatalf("ParseEdits = %+v, %v", got, err)
	}
	for in, want := range map[string]string{
		`[{"op":"option","key":"cache","ref":"none","colour":"red"}]`: "unknown field",
		`[{"op":"rename","key":"cache"}]`:                             `unknown op "rename"`,
		`[{"op":"option","key":"cache"}]`:                             "edit 0: option needs ref",
		`[{"op":"env.unset"}]`:                                        "edit 0: env.unset needs name",
		`[{"op":"link.remove","label":"L","url":"u"}]`:                "edit 0: link.remove takes no url",
		`[{"op":"link.add","label":"L"}]`:                             "edit 0: link.add needs url",
		`{"op":"option"}`:                                             "cannot unmarshal",
		`[] []`:                                                       "one JSON array",
	} {
		if _, err := ParseEdits(strings.NewReader(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", in, err, want)
		}
	}
}

func TestUnifiedDiff(t *testing.T) {
	a := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n"
	b := "1\nTWO\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n"
	want := "--- project.toml\n+++ project.toml\n" +
		"@@ -1,5 +1,5 @@\n 1\n-2\n+TWO\n 3\n 4\n 5\n" +
		"@@ -10,3 +10,4 @@\n 10\n 11\n 12\n+13\n"
	if got := unifiedDiff(a, b); got != want {
		t.Errorf("unifiedDiff =\n%s\nwant\n%s", got, want)
	}
	if got := unifiedDiff(a, a); got != "" {
		t.Errorf("same text: %q", got)
	}
	if got := unifiedDiff("x", "y"); got != "--- project.toml\n+++ project.toml\n@@ -1 +1 @@\n-x\n\\ No newline at end of file\n+y\n\\ No newline at end of file\n" {
		t.Errorf("no final newline:\n%s", got)
	}
}
