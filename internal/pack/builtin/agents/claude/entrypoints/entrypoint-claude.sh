#!/bin/sh
set -eu

# No firewall setup in here: the host applies the egress firewall (profile
# agent-claude) from outside before this runs, and this container holds no
# NET_ADMIN to change it.

# The home directory is per-worktree, so ~/.claude is already isolated:
# credentials, sessions and plugin records never cross worktrees. The plugin
# cache and marketplaces are shared volumes mounted inside ~/.claude/plugins.
CONFIG_DIR="$HOME/.claude"
mkdir -p "$CONFIG_DIR"

# The project's default config (railswitch agent default promote, D135): a
# home holding the request (a new home, or `agent default apply`) takes it
# once, before the steps below put railswitch's own entries back.
REQUEST="$HOME/.railswitch-apply-default"
if [ -f "$REQUEST" ]; then
  if [ -n "$(ls -A /opt/railswitch/agent-default 2>/dev/null)" ]; then
    /usr/local/bin/railswitch-apply-default /opt/railswitch/agent-default "$HOME"
    echo "railswitch: applied the project's default Claude config (what it replaced: ~/.railswitch-default-backup)" >&2
  fi
  rm -f "$REQUEST"
fi

# Claude reads MCP servers and project trust from ~/.claude.json.
CLAUDE_JSON="$HOME/.claude.json"
[ -f "$CLAUDE_JSON" ] || echo '{}' > "$CLAUDE_JSON"

# Record the install method: the image pins a native binary under
# /opt/claude-code, and /doctor warns "install method is 'not set'" otherwise.
if ! jq -e '.installMethod == "native"' "$CLAUDE_JSON" >/dev/null 2>&1; then
  tmp="${CLAUDE_JSON}.tmp.$$"
  jq '.installMethod = "native"' "$CLAUDE_JSON" > "$tmp" && mv "$tmp" "$CLAUDE_JSON"
fi

# Pre-trust the worktree (/app), so a new home's first session does not stop
# at the "Do you trust the files in this folder?" dialog.
if ! jq -e '.projects["/app"].hasTrustDialogAccepted == true' "$CLAUDE_JSON" >/dev/null 2>&1; then
  tmp="${CLAUDE_JSON}.tmp.$$"
  jq '.projects["/app"] = ((.projects["/app"] // {}) + {hasTrustDialogAccepted: true, hasCompletedProjectOnboarding: true})' \
    "$CLAUDE_JSON" > "$tmp" && mv "$tmp" "$CLAUDE_JSON"
fi

# Browser (D59): when the project has a browser, bridge its CDP endpoint onto
# loopback. Chrome's DevTools HTTP server rejects non-localhost Host headers
# (DNS-rebinding guard), so chrome-devtools-mcp connects via 127.0.0.1.
if [ -n "${BROWSER_CDP_URL:-}" ]; then
  hostport=${BROWSER_CDP_URL#*://}
  hostport=${hostport%%/*}
  cdp_host=${hostport%:*}
  cdp_port=${hostport##*:}
  socat TCP-LISTEN:9222,bind=127.0.0.1,fork,reuseaddr "TCP:${cdp_host}:${cdp_port}" >/dev/null 2>&1 &

  # Both opt-outs matter: by default it sends usage statistics to Google and
  # fetches CrUX data from *.googleapis.com, which the firewall rejects.
  # Re-registered whenever the stored args differ.
  DEVTOOLS_ARGS="chrome-devtools-mcp@latest --browser-url=http://127.0.0.1:9222 --no-usage-statistics --no-performance-crux"
  if [ "$(jq -r '.mcpServers["chrome-devtools"].args // [] | join(" ")' "$CLAUDE_JSON" 2>/dev/null)" != "$DEVTOOLS_ARGS" ]; then
    claude mcp remove chrome-devtools -s user 2>/dev/null || true
    # shellcheck disable=SC2086
    claude mcp add -s user chrome-devtools -- npx $DEVTOOLS_ARGS 2>/dev/null || true
  fi
fi

# Register the status hook (D58) on every event it handles. Per event and
# idempotent: an event lacking the hook gets it, so older homes pick up new
# events. settings.json is only rewritten when something changed.
SETTINGS_JSON="$CONFIG_DIR/settings.json"
[ -f "$SETTINGS_JSON" ] || echo '{}' > "$SETTINGS_JSON"
tmp="${SETTINGS_JSON}.tmp.$$"
jq '
  def hook: "/usr/local/bin/claude-status-hook.sh";
  def entry: {hooks: [{type: "command", command: hook, timeout: 5}]};
  def runs_hook: any(.[]?.hooks[]?; .command == hook);
  .hooks = reduce ("SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse",
                   "PermissionRequest", "Stop", "Notification", "SessionEnd") as $e
    (.hooks // {}; if (.[$e] // []) | runs_hook then . else .[$e] = ((.[$e] // []) + [entry]) end)
' "$SETTINGS_JSON" > "$tmp"
if cmp -s "$tmp" "$SETTINGS_JSON"; then rm -f "$tmp"; else mv "$tmp" "$SETTINGS_JSON"; fi

# Refresh global memory from the package's CLAUDE.md. A plain copy, not a bind
# mount at this path: tools that rewrite it via temp + rename fail on a mount.
cp /opt/railswitch/agent/CLAUDE.md "$CONFIG_DIR/CLAUDE.md"

# ~/.local/bin/claude keeps PATH and /doctor's native-install check happy.
mkdir -p "$HOME/.local/bin"
ln -sf /usr/bin/claude "$HOME/.local/bin/claude"

# No args, or a first arg starting with '-': run claude. Otherwise run the
# given command (e.g. bash).
if [ $# -eq 0 ] || [ "${1#-}" != "$1" ]; then
  exec claude --dangerously-skip-permissions "$@"
fi

exec "$@"
