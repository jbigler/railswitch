# railswitch Stage 5 — local packs, proven with Phoenix

Roadmap: `docs/superpowers/plans/2026-10-25-railswitch-roadmap.md` (Stage 5,
D12, D13, D31, D38, D43). Settled with the user on 2026-10-27.

## Intent

Show that the pack format is enough for a framework railswitch knows nothing
about. Phoenix is the test case because it is the next framework the user is
learning, so the result must be usable for everyday Phoenix work (an app, its
database, live reload, `wt add`, nvim and an agent), not only pass a test.

## Goals

- `local:<name>@<N>` refs work for `[stack] pack` and `[options]`, read from
  the workspace (D43).
- `railswitch pack eject|new|show`.
- Pack-format errors name the file and the field.
- The generic pieces stop assuming Rails (browser option, nvim).
- Project-level routes in `project.toml`.
- The rails pack installs JavaScript with npm, bun, yarn or pnpm, chosen by
  lockfile; bun is a rails pack runtime, installed only when the app pins it.
- A Phoenix pack (test fixture, not shipped) runs a fresh `mix phx.new` app
  end to end on the user's host.

## Non-goals

- Shipping a built-in Phoenix pack.
- Library repos, cloning, capability review, signing, update diffs (Stage 7).
  Stage 5's on-disk layout is the one a library repo will use, so Stage 7
  adds fetching and trust without changing the format.
- A railswitch command that generates a new app. The Phoenix app is made with
  a documented `podman run` (as the Rails demo was, docs/stage2-acceptance.md).
- A machine-wide folder of the user's own packs.

## Anti-goals

- Any Phoenix (or Elixir, mix, hex) knowledge in core: `internal/render`,
  `internal/config`, `internal/lifecycle`, `internal/cli`, `internal/version`.
  A core change Phoenix needs must be a generic feature any pack can use.
  `TestGenericity` gains `phoenix`/`elixir`/`mix ` outside test fixtures.
- Local packs that bypass the lock: an edited local pack must show up as a
  `stack.lock` change that the user accepts (`railswitch lock`).

## Constraints

Everything in AGENTS.md, in particular: stdlib + BurntSushi/toml only; argv
only; unit tests without podman/systemd/network; secrets on stdin; `--json`
for read commands (`pack show` is a read command); every behaviour change
updates `docs/`.

## Decided

### Local refs and layout (D43)

- `[stack] pack = "local:<name>@<N>"` reads `<workspace>/packs/<name>/`;
  `[options] <slot> = "local:<name>@<N>"` reads `<workspace>/options/<name>/`.
  Agents already read `<workspace>/agents/<name>/`. The directory format is
  exactly the built-ins' (D12): `pack.toml`/`option.toml`, Containerfiles,
  `entrypoints/`, `deps.allow`, `initdb/`, plus an optional `README.md`.
- The manifest's `name` and `version` must equal the ref's `<name>` and `<N>`;
  a mismatch is an error naming the file and both values.
- Local packages are loaded on demand: only the `local:` refs a project uses
  are read, so a half-written pack under `packs/` (e.g. right after
  `pack new`) breaks nothing until a ref points at it; `pack show` loads one
  by ref. Local agents (Stage 4) move to the same rule. *(My call.)*
- `stack.lock` records local packages with `source = "local"` and the D14
  sha256 of the directory. `up`/`render` refuse when the hash differs and say
  to run `railswitch lock`. `CheckLock` also compares whether an entry is
  local, so switching a slot between a built-in and a byte-identical local
  copy of the same name is a lock change. (It does not compare the built-in
  `source` text, which carries the railswitch version.)
- Port bases and port names are checked for collisions across every package a
  project uses, built-in and local together.
- `project new --pack local:<name>@<N>` (and `--option slot=local:…`) works
  when `<workspace>/packs/<name>/` already exists before `project new`; if it
  doesn't, the error names the missing path. `project new` already accepts an
  existing workspace directory without `project.toml`. *(My call.)*
- The pack format gets a reference, `docs/packs.md` (there is none today):
  every manifest field, the source forms, the conventions a pack must keep
  (`/app`, `appuser` uid 1000 as the last `USER`, Debian when the project has
  agents, since the agent base uses apt) and the `README.md` convention.

