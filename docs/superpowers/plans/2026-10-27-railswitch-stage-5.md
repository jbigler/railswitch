# railswitch Stage 5 (local packs, proven with Phoenix) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A user-written pack (`local:phoenix@1`, a test fixture) runs a fresh `mix phx.new` app end to end, with no Phoenix knowledge in core; local packs/options are locked by hash; `railswitch pack eject|new|show`; project routes; the rails pack installs JS with npm, bun, yarn or pnpm.

**Architecture:**
- **Refs and catalog.** `pack.ParseRef` accepts `local:` for every kind. `Catalog` keeps the built-ins and the workspace root; a `local:` ref is loaded on demand from `<root>/packs|options|agents/<name>/` and cached. `Stack` carries the pack's and each option's `Ref`, so lock entries, eject headers and errors keep `local:`.
- **Lock.** Local entries are `source = "local"`; `CheckLock` treats a change between local and built-in as stale.
- **Versions.** Chains may have no fallback (empty value). New sources `package.json:packageManager.<tool>`, `mise.toml:<tool>`, `.mise.toml:<tool>`; the loader refuses unknown source forms. Placeholders gain `{name?}`.
- **Render.** Option pins merge with a pack runtime of the same name; project `[[routes]]`; route name/host uniqueness per project.
- **CLI.** `pack eject|new|show`.
- **Packs.** Rails deps chooses the JS tool by lockfile; bun is a rails runtime. Phoenix pack lives in `testdata/fixtures/phoenix/packs/phoenix/`.

**Tech Stack:** Go (stdlib + `github.com/BurntSushi/toml`), rootless podman, Quadlet, POSIX sh.

**Spec:** `docs/superpowers/specs/2026-10-27-railswitch-stage-5-design.md` (read it first); roadmap `docs/superpowers/plans/2026-10-25-railswitch-roadmap.md` (Stage 5, D12–D14, D31, D38, D43). Code map: `.superpowers/sdd/stage5-map.md` and `.superpowers/sdd/stage5-S5Iface{Pack,Render,CLI}.md` (gitignored notes with file:line for every symbol named below).

