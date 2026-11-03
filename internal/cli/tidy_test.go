package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

// D160: tidy lists feat (its PR merged, its stack stopped long ago) and
// the older build, not master (the base) or the current build; --rm
// refuses a dirty worktree without --force and an id that is no
// candidate, and removes the rest by wt rm's path and podman rmi.
func TestTidy(t *testing.T) {
	ws, _ := gitWS(t)
	feat := filepath.Join(ws, "worktrees", "feat")
	res, err := (sys.Exec{}).Run(t.Context(), sys.Cmd{Name: "git", Args: []string{"-C", feat, "rev-parse", "HEAD"}})
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(res.Stdout))
	var list struct{ Steps []struct{ Tag string } }
	if err := json.Unmarshal([]byte(mustRun(t, "-C", ws, "-w", "master", "--json", "containerfile", "list")), &list); err != nil {
		t.Fatal(err)
	}
	current := list.Steps[0].Tag
	old := strings.TrimSuffix(current, current[len(current)-8:]) + "deadbeef"

	f := unitsFake()
	f.Passthrough = []string{"git"}
	f.On([]string{"podman", "ps"}, sys.Result{Stdout: []byte("[]")}, nil)
	f.On([]string{"gh", "pr", "list"}, sys.Result{Stdout: []byte(`[{"number": 7, "title": "Feat", "url": "https://github.com/o/n/pull/7", "headRefName": "feat", "headRefOid": "` + head + `", "isDraft": false, "state": "MERGED", "reviewDecision": "", "statusCheckRollup": []}]`)}, nil)
	f.On([]string{"systemctl", "--user", "show", "--timestamp=us+utc"}, sys.Result{Stdout: []byte("Id=filial-feat-rails.service\nInactiveEnterTimestamp=Tue 2026-01-06 09:00:00.000000 UTC\n")}, nil)
	f.On([]string{"podman", "images", "--format", "json"}, sys.Result{Stdout: []byte(fmt.Sprintf(`[{"Id":"1","Names":[%q],"Size":1000,"Containers":0},{"Id":"2","Names":[%q],"Size":2000,"Containers":0}]`, current, old))}, nil)

	code, out, errs := runWith(t, f, "-C", ws, "--json", "tidy")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var doc tidyDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(doc.Items) != 2 {
		t.Fatalf("items %+v", doc.Items)
	}
	wt, img := doc.Items[0], doc.Items[1]
	if wt.ID != "wt:filial/feat" || !slices.Equal(wt.Reasons, []string{"pr-merged", "stopped"}) || wt.PR != 7 || wt.Stack != "stopped" ||
		wt.IdleFrom != "systemd" || wt.IdleSince != "2026-01-06T09:00:00Z" || wt.Dirty || wt.Bytes == 0 {
		t.Errorf("worktree %+v", wt)
	}
	if img.ID != "image:"+old || img.Project != "filial" || img.Bytes != 2000 || doc.Bytes != wt.Bytes+2000 {
		t.Errorf("image %+v, bytes %d", img, doc.Bytes)
	}

	// Dirty: listed and flagged, removed only with --force.
	if err := os.WriteFile(filepath.Join(feat, "wip.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	n := len(f.Calls)
	code, _, errs = runWith(t, f, "-C", ws, "tidy", "--rm", "wt:filial/feat", "image:localhost/filial/nope:x-00000000")
	if code != 1 || !strings.Contains(errs, "uncommitted changes") || !strings.Contains(errs, "not a candidate now") {
		t.Errorf("exit %d: %s", code, errs)
	}
	for _, c := range f.Calls[n:] {
		if slices.Contains(c, "rmi") || slices.Contains(c, "remove") {
			t.Errorf("a refused tidy --rm ran %q", c)
		}
	}
	if _, err := os.Stat(feat); err != nil {
		t.Fatal("the dirty worktree is gone")
	}

	f.On([]string{"podman", "rmi"}, sys.Result{}, nil)
	n = len(f.Calls)
	if code, out, errs := runWith(t, f, "-C", ws, "tidy", "--rm", "--force", "image:"+old, "wt:filial/feat"); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out, errs)
	}
	if _, err := os.Stat(feat); err == nil {
		t.Error("tidy --rm left feat")
	}
	rmi := slices.IndexFunc(f.Calls[n:], func(c []string) bool { return slices.Equal(c, []string{"podman", "rmi", old}) })
	gitRm := slices.IndexFunc(f.Calls[n:], func(c []string) bool { return slices.Contains(c, "worktree") && slices.Contains(c, "remove") })
	if rmi < 0 || gitRm < 0 || gitRm > rmi {
		t.Errorf("rmi at %d, worktree remove at %d (worktrees go first): %q", rmi, gitRm, f.Calls[n:])
	}

	for _, bad := range [][]string{{"tidy", "x"}, {"tidy", "--rm"}, {"tidy", "--force"}, {"tidy", "--days", "0"}, {"--json", "tidy", "--rm", "x"}} {
		if code, _, _ := runWith(t, f, append([]string{"-C", ws}, bad...)...); code != 2 {
			t.Errorf("%q: exit %d, want 2", bad, code)
		}
	}
}

func TestDirBytes(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "a"), make([]byte, 10), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(d, "l")); err != nil {
		t.Fatal(err)
	}
	if n, partial := dirBytes(d); n != 10 || partial {
		t.Errorf("dirBytes = %d, %v", n, partial)
	}
}
