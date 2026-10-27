# Container instructions

<!-- Shipped in railswitch's claude@1 agent package, mounted read-only at
     /opt/railswitch/agent/CLAUDE.md and copied to ~/.claude/CLAUDE.md by the
     entrypoint on every start: edits to the copy are lost on restart. -->

# Browser (chrome-devtools MCP)

When the project has a browser, its Chromium is controllable through the
`chrome-devtools` MCP server (already configured; it connects through a local
CDP bridge on 127.0.0.1:9222). Without a browser the server is not configured.

- Use the chrome-devtools MCP tools to navigate, click, fill forms, read the
  DOM, take screenshots, and inspect console and network activity.
- Open the app at http://$WORKTREE_HOST, this worktree's dev server. Stick to
  that host rather than a service's internal name: the app builds its URLs
  from it.
- The human can watch and use the same browser, so it suits cooperative
  debugging (e.g. ask them to log in by hand).

# Network egress is allowlisted from the host

Outbound traffic is filtered by a firewall the host applies to this container
from outside. You cannot change it from in here: there is no sudo and no
NET_ADMIN.

- A connection to a host that is not on the allowlist fails immediately
  ("Connection refused" / "No route to host" / "administratively prohibited").
  That is the firewall, not an outage: do not retry or work around it.
- If you need a host, name it and ask the human to run
  `railswitch fw allow agent-$RAILSWITCH_AGENT <host>`, or to add it to
  `[agents.<name>] allow` in project.toml, where `<name>` is the value of
  `$RAILSWITCH_AGENT` in your environment.

# Git worktrees: NEVER prune, add or remove from inside this container

This container mounts only this worktree (plus the base repository's .git).
Sibling worktrees are not mounted, and git records each worktree by its
absolute host path.

- NEVER run `git worktree prune`: from in here it sees every other worktree
  as missing and deletes them all from the registry.
- NEVER run `git worktree add`, `git worktree remove`, or `rm -rf` on any
  worktree, or edit anything under `.git/worktrees/`.
- Worktrees are created and removed on the host with railswitch. Ask the
  human to do it.
- `git worktree list` flags every other worktree `prunable`, because only
  this one is mounted. That is expected and does not mean anything is broken.
