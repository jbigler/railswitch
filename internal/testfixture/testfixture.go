// Package testfixture builds temp workspaces from testdata/fixtures for tests
// of every package. Only tests import it.
package testfixture

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/jbigler/railswitch/internal/pack"
)

// Workspace copies the fixture dir to a temp workspace named after it and
// writes the built-in default Containerfiles into its containerfiles/, as
// railswitch new ejects them, so the fixture can't drift from the built-ins.
func Workspace(t testing.TB, dir string) string {
	t.Helper()
	ws := filepath.Join(t.TempDir(), filepath.Base(dir))
	if err := os.CopyFS(ws, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(ws, "db-dumps"), 0o700); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	cat, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	defaults := []struct {
		files fs.FS
		name  string
	}{
		{cat.Packs[pack.Ref{Name: "rails", Version: 1}].Files, "Containerfile.base"},
		{cat.Options[pack.Ref{Name: "postgres", Version: 1}].Files, "Containerfile.pg-client"},
		{cat.Tools["nvim"].Files, "Containerfile.nvim"},
		{cat.Options[pack.Ref{Name: "playwright", Version: 1}].Files, "Containerfile.playwright"},
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