### Pack commands

- `railswitch pack eject <ref> [--as <name>]` copies a built-in pack or option
  into `<workspace>/packs/<name>/` (or `options/<name>/`); `--as` defaults to
  the built-in's own name. A local package may share a built-in's name
  (`local:rails@1` next to `rails@1`): name-keyed links (`works_with`,
  `[db_scheme]`) keep working after an eject. Refs, errors, `pack show` and
  `stack.lock` (`source`) always tell the two apart. The project keeps using
  the built-in until its ref is changed. *(My call.)*
- `railswitch pack new <name>` writes a skeleton `pack.toml` with every field
  present and commented, plus an empty `Containerfile.base`, `entrypoints/`
  and `README.md`.
- `railswitch pack show <ref>` prints the manifest summary (runtimes and
  their sources, ports, caches, health, defaults) and the package's
  `README.md`; `--json` prints one document (added to `docs/json.md`).
- A package's `README.md` is where it documents app-side changes it needs
  (Phoenix: bind address and Repo config).

### Validation

Load errors name the file and the field, joined (as today). Added: ref
name/version mismatch; cross-package port collisions; chain sources that are
not a known source form; unknown keys still refused.

### Generic fixes found in the Stage 5 map

- **Browser option:** its version no longer requires a pack runtime named
  `playwright`. The option carries its own `[pin]` default. Precedence, high
  to low: `[worktrees.<slug>.versions]`, `[versions]`, a pack runtime of the
  same name (the rails pack reads `Gemfile.lock:playwright-ruby-client`), the
  pin default. This keeps today's rule that `[versions]` beats a pin, so a
  deliberate pin works. *(My call.)*
- **nvim:** language servers installed with npm (Mason and the like) need
  Node in the nvim image whatever the project uses (user, 2026-10-27). The
  nvim image is built on the project's base image (D23/D31) and today gets
  Node only from it. `Containerfile.nvim` gains its own Node
  (`ARG NVIM_NODE_VERSION=24`, copied from `docker.io/library/node:<v>-slim`
  into `/opt/nvim-node`), linked into `/usr/local/bin` only when the base has
  no `node`, so a rails base keeps the app's Node. It also sets
  `NPM_CONFIG_PREFIX=/home/appuser/.npm-global`, adds its `bin` to `PATH`
  and creates that directory for `appuser`, which today only the rails base
  does. Existing projects keep their ejected `Containerfile.nvim` until
  `railswitch containerfile eject nvim --force`. *(My call: the version is an
  ARG default in the ejected file, edited per project; no new tool pin.)*
- **nvim's npm volume:** `npm_global_node{node}` no longer requires a pack
  runtime named `node`. Placeholders gain an optional form `{name?}` that
  expands to `""` when `name` is unknown; the volume becomes
  `npm_global_node{node?}` (unchanged for rails), so a pack without Node
  shares one `<prefix>_npm_global_node`, holding packages for nvim's own
  Node. After changing `NVIM_NODE_VERSION`, remove that volume (docs/nvim.md).
  *(My call.)*
- **Version sources:** `mise.toml:<tool>` (and `.mise.toml`) joins the
  generic sources next to `.tool-versions:<tool>`.

### Project routes

`project.toml`:

```toml
[[routes]]
name = "vite"            # host vite.<slug>.<prefix>.localhost
service = "rails"        # a service id of this project
port = 3036              # the container port
```

Rendered as an extra Traefik router/service on that container, with the same
`Host(…) && (ClientIP(…) || <host clause>)` rule as every router (D50). A
routed service joins the proxy network, as option routes already make it do.
Render refuses two routes with the same router name or host within a project
(app, option and project routes together); today nothing checks this, and
hosts of different projects can't collide because they carry the prefix.

### JavaScript package managers (rails pack)

The rails pack's deps entrypoint chooses by lockfile, first match wins:
`package-lock.json` → `npm ci`; `bun.lock`/`bun.lockb` →
`bun install --frozen-lockfile`; `yarn.lock` → `yarn install --immutable`;
`pnpm-lock.yaml` → `pnpm install --frozen-lockfile` (yarn and pnpm through
corepack). A `package.json` with no lockfile, or a lockfile whose tool is
missing, fails with a message naming the fix.

