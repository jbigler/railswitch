package pack

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

const claudeHook = "builtin/agents/claude/claude-status-hook.sh"

// runHook pipes the fixture payload through the hook with env added.
func runHook(t *testing.T, fixture string, env ...string) error {
	t.Helper()
	in, err := os.Open(filepath.Join("testdata", "hook-payloads", fixture+".json"))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	cmd := exec.Command("bash", claudeHook)
	cmd.Stdin = in
	cmd.Env = append(withoutStatus(os.Environ()), env...)
	return cmd.Run()
}

func TestHookMapping(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "claude.json")
	for _, tc := range []struct {
		fixture, state, message string
	}{
		{"SessionStart", "idle", ""},
		{"UserPromptSubmit", "working", ""},
		{"PreToolUse.Bash", "working", "Bash: ls -la"},
		{"PermissionRequest.Bash", "permission", "Permission: Bash: bin/rails db:migrate"},
		{"Notification.permission_prompt", "permission", "Claude needs your permission to use Bash"},
		{"Stop.long", "done", ""},
		{"SessionEnd", "off", ""},
	} {
		if err := runHook(t, tc.fixture, "RAILSWITCH_STATUS_FILE="+file); err != nil {
			t.Fatalf("%s: %v", tc.fixture, err)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var s struct {
			V       int    `json:"v"`
			State   string `json:"state"`
			Message string `json:"message"`
			TS      int64  `json:"ts"`
		}
		if err := json.Unmarshal(data, &s); err != nil {
			t.Fatalf("%s: %v: %s", tc.fixture, err, data)
		}
		if s.V != 1 || s.TS <= 0 || s.State != tc.state {
			t.Errorf("%s: got %s, want state %s", tc.fixture, data, tc.state)
		}
		if tc.fixture == "Stop.long" {
			if n := utf8.RuneCountInString(s.Message); n > 602 || !strings.HasSuffix(s.Message, "Want me to start it and run them?") {
				t.Errorf("Stop.long: message (%d chars) = %q", n, s.Message)
			}
		} else if s.Message != tc.message {
			t.Errorf("%s: message %q, want %q", tc.fixture, s.Message, tc.message)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("status dir holds %d entries, want only the status file (temps renamed)", len(entries))
	}

	// Unset: nothing to do.
	cwd := t.TempDir()
	cmd := exec.Command("bash", mustAbs(t, claudeHook))
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"SessionStart"}`)
	cmd.Env = withoutStatus(os.Environ())
	if err := cmd.Run(); err != nil {
		t.Errorf("unset RAILSWITCH_STATUS_FILE: %v", err)
	}
	if entries, _ := os.ReadDir(cwd); len(entries) != 0 {
		t.Errorf("unset RAILSWITCH_STATUS_FILE wrote %v", entries)
	}

	// Unwritable dir: exit 1, never 2 (2 would block the prompt or tool).
	ro := t.TempDir()
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o700) })
	err := runHook(t, "UserPromptSubmit", "RAILSWITCH_STATUS_FILE="+filepath.Join(ro, "claude.json"))
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Errorf("unwritable dir: %v, want exit 1", err)
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func withoutStatus(env []string) []string {
	var out []string
	for _, e := range env {
		if !strings.HasPrefix(e, "RAILSWITCH_STATUS_FILE=") {
			out = append(out, e)
		}
	}
	return out
}

func TestClaudeBuiltin(t *testing.T) {
	cat := builtins(t)
	ref := Ref{Name: "claude", Version: 1}
	a := cat.Agents[ref]
	if a == nil {
		t.Fatalf("no agent claude@1; have %v", cat.Agents)
	}
	if r, ok := cat.DefaultAgent(); !ok || r != ref {
		t.Errorf("DefaultAgent = %v, %v; want claude@1", r, ok)
	}
	if !slices.Contains(a.Allow, "api.anthropic.com") {
		t.Errorf("allow = %v, want api.anthropic.com", a.Allow)
	}
	if a.Env["DISABLE_AUTOUPDATER"] != "1" {
		t.Errorf("env = %v, want DISABLE_AUTOUPDATER=1", a.Env)
	}
}

// TestHookUsage: Stop counts the session's tokens from its transcript, one
// usage per message id; the next events carry the count over.
func TestHookUsage(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "claude.json")
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	lines := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}`,
		`{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}`,
		`{"type":"assistant","message":{"id":"m2","usage":{"input_tokens":1,"output_tokens":7}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcript, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(payload string) map[string]any {
		t.Helper()
		cmd := exec.Command("bash", claudeHook)
		cmd.Stdin = strings.NewReader(payload)
		cmd.Env = append(withoutStatus(os.Environ()), "RAILSWITCH_STATUS_FILE="+file)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		var s map[string]any
		data, _ := os.ReadFile(file)
		if err := json.Unmarshal(data, &s); err != nil {
			t.Fatalf("%v: %s", err, data)
		}
		return s
	}
	want := map[string]any{"input": float64(11), "output": float64(12), "cache_read": float64(100), "cache_write": float64(20)}
	tp, _ := json.Marshal(transcript)
	s := run(`{"hook_event_name":"Stop","transcript_path":` + string(tp) + `,"last_assistant_message":"done"}`)
	if u, _ := s["usage"].(map[string]any); !mapsEqual(u, want) {
		t.Errorf("Stop usage = %v; want %v", s["usage"], want)
	}
	s = run(`{"hook_event_name":"UserPromptSubmit"}`)
	if u, _ := s["usage"].(map[string]any); s["state"] != "working" || !mapsEqual(u, want) {
		t.Errorf("UserPromptSubmit: %v; want working with the usage carried over", s)
	}
	s = run(`{"hook_event_name":"Stop","transcript_path":"/nonexistent","last_assistant_message":"x"}`)
	if _, ok := s["usage"]; ok {
		t.Errorf("Stop without a transcript: %v; want no usage", s)
	}
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}
