package pack

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
	// a line that isn't JSON (a half-written last line, anything foreign)
	// costs only that line: the rest still counts
	broken := filepath.Join(t.TempDir(), "broken.jsonl")
	if err := os.WriteFile(broken, []byte("not json\n"+lines+`{"type":"assistant","message":{"id":"m3","us`), 0o600); err != nil {
		t.Fatal(err)
	}
	bp, _ := json.Marshal(broken)
	s = run(`{"hook_event_name":"Stop","transcript_path":` + string(bp) + `,"last_assistant_message":"done"}`)
	if u, _ := s["usage"].(map[string]any); !mapsEqual(u, want) {
		t.Errorf("Stop with broken lines: usage = %v; want %v", s["usage"], want)
	}
	s = run(`{"hook_event_name":"Stop","transcript_path":"/nonexistent","last_assistant_message":"x"}`)
	if _, ok := s["usage"]; ok {
		t.Errorf("Stop without a transcript: %v; want no usage", s)
	}
}

// D187: with the payload's session_id, the usage carries the session, its
// context (the last main-chain request's input, cached or not; a
// subagent's sidechain request is not it) and its start (the transcript's
// first timestamp; without one, the start the status file had for that
// session).
func TestHookSession(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	file := filepath.Join(t.TempDir(), "claude.json")
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
		u, _ := s["usage"].(map[string]any)
		return u
	}
	write := func(lines ...string) string {
		p := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(p)
		return string(b)
	}
	tp := write(
		`{"type":"user","timestamp":"2026-11-04T10:00:00.123Z","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","timestamp":"2026-11-04T10:00:05Z","message":{"id":"m1","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}`,
		`{"type":"assistant","timestamp":"2026-11-04T10:01:00Z","message":{"id":"m2","usage":{"input_tokens":3,"output_tokens":7,"cache_read_input_tokens":130,"cache_creation_input_tokens":4}}}`,
		`{"type":"assistant","isSidechain":true,"message":{"id":"m3","usage":{"input_tokens":900,"output_tokens":1}}}`,
	)
	u := run(`{"hook_event_name":"Stop","session_id":"abc-1","transcript_path":` + tp + `,"last_assistant_message":"done"}`)
	if u["session"] != "abc-1" || u["context"] != float64(3+130+4) || u["started"] != float64(1793786400) || u["input"] != float64(913) {
		t.Errorf("usage %v", u)
	}
	// No timestamps: the start the file had for the same session stays.
	untimed := write(`{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":1,"output_tokens":1}}}`)
	if u := run(`{"hook_event_name":"Stop","session_id":"abc-1","transcript_path":` + untimed + `}`); u["started"] != float64(1793786400) || u["context"] != float64(1) {
		t.Errorf("same session, no timestamp: %v", u)
	}
	// Another session without timestamps starts now.
	if u := run(`{"hook_event_name":"Stop","session_id":"def-2","transcript_path":` + untimed + `}`); u["session"] != "def-2" || u["started"].(float64) < 1793786400+86400 {
		t.Errorf("new session: %v", u)
	}
	// No session_id (an older Claude): the counts alone.
	if u := run(`{"hook_event_name":"Stop","transcript_path":` + tp + `}`); len(u) != 4 {
		t.Errorf("no session: %v", u)
	}
}

// The transcript is read in time linear in its length: a 20k-message one
// takes well under a second. The old reduce copied its map on every line,
// several times slower and quadratic; 10 s leaves CI room.
func TestHookLongTranscript(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	var b strings.Builder
	for i := range 20000 {
		fmt.Fprintf(&b, `{"type":"assistant","timestamp":"2026-11-04T10:00:00Z","message":{"id":"m%d","usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}}`+"\n", i)
	}
	tr := filepath.Join(t.TempDir(), "long.jsonl")
	if err := os.WriteFile(tr, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "claude.json")
	tp, _ := json.Marshal(tr)
	cmd := exec.Command("bash", claudeHook)
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"Stop","session_id":"s","transcript_path":` + string(tp) + `}`)
	cmd.Env = append(withoutStatus(os.Environ()), "RAILSWITCH_STATUS_FILE="+file)
	start := time.Now()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("hook took %v on 20k messages", d)
	}
	data, _ := os.ReadFile(file)
	if !strings.Contains(string(data), `"input":20000,"output":40000,"cache_read":60000,"cache_write":80000`) || !strings.Contains(string(data), `"context":8`) {
		t.Errorf("status %s", data)
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