Bun is a rails pack runtime, not a new package kind *(my call; the roadmap
said "built-in ejectable bun addon")*: `[runtimes.bun] from = [".bun-version",
"package.json:packageManager.bun"]` with no fallback. A chain may now have no
fallback: when no source matches, the value is empty. The new source form
`package.json:packageManager.<name>` yields the version only when
`packageManager` names that tool (`bun@1.2.3+sha512…` gives `1.2.3`; `yarn@4`
gives nothing for `bun`). The rails `Containerfile.base`
takes `BUN_VERSION` and installs bun only when it is non-empty, so apps
without bun get no bun. Existing projects carry an ejected
`containerfiles/Containerfile.base` from before; the deps error for a bun
lockfile without bun says to run `railswitch containerfile eject base --force`
(or to merge the bun step by hand).

Any pack can declare the same `[runtimes.bun]` (an Ember pack would, and could
start `FROM docker.io/oven/bun:{bun}`): the sources, the empty-chain rule and
`{bun}` are core; the install step and the lockfile-to-command deps script are
copied into each pack that wants them. User, 2026-10-27: a copy per pack is
fine for now; a shared add-on package kind can come later (Stage 7) if the
copies drift.

### Phoenix fixture pack

`testdata/fixtures/phoenix/packs/phoenix/`, inside a fixture workspace
`testdata/fixtures/phoenix/` (never embedded; the acceptance run copies it):

- base image from Elixir/Erlang versions read from `.tool-versions`
  (`elixir`, `erlang`), then `mise.toml`, then pinned fallbacks;
- `[app]`: server `mix phx.server`, port 4000, health declared by the pack
  (an HTTP GET on `/`);
- deps: `mix deps.get --check-locked`; `deps/` and `_build/` as worktree
  volumes; hex and mix homes as shared caches;
- `deps.allow`: the hex hosts and the hosts the esbuild/tailwind installers
  download from;
- `[env]` `MIX_ENV = "dev"`, `PORT = "4000"`;
- `[db_name]` fallback `{project.prefix}_dev`;
- `README.md` with the app-side changes:
  - generate with `mix phx.new --inside-docker-env` (dev binds `0.0.0.0`), or
    change `ip: {127, 0, 0, 1}` to `{0, 0, 0, 0}` in `config/dev.exs`;
  - in the dev Repo config drop `username`, `password`, `hostname` (Postgrex
    reads the `PG*` env the postgres option gives, D29) and use
    `database: System.get_env("PGDATABASE", "<app>_dev")`.

Generated app facts this relies on (Phoenix installer, `main`): dev binds
`127.0.0.1` unless `--inside-docker-env`/`/.dockerenv`; `runtime.exs` reads
`PORT` (default 4000); the dev Repo config hard-codes
`username`/`password`/`hostname: "localhost"`; live reload is a websocket on
the endpoint's own port, so the app route covers it.

### Bootstrapping a Phoenix app

Documented in the pack README and the acceptance doc, no railswitch code:

```sh
mkdir -p ~/tmp/rs-phx
podman run --rm -v "$HOME/tmp/rs-phx":/w:Z -w /w docker.io/library/elixir:<version> \
  sh -c 'mix local.hex --force && mix archive.install --force hex phx_new && yes | mix phx.new hello --inside-docker-env && cd hello && git init -q && git add -A && git -c user.email=t@t -c user.name=t commit -qm init'
```

### Acceptance

- Unit: the Phoenix fixture renders (goldens) with no Phoenix-specific code
  in core; `TestGenericity` extended.
- Host (user): fresh `mix phx.new` app → copy the fixture to
  `<ws>/packs/phoenix/` → `project new --pack local:phoenix@1` → `up` → app at
  `http://<branch>.<prefix>.localhost/`, live reload on a template edit,
  `wt add` with its own database, nvim and `railswitch agent` attach; edit
  the pack → `up` refuses until `railswitch lock`.
- Host (user): a `rails new -j bun` app installs with bun.

## Left to the builder

- Struct and file layout of the ref resolver and loader changes.
- `pack show` text layout and JSON field names (documented in docs/json.md).
- The Phoenix fixture's Containerfile and entrypoint details, image tags, and
  exact `deps.allow` hosts.
- How the bun addon installs bun.
- Task order and size.
