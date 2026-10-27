# Packages: packs, options, tools and agents

A **package** is a directory with one manifest and its assets (D12):

| Kind | Manifest | Chosen in `project.toml` by |
|---|---|---|
| pack (the app's stack) | `pack.toml` | `[stack] pack = "<ref>"` |
| option (database, cache, storage, jobs, mail, browser) | `option.toml` | `[options] <slot> = "<ref>"` |
| tool (nvim) | `tool.toml` | `[tools.nvim]` |
| agent | `agent.toml` | `[agents.<name>] use = "<ref>"` |

Besides the manifest a package holds its Containerfiles (defaults that
`railswitch project new` ejects into `containerfiles/`, D32), `entrypoints/`,
`deps.allow` (hosts the deps service may reach), `initdb/` and an optional
`README.md`.

## Refs

A ref is `<name>@<N>` for a package built into railswitch (`rails@1`,
`postgres@1`, `claude@1`) or `local:<name>@<N>` for one the project
supplies. Names are lowercase (`[a-z][a-z0-9-]*`); `N` is a positive integer.

## Local packages

A `local:` ref reads a directory of the workspace, in exactly the format of
the built-ins:

| Ref in | Directory |
|---|---|
| `[stack] pack = "local:<name>@<N>"` | `<workspace>/packs/<name>/` |
| `[options] <slot> = "local:<name>@<N>"` | `<workspace>/options/<name>/` |
| `[agents.<a>] use = "local:<name>@<N>"` | `<workspace>/agents/<name>/` |

- The manifest's `name` must be the directory's name, and its `version` must
  be the ref's `N`; otherwise the error names the file and both values.
- Only the packages a project's refs name are read, when a command first
  needs them. A half-written package under `packs/` breaks nothing until a
  ref points at it.
- A local package may share a built-in's name: `local:rails@1` and
  `rails@1` are different refs. Name-keyed links keep working on a copy (an
  option's `works_with = ["rails"]`, a pack's `[db_scheme] postgres = …`).
- Port bases and port names must not collide across the packages one
  project uses, built-in and local together: `ports: base 13000 is used by
  pack rails@1 and option local:o@1`.
- `packs/`, `options/` and `agents/` are tracked in the workspace repo (D38),
  so a local package travels with the project.

## Locking

`railswitch lock` writes `stack.lock`: every package the project uses with
its version, its source (`built in (railswitch <version>)` or `local`) and
the sha256 of its directory (D14: each regular file's path, length and
bytes, in sorted path order; a symlink or other special file is an error
naming it). `render` and `up` refuse a lock that no longer matches:

```
railswitch: stack.lock is out of date for local:phoenix@1: run railswitch lock
```

Any change to a local package (an edit, an added or removed file) is such a
change, so package edits are reviewed and committed like any other. So is
switching a slot between a built-in and a local copy of it, even a
byte-identical one. `railswitch project new --pack local:<name>@<N>` (and
`--option <slot>=local:<name>@<N>`) works when the package is already in
the workspace directory before `project new` runs.

## Version chains

A pack's `[runtimes.<name>]` and `[db_name]` are chains: `from` lists
sources, read in the worktree, first match wins; `fallback` is the value
when none matches. The value becomes the placeholder `{<name>}` (and
`{<name>.major}`, the part before the first `.` or `-`), used in image tags,
build args, volume names and env. `[worktrees.<slug>.versions]` overrides a
runtime for one worktree.

| Source | Reads |
|---|---|
| `.ruby-version`, `.nvmrc`, `.node-version`, `.bun-version` | the first line (a leading `v` dropped for Node and bun) |
| `.tool-versions:<tool>` | the `<tool> <version>` line (asdf) |
| `mise.toml:<tool>`, `.mise.toml:<tool>` | `[tools] <tool> = "<v>"`, `{ version = "<v>" }` or `["<v>", …]` (first) |
| `Gemfile` | `ruby "<v>"` |
| `Gemfile.lock`, `Gemfile.lock:<gem>` | `RUBY VERSION`, or the resolved version of `<gem>` |
| `package.json`, `package.json:<engine>` | `engines.node`, or `engines.<engine>` |
| `package.json:packageManager.<tool>` | `"packageManager": "<tool>@<v>[+<hash>]"`; nothing when it names another tool |
| `package-lock.json:<package>` | the installed version of `<package>` |
| `uv.lock:<package>` | the locked version of `<package>` |
| `<path>/database.yml[:<env>.database]` | `development.database` (`[db_name]` only) |

A source is a path relative to the worktree; the file name picks the
parser, so `config/database.yml` and `.config/mise.toml` work. Loading a
package refuses a source of any other form (`runtimes.bun.from[1]: "x.lock"
is not a known version source`). A file that is missing or has no value is
skipped; a file that is unreadable or holds an invalid value is skipped
with a warning.

`fallback` is optional for a runtime. With none and no matching source the
value is empty: the rails pack's `bun` is empty unless the app pins bun, and
its `Containerfile.base` installs bun only when `BUN_VERSION` is set.

### Optional placeholders

`{<name>?}` is `""` when `<name>` has no value in the package's scope, where
`{<name>}` would be an error. The nvim tool names its npm volume
`npm_global_node{node?}`, so it works with a pack that has no `node` runtime.
`{env.X?}` still needs `X` set.

## Option pins and pack runtimes

An option can pin the version of its image (`[pin] name`, `default`):
`postgres = "16"`, `playwright = "1.60.0"`. When the project's pack also
has a runtime of that name, the pack's chain finds the version in the app
(the rails pack reads `playwright` from `Gemfile.lock`, so the browser
matches the app's client). High to low:

1. `[worktrees.<slug>.versions] <name>`
2. `[versions] <name>`
3. the pack's `[runtimes.<name>]`
4. the pin's `default`

`railswitch project new` writes a chosen option's pin default to
`[versions]` (`postgres = "16"`), except one the pack has a runtime for,
which would otherwise beat the app's own file.

## Routes

Traefik routes a host to a container port. Every router's rule is
`Host(<host>) && (ClientIP(<project subnet>) || <host clause>)` (D50), and
a routed service joins the project's proxy network.

| Route | Host | Declared by |
|---|---|---|
| the app | `<slug>.<prefix>.localhost` → `[app] port` | the pack |
| an option's | e.g. `s3.<slug>.<prefix>.localhost` | `[[routes]]` in `option.toml` (`name`, `host`, `port`, `root_redirect`) |
| the project's own | `<name>.<slug>.<prefix>.localhost` | `[[routes]]` in `project.toml` |

A project route needs no pack change, e.g. Vite's dev server for
`vite_rails` hot reload:

```toml
[[routes]]
name    = "vite"     # a DNS label; the router is <prefix>-<slug>-vite
service = "rails"    # any long-running service of the project (not deps, not an agent)
port    = 3036       # the container port
```

Render refuses two routes of one project with the same name or host
(`routes: router "s3" is used by rails and storage`). `railswitch machine`
lists every route.

## Commands

- `railswitch pack eject <ref> [--as <name>]` copies a built-in pack or
  option into `packs/<name>/` (or `options/<name>/`), the manifest's `name`
  set to `<name>` (default: the built-in's). The project keeps the built-in
  until its ref says `local:<name>@<N>`; then `railswitch lock`. An existing
  directory is never overwritten: a local package is yours to edit.
- `railswitch pack new <name>` writes `packs/<name>/` with a `pack.toml`
  listing every field (commented), a `Containerfile.base`, an
  `entrypoints/entrypoint-app.sh` and a `README.md`. Nothing reads it until a
  ref names it, so it can stay half-written.
- `railswitch pack show <ref>` prints a pack's or option's summary (app,
  runtimes and their sources, ports, pin, gives) and its `README.md`;
  `--json` prints one document (`docs/json.md`). Built-ins need no
  workspace.

## README.md

A package's `README.md` says what it expects of the app: the changes an app
must make to run under it (bind the dev server to `0.0.0.0`, read the
database from the env an option gives, …). `railswitch pack show` prints it.

## The rails pack: JavaScript

The rails deps service installs the app's JavaScript with the tool its
lockfile names, first match in this order (D69):

| Lockfile | Install |
|---|---|
| `package-lock.json` | `npm ci --no-audit --no-fund` |
| `bun.lock` or `bun.lockb` | `bun install --frozen-lockfile` |
| `yarn.lock` | `yarn install --immutable` |
| `pnpm-lock.yaml` | `pnpm install --frozen-lockfile` |

- A `package.json` without a lockfile fails: create one with
  `railswitch deps -- npm install` (or `bun`, `yarn`, `pnpm install`).
- **bun** is in the image only when the app pins a version: the pack's
  `[runtimes.bun]` reads `.bun-version`, `.tool-versions:bun` and
  `package.json` `"packageManager": "bun@<version>"`, with no fallback, and
  `Containerfile.base` installs that release when `BUN_VERSION` is set. A
  `bun.lock` without a pinned version fails naming the fix. A project
  created before this keeps its ejected `containerfiles/Containerfile.base`
  without the bun step until `railswitch containerfile eject base --force`
  (or merging the step by hand).
- **yarn and pnpm** come through corepack (`corepack enable` in the image),
  at the version `package.json` `"packageManager"` names. Node 25 and later
  no longer ship corepack: such an app fails naming the fix.
- The install is skipped when nothing changed since the last one: a stamp
  in `node_modules/` records the Node and tool versions and the lockfile.

Another pack that wants the same (an Ember pack on bun, say) declares its
own `[runtimes.bun]` and copies the install step and the lockfile switch;
there is no shared add-on package (user, 2026-10-27).

## Conventions every package keeps

- The app's worktree is mounted at `/app`; the working directory is `/app`.
- The image's last `USER` is `appuser`, uid and gid 1000 (`UserNS=keep-id`
  maps the host user onto it). Directories that volumes mount on exist and
  belong to `appuser`.
- Every `FROM` is fully qualified (`docker.io/library/…`); nothing is read
  from the build context.
- A project with agents builds the agent base on the pack's image with
  `apt`, so such a pack's image is Debian-based (`[image] os = "debian"`);
  an option's addon must match the pack's `os`.
- Entrypoints are mounted at `/usr/local/bin/<file name>`; a workspace file
  `entrypoints/<file name>` replaces the package's (D13).
- The dev server listens on `0.0.0.0` (a pack sets the env its framework
  reads, or its `README.md` says what the app must change).

## Manifest fields

### `pack.toml`

| Field | Meaning |
|---|---|
| `name`, `version` | the package name (its directory) and version (the `N` of `@N`) |
| `[app] service` | the app's service id (`[stack] app` overrides it) |
| `[app] server` | the dev server command |
| `[app] port` | the container port the app route sends `<slug>.<prefix>.localhost` to |
| `[app] install` | the deps command, run through `deps_entrypoint` |
| `[app] health`, `health_start_period` | the app's health command (empty: none) and its start grace |
| `[app] entrypoint`, `deps_entrypoint` | package files mounted as the app's and the deps service's entrypoint |
| `[deps] frozen_env` | env on the deps service only (a frozen install); `railswitch deps` leaves it out |
| `[image] file`, `tag`, `os`, `args` | the base image's Containerfile (ejected to `containerfiles/`), tag part, OS family and build args |
| `[runtimes.<name>]` | a version chain (`from`, optional `fallback`): `{<name>}` |
| `[db_name]` | the database name chain: `{db}` |
| `[env]` | the app's env (layer 1): only what the framework reads |
| `[db_scheme] <option> = "<scheme>"` | the `DATABASE_URL` scheme per database option |
| `[share] env` | env while the worktree is shared on the tailnet |
| `[[caches]]` | volumes shared by every worktree: `name`, `mount`, `chown` |
| `[[worktree_volumes]]` | one volume per worktree: `name`, `mount`, `chown` |
| `[[ports]]` | `name`, `env`, `base` (host port `base` + worktree ID), `container` or `same_port`, `share` |
| `[defaults] <slot> = "<ref>"` | the options `project new` picks |
| `[tasks]`, `[[links]]` | named commands and worktree links |
| `[unit]` | `exec`, `memory`, `pids_limit`, `shm_size`, `run_init`, `tty`, `success_exit_status` |

### `option.toml`

| Field | Meaning |
|---|---|
| `name`, `version`, `slot`, `service` | the option, its slot and its service id |
| `works_with` | pack names it works with, or `"*"` |
| `needs`, `generated` | slots that must be filled; generated passwords it uses (`{generated.X}`) |
| `[pin]` | `name`, `default`: the image version (`[versions] <name>` overrides) |
| `[image]` | exactly one of `pull` (a registry ref), `use = "app"`, or `build` (a standalone Containerfile with `tag`, `args`) |
| `[app_addon]` | `file`, `os`: a Containerfile the app's base chain should add |
| `[health]` | `cmd`, `interval`, `timeout`, `retries`, `start_period` |
| `entrypoint`, `command`, `firewall` | its entrypoint file, command and firewall profile(s) |
| `[gives]`, `[deps_env]`, `[agent_gives]`, `[env]` | env it gives the app, the deps service, agents, and its own service |
| `[share] gives` | `[gives]` while shared |
| `[dump]` | `command`, `file`: how `railswitch db dump` dumps it |
| `[[volumes]]`, `[[mounts]]`, `[[ports]]`, `[[routes]]`, `[[links]]`, `[unit]` | as named |

### `tool.toml` and `agent.toml`

Tools (`nvim`) and agents (`claude@1`, or `local:<name>@N` under
`agents/`) build on the base image; see `docs/nvim.md` and the agent
decisions (D30, D55, D62) in the roadmap. Their fields: `name`, `service` or
`version`/`run`/`default`, `[image] file`, `entrypoint`, `firewall` or
`allow`, `worktree`/`home`/`caches` (tools), `[[shared]]`, `[[mounts]]`,
`[env]`, `[[secrets]]` (agents), `[[ports]]`, `[[links]]`, `[unit]`.
