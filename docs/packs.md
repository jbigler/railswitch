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
