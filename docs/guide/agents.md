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
railswitch agent                    # the default agent, in this terminal
railswitch agent omp                # another one, by name
railswitch agent --new-term claude  # in a new terminal window
railswitch agent stop
railswitch agent --no-token         # without the token: it uses the login in its home
```

`--no-token` runs an on-demand agent without the secret that holds its
package's token (for Claude, `claude_token`), so it uses the login kept in
its home instead: at the first run, log in from inside (Claude: `/login`),
and that worktree keeps the login. The token stays set; run without the
flag to use it again.

With several agents, a command without a name (`agent`, `agent update`,
`agent default promote`) uses the default: the one you mark in
`project.toml`, else Claude (the built-in default agent).

```toml
[agents.omp]
use = "local:omp@1"
default = true
```

`railswitch agent` starts the worktree's stack first (`up`), but doesn't
wait for the app to answer on its URL: an app that is still booting, or
unhealthy, never holds up the agent. (It does wait while the worktree's
`[hooks] create` haven't run yet, since they need the app.)

An agent runs **on demand** (a container for as long as you use it, in
one terminal) or as a **service** (below), as its package says unless
`[agents.<name>] run` says otherwise. The built-in `claude@1` runs as a
service, so the dashboard can show its screen and type to it, and `up`
needs its token (`railswitch secret set claude_token`); for on demand: One agent runs at a time per
worktree. Agents start from the terminal; the dashboard shows when one is
waiting on you: a permission prompt, a question, a finished reply (`◆ N
need input`).

```toml
[agents.claude]
use = "claude@1"
run = "on-demand"
```

### A service agent: one session, many terminals

A service agent's container is its environment, ready before you need it:
`up` starts the default agent's container when it runs as a service (and
no agent runs in the worktree). The agent itself runs in a terminal
session inside it (tmux), which `railswitch agent` starts, or joins when it
is already running:

```bash
railswitch agent                     # join the session, or start the agent in one
railswitch agent -- --resume         # start it with arguments (only when no session runs)
railswitch agent --watch             # watch it, without typing into it
railswitch agent screen              # print its screen as it shows now
echo 'run the specs' | railswitch agent send   # type a line into it, then Enter
```

`agent screen` and `agent send` work without a terminal (the dashboard
uses them). `send` takes its text on stdin, at most 4 KiB, with no
control characters but line breaks and tabs; a multi-line text is pasted
as one input. It types only into a running session and never starts one.

- **Several terminals at once.** Run `railswitch agent` in as many
  terminals as you like: each shows the same agent, live, and each can
  type into it. The window follows whoever typed last.
- **Leaving.** Close the terminal: the agent keeps running, and
  `railswitch agent` joins it again. Every key goes to the agent (there is
  no tmux prefix key). The mouse wheel scrolls back through its output;
  hold Shift to select text.
- **When the agent exits** (`/exit`), its session ends but the container
  stays: the next `railswitch agent` starts a fresh agent at once.
  `railswitch agent stop` stops the container.
- **Changes wait for the session.** A change to the agent's settings or
  image doesn't restart a container whose agent has a session (`up` says
  so); `railswitch agent stop`, then `railswitch agent`, takes it.
- **Scrolling is shared.** Scrolling back puts the agent's pane into
  scroll mode for every terminal; type `q` (or scroll to the bottom) to
  return to the agent.
- **Another agent.** `railswitch agent omp` stops a service agent that has
  no session (saying so) and starts omp; one with a session is never
  stopped for you.
- **Cost.** An agent waiting at its prompt spends no tokens; an idle
  container holds only memory.

A project that ejected `agent-base` before this needs `tmux` added to the
file's package list (`railswitch containerfile show agent-base` shows the
default's change).

**Updating it.** An agent's image holds the agent's version from when it
was built (Claude Code's own updater is off). To take the latest release:

```bash
railswitch agent update claude
```

It rebuilds the image without the build cache, so Claude Code is
downloaded again. Every worktree uses that image: each agent started
afterwards runs the new version, and a running one keeps the old version
until you stop it.

Its home, logins and history included, is a folder per worktree
(`.railswitch/agent-home/<worktree>/<agent>`), kept across `down` and
deleted by `wt rm --force`.

### In the browser

The dashboard's worktree page has a **Terminal** row: **agent ▣** opens
the agent in a live terminal in the page (D149), the same session
`railswitch agent` joins, so slash suggestions, `/plugin` and other menus
work as in your terminal. Closing the panel leaves a service agent
running.

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

## Shared notes

What the project's agents should all know (a command that must be run a
certain way, a gotcha, a convention) goes in the project's notes: one
folder, `notes/` in the workspace, which every agent of every kind (Claude,
your own packages) reads and writes as `~/notes/`, in every worktree.

- **A note is a Markdown file named for what it says,** words joined by
  underscores: `run_db_prepare_after_a_schema_change.md`. Agents write
  them; so can you, with any editor.
- **Every agent starts with the list of notes:** the rule for using them,
  then every note's file name, newest first. Only names: it reads a whole
  note when its name is relevant (or searches them with `grep`), so the
  notes' size never costs context. Claude gets this at every session start and after a
  compaction; a package of your own gets it the way it delivers context
  (see [the package format](../packs.md)).
- **Saved notes are for everyone at once:** a note one agent writes is in
  every other agent's index at its next start, and any agent can read it
  right away.
- **They are kept:** `wt rm` and `down` never touch `notes/`; back it up,
  edit it or commit it somewhere as you like.
- **From railswitch:** `railswitch notes ls` lists them, `notes show
  <name>` prints one, `notes write <name> --if-hash <hash>` replaces it
  with stdin (the hash `notes show --json` printed; `--if-hash ""` adds a
  new note), and `notes rm <name> --if-hash <hash>` removes it. A note an
  agent changed since you read it is refused, not overwritten.

Never put secrets, credentials or personal data in a note, and in a
healthcare project no patient data: every agent reads them.

## Claude's own memory

Claude Code's auto memory is off in railswitch's agents: the project's
shared notes are Claude's memory, so what it learns reaches every agent
rather than one tool's private store. Memory files from earlier runs
(rails-dev-podman's `.container-config/claude-memory/`, a
`~/.claude/projects/*/memory/` folder) can be copied into `notes/`; give
each a name that says what it holds (their own names, like
`feedback_testing.md`, are often too vague for an agent to know when to
read them). Claude's own `MEMORY.md` index file is left out of the list.

```bash
cp /path/to/old-memory/project_setup.md notes/ci_caches_gems_per_ruby_version.md
```

## Your own agent

An agent package is a folder with `agent.toml` (its hosts, mounts, secrets
and environment) and a Containerfile built on the agent base image. Copy
the built-in one (`railswitch pack eject claude@1`) or start from scratch
in `agents/<name>/`; see [the package format](../packs.md).

Agents are each person's own choice: templates never carry them
([Sharing](sharing.md)).

Next: [The editor](editor.md).
