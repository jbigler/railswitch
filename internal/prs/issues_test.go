package prs

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

// D144: gh's issue objects reduced to names and logins; arrays never null.
func TestIssues(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"gh", "issue", "list"}, sys.Result{Stdout: []byte(`[
{"assignees":[{"login":"ann"}],"author":{"login":"bob","is_bot":false},"labels":[{"name":"bug","color":"D6393F"}],"number":7,"title":"Fix it","updatedAt":"2026-10-31T19:00:32Z","url":"https://github.com/o/n/issues/7"},
{"assignees":[],"author":{"login":"cat"},"labels":[],"number":8,"title":"Other","updatedAt":"2026-10-30T00:00:00Z","url":"https://github.com/o/n/issues/8"}]`)}, nil)
	got, err := Issues(context.Background(), f, "o/n")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gh", "issue", "list", "--repo", "o/n", "--state", "open", "--limit", "100", "--json", "number,title,url,labels,assignees,updatedAt,author"}
	if !slices.Equal(f.Calls[0], want) || f.Cmds[0].Dir != NeutralDir {
		t.Errorf("call %q in %q", f.Calls[0], f.Cmds[0].Dir)
	}
	b, _ := json.Marshal(got)
	if s := string(b); s != `[{"number":7,"title":"Fix it","url":"https://github.com/o/n/issues/7","labels":["bug"],"assignees":["ann"],"author":"bob","updated":"2026-10-31T19:00:32Z"},{"number":8,"title":"Other","url":"https://github.com/o/n/issues/8","labels":[],"assignees":[],"author":"cat","updated":"2026-10-30T00:00:00Z"}]` {
		t.Errorf("got %s", s)
	}
}
