# Packages: packs, options, tools and agents

A **package** is a directory with one manifest and its assets (D12):

| Kind | Manifest | Chosen in `project.toml` by |
|---|---|---|
| pack (the app's stack) | `pack.toml` | `[stack] pack = "<ref>"` |
| option (database, cache, storage, jobs, mail, browser) | `option.toml` | `[options] <slot> = "<ref>"`, or any other key for an extra service |
| tool (nvim) | `tool.toml` | `[tools.nvim]` |
| agent | `agent.toml` | `[agents.<name>] use = "<ref>"` |

Besides the manifest a package holds its Containerfiles (defaults that
`railswitch project new` ejects into `containerfiles/`, D32), `entrypoints/`,
`deps.allow` (hosts the deps service may reach), `initdb/` and an optional
`README.md`.

## Extra services

`[options]` takes keys besides the six slots (D81). Each runs its option as
a service named after the key, with the role the option's `slot` gives:

```toml
[options]
database  = "postgres@1"           # service db
analytics = "local:clickhouse@1"   # service analytics
```

An option fills one key per project, and two options may not give the same
env name. A service dropped from `[options]` (or set to `"none"`) is
stopped by the render that removes its unit, so its container never keeps
running on its volume. A second database of one engine is a database in its server: the
app creates it, and `db dump` dumps it. `postgres@1` creates `{db}` itself
at its first start (`POSTGRES_DB`), for apps that never create their own;
`[services.db.env] POSTGRES_DB = ""` turns that off.

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
naming it). Every lock write also stores a copy of each package it pins
in `~/.local/share/railswitch/locked/<sha256>/` (D96), outside every
workspace; a copy is checked against its hash on every read, and one that
no longer matches counts as missing.

### Updates wait for review (D97)

A package whose content changes under the ref the lock pins (a railswitch
upgrade changes a built-in; a local package edited in a text editor: an
edit, an added or removed file) is not accepted unseen. Until it is
reviewed the project keeps running the stored copy of what the lock
accepted, in `render`, `up` and every dry run, and `overview --json`
reports `review_pending`. Without a stored copy (a lock written before
7a), or for a ref that changed (a package added, removed or at another
version in `project.toml`, or a slot switched between a built-in and a
local copy of it, even a byte-identical one), `render` and `up` refuse:

```
railswitch: stack.lock is out of date for local:phoenix@1: an update is waiting for review: railswitch lock --review
```

