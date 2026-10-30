package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jbigler/railswitch/internal/lifecycle"
)

// TestNvimTarget: a file given as the container sees it, as a host path in
// the worktree, or relative (to the root when it is there, else to the
// working directory), with what tools print after the line number cut off;
// anything outside the worktree is refused.
func TestNvimTarget(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"app/models/user.rb", "app/models/helper.rb", "spec/x_spec.rb"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	models := filepath.Join(dir, "app", "models")
	for _, c := range []struct {
		arg, cwd string
		want     lifecycle.NvimTarget
	}{
		{"/app/app/models/user.rb:42", dir, lifecycle.NvimTarget{Path: "/app/app/models/user.rb", Line: 42}},
		{filepath.Join(dir, "app/models/user.rb") + ":7:3", "/", lifecycle.NvimTarget{Path: "/app/app/models/user.rb", Line: 7, Col: 3}},
		{"app/models/user.rb", models, lifecycle.NvimTarget{Path: "/app/app/models/user.rb"}},
		{"helper.rb:9", models, lifecycle.NvimTarget{Path: "/app/app/models/helper.rb", Line: 9}},
		{"./spec/x_spec.rb:12", dir, lifecycle.NvimTarget{Path: "/app/spec/x_spec.rb", Line: 12}},
		{"app/models/user.rb:42:in 'User#save'", dir, lifecycle.NvimTarget{Path: "/app/app/models/user.rb", Line: 42}},
		{"app/models/user.rb:10:5: undefined method", dir, lifecycle.NvimTarget{Path: "/app/app/models/user.rb", Line: 10, Col: 5}},
		{"lib/odd:name.rb:3", dir, lifecycle.NvimTarget{Path: "/app/lib/odd:name.rb", Line: 3}},
		{"new_file.rb", dir, lifecycle.NvimTarget{Path: "/app/new_file.rb"}},
		{"/app", dir, lifecycle.NvimTarget{Path: "/app"}},
	} {
		got, err := nvimTarget(c.arg, dir, c.cwd)
		if err != nil || got != c.want {
			t.Errorf("nvimTarget(%q, cwd %s) = %+v, %v; want %+v", c.arg, c.cwd, got, err, c.want)
		}
	}
	for _, bad := range []string{"/etc/passwd", "../outside.rb", "/app/../etc/passwd", "app/x\n.rb"} {
		if got, err := nvimTarget(bad, dir, dir); err == nil {
			t.Errorf("nvimTarget(%q) = %+v; want refused", bad, got)
		}
	}
}
