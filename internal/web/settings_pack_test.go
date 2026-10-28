package web

import (
	"strings"
	"testing"
)

// The Settings page shows the pack with its choices and stages a pack
// edit; the staged card names the change.
func TestSettingsPackRow(t *testing.T) {
	f := settingsFixture(t)
	body := f.do(req("GET", "/p/ftest/settings", nil)).Body.String()
	if strings.Contains(body, `data-pack`) {
		t.Error("a pack row with no other choice")
	}
	f.cli.set("--json -C /ws/ftest project show", strings.Replace(projectShowJSON, `"options":[`, `"pack":{"ref":"web@1","choices":["local:web@1","web@1"]},"options":[`, 1))
	body = f.do(req("GET", "/p/ftest/settings", nil)).Body.String()
	contains(t, "settings", body, `<div class="opt" data-pack data-key="pack" data-ref="web@1">`,
		`<input type="radio" name="stack-pack" value="local:web@1">`, `value="web@1" checked>`)
	noCSPViolation(t, "settings", body)
	body = f.do(jsonPost("/p/ftest/settings/preview", `[{"op":"pack","ref":"local:web@1"}]`)).Body.String()
	contains(t, "staged", body, "Pack: web@1 → local:web@1")
}
