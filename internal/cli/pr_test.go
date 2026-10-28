package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

const ghFixture = `[
  {"number": 4, "title": "Old feat", "url": "https://github.com/o/n/pull/4", "headRefName": "feat", "isDraft": false, "state": "CLOSED", "reviewDecision": "", "statusCheckRollup": []},
  {"number": 7, "title": "Feat", "url": "https://github.com/o/n/pull/7", "headRefName": "feat", "isDraft": false, "state": "OPEN", "reviewDecision": "APPROVED",
   "statusCheckRollup": [{"__typename": "CheckRun", "name": "ci", "status": "COMPLETED", "conclusion": "SUCCESS"}]},
  {"number": 9, "title": "Other", "url": "https://github.com/o/n/pull/9", "headRefName": "other", "isDraft": true, "state": "OPEN", "reviewDecision": "", "statusCheckRollup": []},
  {"number": 11, "title": "Fork's master", "url": "https://github.com/o/n/pull/11", "headRefName": "master", "isCrossRepository": true, "isDraft": false, "state": "OPEN", "reviewDecision": "", "statusCheckRollup": []}
]`

func TestPrLsJSON(t *testing.T) {
	ws, _ := gitWS(t)
	f := fakeHost()
	f.Passthrough = []string{"git"}
	f.On([]string{"gh", "pr", "list"}, sys.Result{Stdout: []byte(ghFixture)}, nil)
	code, out, errs := runWith(t, f, "-C", ws, "--json", "pr", "ls")
	if code != 0 {
		t.Fatalf("pr ls: exit %d: %s", code, errs)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	want := []map[string]any{
		{"slug": "master", "branch": "master", "pr": nil},
		{"slug": "feat", "branch": "feat", "pr": map[string]any{
			"number": float64(7), "title": "Feat", "url": "https://github.com/o/n/pull/7",
			"state": "open", "checks": "pass", "review": "approved"}},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows %v; want %v", rows, want)
	}
	// One query per worktree branch, never in the workspace (prs.NeutralDir).
	var heads []string
	for i, c := range f.Calls {
		if c[0] != "gh" {
			continue
		}
		heads = append(heads, c[5])
		if !slices.Equal(c[:5], []string{"gh", "pr", "list", "--repo", "[ORG]/[REPO]"}) || f.Cmds[i].Dir != "/" {
			t.Errorf("gh argv %q dir %q", c, f.Cmds[i].Dir)
		}
	}
	if !slices.Equal(heads, []string{"--head=master", "--head=feat"}) {
		t.Errorf("gh --head args %q; want master, feat", heads)
	}

	code, out, _ = runWith(t, f, "-C", ws, "pr", "ls")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if code != 0 || len(lines) != 3 || strings.Join(strings.Fields(lines[0]), " ") != "WORKTREE BRANCH PR STATE CHECKS REVIEW TITLE" ||
		strings.Join(strings.Fields(lines[1]), " ") != "master master - - - - -" ||
		strings.Join(strings.Fields(lines[2]), " ") != "feat feat #7 open pass approved Feat" {
		t.Errorf("text:\n%s", out)
	}
}

func TestPrLsNotGitHub(t *testing.T) {
	ws, _ := lockedWS(t)
	path := filepath.Join(ws, "project.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), `"git@github.com:[ORG]/[REPO].git"`, `"https://gitlab.com/o/n.git"`, 1)
	if edited == string(data) {
		t.Fatal("fixture has no repo line")
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	f := fakeHost()
	code, _, errs := runWith(t, f, "-C", ws, "pr", "ls")
	if code != 1 || !strings.Contains(errs, "pr ls: https://gitlab.com/o/n.git is not a GitHub repo") {
		t.Errorf("exit %d stderr %q; want 1, not a GitHub repo", code, errs)
	}
	if slices.ContainsFunc(f.Calls, func(c []string) bool { return c[0] == "gh" }) {
		t.Error("ran gh")
	}
}

func TestPrLsGhFails(t *testing.T) {
	ws, _ := gitWS(t)
	f := fakeHost()
	f.Passthrough = []string{"git"}
	f.On([]string{"gh"}, sys.Result{ExitCode: 4}, errors.New("gh: non-zero exit (exit 4): To get started with GitHub CLI, please run:  gh auth login"))
	code, out, errs := runWith(t, f, "-C", ws, "pr", "ls")
	if code != 1 || out != "" || !strings.Contains(errs, "gh auth login") {
		t.Errorf("exit %d stdout %q stderr %q; want 1 with gh's message", code, out, errs)
	}
}

// TestPrLsTextPrintable: a branch a container wrote into the worktree's
// git files prints without its control characters.
func TestPrLsTextPrintable(t *testing.T) {
	ws, _ := gitWS(t)
	head := filepath.Join(ws, "master", ".git", "worktrees", "feat", "HEAD")
	if err := os.WriteFile(head, []byte("ref: refs/heads/jb/\x1b[31mred\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := fakeHost()
	f.Passthrough = []string{"git"}
	f.On([]string{"gh", "pr", "list"}, sys.Result{Stdout: []byte(`[{"number": 3, "title": "T\u001b]0;x\u0007", "url": "u", "headRefName": "jb/\u001b[31mred", "isDraft": false, "isCrossRepository": false, "state": "OPEN", "reviewDecision": "", "statusCheckRollup": []}]`)}, nil)
	code, out, errs := runWith(t, f, "-C", ws, "pr", "ls")
	if code != 0 || strings.Contains(out, "\x1b") || strings.Contains(out, "\x07") || !strings.Contains(out, "jb/ [31mred") || !strings.Contains(out, "#3") {
		t.Errorf("exit %d %s\n%q", code, errs, out)
	}
}
