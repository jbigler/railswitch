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
  {"number": 9, "title": "Other", "url": "https://github.com/o/n/pull/9", "headRefName": "other", "isDraft": true, "state": "OPEN", "reviewDecision": "", "statusCheckRollup": []}
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
	i := slices.IndexFunc(f.Calls, func(c []string) bool { return c[0] == "gh" })
	if i < 0 {
		t.Fatal("no gh call")
	}
	wantArgv := []string{"gh", "pr", "list", "--repo", "[ORG]/[REPO]", "--state", "all", "--limit", "200",
		"--json", "number,title,url,headRefName,isDraft,state,reviewDecision,statusCheckRollup"}
	if !slices.Equal(f.Calls[i], wantArgv) || f.Cmds[i].Dir != ws {
		t.Errorf("gh argv %q dir %q", f.Calls[i], f.Cmds[i].Dir)
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
	ws, _ := lockedWS(t)
	f := fakeHost()
	f.On([]string{"gh"}, sys.Result{ExitCode: 4}, errors.New("gh: non-zero exit (exit 4): To get started with GitHub CLI, please run:  gh auth login"))
	code, out, errs := runWith(t, f, "-C", ws, "pr", "ls")
	if code != 1 || out != "" || !strings.Contains(errs, "gh auth login") {
		t.Errorf("exit %d stdout %q stderr %q; want 1 with gh's message", code, out, errs)
	}
}
