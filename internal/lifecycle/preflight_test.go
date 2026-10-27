package lifecycle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
	"github.com/jbigler/railswitch/internal/testfixture"
)

// filialModel builds the Filial fixture's model with its master worktree.
func filialModel(t *testing.T) *render.Model {
	t.Helper()
	ws := testfixture.Workspace(t, "../../testdata/fixtures/filial")
	p, err := config.Load(filepath.Join(ws, "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cat, err := pack.LoadCatalog(ws)
	if err != nil {
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
	m, err := render.Build(render.Inputs{
		Workspace: ws, Project: p, Stack: st, Generated: gen,
		Machine:   machine.ProjectEntry{Prefix: "filial", Workspace: ws, Subnet: 1},
		Worktrees: []render.Worktree{{Slug: "master", ID: 1, Dir: filepath.Join(ws, "master"), Branch: "master"}},
		Host:      render.Host{CPUs: 8, Home: "/home/u"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPreflightMissingSecret(t *testing.T) {
	m := filialModel(t)
	f := &systest.Fake{}
	p := podman.Podman{R: f}
	if err := CheckSecrets(context.Background(), p, m, "master"); err != nil {
		t.Fatalf("all set: %v", err)
	}
	f.On([]string{"podman", "secret", "exists", "filial_github_token"}, sys.Result{ExitCode: 1}, fmt.Errorf("podman: %w (exit 1)", sys.ErrExit))
	err := CheckSecrets(context.Background(), p, m, "master")
	if err == nil || !strings.Contains(err.Error(), "secret github_token is not set: railswitch secret set github_token") {
		t.Fatalf("missing: got %v", err)
	}
	if strings.Contains(err.Error(), "twilio") {
		t.Fatalf("set secret reported: %v", err)
	}
	if err := CheckSecrets(context.Background(), p, m, "gone"); err == nil {
		t.Fatal("unregistered slug: want error")
	}
}

func TestPreflightNamesMount(t *testing.T) {
	m := filialModel(t)
	gone := filepath.Join(t.TempDir(), "gone")
	s := &m.Services[0] // checked first: later mounts need applied assets
	s.Mounts = append([]render.Mount{{Source: gone, Target: "/k", Options: "z", Origin: `[[mount]] #0 service = "nvim"`}}, s.Mounts...)
	l := &L{R: &systest.Fake{}}
	err := l.Preflight(context.Background(), m, "master")
	if err == nil || !strings.Contains(err.Error(), gone) || !strings.Contains(err.Error(), `[[mount]] #0 service = "nvim"`) {
		t.Fatalf("got %v", err)
	}
}

// git-guard (D60): <base>/.git/hooks is a mount source; Preflight creates it.
func TestPreflightCreatesHooks(t *testing.T) {
	m := filialModel(t)
	git := filepath.Join(m.Inputs.Workspace, "master", ".git")
	if err := os.Mkdir(git, 0o755); err != nil {
		t.Fatal(err)
	}
	l := &L{R: &systest.Fake{}}
	_ = l.Preflight(context.Background(), m, "master") // unapplied assets fail it later
	fi, err := os.Stat(filepath.Join(git, "hooks"))
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() || fi.Mode().Perm() != 0o755 {
		t.Errorf("hooks = %v, want a 0755 directory", fi.Mode())
	}
}