- `railswitch lock --review [--json]` shows every changed file as a diff,
  risky lines flagged (the checks below), and the hash of the exact
  `stack.lock` an accept writes ([JSON](json.md#lock---review)).
- `railswitch lock --accept <hash> [--restart]` recomputes the review,
  refuses another hash ("the update changed since it was reviewed"), then
  locks, stores and renders, all or none; `--restart` restarts the running
  worktrees the update changes (exit 3: written, restart failed).
- `railswitch lock` writes the lock when there is none or nothing to
  review; otherwise it prints the review and exits 1 naming the accept
  command.
- Other writes never accept a waiting update: `settings apply`, `project
  write`, `agent add` and Containerfile writes keep a waiting package's
  entry (and refuse when it has no stored copy); `pack file write` accepts
  only the file it writes, reviewed in the editor: when any other file of
  that package changed since it was accepted, the write refuses ("other
  files of <ref> changed since it was accepted: review them first").
- Ejects copy what was accepted: `containerfile eject` (Reset to default
  in the editor), the default `containerfile show` compares against,
  `agent add`'s Containerfiles and `pack eject` of a built-in the project
  locks all read the stored accepted copy while an update waits.
- A package that leaves the lock is remembered in its `[[removed]]` list
  (kind, key, version, source, sha256; the newest 32, one per ref). When
  the ref comes back (an option turned off and on in Settings, a version
  changed and changed back), it is locked at what was accepted, not at its
  current content: a waiting update still waits, and `lock --review`
  shows it against the remembered copy.

The checks flag, and everything else is shown as a diff only (it runs
inside a firewalled container):

| Check | Flagged |
|---|---|
| `firewall` | a host added to `deps.allow` or an agent's `allow`; an option's or tool's `firewall` profiles changed |
| `runs-here` | an added or changed `Containerfile.*` instruction (continuation lines and heredoc bodies joined, flagged at its first changed line; commented lines too) that pipes `curl`/`wget` into a shell or interpreter (`sh`, `bash`, `zsh`, `dash`, `ash`, `ksh`, `fish`, `python`, `python3`, `perl`, `ruby`, `node`, `php`; any case or spacing, through `sudo`/`env` or a path), runs one through `<(`, `$(` or backticks, `ADD http(s)://`, `--security=insecure`, `--network=host`; a manifest that does not load; any file that is not text (a NUL or invalid UTF-8 byte: it is still diffed and checked, the byte shown as `\xNN`) |
| `image` | a `Containerfile.*` `FROM` line added or changed; an option's `[image] pull` or a pack's `[image] file` added or changed |
| `host-mount` | a `[[mounts]]` entry added or changed whose source is an absolute path or a placeholder |
| `port` | a `[[ports]]` entry added, or its `base`, `container` or `share` changed |
| `privilege` | a `[unit] add_capability` added; a top-level `.sh` of a package with `add_capability` changed |

Manifests are compared field by field, so reordering or reformatting one
flags nothing. `railswitch project new --pack local:<name>@<N>` (and
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
| `.java-version` | the first line (jenv) |
| `Gemfile` | `ruby "<v>"` |
| `Gemfile.lock`, `Gemfile.lock:<gem>` | `RUBY VERSION`, or the resolved version of `<gem>` |
| `package.json`, `package.json:<engine>` | `engines.node`, or `engines.<engine>` |
| `package.json:packageManager.<tool>` | `"packageManager": "<tool>@<v>[+<hash>]"`; nothing when it names another tool |
| `package-lock.json:<package>` | the installed version of `<package>` |
| `uv.lock:<package>` | the locked version of `<package>` |
| `<path>/database.yml[:<env>.database]` | `development.database` (`[db_name]` only) |

A Java version (`.java-version`, `.tool-versions:java`, `mise.toml:java`)
loses its distribution prefix and its `+<build>` part:
`temurin-25.0.3+9.0.LTS` is `25.0.3`, `corretto-21.0.2.13.1` is
`21.0.2.13.1`. Which JDK image or distribution that becomes is the pack's
Containerfile's choice.

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

### Env references

An env value may use `{env.X}`: X's value from the pack's `[env]`, an
option's `[gives]` or the project's `[env]` (highest wins), e.g. a
project's `CLICKHOUSE_DATABASE_URL = "{env.CLICKHOUSE_URL}/events"`. An
`{env.X}` that holds a generated password (`{env.DATABASE_URL}`) expands
only in env, never in names, labels or links, and `railswitch env` masks the
result. A project `[env]` value reaches every service but deps; put one that
carries a password in `[services.<app>.env]` to keep it in the app. A value
whose `{env.X}` has no value at all is left out.

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

## Links and tasks

A pack, option or tool `[[links]]` (`label`, `url`), like a project's
`[[links]]` in `project.toml`, is a worktree link. Its `url` may use only
`{worktree.host}`, `{worktree.slug}` and `{branch}`; any other placeholder
is a load error naming the file. A URL without a `scheme://` gets
`http://` (`vnc.{worktree.host}` → `http://vnc.<slug>.<prefix>.localhost`).
A worktree's links, in order: App (`http://{worktree.host}/`), the
project's `[[links]]` in file order, then the stack's: the pack's, the
options' in slot order, then the enabled tools', each in file order.

`project.toml` hides stack links by label; a label that no pack, option
or tool link of the project carries is an error at render
(`hide_links: no stack link labelled "Nope"`):

```toml
hide_links = ["Routes", "Mailer previews"]   # top level, before any table
```

A project's `[tasks]` adds to the pack's `[tasks]`: a project task
overrides the pack's of the same name, and `""` removes it. A task name
must be non-empty and one line, and not `ls` (that is `railswitch task
ls`), in the project's `[tasks]` and a pack's alike.

```toml
[tasks]
test    = "bin/rspec"     # overrides the pack's test
console = ""              # removes the pack's console
lint    = "bin/rubocop"   # a project task
```

## Commands

- `railswitch pack eject <ref> [--as <name>]` copies a built-in pack or
  option into `packs/<name>/` (or `options/<name>/`), the manifest's `name`
  set to `<name>` (default: the built-in's). The project keeps the built-in
  until its ref says `local:<name>@<N>`; then `railswitch lock --review` and
  `railswitch lock --accept <hash>`. An existing
  directory is never overwritten: a local package is yours to edit.
- `railswitch pack new <name>` writes `packs/<name>/` with a `pack.toml`
  listing every field (commented), a `Containerfile.base`, an
  `entrypoints/entrypoint-app.sh` and a `README.md`. Nothing reads it until a
  ref names it, so it can stay half-written. For a new project, whose
  workspace `project new` has not created yet, `pack new --dir <workspace>
  <name>` writes `<workspace>/packs/<name>/`; then `railswitch project new
  <workspace> --pack local:<name>@1 …`.
- `railswitch pack new --option <slot> <name>` writes `options/<name>/`
  with an `option.toml` that loads as it is (`name`, `version = 1`, the
  slot, `service = "<name>"`, `works_with = ["*"]` and a placeholder
  `[image] pull`; every other field listed, commented) and a `README.md`.
- `railswitch pack show <ref>` prints a pack's or option's summary (app,
  runtimes and their sources, ports, pin, gives) and its `README.md`;
  `--json` prints one document (`docs/json.md`), with a local package's
  files. Built-ins need no workspace.
- `railswitch pack file show <ref> <path>` prints one file of a package,
  local or built in. `railswitch pack file write <ref> <path> --if-hash <h>
  [--dry-run] [--restart]` replaces a local package's file with stdin
  (`--if-hash ""`: adds it), and `railswitch pack file rm <ref> <path>
  --if-hash <h>` removes one (never the manifest). The hash is the one
  `pack file show --json` gave; a file changed since is refused. The
  package must still load with the change. When the project uses the
  package, the write also locks and renders, and a failure puts the file
  and `stack.lock` back; `--restart` then restarts the running worktrees
  the change reaches (D91). These are the dashboard's Library page's
  commands.
- The files `pack file write` takes (D93): the manifest (`pack.toml` or
  `option.toml`), `Containerfile.<name>`, `deps.allow`, `README.md` and
  `<name>.sh` at the package's top, and `entrypoints/<file>` and
  `initdb/<file>`, where `<name>` and `<file>` match
  `[A-Za-z0-9][A-Za-z0-9._-]*`. Anything else, a symlinked file, or a
  symlinked package directory, `entrypoints/` or `initdb/` is refused.
  `.sh` files are written 0755, others 0644.
- `railswitch pack ls` lists every pack and option: each built-in at its
  newest version and, in a workspace, its local packages (a local package
  that doesn't load yet is listed if its manifest has a `version`).

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
| `yarn.lock` | `yarn install --immutable` (yarn 1: `--frozen-lockfile`) |
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
  at the version `package.json` `"packageManager"` names (yarn 1 without
  one). Corepack never writes `packageManager` into `package.json`
  (`COREPACK_ENABLE_AUTO_PIN=0`). Node 25 and later no longer ship
  corepack, so the image installs it from npm there. A project whose
  ejected `containerfiles/Containerfile.base` predates this fails naming
  the fix: `railswitch containerfile eject base --force`.
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
- The app entrypoint passes a stop (SIGTERM, SIGINT) to everything its
  command started and then exits 0, so `stop` ends a stopped unit, not a
  failed one. As PID 1 a shell ignores SIGTERM: an entrypoint that ends in
  `exec "$@"` relies on the command handling it itself, which a `bin/dev`
  that runs foreman as a child does not. `rails@1`'s entrypoint does this.
- The image has a POSIX `sh`. `exec`, `task`, `deps`, an on-demand agent
  and nvim's in-container client run their command under a `sh` wrapper
  that watches a pipe railswitch holds (`--preserve-fds 1`). When
  railswitch ends (a closed terminal window, a cancelled job), the wrapper
  sends the command's process group SIGHUP, then SIGKILL after 3 s: podman
  would leave an exec session or a `run` running. Transient containers
  run with `--init` so that SIGHUP reaches a command that would be PID 1.
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
| `[[caches]]` | volumes shared by every worktree: `name`, `mount`, `chown`, `data` |
| `[[worktree_volumes]]` | one volume per worktree: `name`, `mount`, `chown`, `data` |
| `[[ports]]` | `name`, `env`, `base` (host port `base` + worktree ID), `container` or `same_port`, `share` |
| `[defaults] <slot> = "<ref>"` | the options `project new` picks |
| `[tasks]`, `[[links]]` | named commands and worktree links |
| `[unit]` | `exec`, `memory`, `pids_limit`, `shm_size`, `run_init`, `tty` (`--tty --interactive`), `interactive` (`--interactive` only: stdin stays open, no tty), `success_exit_status` |
| `[detect] files` | `project detect` suggests the pack for a repository that has every one of these files (D87); no files: never suggested |

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
| `[dump]` | `command`, `file`: how `railswitch db dump` dumps it; with `list` (a command printing one database name per line) both run once per name as `{dump.db}`, each to its own file. `postgres@1` dumps every non-template database but `postgres` |
| `[[volumes]]` | `name`, `mount`, `chown`, `seed`, `data` |
| `[[mounts]]`, `[[ports]]`, `[[routes]]`, `[[links]]`, `[unit]` | as named |
| `[[detect]]` | `file`, `match`, `evidence`: `project detect` suggests the option when a line of `file` matches `match` (RE2, unanchored: start it with `^\s*` so a commented line doesn't count); any rule is enough; `evidence` is what the page shows (default `<file>: <match>`) |

### Detection rules

Rules belong to the packages (D87); core only runs them. A rule path
(`[detect] files`, `[[detect]] file`) is a clean relative path inside the
repository made of letters, digits, `.`, `_`, `+`, `-` and `/`: no `..`, no
leading `/`, no glob. `project detect` checks out only these paths. A path
that is a symlink, has a symlinked parent, or is over 1 MiB counts as
absent. `match` must compile. Load refuses a rule that breaks either.

| Package | Rule |
|---|---|
| `rails@1` | `Gemfile` and `config/application.rb` |
| `postgres@1` | `Gemfile`: `gem "pg"` |
| `redis@1` | `Gemfile`: `gem "redis"` |
| `rustfs@1` | `Gemfile`: `gem "aws-sdk-s3"` |
| `sidekiq@1` | `Gemfile`: `gem "sidekiq"` |
| `playwright@1` | `Gemfile`: `gem "capybara-playwright-driver"`, or a gem whose name starts with `playwright` |

```toml
[detect]                                     # pack.toml
files = ["Gemfile", "config/application.rb"]

[[detect]]                                   # option.toml
file = "Gemfile"
match = '''^\s*gem\s+['"]pg['"]'''
evidence = "Gemfile: pg"
```

Every volume table (`[[caches]]`, `[[worktree_volumes]]`, an option's
`[[volumes]]`, a tool's or agent's `[[shared]]`) takes `data = "persistent" |
"cache"` (default `persistent`). `cache` says the volume holds only content
the service rebuilds, so the settings preview does not report "Data not
carried over" when an option change leaves it behind. No built-in package
marks one today.

### `tool.toml` and `agent.toml`

Tools (`nvim`) and agents (`claude@1`, or `local:<name>@N` under
`agents/`) build on the base image; see `docs/nvim.md` and the agent
decisions (D30, D55, D62) in the roadmap. Their fields: `name`, `service` or
`version`/`run`/`default`, `[image] file`, `entrypoint`, `firewall` or
`allow`, `worktree`/`home`/`caches` (tools), `[[shared]]`, `[[mounts]]`,
`[env]`, `[[secrets]]` (agents), `[[ports]]`, `[[links]]`, `[unit]`.
