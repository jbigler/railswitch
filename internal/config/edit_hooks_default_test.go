package config

import (
	"strings"
	"testing"
)

// D215: SetDefaultAgent marks one agent and unmarks the others, leaving
// the rest of the text as it was.
func TestSetDefaultAgent(t *testing.T) {
	const data = "schema = 1\n\n[agents.claude]\nuse = \"claude@1\"\ndefault = true # mine\n\n[agents.probe]\nuse = \"local:probe@1\"\n\n[agents.probe.env]\nA = \"1\"\n"
	got, err := SetDefaultAgent(data, "probe")
	if err != nil {
		t.Fatal(err)
	}
	want := "schema = 1\n\n[agents.claude]\nuse = \"claude@1\"\n\n[agents.probe]\nuse = \"local:probe@1\"\ndefault = true\n\n[agents.probe.env]\nA = \"1\"\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if again, err := SetDefaultAgent(got, "probe"); err != nil || again != got {
		t.Errorf("setting it again changed the text: %v\n%s", err, again)
	}
	if _, err := SetDefaultAgent(data, "nobody"); err == nil || !strings.Contains(err.Error(), "no agent nobody") {
		t.Errorf("unknown agent: %v", err)
	}
	// an agent written as dotted keys, with no default to take out, is left alone
	dotted := "schema = 1\nagents.b.use = \"b@1\"\n\n[agents.a]\nuse = \"a@1\"\n"
	if got, err := SetDefaultAgent(dotted, "a"); err != nil || got != dotted+"default = true\n" {
		t.Errorf("beside a dotted agent: %v\n%s", err, got)
	}
	if _, err := SetDefaultAgent("schema = 1\nagents = { a = { use = \"a@1\" }, b = { use = \"b@1\" } }\n", "a"); err == nil {
		t.Error("an inline agents table was edited")
	}
}
