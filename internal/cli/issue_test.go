package cli

import (
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

// D144: issue ls asks gh for project.toml's repo; the title on a terminal
// is printable.
func TestIssueLs(t *testing.T) {
	ws, _ := gitWS(t)
	f := fakeHost()
	f.On([]string{"gh", "issue", "list"}, sys.Result{Stdout: []byte(`[{"number":3,"title":"Bad\u001b[2Jtitle","url":"https://github.com/o/n/issues/3","labels":[],"assignees":[],"author":{"login":"ann"},"updatedAt":"2026-10-31T00:00:00Z"}]`)}, nil)
	code, out, errs := runWith(t, f, "-C", ws, "--json", "issue", "ls")
	if code != 0 || !strings.HasPrefix(out, `{"issues":[{"number":3,`) || !strings.Contains(out, `"labels":[],"assignees":[]`) {
		t.Fatalf("issue ls --json: exit %d out %s err %s", code, out, errs)
	}
	if c := f.Calls[len(f.Calls)-1]; strings.Join(c[:5], " ") != "gh issue list --repo [ORG]/[REPO]" {
		t.Errorf("gh argv %q", c)
	}
	code, out, _ = runWith(t, f, "-C", ws, "issue", "ls")
	if code != 0 || strings.Contains(out, "\x1b") || !strings.Contains(out, "Bad [2Jtitle") {
		t.Errorf("issue ls: exit %d %q", code, out)
	}
	if code, _, _ := runWith(t, f, "-C", ws, "issue", "ls", "x"); code != 2 {
		t.Errorf("issue ls x: exit %d; want 2", code)
	}
}
