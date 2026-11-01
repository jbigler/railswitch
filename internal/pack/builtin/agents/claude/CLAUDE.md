# Container instructions

<!-- Shipped in railswitch's claude@1 agent package, mounted read-only at
     /opt/railswitch/agent/CLAUDE.md and copied to ~/.claude/railswitch.md
     on every start (edits to that copy are lost; ~/.claude/CLAUDE.md, which
     imports it, is yours). -->

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

# Shared memory (beads)

When the project turns on shared memory, `bd` is installed and every
session starts with its context (the "Shared memory (railswitch)" section,
then `bd prime`). Without it there is no `bd`: ignore this section.

- The store is shared by the agents of every worktree of the project. Label
  each issue you create `worktree:$WORKTREE_SLUG`; filter your own work with
  `bd ready -l worktree:$WORKTREE_SLUG`.
- Claim before you start (`bd update <id> --claim`): when it fails, another
  agent has it; pick something else.
- Follow the project's guidance printed at the top of that context: never
  store secrets, credentials, or personal or patient data.
- Memories go to `bd remember`, never to your auto memory
  (`~/.claude/projects/*/memory/`, `MEMORY.md`): your home is this
  worktree's and is deleted with it. When asked to remember something,
  or when you learn something the next agent should know, search
  `bd memories <word>` first and update that key
  (`bd remember --key <key> "<text>"`) rather than add another.
- `bd` never runs git here (its config sets no-git-ops). Never run
  `bd init`.

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
