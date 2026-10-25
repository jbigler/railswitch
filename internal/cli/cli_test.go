package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main(context.Background(), args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := run(t, "version")
	if code != 0 || out != "railswitch dev\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestVersionJSON(t *testing.T) {
	code, out, _ := run(t, "--json", "version")
	if code != 0 || out != "{\"version\":\"dev\"}\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, errs := run(t, "nope")
	if code != 2 || !strings.Contains(errs, `unknown command "nope"`) {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
}
