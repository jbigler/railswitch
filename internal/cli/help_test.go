package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every flag set's --help names its arguments: a command added without a
// synopsis fails here.
func TestEveryFlagSetHasSynopsis(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	named := regexp.MustCompile(`e\.flags\("([^"]+)"\)`)
	var names []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range named.FindAllSubmatch(b, -1) {
			names = append(names, string(m[1]))
		}
	}
	// Built from a variable: simple's, shareCommand's and fw's.
	names = append(names, "up", "stop", "share", "unshare", "fw show", "fw allow")
	if len(names) < 90 {
		t.Fatalf("found %d flag sets; the pattern no longer matches e.flags calls", len(names))
	}
	for _, n := range names {
		if synopses[n] == "" {
			t.Errorf("e.flags(%q) has no entry in synopses", n)
		}
	}
}

func TestHelpShowsArgumentsAndLongFlags(t *testing.T) {
	var out bytes.Buffer
	e := &env{stderr: &out}
	fs := e.flags("project new")
	fs.String("repo", "", "the app's git `url`")
	fs.Bool("up", false, "run up")
	fs.String("pack", "rails@1", "the stack's pack `ref`")
	if code, ok := parse(fs, []string{"--help"}); ok || code != 0 {
		t.Fatalf("--help: code %d ok %v", code, ok)
	}
	got := out.String()
	for _, want := range []string{"usage: railswitch project new <dir> --repo <url>", "\n  --repo url\n", "\n  --up\n", `(default "rails@1")`} {
		if !strings.Contains(got, want) {
			t.Errorf("help lacks %q:\n%s", want, got)
		}
	}
}
