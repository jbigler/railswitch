// Package testfixture builds temp workspaces from testdata/fixtures for tests
// of every package. Only tests import it.
package testfixture

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/pack/builtin"
)

type defaultFile struct {
	files fs.FS
	name  string
}

// Workspace copies the fixture dir to a temp workspace named after it and
// writes the built-in default Containerfiles into its containerfiles/, as
// railswitch project new ejects them, so the fixture can't drift from the
// built-ins: the pack's, options', tools', agent-base and every agent
// package's the fixture's project uses (built-in or its own agents/).
func Workspace(t testing.TB, dir string) string {
	t.Helper()
	ws := filepath.Join(t.TempDir(), filepath.Base(dir))
	if err := os.CopyFS(ws, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(ws, "db-dumps"), 0o700); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	cat, err := pack.LoadCatalog(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := config.Load(filepath.Join(ws, "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := cat.Resolve(p)
	if err != nil {
		t.Fatal(err)
	}
	agentBase, err := fs.Sub(builtin.FS, "agentbase")
	if err != nil {
		t.Fatal(err)
	}
	// The project's pack and agents, and every built-in option's and tool's
	// Containerfile, so a test may switch options on without ejecting.
	defaults := []defaultFile{
		{st.Pack.Files, st.Pack.Image.File},
		{agentBase, "Containerfile.agent-base"},
	}
	for _, o := range cat.Options {
		if o.AppAddon != nil {
			defaults = append(defaults, defaultFile{o.Files, o.AppAddon.File})
		}
		if o.Image.Build != "" {
			defaults = append(defaults, defaultFile{o.Files, o.Image.Build})
		}
	}
	for _, tl := range cat.Tools {
		defaults = append(defaults, defaultFile{tl.Files, tl.Image.File})
	}
	for _, a := range st.Agents {
		defaults = append(defaults, defaultFile{a.Agent.Files, a.Agent.Image.File})
	}
	if err := os.Mkdir(filepath.Join(ws, "containerfiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range defaults {
		data, err := fs.ReadFile(d.files, d.name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, "containerfiles", d.name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}
