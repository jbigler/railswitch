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
