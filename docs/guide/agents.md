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

## Claude's instructions

railswitch's instructions for Claude (the browser, the container) are
refreshed on every start as `~/.claude/railswitch.md`, and
`~/.claude/CLAUDE.md` imports them with one line, `@railswitch.md`.
The rest of `CLAUDE.md` is yours: lines a tool adds there (`rtk init`'s
`@RTK.md`, say) or you write survive restarts.

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
`CLAUDE.md`, `CLAUDE.local.md`, `agents/`, `commands/`, `hooks/`, `skills/`,
`output-styles/`, the plugin and marketplace records and `.claude.json`),
never its login, sessions or history. It lives in
`.railswitch/agent-default/<agent>/`. A new worktree's agent starts from
it; an existing one takes it the next time it starts (stop a running one
first). Each item the default has replaces the home's, and the replaced
ones are kept in `~/.railswitch-default-backup` in the agent's home;
Claude's `.claude.json` is merged instead, keeping each worktree's own
project entries. Plugins themselves are shared by every worktree already,
so a promoted plugin list works everywhere at once.

## Shared memory

Each worktree's agent starts from nothing: what one agent learned, or
planned, the next one doesn't know. Shared memory gives every agent of the
project, in every worktree, one task tracker and notebook:
[beads](https://github.com/gastownhall/beads) (`bd`). It is off until you
turn it on in `project.toml` (or with the `memory.on` and
`memory.guidance` Settings edits, `railswitch settings apply`):

```toml
[memory]
# Optional: what agents are told about the store, replacing the default
# (no secrets, credentials, personal or patient data).
guidance = "Never store patient data, credentials or customer names. Summarise, don't paste logs."
```

What happens then (D136–D146):

- **One server per project.** The first `railswitch agent` starts
  `<prefix>-memory`, a Dolt database server whose data is the volume
  `<prefix>_memory`. Agents talk to it as clients. It isn't in any worktree:
  `down` and `wt rm` leave it and its data alone, and `project rm` stops it
  (`--purge` also deletes the volume). Nothing is pushed anywhere and there
  is no git sync: the volume and your backups are the only copies.
- **Only agents reach it.** It sits on its own network, which only the
  project's agents join and which has no way out. That network is the one
  thing an agent's firewall gains: the app, workers and installs can't
  reach it, and nothing is published on your machine.
- **Agents get `bd`.** The agent images install a pinned bd (the release's
  checksum is checked), configured for the server and in stealth mode: bd
  never touches the worktree's git. Usage metrics are off.
- **They start primed.** At every session start (Claude: a SessionStart
  hook) the agent reads `railswitch-prime`: your guidance, its worktree's
  label, then `bd prime` (beads' workflow and every memory).
- **Work is labelled by worktree.** Agents label what they create
  `worktree:<slug>` and look for work with `bd ready -l worktree:<slug>`.
  Claiming (`bd update <id> --claim`) is atomic: when two agents claim one
  task, one gets it and the other is told it's taken.
- **Memories go to bd.** Every agent is told to keep what it learns in
  shared memory (`bd remember`) and not in its own tool's memory files
  (Claude's auto memory), which live in the worktree's agent home and are
  deleted with it. This replaces rails-dev-podman's shared
  `claude-memory/` folder. Claude's own memory still works; it is just
  that worktree's.

**Never put secrets, credentials or personal data in memory**, and in a
healthcare project no patient data: every agent of the project reads all
of it, at the start of every session, and it stays until deleted. The
guidance says so to the agents; it doesn't stop them, so keep the rule in
your own instructions too.

### Bringing in old memory files

Memory files from earlier agent runs (rails-dev-podman's `claude-memory/`,
another machine's `~/.claude/projects/*/memory/`) can be reviewed into
shared memory by an agent. Copy them into a worktree's agent home, outside
the worktree so they never reach git:

```bash
mkdir -p .railswitch/agent-home/<worktree>/claude/memory-import
cp -r /path/to/old-memory/. .railswitch/agent-home/<worktree>/claude/memory-import/
```

Then ask the agent to read `~/memory-import`, store each file still true
for every agent with `bd remember` (skipping secrets, personal or patient
data, and anything about one branch), and list what it skipped. Delete the
folder afterwards and take a `railswitch memory backup`.

### Seeing it

```bash
railswitch memory                # the server, counts, each worktree's ready, claimed and blocked work, recent activity
railswitch --json memory --since 41   # only the activity after record 41 (docs/json.md)
```

Activity is who (`<worktree>/<agent>`) wrote what, from beads' own journal,
kept 7 days.

### Backup and restore

```bash
railswitch memory backup                     # memory-backups/<prefix>-<time>.jsonl in the workspace
railswitch memory backup --to ~/memory.jsonl
railswitch memory restore memory-backups/<file>.jsonl
```

A backup holds every issue (with its labels, dependencies and comments)
and every memory, readable only by you. Restoring writes each of them back
over the one with the same id, asking first; what the file doesn't hold
stays. It works on an empty volume too: the database is created first. Not
in a backup: the activity journal and Dolt's own history.

### The bd version

bd refuses a database a newer bd has migrated, so every agent must run the
same bd. The version is pinned in `containerfiles/Containerfile.agent-base`
(`ARG BD_VERSION`, with the release checksums), and `railswitch doctor`
checks it (`memory version`): running agents built before the pin changed,
and whether the pinned bd opens the server's database. To move to a new bd:
change the pin and both checksums (or accept the agent base update in
Review), stop every running agent, run `railswitch agent` once in a
worktree to build the new images, then `railswitch memory migrate` once
(bd won't migrate a shared database by itself).

A project made before shared memory existed has an agent base without
the bd lines: accept the agent base update (Review update), and when its
`Containerfile.agent-base` is its own copy, take the new default in
Settings → Images. Until then `railswitch doctor` fails `memory version`.

### Turning it off

Remove `[memory]` (or the `memory.off` Settings edit): the next render
drops the server's unit, stops the server and drops the agents' bd setup.
Its volume stays, so turning it on again finds every memory; it goes with
`project rm --purge` or `podman volume rm <prefix>_memory`.

## Your own agent

An agent package is a folder with `agent.toml` (its hosts, mounts, secrets
and environment) and a Containerfile built on the agent base image. Copy
the built-in one (`railswitch pack eject claude@1`) or start from scratch
in `agents/<name>/`; see [the package format](../packs.md).

Agents are each person's own choice: templates never carry them
([Sharing](sharing.md)).

Next: [The editor](editor.md).
