#!/bin/sh
# Claude Code hook: writes this agent's status (D58 v1) to
# $RAILSWITCH_STATUS_FILE: {"v":1,"state":"…","message":"…","ts":<unix seconds>},
# with "usage": the session's tokens, counted from its transcript when a turn
# ends or a session starts (resumed), carried over by the other events; with
# the session's id, its context (the last request's input, cached or not)
# and when it started (its transcript's first timestamp, else when first seen).
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
usage=null
if [ -f "$file" ]; then
  prev=$(jq -r '.state // empty' "$file" 2>/dev/null || true)
  usage=$(jq -c '.usage // null' "$file" 2>/dev/null || echo null)
fi
# The transcript repeats a message's usage on each of its content blocks:
# one count per message id. A transcript that can't be read leaves no usage.
event=$(jq -r '.hook_event_name // empty' "$in")
transcript=$(jq -r '.transcript_path // empty' "$in")
session=$(jq -r '.session_id // "" | strings' "$in")
case "$event" in
Stop | SessionStart)
  prevusage=$usage
  usage=null
  if [ -n "$transcript" ] && [ -f "$transcript" ]; then
    # raw lines, each parsed alone: a line that isn't JSON (a half-written
    # last one) is skipped instead of losing the whole count
    usage=$(jq -cnR --arg session "$session" --argjson now "$(date +%s)" --argjson prev "$prevusage" '
      def n: numbers // 0;
      reduce (inputs | fromjson? | objects) as $l ({by: {}, last: null, first: null};
        .first //= ([$l.timestamp | strings | sub("\\.[0-9]+"; "") | try fromdateiso8601 catch empty][0])
        | ($l.message // {}) as $m
        | if $l.type == "assistant" and ($m | type) == "object" and ($m.id | type) == "string" and ($m.usage | type) == "object" then
            .by[$m.id] = $m.usage
            # a subagent request (a sidechain) is not the session context
            | if $l.isSidechain == true then . else .last = $m.usage end
          else . end)
      | [.by[]] as $u
      | def sum($k): [$u[] | .[$k] | numbers] | add // 0;
      {input: sum("input_tokens"), output: sum("output_tokens"),
       cache_read: sum("cache_read_input_tokens"), cache_write: sum("cache_creation_input_tokens")}
      + if $session == "" then {} else
          {session: $session,
           context: (.last // {} | (.input_tokens | n) + (.cache_read_input_tokens | n) + (.cache_creation_input_tokens | n)),
           started: (.first // (if ($prev | type) == "object" and $prev.session == $session then $prev.started else null end) // $now)}
        end
    ' "$transcript" 2>/dev/null) || usage=null
    [ -n "$usage" ] || usage=null
  fi
  ;;
esac

# Temp + rename in the same dir, so a reader never sees a half-written file. A
# temp of its own: subagents fire these hooks alongside the main session.
tmp=$(mktemp "$dir/.status.XXXXXX")
jq -c --arg prev "$prev" --argjson ts "$(date +%s)" --argjson usage "$usage" '
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
  def out($state; $message): {v: 1, state: $state, message: $message, ts: $ts} + (if $usage then {usage: $usage} else {} end);
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
