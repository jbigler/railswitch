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
	f.On([]string{"gh", "api", "user"}, sys.Result{Stdout: []byte("ann\n")}, nil)
	code, out, errs := runWith(t, f, "-C", ws, "--json", "issue", "ls")
	if code != 0 || !strings.HasPrefix(out, `{"me":"ann","issues":[{"number":3,`) || !strings.Contains(out, `"labels":[],"assignees":[]`) {
		t.Fatalf("issue ls --json: exit %d out %s err %s", code, out, errs)
	}
	if c := f.Calls[len(f.Calls)-2]; strings.Join(c[:5], " ") != "gh issue list --repo [ORG]/[REPO]" {
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

// pr ls --all: every open PR with who it is for, and who gh signs in as.
func TestPrLsAll(t *testing.T) {
	ws, _ := gitWS(t)
	f := fakeHost()
	f.On([]string{"gh", "pr", "list"}, sys.Result{Stdout: []byte(`[{"number":3,"title":"Add\u001b[2Jbilling","url":"https://github.com/o/n/pull/3","headRefName":"billing","isDraft":true,"author":{"login":"ann"},"assignees":[],"reviewRequests":[{"login":"bob"}],"reviewDecision":"","updatedAt":"2026-11-01T00:00:00Z"}]`)}, nil)
	f.On([]string{"gh", "api", "user"}, sys.Result{Stdout: []byte("bob\n")}, nil)
	code, out, errs := runWith(t, f, "-C", ws, "--json", "pr", "ls", "--all")
	if code != 0 || !strings.HasPrefix(out, `{"me":"bob","prs":[{"number":3,`) || !strings.Contains(out, `"reviewers":["bob"]`) {
		t.Fatalf("pr ls --all --json: exit %d out %s err %s", code, out, errs)
	}
	code, out, _ = runWith(t, f, "-C", ws, "pr", "ls", "--all")
	if code != 0 || strings.Contains(out, "\x1b") || !strings.Contains(out, "[draft] Add [2Jbilling") {
		t.Errorf("pr ls --all: exit %d %q", code, out)
	}
}
