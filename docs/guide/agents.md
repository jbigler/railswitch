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

`railswitch agent` starts the worktree's stack first (`up`), but doesn't
wait for the app to answer on its URL: an app that is still booting, or
unhealthy, never holds up the agent. (It does wait while the worktree's
`[hooks] create` haven't run yet, since they need the app.)

An agent runs **on demand** (a container for as long as you use it) or as
a **service** (`railswitch agent` starts its unit if needed and attaches;
`up` never starts it). One agent runs at a time per worktree. Agents start
from the terminal; the dashboard shows when one is waiting on you: a
permission prompt, a question, a finished reply (`◆ N need input`).

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
- **No host credentials.** No SSH keys, SSH agent or git config file reach
  it (only your git name and email are passed in). For a GitHub
  repository, `project new` declares a `github_token` secret for the
  agents and nvim: `railswitch secret set github_token` gives them git
  over HTTPS. To sign commits, add a signing-only key with `secret add
  --git-signing`.
- **GitHub CLI.** Agents have `gh`. A `github_token` (a secret with
  `git_host = "github.com"`) an agent receives reaches it as `GH_TOKEN`
  too, so `gh pr`, `gh stack` and the like work with the token you set
  (not when the agent already gets a `GH_TOKEN` of its own). A project
  made before this gets `gh` once it accepts the agent base update
  (Review update) and, when its `Containerfile.agent-base` is its own
  copy, takes the new default in Settings → Images.
- **Git hooks are guarded.** The worktree's `.git/hooks` are mounted
  read-only, and `railswitch hooks check` (and `doctor`) report hooks or
  git config an agent changed.

## Setting up its home

An agent's home is its own per worktree, so tools it keeps there (`gh`
extensions, agent skills) go in its setup list, in Settings → Agent setup
or `project.toml`:

```toml
[agents.claude]
use = "claude@1"
setup = [
  "gh extension install github/gh-stack",
  "gh skill install github/gh-stack --scope user",
]
```

The commands run in the agent's container, one per line in its own shell
(no input: one that would prompt fails), before it first starts in a
worktree, and again whenever you change the list. A failing one stops the
rest and is reported, the agent starts anyway, and the next start tries
them again. An agent package needs an entrypoint for this. Agents are
yours: templates leave them out.

## A default config for every worktree

Set an agent up the way you like in one worktree (theme, plugins and
marketplaces, skills, settings), then make that the project's default:

```bash
railswitch agent default promote claude         # this worktree's config is the default
railswitch agent default promote claude --all   # …and every other worktree takes it too
railswitch agent default apply claude           # this worktree takes the default
railswitch agent default apply claude --all     # every worktree does
```

Or in the dashboard: **default config…** next to Agent in a worktree.

The default holds only the files the agent's package lists as its config
(for Claude: `settings.json`, `keybindings.json`, the status line,
`CLAUDE.local.md`, `agents/`, `commands/`, `hooks/`, `skills/`,
`output-styles/`, the plugin and marketplace records and `.claude.json`),
never its login, sessions or history. It lives in
`.railswitch/agent-default/<agent>/`. A new worktree's agent starts from
it; an existing one takes it the next time it starts (stop a running one
first). Each item the default has replaces the home's, and the replaced
ones are kept in `~/.railswitch-default-backup` in the agent's home;
Claude's `.claude.json` is merged instead, keeping each worktree's own
project entries. Plugins themselves are shared by every worktree already,
so a promoted plugin list works everywhere at once.

## Your own agent

An agent package is a folder with `agent.toml` (its hosts, mounts, secrets
and environment) and a Containerfile built on the agent base image. Copy
the built-in one (`railswitch pack eject claude@1`) or start from scratch
in `agents/<name>/`; see [the package format](../packs.md).

Agents are each person's own choice: templates never carry them
([Sharing](sharing.md)).

Next: [The editor](editor.md).