**Controller calls (flag for the user's review):**
- **C1. On-demand local loading.** `LoadLocalAgents` goes away; only refs a project uses are read (spec). A broken, unused dir under `packs/` breaks nothing.
- **C2. `CheckLock` compares locality, not the `source` text** (the built-in text carries the railswitch version).
- **C3. `project new` copies an option's pin into `[versions]` only when the pack has no runtime of that name.** Otherwise a new rails project would write `[versions] playwright = "1.60.0"`, which beats `Gemfile.lock`.
- **C4. Project routes** may target any long-running service except agents; the target joins the proxy network (as option routes do). Host is `<name>.{worktree.host}`.
- **C5. `pack eject` copies built-ins only, refuses an existing directory** (no `--force`: a local pack is the user's code).
- **C6. One Phoenix copy**, in the fixture workspace `testdata/fixtures/phoenix/` (outside `internal/`, so `TestGenericity` exempts it).
- **C7. nvim brings its own Node** (user, 2026-10-27: LSPs need it whatever the project uses): `Containerfile.nvim` copies Node (`ARG NVIM_NODE_VERSION=24`) to `/opt/nvim-node` and links it only when the base has no `node`; no new tool pin.

## Global Constraints

Everything in AGENTS.md and the earlier plans' Global Constraints still holds: argv-only runner with timeouts; secrets on stdin only; hardening; schema versions; `--json` is a global flag; loopback-only publishing; exact names, no prune; `safefile` for worktree files; isolation; unit tests need no podman/systemd/network (`git`, `bash` allowed); `make check` before every commit; every behaviour change updates `docs/`.

Added here:
- **Genericity:** no `phoenix`, `elixir`, `erlang` or `mix ` in non-test code under `internal/` or `cmd/`, including `internal/pack/builtin/` (Task 8 extends `TestGenericity`).
- **No core change for Phoenix:** Tasks 8–9 change no Go code under `internal/render`, `internal/config`, `internal/lifecycle`, `internal/version` (a need found there is a generic feature and goes back to an earlier task's shape, reported as a deviation).
- **Clean cutover:** `ParseAgentRef` and `LoadLocalAgents` are deleted, not aliased; every caller and the two tests that assert "until Stage 5" are updated.
- **Error shape:** pack-format errors are `"<path>: <field>: <msg>"` with `<path>` the manifest's path (workspace-relative for local packages, e.g. `packs/phoenix/pack.toml`).

## Review Focus

1. **Editing a local pack after `lock`** (changing, adding or removing any file, or a symlink inside it): `up`/`render` refuse with `stack.lock is out of date … run railswitch lock`; a symlink error names the file. → Task 2 `TestCheckLockLocalPackEdits`.
2. **A byte-identical eject switched in** (`rails@1` → `local:rails@1` after `pack eject rails`): stale lock, though the hash is equal. → Task 2 `TestCheckLockBuiltinToLocal`.
3. **A half-written local pack** right after `pack new` (or any broken dir under `packs/`): `render`, `up`, `status` keep working until a ref points at it; `pack show local:x@1` names the missing fields. → Task 1 `TestBrokenUnusedLocalPackIgnored`, Task 6 `TestPackNewThenRender`.
4. **Route collisions inside one project:** a project route named `app`, or `s3` next to rustfs, or two `[[routes]]` with one name → render refuses naming both owners. → Task 5 `TestRouteCollisions`.
5. **JS tool mismatch:** a `bun.lock` without a bun version source, two lockfiles at once, `packageManager = "yarn@4…"` with the bun runtime → deps fails naming the fix / picks by the documented order / bun stays empty. → Task 3 `TestPackageManagerSource`, Task 7 `TestRailsDepsLockfiles`.

---

### Task 1: Local refs and on-demand catalog

**Files:**
- Modify: `internal/pack/ref.go`, `internal/pack/load.go`, `internal/pack/resolve.go`, `internal/config/project.go`, every `ParseAgentRef`/`LoadLocalAgents` caller (`internal/cli/agent_add.go`, `internal/cli/new.go`, `internal/cli/containerfile.go`, `internal/testfixture/testfixture.go`, …)
- Test: `internal/pack/local_test.go` (new), `internal/pack/agent_test.go`, `internal/config/project_test.go`
- Create: `docs/packs.md` (section "Local packages")

**Interfaces:**
- Produces:
  - `func ParseRef(s string) (Ref, error)` accepts `local:<name>@<N>` (sets `Local`); error `%q is not a ref name@N or local:name@N, e.g. "postgres@1"`. `ParseAgentRef` deleted.
  - `type Catalog struct { Packs map[Ref]*Pack; Options map[Ref]*Option; Tools map[string]*Tool; Agents map[Ref]*Agent; Root string }` — maps hold built-ins plus local packages loaded so far (key `Local: true`).
  - `func LoadCatalog(root string) (*Catalog, error)` — built-ins, `Root = root`; reads nothing under root.
  - `func (c *Catalog) Pack(r Ref) (*Pack, error)`, `func (c *Catalog) Option(r Ref) (*Option, error)`, `func (c *Catalog) Agent(r Ref) (*Agent, error)` — built-in: map lookup, `nil, nil` when absent. Local: load `<Root>/packs/<name>/` (`options/`, `agents/`) once and cache; errors: `local:x@1: <Root>/packs/x does not exist`; `Root == ""` → `local:x@1: local packages need a workspace`; load errors as today with the path prefixed `packs/x/…`; version mismatch `packs/x/pack.toml: version 2 does not match the ref local:x@1`.
  - `type Stack struct { Pack *Pack; PackRef Ref; Options map[string]*Chosen; Tools []*Tool; Agents []*ChosenAgent }`; `type Chosen struct { Option *Option; Ref Ref; Service string }`.
  - `Resolve` refuses port collisions across the chosen pack, options and tools: `ports: base %d is used by %s and %s`, `ports: name %q is used by %s and %s` (owners as `pack local:x@1`, `option postgres@1`, `tool nvim`).
  - `config`: `[stack] pack`, `[options].<slot>` and `[agents.<n>].use` accept `local:`; `from` does not.

- [ ] **Step 1: Write failing tests** in `internal/pack/local_test.go` (helper `writeLocalPack(t, root, dir, toml string)` writing `pack.toml` + `Containerfile.base`):
  - `TestParseRefLocal`: `ParseRef("local:phoenix@1")` = `Ref{Name:"phoenix", Version:1, Local:true}`; `String()` round-trips; `ParseRef("local:Phx@1")` errors.
  - `TestResolveLocalPack`: project `[stack] pack = "local:p@1"` with `<root>/packs/p/` → `st.Pack.Name == "p"`, `st.PackRef.Local`; `c.Packs[Ref{"p",1,true}]` cached (second `Pack` call returns the same pointer).
  - `TestResolveLocalOption`: `[options] cache = "local:o@1"` from `<root>/options/o/` → `st.Options["cache"].Ref.Local`.
  - `TestLocalPackVersionMismatch`: dir has `version = 2`, ref `@1` → error contains `packs/p/pack.toml` and `version 2 does not match the ref local:p@1`.
  - `TestLocalPackMissingDir`: error contains `<root>/packs/nope does not exist`.
  - `TestBrokenUnusedLocalPackIgnored`: `<root>/packs/broken/pack.toml` = `name = "broken"\n` and a project using `rails@1` → `Resolve` succeeds.
  - `TestLocalSharesBuiltinName`: `local:rails@1` (a copy of the built-in dir) resolves next to `rails@1` without a collision; `refList` output distinguishes them.
  - `TestResolvePortCollisionAcrossPackages`: local option with `[[ports]] base = 13000` next to `rails@1` → `ports: base 13000 is used by pack rails@1 and option local:o@1`.
  - Update `agent_test.go` (`TestLoadLocalAgents` → on-demand via `c.Agent`) and `config/project_test.go:231-241` (local accepted for stack/options/agents, refused for `from`).
- [ ] **Step 2:** `go test ./internal/pack ./internal/config` → FAIL (undefined `Catalog.Pack`, `PackRef`, …).
- [ ] **Step 3: Implement.** One unexported `loader` per local package (fresh `bases`/`names`; the cross-package check lives in `Resolve`). `Resolve` uses `c.Pack/Option/Agent`, so a local error surfaces as `stack.pack: …`, `options.<slot>: …`, `agents.<n>.use: …`. Fix every place that rebuilt `Ref{Name, Version}` from a manifest (`resolve.go:166` claim label, `cli/containerfile.go:68,73`) to use `PackRef`/`Chosen.Ref`. `docs/packs.md`: start the reference with "Local packages" (refs, dirs, on-demand loading, name/version rule, same-name-as-built-in).
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5:** `make check`; commit `feat(pack): local: refs for packs and options, loaded on demand (D43)`.

### Task 2: Lock, `project new` and eject headers for local packages

**Files:**
- Modify: `internal/workspace/workspace.go` (`lockEntries`, `CheckLock`), `internal/cli/new.go` (`newProject`), `internal/cli/containerfile.go` (`stackBuiltins`)
- Test: `internal/workspace/workspace_test.go`, `internal/cli/new_test.go`, `internal/cli/containerfile_test.go`
- Docs: `docs/packs.md` ("Locking")

**Interfaces:**
- Consumes: Task 1 `Catalog.Pack/Option`, `Stack.PackRef`, `Chosen.Ref`.
- Produces: lock entries `source = "local"` for local packs/options (key stays the bare name); `CheckLock` stale when `(entry.Source == "local") != ref.Local`, same message `stack.lock is out of date for local:x@1: run railswitch lock`.

- [ ] **Step 1: Write failing tests:**
  - `TestLockLocalPack`: lock a workspace on `local:p@1` → `l.Packs["p"].Source == "local"`, `SHA256 == ContentHash(os.DirFS(root/packs/p))`.
  - `TestCheckLockLocalPackEdits` (Review Focus 1): after `Lock`, each of {append to `pack.toml` comment, add `entrypoints/x.sh`, remove `README.md`} → `errors.Is(err, ErrStaleLock)`, message contains `local:p@1`; a symlink `packs/p/link` → error contains `packs/p/link` and `not a regular file`.
  - `TestCheckLockBuiltinToLocal` (Review Focus 2): lock on `rails@1`, copy the built-in rails dir byte-for-byte to `packs/rails/`, switch to `local:rails@1` → stale; after `Lock`, clean.
  - `TestNewLocalPack`: pre-create `<ws>/packs/p/` (pack `p`, app `web`), `project new <ws> --repo … --prefix lp --pack local:p@1` → exit 0, `project.toml` has `pack = "local:p@1"`, `stack.lock` `source = "local"`, base Containerfile ejected with header `# Ejected from local:p@1 Containerfile.base`.
  - `TestNewLocalPackMissing`: no `<ws>/packs/p/` → exit 1, stderr contains `packs/p does not exist`, workspace left as before (nothing created).
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3: Implement.** `newProject` uses `cat.Pack(ref)` (nil → `--pack: unknown pack %s (available: …)`). `newProject` must load the catalog with `Root = ws` (already `LoadCatalog(ws)`).
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5:** `make check`; commit `feat: lock and project new for local packs and options`.

### Task 3: Version chains — optional fallback, new sources, known-source check, optional placeholders

**Files:**
- Modify: `internal/version/parsers.go`, `internal/version/chain.go`, `internal/pack/load.go` (`checkPack` runtimes/db_name), `internal/pack/expand.go`, `internal/render/model.go` (`values()` `setVersion`)
- Test: `internal/version/chain_test.go` (+ `testdata/`), `internal/pack/pack_test.go`, `internal/pack/expand_test.go`, `internal/render/model_test.go`
- Docs: `docs/packs.md` ("Version chains": every source form, empty chains, `{name?}`)

**Interfaces:**
- Produces:
  - Source `package.json:packageManager.<tool>` → `<ver>` from `"packageManager": "<tool>@<ver>[+<hash>]"`, not found when the tool differs.
  - Sources `mise.toml:<tool>` and `.mise.toml:<tool>` → `[tools] <tool> = "<ver>"`, `= { version = "<ver>" }` or `= ["<ver>", …]` (first).
  - `func KnownSource(src string) bool` (version) — the file's base name has a parser and the key form is one that parser takes.
  - Loader: `runtimes.<n>.fallback` optional; every `runtimes.<n>.from[i]` and `db_name.from[i]` must satisfy `KnownSource`: `runtimes.bun.from[1]: "x.lock" is not a known version source`.
  - A runtime with no fallback and no matching source sets `{<n>}` and `{<n>.major}` to `""` (render `values()`; `from = "runtimes.<n> (none)"`). An invalid value still fails.
  - `pack.Expand`: `{name?}` expands to `""` when the scope reports `name` unknown (`*UnknownPlaceholderError`); any other error (e.g. `ErrUnset` for `env.*`) still fails.

- [ ] **Step 1: Write failing tests:**
  - `TestParsers` rows: `package.json:packageManager.bun` with `"bun@1.2.3+sha512.abc"` → `1.2.3`; with `"yarn@4.1.0"` → not found; `mise.toml:elixir` with string, table and array forms; `.mise.toml:erlang`.
  - `TestPackageManagerSource` (Review Focus 5): chain `[".bun-version", "package.json:packageManager.bun"]`, no fallback, dir with only `package.json` `packageManager = "yarn@4"` → `Value == ""`, no warning.
  - `TestKnownSource`: true for `.ruby-version`, `config/database.yml:development.database`, `.tool-versions:elixir`, `package.json:packageManager.bun`; false for `x.lock`, `package.json:packageManager` (no tool).
  - `TestLoadRuntimeWithoutFallback` (loads) and `TestLoadUnknownSource` (error names `runtimes.<n>.from[0]` and the file).
  - `TestExpandOptional`: `npm_global_node{node?}` with `node` unset → `npm_global_node`; set → `npm_global_node22.11.0`.
  - `TestEmptyRuntime` (render): minimal fixture pack plus `[runtimes.bun]` without fallback and `args.BUN_VERSION = "{bun}"` → the base image step's args have `BUN_VERSION=""`; `.bun-version` `1.2.3` in the worktree → `1.2.3`.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3: Implement.** `package.json` parser branches on the key prefix `packageManager.`. `.mise.toml` gets its own registry key. Keep `Resolved.Source` `"none"` for the empty case.
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5:** `make check`; commit `feat(version): optional fallbacks, packageManager and mise sources, {name?}`.

### Task 4: Browser and nvim without Rails runtimes

**Files:**
- Modify: `internal/pack/builtin/options/playwright/option.toml` (`[pin] name = "playwright"`, `default = "1.60.0"`), `internal/render/model.go` (`values()` pin loop), `internal/cli/new.go:316-323` (pin copy, C3), `internal/pack/builtin/tools/nvim/tool.toml` (`npm_global_node{node?}`), `internal/pack/builtin/tools/nvim/Containerfile.nvim` (own Node, C7)
- Test: `internal/render/model_test.go`, `internal/cli/new_test.go`, render goldens (expected unchanged), `TestBuiltinContainerfilesSelfContained` (still passes)
- Docs: `docs/nvim.md` (Node in the nvim image, `NVIM_NODE_VERSION`, volume name, remove the volume after a Node change, `containerfile eject nvim --force` for existing projects), `docs/packs.md` ("Option pins and pack runtimes")

**Interfaces:**
- Consumes: Task 3 `{name?}`.
- Produces: version precedence for a pinned name, high to low: `[worktrees.<slug>.versions]`, `[versions]`, the pack runtime of the same name, the pin default; `from` strings `worktrees.<slug>.versions`, `versions`, `runtimes.<n> (<source>)`, `<option> pin default`.
- Produces (C7) `Containerfile.nvim`: `ARG NVIM_NODE_VERSION=24` before the first `FROM`; a stage `FROM docker.io/library/node:${NVIM_NODE_VERSION}-slim AS nvim-node`; in the final stage, as root, copy its `/usr/local/bin/node` and `/usr/local/lib/node_modules` to `/opt/nvim-node/{bin,lib/node_modules}` and, only when `command -v node` fails, link `node`, `npm`, `npx` into `/usr/local/bin`; `ENV NPM_CONFIG_PREFIX=/home/appuser/.npm-global`, `PATH` gains `/home/appuser/.npm-global/bin`; `mkdir -p /home/appuser/.npm-global` owned by 1000. No source-text test: the host acceptance checks it (Task 9: `node --version` in a Phoenix nvim container works; in a rails nvim container it equals the app's Node).

- [ ] **Step 1: Write failing tests:**
  - `TestPlaywrightPinWithoutRuntime`: a fixture pack without `[runtimes.playwright]` plus `browser = "playwright@1"` → playwright tag value `1.60.0`.
  - `TestPlaywrightRuntimeBeatsPinDefault`: filial (Gemfile.lock `playwright-ruby-client (1.54.0)`) → `1.54.0`.
  - `TestVersionsBeatRuntime`: filial + `[versions] playwright = "1.55.0"` → `1.55.0`; plus `[worktrees.master.versions] playwright = "1.56.0"` → `1.56.0` on master.
  - `TestNewKeepsRuntimePinsOut` (C3): `project new --option browser=playwright@1` on `rails@1` → `project.toml` has no `[versions] playwright`; `--option database=postgres@1` still writes `postgres = "16"`.
  - `TestNvimWithoutNode`: fixture pack without `[runtimes.node]`, `[tools.nvim]` → nvim unit volume `<prefix>_npm_global_node`; `TestGolden` unchanged for filial/minimal.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test ./...` (incl. goldens without `-update`) → PASS.
- [ ] **Step 5:** `make check`; commit `fix: browser and nvim no longer need Rails runtimes`.

### Task 5: Project routes and route uniqueness

**Files:**
- Modify: `internal/config/project.go` (`Routes`, `Validate`), `internal/render/model.go` (`builder.services()` before the network loop at ~L356; uniqueness in `Build`), `testdata/fixtures/minimal/project.toml` (one route), render goldens (`-update`, review the diff)
- Test: `internal/config/project_test.go`, `internal/render/model_test.go`, `internal/render/quadlet_test.go`
- Docs: `docs/packs.md` ("Routes": app, option and project routes)

**Interfaces:**
- Produces:
  - `type Route struct { Name string \`toml:"name"\`; Service string \`toml:"service"\`; Port int \`toml:"port"\` }`; `Project.Routes []Route \`toml:"routes"\``.
  - Validate: `routes[%d].name` matches `^[a-z][a-z0-9-]*$`, unique; `routes[%d].service` required; `routes[%d].port` 1..65535.
  - Render: `Route{Name, Host: Name + ".{worktree.host}", Port}` appended to the service's `Routes`; unknown id → `routes[%d].service: no such service %q (services: …)`; a oneshot or agent → `routes[%d].service: %s has no long-running container to route to`.
  - Uniqueness over every service's routes: `routes: router %q is used by %s and %s`, `routes: host %q is used by %s and %s` (compare unexpanded host templates).

- [ ] **Step 1: Write failing tests:**
  - `TestProjectRoute`: minimal + `[[routes]] name = "vite", service = "rails", port = 3036` → rails unit has `traefik.http.routers.mini-master-vite.rule=` with `RouterRule("vite.master.mini.localhost", …)` and `…services.mini-master-vite.loadbalancer.server.port=3036`; rails joins proxy (already true).
  - `TestProjectRouteOnDataService`: a route to `db` → db joins `proxy`, has the labels.
  - `TestProjectRouteErrors`: unknown service; `deps`; port 0; bad name `Vite`.
  - `TestRouteCollisions` (Review Focus 4): project route `app`; route `s3` with rustfs; two project routes `vite` → each refused naming both owners.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3: Implement**; `go test ./internal/render -run TestGolden -update`; check that the golden diff is only the minimal fixture's vite labels.
- [ ] **Step 4:** `go test ./...` → PASS (incl. `TestRouterClientIP`, `TestGoldensLintClean`).
- [ ] **Step 5:** `make check`; commit `feat: project routes; refuse duplicate routes in a project`.

### Task 6: `railswitch pack eject|new|show`

**Files:**
- Create: `internal/cli/pack.go`, `internal/cli/pack_test.go`
- Modify: `internal/cli/cli.go` (`"pack": cmdPack`), `internal/cli/completion.go` (`completionSubs["pack"] = {"eject","new","show"}`, `completionFlags["pack eject"] = {"--as="}`), `docs/completion.md`, `docs/json.md` (`pack show`), `docs/packs.md` ("Commands")

**Interfaces:**
- Consumes: Task 1 `ParseRef`, `Catalog.Pack/Option`, `LoadCatalog`.
- Produces:
  - `pack eject <ref> [--as <name>]`: `<ref>` a built-in pack or option (local → `pack eject: %s is already local`); copies every file of its `Files` to `<root>/packs/<as>/` (options: `options/<as>/`), rewriting the manifest's top-level `name = "…"` line to `<as>`; an existing target dir → refuse (C5). Prints `wrote packs/<as>/: use it with [stack] pack = "local:<as>@<N>"` (options: `[options] <slot> = …`).
  - `pack new <name>`: `<root>/packs/<name>/` with `pack.toml` (every field of `pack.Pack`, commented, with a one-line meaning each), `Containerfile.base` (`ARG`/`FROM`/`USER appuser` skeleton that passes the Containerfile contract once uncommented), `entrypoints/entrypoint-app.sh` (`exec "$@"`), `README.md` (`# <name>` + "App-side changes" heading). Refuses an existing dir.
  - `pack show <ref>`: works outside a workspace for built-ins. Text: name, version, source (`built in` / `local <root>/packs/<name>`), then for a pack: app (service, server, port, health), runtimes (`name: from … fallback …`), ports, caches, worktree volumes, defaults; for an option: slot, service, pin, gives (names only), ports, routes; then `README.md` if present. `--json`: `{"ref","kind","source","path","app","runtimes":[{"name","from","fallback"}],"ports":[{"name","base","container"}],"pin","gives","readme"}` (absent parts omitted/empty arrays; documented in `docs/json.md`).

- [ ] **Step 1: Write failing tests:**
  - `TestPackEjectRails`: in a locked rails workspace, `pack eject rails@1` → every built-in file present, byte-equal except `pack.toml` whose `name` line is unchanged (`--as` defaults to `rails`); then set `pack = "local:rails@1"`, `lock`, `render` → succeeds; stack.lock `source = "local"`.
  - `TestPackEjectAs`: `pack eject postgres@1 --as pg` → `options/pg/option.toml` has `name = "pg"`; `local:pg@1` resolves.
  - `TestPackEjectRefusesExisting` and `TestPackEjectLocalRefused`.
  - `TestPackNewThenRender` (Review Focus 3): `pack new web` → files exist; `render` on the unchanged project succeeds; `pack show local:web@1` exits 1 naming `packs/web/pack.toml` and a missing field.
  - `TestPackShowJSON`: `--json pack show rails@1` decodes; `runtimes` contains `ruby` with `from[0] == ".ruby-version"`; `pack show playwright@1` has `pin.default == "1.60.0"`.
  - `TestPackShowReadme`: local pack with `README.md` → text output ends with it.
  - `TestCompletionFlagsMatch` passes with the new flag set.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3: Implement** following `containerfile.go` (`cmdContainerfile` sub-dispatch, `parseNamed`, `e.flags`, `json.NewEncoder`, `orEmpty`).
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5:** `make check`; commit `feat(cli): railswitch pack eject|new|show`.

### Task 7: Rails pack — JS tool by lockfile, bun runtime

**Files:**
- Modify: `internal/pack/builtin/packs/rails/{pack.toml,Containerfile.base,entrypoints/entrypoint-deps.sh,deps.allow}`, render goldens (`-update`; base image args gain `BUN_VERSION`)
- Test: `internal/pack/pack_test.go` (script tests, pattern of `TestRailsDepsEntrypointExecsArgs`), `internal/cli/containerfile_test.go` (`TestBuiltinContainerfilesSelfContained` still passes)
- Docs: `docs/packs.md` ("The rails pack: JavaScript"), roadmap Stage 5 note

**Interfaces:**
- Consumes: Task 3 sources and empty chains.
- Produces:
  - `[runtimes.bun] from = [".bun-version", "package.json:packageManager.bun"]` (no fallback); `[image] args` gains `BUN_VERSION = "{bun}"`; the tag stays `ruby{ruby}-node{node}` (the content hash covers the args).
  - `Containerfile.base`: `ARG BUN_VERSION` (before the first `FROM` and after the ruby `FROM`); as root after the node copy, `RUN if [ -n "$BUN_VERSION" ]; then <install bun $BUN_VERSION to /usr/local/bin/bun>; fi`; `RUN corepack enable` when `corepack` exists (Node < 25), else nothing.
  - `entrypoint-deps.sh` JS block, first match wins: `package-lock.json` → `npm ci --no-audit --no-fund`; `bun.lock`/`bun.lockb` → `bun install --frozen-lockfile`; `yarn.lock` → `yarn install --immutable`; `pnpm-lock.yaml` → `pnpm install --frozen-lockfile`. Messages (stderr, exit 1):
    - `package.json` and no lockfile: `deps: package.json has no lockfile: run railswitch deps -- npm install (or bun/yarn/pnpm install)`;
    - lockfile, tool missing: `deps: <lockfile> needs <tool>, which is not in the image: <fix>` with fix for bun `pin it in .bun-version or package.json "packageManager": "bun@<version>", then railswitch build (an older ejected containerfiles/Containerfile.base needs railswitch containerfile eject base --force)`, for yarn/pnpm `set package.json "packageManager" and use a Node with corepack (Node ≤ 24)`.
    - The stamp keeps today's shape, keyed on the tool's `--version`, `package.json` and the lockfile in use.
  - `deps.allow` gains the bun, yarn and pnpm download/registry hosts the builder confirms from their docs (e.g. `registry.yarnpkg.com`, `repo.yarnpkg.com`).

- [ ] **Step 1: Write failing tests** `TestRailsDepsLockfiles` (Review Focus 5): run the script (`cd /app` → temp dir) with fake `bundle`, `npm`, `bun`, `yarn`, `pnpm`, `node` on `PATH` that log their argv; table: only `package-lock.json` → `npm ci …`; `bun.lock` → `bun install --frozen-lockfile`; `bun.lockb`; `yarn.lock`; `pnpm-lock.yaml`; `package-lock.json` + `yarn.lock` → npm; `bun.lock` without a fake `bun` → exit 1, stderr contains `.bun-version`; `package.json` alone → exit 1 `has no lockfile`; second run with same inputs → `node_modules up to date`, no install.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3: Implement**; `go test ./internal/render -run TestGolden -update`; diff shows only `BUN_VERSION` in image args/hashes.
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5:** `make check`; commit `feat(rails pack): npm, bun, yarn or pnpm by lockfile; bun runtime`.

### Task 8: Phoenix fixture pack and workspace

**Files:**
- Create: `testdata/fixtures/phoenix/` — `project.toml` (prefix `phx`, `[stack] pack = "local:phoenix@1"`, `app = "web"`, `[options] database = "postgres@1"`, cache/storage/browser `none`, `[tools.nvim]`, `[agents.claude] use = "claude@1"`), `master/.tool-versions` (`elixir 1.18.4`, `erlang 27.3.4`; the README notes that asdf's `1.18.4-otp-27` form must be written as `1.18.4`), `master/mix.lock` (empty), `packs/phoenix/{pack.toml,Containerfile.base,entrypoints/entrypoint-app.sh,entrypoints/entrypoint-deps.sh,deps.allow,README.md}`; goldens `internal/render/testdata/golden/phoenix/`
- Modify: `internal/testfixture/testfixture.go` (eject list from the resolved stack, not hard-coded rails/postgres), `internal/render/quadlet_test.go` `TestGolden` and `internal/render/lint_test.go` `TestGoldensLintClean` (add `phoenix`), `internal/genericity_test.go` (`phoenixRe = (?i)\b(phoenix|elixir|erlang)\b|\bmix `)

**Interfaces:**
- Consumes: Tasks 1–5 (no core change here; see Global Constraints).
- Produces (pack contents, values from the spec and `docs/reference/design-data.md` §1):
  - `[app]` service `web`, server `mix phx.server`, port 4000, health `curl -fsS -o /dev/null http://localhost:4000/`, `health_start_period = "300s"` (first compile), entrypoints app/deps.
  - `[image]` `file = "Containerfile.base"`, `tag = "elixir{elixir}-otp{erlang.major}"`, `os = "debian"`, `args = { ELIXIR_VERSION = "{elixir}", OTP_MAJOR = "{erlang.major}" }`; `Containerfile.base` `FROM docker.io/library/elixir:${ELIXIR_VERSION}-slim` (or the `-otp-` tag form; the builder checks which tags exist), apt `build-essential git curl inotify-tools ca-certificates`, `mix local.hex --force && mix local.rebar --force` for `appuser`, `appuser` uid 1000 last `USER`, `WORKDIR /app`.
  - `[runtimes.elixir] from = [".tool-versions:elixir", "mise.toml:elixir", ".mise.toml:elixir"]`, fallback `1.18.4`; `[runtimes.erlang]` likewise, fallback `27.3.4`.
  - `[db_name] fallback = "{project.prefix}_dev"`; `[db_scheme] postgres = "ecto://"`.
  - `[env] MIX_ENV = "dev"`, `PORT = "4000"`, `PHX_HOST = "{worktree.host}"`.
  - deps entrypoint: `mix deps.get --check-locked` (an empty `mix.lock` on first run: `mix deps.get`, documented) then `mix assets.setup` when the alias exists; app entrypoint: `mix ecto.create && mix ecto.migrate`, then `exec "$@"`.
  - `[[caches]]` `mix_elixir{elixir}` → `/home/appuser/.mix`, `hex` → `/home/appuser/.hex`; `[[worktree_volumes]]` `deps` → `/app/deps`, `build` → `/app/_build`.
  - `[[ports]]` `app` base 13000 container 4000 `share = true`.
  - `deps.allow`: `hex.pm`, `repo.hex.pm`, `builds.hex.pm`, `meta:github`, `github.com`, `objects.githubusercontent.com`, `release-assets.githubusercontent.com`, `registry.npmjs.org` (esbuild).
  - `README.md`: the bootstrap `podman run` from the spec; app-side changes (`--inside-docker-env` or `ip: {0, 0, 0, 0}`; dev Repo config without `username`/`password`/`hostname`, `database: System.get_env("PGDATABASE", "<app>_dev")`).

- [ ] **Step 1: Write failing tests:** `TestGolden`/`TestGoldensLintClean` with `phoenix` (no goldens yet → FAIL); `TestPhoenixFixtureResolves` (render package): `st.PackRef == Ref{"phoenix",1,true}`, web env has `PGDATABASE=phx_dev`, `DATABASE_URL` starts `ecto://`, base image args `ELIXIR_VERSION=1.18.4` and `OTP_MAJOR=27`; nvim volume `phx_npm_global_node`. `TestGenericity` with `phoenixRe` passes on current code.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3: Implement** the fixture and `testfixture` change; `go test ./internal/render -run TestGolden -update`; read every new golden.
- [ ] **Step 4:** `go test ./...` → PASS; `git diff --stat main -- internal/render internal/config internal/lifecycle internal/version` shows only test files.
- [ ] **Step 5:** `make check`; commit `test: Phoenix fixture pack (local:phoenix@1) and goldens`.

### Task 9: Docs, decisions, acceptance and the Phoenix end-to-end test

**Files:**
- Create: `docs/stage5-acceptance.md`, `internal/lifecycle/e2e_phoenix_integration_test.go` (`//go:build integration`)
- Modify: `docs/packs.md` (complete: every manifest field of `pack.toml`/`option.toml`/`tool.toml`/`agent.toml`, conventions `/app`, `appuser` 1000, Debian with agents, `README.md`), roadmap (decisions D65–D70 below; Stage 5 row "detailed (`2026-10-27-railswitch-stage-5.md`)"), `AGENTS.md` (decisions range, `docs/packs.md` in the package map line for `internal/pack`)

**Interfaces:**
- Roadmap decisions to add: D65 local packages on demand (C1); D66 lock locality (C2); D67 project routes and route uniqueness (C4); D68 option pin vs pack runtime precedence and C3; D69 JS tool by lockfile, bun as a pack runtime, copy per pack (user, 2026-10-27); D70 `pack eject|new|show` (C5) and `{name?}`; D71 nvim's own Node (C7, amends D23/D45; user, 2026-10-27).
- `TestE2EPhoenix` (skipped unless `RAILSWITCH_E2E_PHX_REPO` is set; prefix `RAILSWITCH_E2E_PREFIX`, default `s5p`): copy `testdata/fixtures/phoenix/packs/phoenix` into a temp workspace → `project new --pack local:phoenix@1 --option database=postgres@1 --agent none` → `up` → `curl` the app host → 200 → `wt add feat` → `up` in feat → 200, and `PGDATABASE` differs between the two (`railswitch env web`) → append a comment to `packs/phoenix/README.md` → `up` fails with `stack.lock is out of date` → `lock` → `up` OK → `down --yes` both, `wt rm feat`, `project rm --yes --purge`; afterwards no object named `<prefix>-…`/`<prefix>_…` remains.

- [ ] **Step 1:** Write the e2e test; `go vet -tags integration ./...` clean.
- [ ] **Step 2:** Write `docs/stage5-acceptance.md`: prerequisites, the bootstrap `podman run`, the app-side edits, `TestE2EPhoenix` command, by-hand checks (live reload: edit a `.heex` template, the page reloads; `railswitch nvim`, then in the nvim container `node --version` and `npm install -g` of a language server work (C7), and on a rails project the nvim container's `node --version` equals the app's; `railswitch agent` with `[agents.claude]` added via `agent add`), `rails new -j bun` check (`deps` uses `bun install --frozen-lockfile`), run log section, done checklist from the roadmap.
- [ ] **Step 3:** Docs and decisions as listed; `make check`.
- [ ] **Step 4:** Commit `docs: Stage 5 pack reference, decisions, acceptance; Phoenix e2e test`.
- [ ] **Step 5 (user, host):** run the acceptance doc; every gap goes through a fix task (generic feature + test) and the gap log.
