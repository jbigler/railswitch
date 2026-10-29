package web

import (
	"strings"
	"testing"
)

// TestSettingsHooks (D126): the Hooks card shows each list one command per
// line, and a staged hooks.set reaches the CLI with its values.
func TestSettingsHooks(t *testing.T) {
	f := settingsFixture(t)
	f.cli.set("--json -C /ws/ftest project show", strings.Replace(projectShowJSON, `"options":[`, `"hooks":{"deps":[],"start":["bin/rails db:test:prepare","echo \"<b>\""],"create":[]},"options":[`, 1))
	body := f.do(req("GET", "/p/ftest/settings", nil)).Body.String()
	contains(t, "settings", body, `id="hooks"`, `data-list="start" data-orig="bin/rails db:test:prepare
echo &#34;&lt;b&gt;&#34;"`, `>bin/rails db:test:prepare
echo &#34;&lt;b&gt;&#34;</textarea>`, `data-list="deps" data-orig=""`, `data-list="create"`)
	noCSPViolation(t, "settings", body)

	body = f.do(jsonPost("/p/ftest/settings/preview", `[{"op":"hooks.set","key":"start","values":["a","b"]},{"op":"hooks.set","key":"deps"}]`)).Body.String()
	contains(t, "staged", body, "Hooks start: 2 commands", "Hooks deps: removed")
	stdin, ok := f.cli.stdinOf("--json -C /ws/ftest settings preview")
	if !ok || string(stdin) != `[{"op":"hooks.set","key":"start","values":["a","b"]},{"op":"hooks.set","key":"deps"}]` {
		t.Errorf("preview stdin %q", stdin)
	}
}
