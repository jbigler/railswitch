package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

// D159: agent queue add (words, stdin, --issue), ls (--json), rm, clear;
// text agent send would refuse is refused.
func TestAgentQueueCLI(t *testing.T) {
	ws, _ := lockedWS(t)
	f := fakeHost()
	f.On([]string{"gh", "issue", "view", "42"}, sys.Result{Stdout: []byte("Login\x1b[2J times out\n")}, nil)
	wt := []string{"-C", ws, "-w", "feat", "agent", "queue"}
	q := func(args ...string) []string { return append(append([]string{}, wt...), args...) }
	if code, out, errs := runWith(t, f, q("add", "fix", "the", "tests")...); code != 0 || !strings.Contains(out, "queued #1") {
		t.Fatalf("add words: %d %q %q", code, out, errs)
	}
	if code, _, errs := runStdin(t, f, "one\r\ntwo\r\n", q("add")...); code != 0 {
		t.Fatalf("add stdin: %d %q", code, errs)
	}
	if code, _, errs := runWith(t, f, q("add", "--issue", "42")...); code != 0 {
		t.Fatalf("add issue: %d %q", code, errs)
	}
	if code, _, _ := runStdin(t, f, "x\x1b[2J", q("add")...); code != 1 {
		t.Errorf("escape: exit %d; want 1", code)
	}
	if code, _, _ := runStdin(t, f, " \n", q("add")...); code != 1 {
		t.Errorf("blank: exit %d; want 1", code)
	}
	if code, _, _ := runWith(t, f, q("add", "--issue", "42", "and", "words")...); code != 2 {
		t.Errorf("issue and words: exit %d; want 2", code)
	}
	if code, _, errs := runWith(t, f, q("rm", "#1")...); code != 0 {
		t.Fatalf("rm: %d %q", code, errs)
	}
	if code, _, _ := runWith(t, f, q("rm", "1")...); code != 1 {
		t.Errorf("rm again: exit %d; want 1", code)
	}
	code, out, errs := runWith(t, f, append([]string{"--json"}, q("ls")...)...)
	var doc queueDoc
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || doc.Slug != "feat" || len(doc.Items) != 2 {
		t.Fatalf("ls --json: %d %q %q", code, out, errs)
	}
	if doc.Items[0].Text != "one\ntwo" || !strings.HasPrefix(doc.Items[1].Text, "Work on GitHub issue #42: Login [2J times out\n") {
		t.Errorf("items %+v", doc.Items)
	}
	if code, out, _ := runWith(t, f, q("ls")...); code != 0 || !strings.Contains(out, "2   one\n") {
		t.Errorf("ls: %d %q", code, out)
	}
	if code, _, _ := runWith(t, f, q("clear")...); code != 0 {
		t.Fatalf("clear: %d", code)
	}
	if code, out, _ := runWith(t, f, append([]string{"--json"}, q("ls")...)...); code != 0 || !strings.Contains(out, `"items":[]`) {
		t.Errorf("after clear: %q", out)
	}
	if code, out, _ := runWith(t, f, q("next")...); code != 0 || !strings.Contains(out, "held: the queue is empty") {
		t.Errorf("next, empty: %d %q", code, out)
	}
}
