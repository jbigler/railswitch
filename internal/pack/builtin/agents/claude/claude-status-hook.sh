#!/bin/sh
# Claude Code hook: writes this agent's status (D58 v1) to
# $RAILSWITCH_STATUS_FILE: {"v":1,"state":"…","message":"…","ts":<unix seconds>}.
# entrypoint-claude.sh registers it for several hook events; the event name
# arrives in the JSON payload on stdin.
set -eu

file="${RAILSWITCH_STATUS_FILE:-}"
[ -n "$file" ] || exit 0

in=""
tmp=""
# Exit 2 from a UserPromptSubmit or PreToolUse hook blocks the prompt or the
# tool. Recording status must never do that, so any failure exits 1: Claude
# reports it and carries on.
trap 'status=$?; rm -f ${in:+"$in"} ${tmp:+"$tmp"}; [ "$status" = 0 ] || exit 1' EXIT
dir=$(dirname "$file")
in=$(mktemp)
cat > "$in"

# The previous state: an idle_prompt notification after done, question or
# permission keeps that state and its message.
prev=""
[ -f "$file" ] && prev=$(jq -r '.state // empty' "$file" 2>/dev/null || true)

# Temp + rename in the same dir, so a reader never sees a half-written file. A
# temp of its own: subagents fire these hooks alongside the main session.
tmp=$(mktemp "$dir/.status.XXXXXX")
jq -c --arg prev "$prev" --argjson ts "$(date +%s)" '
  # Markdown flattened to one line: fences, bold, backticks, heading and list markers.
  def flat:
    gsub("```[\\s\\S]*?```"; " ")
    | gsub("\\*\\*|`"; "")
    | gsub("(?m)^[ \\t]*(#+|[-*]|\\d+\\.)[ \\t]+"; "")
    | gsub("\\s+"; " ")
    | sub("^ "; "") | sub(" $"; "");
  def tail($n): if length > $n then "… " + (.[length - $n:] | sub("^ "; "")) else . end;
  def rel($cwd): if $cwd != "" and startswith($cwd + "/") then .[($cwd | length) + 1:] else . end;
  def question: (.tool_input.questions // [])[0].question // "Claude has a question";
  def summary:
    (.cwd // "") as $cwd
    | (.tool_input // {}) as $t
    | if .tool_name == "Bash" then
        "Bash: " + (($t.command // "") | split("\n")[0] | .[0:80])
      elif (.tool_name | IN("Edit", "Write", "Read", "MultiEdit", "NotebookEdit")) then
        .tool_name + " " + (($t.file_path // $t.notebook_path // "") | rel($cwd))
      else
        (.tool_name // "tool")
      end;
  def out($state; $message): {v: 1, state: $state, message: $message, ts: $ts};
  .hook_event_name as $e
  | if $e == "SessionStart" then out("idle"; "")
    elif $e == "UserPromptSubmit" or $e == "PostToolUse" then out("working"; "")
    elif $e == "PreToolUse" or $e == "PermissionRequest" then
      if .tool_name == "AskUserQuestion" then
        # AskUserQuestion fires PermissionRequest too, even with bypassPermissions.
        out("question"; question)
      elif $e == "PermissionRequest" then out("permission"; "Permission: " + summary)
      else out("working"; summary)
      end
    elif $e == "Notification" then
      if .notification_type == "permission_prompt" then
        out("permission"; .message // "Claude needs your permission")
      elif (.notification_type // "" | startswith("elicitation_dialog")) then
        out("question"; .message // "Claude has a question")
      elif .notification_type == "idle_prompt" and ($prev | IN("done", "question", "permission") | not) then
        out("idle"; "")
      else empty
      end
    elif $e == "Stop" then out("done"; (.last_assistant_message // "") | flat | tail(600))
    elif $e == "SessionEnd" then out("off"; "")
    else empty
    end
' "$in" > "$tmp"
# Nothing to record (an unknown event, or a notification that keeps the state).
[ -s "$tmp" ] || exit 0
chmod 644 "$tmp" # mktemp's 0600; railswitch reads it on the host
mv "$tmp" "$file"
tmp=""
