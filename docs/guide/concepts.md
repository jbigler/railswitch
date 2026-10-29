# Concepts

## Workspace, worktrees, project

A **project** is one app repository on this machine. Its **workspace** is
a folder holding `project.toml` (how the project is set up), `stack.lock`
(exactly which package content it runs) and one **worktree** per branch:
`~/workspace/shop/main`, `~/workspace/shop/feature-x`. Every worktree
runs its own set of containers.

The project's **prefix** (`shop`) names everything it owns: containers
(`shop-main-rails`), volumes, networks and hostnames
(`main.shop.localhost`). Each worktree gets a number on the machine that
sets its ports (the database on 55432 plus that number, and so on), all
published on 127.0.0.1 only.

## Packs, options, slots

- A **pack** knows one framework: how to build its image, start its
  server, install its dependencies and check it's up. Rails is built in;
  other stacks are local packs (`railswitch pack new`), and teams can
  publish packs in a library.
- An **option** fills a **slot** beside the app: `database`, `cache`,
  `storage`, `jobs`, `mail`, `browser`. Built in: PostgreSQL, Redis,
  RustFS (S3 storage), Sidekiq, Playwright. Any other key adds one more
  service of that name: a second database, ClickHouse beside PostgreSQL.
- **Versions** pin what the images run (`postgres = "16"`). Runtimes such
  as Ruby and Node come from the app's own files (`.ruby-version`,
  `.nvmrc`, `.tool-versions`, `Gemfile.lock`, `package.json`), so every
  worktree runs what its branch says.

`project.toml` names them: `[stack] pack = "rails@1"`, `[options] database
= "postgres@1"`. A ref is `name@N` (built in), `local:name@N` (this
workspace's `packs/` or `options/`) or `alias:name@N` (a library's).

## The kinds of containers

| Kind | What it is | Example |
|---|---|---|
| app | the web app the pack runs | `shop-main-rails` |
| worker | the app's background jobs (the `jobs` slot) | `shop-main-worker` |
| deps | a short run before the app starts: installs exactly the lockfiles | `shop-main-deps` |
| data | databases, caches, storage; its data in a volume per worktree | `shop-main-db` |
| browser | a browser for system tests, with its own firewall | `shop-main-browser` |
| tool | your editor (Neovim), mail catchers | `shop-main-nvim` |
| agent | an AI coding agent, sandboxed | `shop-main-agent-claude` |

The app, worker and deps mount the worktree; data containers don't. Every
container runs with no privilege escalation and memory and process
limits; the app, worker, deps, tools and agents also drop every
capability. Each gets an egress firewall applied from outside it, except
the browser, which runs its own. Image builds are not containers of the
project: a Containerfile's `RUN` lines run on this machine, outside the
firewall, which is why a review flags every new download in one.

Caches (gems, npm, corepack) are shared by the project's worktrees, so a
new worktree doesn't download everything again. Data is per worktree: a
new worktree's database starts from the dumps in `<workspace>/db-dumps/`
if there are any (`railswitch db dump` writes them from a worktree you
like), else empty; then the app's own setup runs (`db:prepare` for
Rails). `railswitch db restore` puts the dumps back into a running
worktree's database at any time, replacing what it holds (it asks
first, naming each database; `--from <dir>` for dumps from elsewhere,
`--yes` without asking). It restores databases only: a seeded volume
(storage) comes back from its dump only when the volume is new
(`down`, then `up`).

## What stays on your machine

Nothing in a container sees your home directory, SSH keys, SSH agent or
git config file (only your git name and email are passed in). Secrets reach only the containers that declare them, through
podman's secret store. `project.toml` is yours: the dashboard and the CLI
edit it in place, keeping your comments.

## Changes you review

`stack.lock` records the content of every package the project runs. When
that content changes (a railswitch upgrade, a library update, a hand
edit), the project keeps running what you accepted and the dashboard shows
**Review update**: each file as a diff, with risky lines flagged. Nothing
new runs until you accept it. See [Customizing a project](customizing.md).

Next: [Agents](agents.md).
