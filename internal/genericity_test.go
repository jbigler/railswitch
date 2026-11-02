package internal_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	filialRe = regexp.MustCompile(`(?i)filial`)
	claudeRe = regexp.MustCompile(`(?i)claude`)
	// The Linux terminals of C5; only internal/term may name them.
	termRe = regexp.MustCompile(`(?i)\b(tmux|kitty|wezterm|konsole|gnome-terminal|xfce4-terminal|terminator|ghostty|alacritty|foot|xterm)\b`)
	// Phoenix and its toolchain: core must stay framework-free (Stage 5).
	phoenixRe = regexp.MustCompile(`(?i)\b(phoenix|elixir|erlang)\b|\bmix `)
)

// TestGenericity keeps project, agent and terminal names out of core code.
func TestGenericity(t *testing.T) {
	for _, root := range []string{".", "../cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel := filepath.ToSlash(filepath.Join("internal", path))
			if root != "." {
				rel = filepath.ToSlash(strings.TrimPrefix(path, "../"))
			}
			if filialRe.Match(b) {
				t.Errorf("%s: names filial", rel)
			}
			if claudeRe.Match(b) && !strings.HasPrefix(rel, "internal/pack/builtin/agents/claude/") {
				t.Errorf("%s: names claude outside internal/pack/builtin/agents/claude/", rel)
			}
			// The agent session (D142) runs tmux inside the container, not as a host terminal.
			agentSession := rel == "internal/render/agentsession.go" || rel == "internal/pack/builtin/agentbase/Containerfile.agent-base"
			// The dashboard's browser terminal (D149) is the xterm.js library, not a host terminal.
			browserTerm := strings.HasPrefix(rel, "internal/web/static/term/")
			if m := termRe.Find(b); m != nil && !strings.HasPrefix(rel, "internal/term/") && !agentSession && !browserTerm {
				t.Errorf("%s: names terminal %q outside internal/term/", rel, m)
			}
			// Stage 5: Phoenix runs from a user-written pack; core knows
			// nothing of it (its fixture lives in testdata/).
			if m := phoenixRe.Find(b); m != nil {
				t.Errorf("%s: names %q: Phoenix knowledge belongs in its pack", rel, m)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
