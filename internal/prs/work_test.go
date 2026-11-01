package prs

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

// Open: who each PR is for; teams asked for a review left out; arrays
// never null.
func TestOpen(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"gh", "pr", "list"}, sys.Result{Stdout: []byte(`[
{"number":3,"title":"Add billing","url":"https://github.com/o/n/pull/3","headRefName":"billing","isDraft":false,"author":{"login":"ann"},"assignees":[{"login":"bob"}],"reviewRequests":[{"__typename":"User","login":"cat"},{"__typename":"Team","name":"Core","slug":"core"}],"reviewDecision":"REVIEW_REQUIRED","updatedAt":"2026-11-01T00:00:00Z"},
{"number":4,"title":"WIP","url":"https://github.com/o/n/pull/4","headRefName":"wip","isDraft":true,"author":{"login":"bob"},"assignees":[],"reviewRequests":[],"reviewDecision":"","updatedAt":"2026-11-02T00:00:00Z"}]`)}, nil)
	got, err := Open(context.Background(), f, "o/n")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	want := `[{"number":3,"title":"Add billing","url":"https://github.com/o/n/pull/3","branch":"billing","draft":false,"author":"ann","assignees":["bob"],"reviewers":["cat"],"review":"pending","updated":"2026-11-01T00:00:00Z"},{"number":4,"title":"WIP","url":"https://github.com/o/n/pull/4","branch":"wip","draft":true,"author":"bob","assignees":[],"reviewers":[],"review":"none","updated":"2026-11-02T00:00:00Z"}]`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
	if f.Cmds[0].Dir != NeutralDir {
		t.Errorf("ran in %q", f.Cmds[0].Dir)
	}
}

func TestMe(t *testing.T) {
	for out, want := range map[string]string{"octo-cat\n": "octo-cat", "-bad\n": "", "a b\n": "", "\n": ""} {
		f := &systest.Fake{}
		f.On([]string{"gh", "api", "user"}, sys.Result{Stdout: []byte(out)}, nil)
		got, err := Me(context.Background(), f)
		if got != want || (want == "") != (err != nil) {
			t.Errorf("Me(%q) = %q, %v; want %q", out, got, err, want)
		}
	}
}

func TestIssueBranch(t *testing.T) {
	for _, c := range []struct {
		n     int
		title string
		want  string
	}{
		{118, "Billing page 500s when the card is declined", "issue-118-billing-page-500s-when"},
		{7, "Ünïcode & symbols!!", "issue-7-n-code-symbols"},
		{9, "", "issue-9"},
		{12, "Supercalifragilisticexpialidocious words", "issue-12"},
	} {
		if got := IssueBranch(c.n, c.title); got != c.want || len(got) > 32 {
			t.Errorf("IssueBranch(%d, %q) = %q; want %q", c.n, c.title, got, c.want)
		}
	}
}
