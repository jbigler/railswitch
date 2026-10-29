# Agents

An agent is an AI coding assistant (Claude Code is built in) running in
its own container beside the app. It sees the worktree and the app's
runtimes, and nothing else of your machine.

## Adding one

A new project gets the built-in default agent unless you untick it. To add
one later:

```bash
railswitch agent add claude                 # the built-in claude@1
railswitch agent add helper --use local:helper@1 --run service
```

That adds `[agents.<name>]` to `project.toml`, declares the secrets its
package needs, and ejects its Containerfile so you can change its image.
Then set its secret, for Claude a token from `claude setup-token`:

```bash
railswitch secret set claude_token
```

## Running it

```bash
railswitch agent                    # the only agent, in this terminal
railswitch agent --new-term claude  # in a new terminal window
railswitch agent stop
```

An agent runs **on demand** (a container for as long as you use it) or as
a **service** (started by `up`, and `agent` attaches to it). One agent
runs at a time per worktree. The dashboard starts it in a terminal from
the worktree's row and shows when it is waiting on you: a permission
prompt, a question, a finished reply (`◆ N need input`).

Its home, logins and history included, is a folder per worktree
(`.railswitch/agent-home/<worktree>/<agent>`), kept across `down` and
deleted by `wt rm --force`.

## Its sandbox

- **Firewall.** An agent's firewall is always enforce: it reaches only its
  package's hosts (Claude: Anthropic's API, GitHub, npm), the project's
  `agent` list, and the worktree's own containers. It can't change its own
  rules.
- **The rest of the project too.** An agent edits code the app, workers
  and installs run, so a project with agents has them on enforce as well.
  See [The firewall](firewall.md).
- **No host credentials.** No SSH keys, SSH agent or git config reach it.
  For git over HTTPS, declare a token secret with `git_host`; to sign
  commits, a signing-only key with `secret add --git-signing`.
- **Git hooks are guarded.** The worktree's `.git/hooks` are mounted
  read-only, and `railswitch hooks check` (and `doctor`) report hooks or
  git config an agent changed.

## Your own agent

An agent package is a folder with `agent.toml` (its hosts, mounts, secrets
and environment) and a Containerfile built on the agent base image. Copy
the built-in one (`railswitch pack eject claude@1`) or start from scratch
in `agents/<name>/`; see [the package format](../packs.md).

Agents are each person's own choice: templates never carry them
([Sharing](sharing.md)).

Next: [The editor](editor.md).
