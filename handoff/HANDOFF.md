# Handoff: a new dev-stack tool, built from what rails-dev-podman taught us

This document is for the agent that starts the new project. It holds:

- the decisions made so far;
- the design of the tool and of its screens;
- what to carry over from `rails-dev-podman` and what to drop;
- the order to build it in.

The user is the decision-maker. Anything marked **Open** must be confirmed with them before you build on it.

> **Implement it in Go** (§2a).
>
> **Read first:** this whole file, then `rails-dev-podman/AGENTS.md`. AGENTS.md is the current tool's specification. Nearly every paragraph in it records a bug that was hit and fixed. The new tool must not hit them again (see [Gotchas](#7-gotchas-each-becomes-a-check-or-a-test)).

---

## 1. Why a new project

`rails-dev-podman` is a working wrapper for one Rails app (Filial Rails). It gives each git worktree its own rootless podman stack:

- **Units:** Quadlet units, templated as `<prefix>-<svc>@<slug>`, managed with `systemctl --user`;
- **Commands and scripts:** mise tasks, and bash scripts in `.scripts/`;
- **Routing:** a Traefik proxy giving each worktree hostnames;
- **Egress firewall:** applied from outside by an OCI hook;
- **Dashboard:** a Ruby web page (Roda + Falcon).

A design pass (the canvas in §5) turned it into a general tool for **web apps on any stack**. That changes the core data model:

- **Then:** bash plus templates, with Rails and Filial assumptions everywhere: a `rails@` unit, `PG*` in `.env`, `rustfs-init`, Dockerfile stages, `filial_shared_gems_*`.
- **Now:** `project.toml` plus *packs*, *options* and a `stack.lock`, rendered into units by one engine.

Adapting the old repo would mean rewriting nearly every script while keeping it in daily use. So the plan is a new repo, with two rules:

1. **The old repo is the specification and the acceptance test,** not a codebase to copy wholesale. Port the generic parts nearly as they are (§6). Turn its gotchas into checks (§7).
2. **The old repo stays in daily use until the new tool can *adopt* the Filial workspace** (the Adopt screen, §5). Adopting Filial is the acceptance test for version one.

## 2. Decisions

| Topic | Decision | Status |
|---|---|---|
| New repo vs refactor | New repo, from scratch; old repo as spec and acceptance test | Decided |
| Scope | Web apps only, any language (Rails, Hanami, Phoenix, Django, Flask, Node…) | Decided |
| Language | **Go**, built as a single static binary: the engine, the CLI, and the web UI and dashboard. There is no runtime to install on the host, so a Python or Elixir project never needs Ruby to run the tool. Call the `podman`, `systemctl` and `git` commands rather than using podman's Go bindings, which are tied to podman versions. The Ruby dashboard is ported to Go, not kept | Decided |
| UI | One local web app on loopback (`wt.localhost:7700`), serving both setup/settings and the dashboard. HTML/CSS/JS embedded in the binary (`embed`) | Decided |
| Source of truth | Files (`project.toml`, `stack.lock`), with the CLI as the full interface. The browser never changes state directly; it runs CLI commands and shows their output, as dashboard actions do today. Everything must work without a browser (SSH, scripts) | Decided |
| mise | Stays for runtime versions inside containers. The tool's own commands (`up`, `wt`, `share`…) become its own CLI, not mise tasks | Decided |
| Dashboard scope | **One project at a time**, with a project switcher. Other projects' agents that need input stay visible | Decided |
| Dashboard colours | A user palette: four presets plus eight editable colours, saved machine-wide | Decided |
| Tool name | The canvas uses `wt` as a placeholder | **Open** |
| Repo name/location | Not created yet; the user creates it | **Open** |

## 2a. Implementation: Go

Everything is one Go module that builds one static binary. Suggested layout (adjust as needed, but keep the boundaries):

```
cmd/wt/                 # CLI entry point (name is a placeholder until decided)
internal/config/        # project.toml + stack.lock parsing and validation
internal/pack/          # pack and option contracts, built-in packs (embedded)
internal/render/        # Quadlet/systemd unit rendering + render-time lints (§7)
internal/podman/        # thin wrappers over the podman/systemctl CLIs (exec, never bindings)
internal/worktree/      # git worktree add/remove (scoped), ID registry
internal/machine/       # shared proxy, networks, subnet pool, port registry
internal/firewall/      # firewall descriptor + annotations; the hook script itself stays shell
internal/web/           # HTTP server: dashboard + setup/settings screens, SSE, guards
internal/jobs/          # actions in systemd-run scopes, log tailing with size caps
internal/gitsafe/       # sanitized shadow GIT_COMMON_DIR for reading worktrees
web/                    # HTML/CSS/JS, embedded with go:embed
```

**Rules for the Go code:**
- **Libraries:** prefer the standard library: `net/http`, `html/template`, `text/template` for units, `os/exec`, `embed`, `crypto/sha256`, `log/slog`. Add only small, well-maintained dependencies, such as a TOML parser (`github.com/BurntSushi/toml` or `github.com/pelletier/go-toml/v2`).
- **Calling commands:** argv only, never through a shell, and always with a `context` and a timeout.
- **Testing without a host:** commands go through an interface, so tests can replace them. Unit tests run without podman, systemd, gh or network, as `dashboard:test` does today. Rendering is tested with golden files.
- **Integration tests** that need podman sit behind a build tag (`//go:build integration`).
- **Before every commit:** `go vet` and `staticcheck` (or `golangci-lint`), and `gofmt`.
- **Porting the old repo:**
  - the Ruby dashboard modules are ported to Go, keeping their behaviour and the security rules in §7;
  - the OCI firewall hook may stay a shell script, since crun runs it, but the descriptor and annotations are generated by Go;
  - the fixture-based tests in `.dashboard/test/` show which behaviour to keep.
- **The web UI** is served by the same binary. A light approach (server-rendered `html/template` plus small JS, as today with Turbo streams/SSE) is preferred over a heavy frontend build. If a build step is added, its output is embedded and committed, so `go build` alone produces a working binary.

## 3. The model

### 3.1 Files in a project

```
<workspace>/
  project.toml      # choices and overrides only; small, reviewable, in git
  stack.lock        # every pack/option by commit + sha256, every image by digest
  mise.local.toml   # secrets (values), git-ignored, never in a template
  master/           # base worktree (the app repo)
  <slug>/           # linked worktrees
```

Where `project.toml` and `stack.lock` live, and how a teammate *joins* an existing project, is still undecided (§9).

### 3.2 `project.toml`

This is what the Review screen generates for Filial Rails. `units:install` in the example stands for the new tool's render command.

```toml
from = "rails-postgres-redis-rustfs@1"      # template it came from (optional)

[project]
name        = "Filial Rails"
prefix      = "filial"                     # unique on this machine; names objects and hosts
repo        = "git@github.com:[ORG]/[REPO].git"
base_branch = "master"

[stack]
pack = "rails@1"
app  = "rails"                             # the service with role app

[options]                                  # one per slot; "none" turns a slot off
database = "postgres@1"
cache    = "redis@1"
storage  = "rustfs@1"
jobs     = "none"
mail     = "none"
browser  = "playwright@1"

[versions]                                 # pins for options' images;
postgres = "16"                            # runtimes (ruby, node) come from the app's files
redis    = "8"
rustfs   = "1.0.0-alpha.83"

# Only what differs from the pack and options:
[env]
TZ = "Asia/Bangkok"

[services.rails.env]
HEADLESS_SYSTEM_TESTS = "0"

[worktrees.postgres-trial.versions]        # per-worktree override
postgres = "17"

[secrets]                                  # values live in mise.local.toml
names = ["DB_PASSWORD", "S3_SECRET_KEY"]

[[links]]                                  # Worktree links (see 3.10)
label = "Admin"
url   = "{worktree.host}/admin"
```

The **expanded** form is never stored. The render step fills it in from the pack and options, and the UI can show it:

```toml
[services.rails]
role    = "app"
image   = "rails"            # Containerfile.app › target rails
command = "bin/dev"
route   = "{worktree.host} -> 3000"

[services.deps]
role    = "deps"
command = "bundle install && npm install"

[services.db]                # from PostgreSQL
role   = "data"
image  = "docker.io/library/postgres:{postgres}"
health = "pg_isready"
volume = "db-data-pg{postgres.major}:…"

[services.browser]           # from Playwright
role  = "tool"
route = "vnc.{worktree.host} -> novnc"
```

### 3.3 Roles

| Role | Meaning | Example |
|---|---|---|
| `app` | The web app; routed; mounts the worktree | rails, web |
| `deps` | Oneshot before each app start; the only role with registry access | bundle/npm/uv/mix install |
| `worker` | Same image and mounts as app, not routed | sidekiq, celery |
| `data` | Database, cache, storage, mail; health-gated | postgres, valkey, rustfs |
| `tool` | Developer tools | playwright/VNC, nvim, claude |

### 3.4 Stack packs (one per framework)

A pack knows one framework. Its contract:

- **Install command** (run by `deps`), **server command** and **port**.
- **Version-file chains with a fallback**, read per worktree. For example ruby: `.ruby-version` › `.tool-versions` › `Gemfile` `ruby` › fallback. Node: `.nvmrc` › `.node-version` › `package.json` engines › fallback. Playwright: `Gemfile.lock` › fallback.
- **Caches** to share across worktrees (gems by ruby version, npm cache…).
- **Allowed-hosts env:** `DEV_HOSTS`/`DOMAIN` for Rails, `PHX_HOST` for Phoenix, `ALLOWED_HOSTS` for Django…
- **Database URL scheme** per database option (`postgres://`, `mysql2://`, `ecto://`…).
- **Tasks** (console, tests…), **registries** for the `deps` firewall profile (`deps.allow`), its `Containerfile.app`, and entrypoints.
- **Suggested worktree links** (§3.10).

Packs sketched in the canvas:

| Pack | App service | Server | Port | Install | Runtimes | Job options |
|---|---|---|---|---|---|---|
| rails | rails | `bin/dev` | 3000 | `bundle install && npm install` | ruby, node | sidekiq, solid-queue, goodjob |
| hanami | web | `bundle exec hanami dev` | 2300 | `bundle install && npm install` | ruby, node | sidekiq |
| phoenix | web | `mix phx.server` | 4000 | `mix deps.get` | elixir, erlang | oban (in-app, no worker service) |
| django | web | `uv run manage.py runserver 0.0.0.0:8000` | 8000 | `uv sync` | python | celery, rq |
| flask | web | `uv run flask run --host 0.0.0.0` | 5000 | `uv sync` | python | celery, rq |
| node | web | `npm run dev` | 3000 | `npm ci` | node | bullmq |

Packs must also declare the dev server's **bind address** (must be `0.0.0.0` in a container) and its **HMR/websocket ports**. These are not designed yet (§9).

### 3.5 Options (one choice for a slot)

Slots are database, cache, storage, jobs, mail and browser. An option is **an image plus what it provides**:

| Field | Example (postgres) |
|---|---|
| Image | `docker.io/library/postgres:{postgres}` |
| Version pin | `postgres = "16"` (major used in volume name) |
| Port (inside the dev network) | 5432 |
| Health check (gates app start) | `pg_isready` |
| Gives the app | `DATABASE_URL={scheme}…@db/{db}` (scheme from the pack) |
| Data volume | `/var/lib/postgresql/data`, named `db-data-pg{postgres.major}` |
| Dump / restore | `pg_dump -Fc {db}` / `pg_restore -d {db}` (restore on a fresh volume) |
| Secret | `POSTGRES_PASSWORD` |
| Works with | which packs |
| Suggested links | e.g. Mailpit → `mail.{worktree.host}` |

Options in the canvas:

- **Database:** postgres, mysql, mariadb, sqlite, none.
- **Cache:** redis, valkey, none.
- **Storage:** rustfs, minio, none. Storage has two addresses, `s3.` (API) and `s3-ui.` (console), which the option model doesn't express well yet.
- **Mail:** mailpit.
- **Jobs:** per pack.
- **Browser:** playwright.

Users can **add an option** from a registry image or a pasted or uploaded `Containerfile.<name>`. They choose to offer it as a slot option, as a new service, or as an image only (a base to build others on). Example: Memcached gives the app `MEMCACHE_SERVERS=cache:11211` and is health-checked with `nc -z localhost 11211`. Adding an image adds a choice on step 1: images and options are the same thing seen from two screens.

### 3.6 Images and versions

- **An image is pulled** (`pull = "docker.io/library/postgres:{postgres}"`) **or built** (`build = { file = "Containerfile.app", target = "rails" }`).
  - A built image is *file + target + args*, and the tag carries its versions, e.g. `rails:ruby3.3.6-node22.11`.
  - Worktrees on the same versions share an image; rebuilding one worktree only rebuilds the tags it uses.
  - Several containers built from one Containerfile with different targets build their shared stages once.
- **Custom Containerfiles need a suffix:** `Containerfile.<name>`. The name says what the file is for.
- **Runtime versions come from the app's own files,** read per worktree, so one worktree can move to a new ruby, node or playwright and be rebuilt without affecting the others. Per-worktree overrides go in `project.toml` `[worktrees.<slug>.versions]`, never in a file inside the worktree: containers can write to the worktree.
- **Option versions are pinned** in `[versions]`. Changing a database's major version is "data not carried over", so the volume is named by major version and the old volume is kept until it is cleaned.
- **Build args are not runtime env.** They live on the image and change its tag.

### 3.7 Environment layers (lowest first; later wins)

1. **Pack:** framework variables, written in terms of the worktree.
2. **Option:** what each option gives the app (`DATABASE_URL`, `REDIS_URL`…) and needs itself.
3. **Project:** `project.toml [env]`, every service; kept in templates.
4. **Service:** `project.toml [services.<id>.env]`; kept in templates.
5. **Worktree:** hosts and ports worked out per worktree; read-only.
6. **Secret:** names in `project.toml`, values only on this host (`mise.local.toml`), never in a template.
7. **Share:** only while a worktree is shared over the tailnet; replaces the pack's host variable.

Editing a pack or option variable adds an override at layer 3 or 4. The UI keeps the original visible, struck through.

### 3.8 Initialisation: volume seeds, not init containers

`rustfs-init` (a oneshot that untars `rustfs_data.tar.gz` into an empty volume) becomes a **volume seed**: when the volume is first created, `podman volume import` fills it. The database's restore works the same way: the option's restore command runs on a fresh volume. There are no init containers.

### 3.9 Templates and the library

- A **template** is a pack, a set of options and your overrides, ready to start a project from.
  - Exporting turns this project's lines into questions with defaults: which option for each slot, each pin, `TZ`, and secrets. Project-only lines (name, prefix, repo, per-worktree overrides) are dropped.
  - A template package is small: `<name>.stack/` with `stack.toml`, `containerfiles/` (only the ones you edited) and `README.md`.
- The **library** has three tabs: Templates, Stack packs, Options. It can import a file or add a library git repository.
- A project records the template, pack and options it uses, with their versions. Updates appear as a diff against `project.toml` and are only applied by the user, never silently.

### 3.10 Worktree links

- **Links are settings.** Packs, options and core suggest them; the user shows or hides each, and adds their own.
- **Placeholders:** `{worktree.host}`, `{worktree.slug}`, `{branch}`.
- **Storage:** links are stored in `project.toml` (`[[links]]`) and carried by templates.

| Source | Links |
|---|---|
| core | App `{worktree.host}`, Logs |
| Rails pack | Letter opener `/letter_opener` (only if the app has `letter_opener_web`), Mailer previews `/rails/mailers`, Routes `/rails/info/routes` |
| options | Mailpit `mail.`, Storage console `s3-ui.`, Browser (VNC) `vnc.`, Sidekiq `/sidekiq`, GoodJob `/good_job` |
| Phoenix pack | LiveDashboard `/dev/dashboard`, Mailbox `/dev/mailbox` |
| user | e.g. Admin `{worktree.host}/admin` |

### 3.11 Trust: locked, reviewable packs and options

Packs and options run code on the host (Containerfile `RUN`, entrypoints) and change what the firewall allows. So:

- **Nothing from a library is installed or updated until it has been reviewed.** The review screen shows:
  - "What it can do" checks: firewall additions (e.g. flags `objects.githubusercontent.com # not a package registry`), what runs on this machine (flags `curl | sh`), host mounts, ports, privileges, images;
  - every file, with diffs against the installed version;
  - the resulting `stack.lock`.

  The user must tick "I've read the N flagged items".
- **`stack.lock`:**
  - every pack and option is pinned by commit and content sha256, and every image by the digest from its first pull;
  - if a library later rewrites a published version, the hash no longer matches and install stops.
- **Levels of trust:**
  - built-in packs and options are locked to the tool's own version;
  - local ones are locked by hash;
  - an unsigned library, or a signer not yet trusted, is shown but can't be installed.

  How signers are managed is not designed yet (§9).

## 4. Machine-wide layer

One machine runs many projects. Each project registers its worktrees with the machine and never runs its own proxy or dashboard.

- **One proxy (`wt-traefik`),** which joins every project's `<prefix>_proxy` network at `.2`.
  - Subnets come from the pool `10.214.0.0/16`, one `/24` per project; for example filial `10.214.0.0/24` with Traefik at `10.214.0.2`, billing `10.214.1.0/24`.
  - Projects can't reach each other: each project's containers are only on its own proxy network, and only Traefik is on all of them.
  - Every routed container needs the `traefik.docker.network` label, so Traefik uses the right network.
  - Attach Traefik with `podman network connect --ip`.
  - Router names must carry the project prefix (§9).
- **Hostnames:** `<slug>.<prefix>.localhost`, plus `s3.`, `s3-ui.`, `vnc.` and `mail.` in front.
  - One project may also keep short names (`<slug>.localhost`). For Filial this keeps existing URLs and OAuth callbacks working.
- **Ports:** one registry of worktree IDs for the whole machine (today's formulas, ID = worktree ID):

  | Port | Formula |
  |---|---|
  | DB | 55432 + ID |
  | Ruby debug | 33000 + ID |
  | Neovim | 17000 + ID |
  | Playwright | 18888 + ID |
  | App, loopback, for share | 13000 + ID |
  | Storage, loopback, for share | 19000 + ID |

  In the new tool, packs and options should declare which ports they publish, rather than this list being fixed.
- **One dashboard (`wt-dashboard.service`).** A worktree's page is `/<prefix>/<slug>`.
- **Moving Filial onto the shared proxy (part of Adopt):**
  1. `filial-traefik`, `filial-dozzle` and `filial-dashboard` stop.
  2. `wt-traefik` starts on the same ports and joins `filial_proxy` at 10.214.0.2, the address the old Traefik had.
  3. Worktrees need no restart: their network, firewall annotation and `--add-host` entries don't change. Routes drop for the few seconds between the two Traefiks.

## 5. Screens (design canvas)

The canvas is a set of interactive mock-ups:

- **Original:** https://claude.ai/artifact/2cjcGLbuRHW5U32rJVjQak. It's private to the user, so you may not be able to open it.
- **Copies:** `design/*.dc.html` (sent with this file), with `design/canvas.json` as the board index. They're HTML in a canvas-template format: `{{…}}` holes, `<sc-for>` and `<sc-if>` blocks, and each file's logic is in the `renderVals()` script at the end. The **data in those scripts** (packs, options, env, decisions) is the most useful part to read.

Use them as UI specifications, not as code to copy. Every screen uses the Flexoki colours of the current dashboard and Nunito type.

| Board | What it decides |
|---|---|
| `Main.dc.html` — New project, step 1 of 2 | Repo first (cloned into `master/` and read), then branch, name and prefix (unique on the machine). The stack is detected from the repo, with a pack picker. Slot rows show whether each was "Read from the repository · Gemfile: pg" or "Changed by you". Then tools, and a summary of routes, containers, app env and warnings. Banner: "Adopt this workspace" |
| `Review.dc.html` — step 2 | Generated `project.toml` with three views: As project / Expanded (purple lines are filled in by the render, not stored) / As template (red lines are dropped, yellow become questions). The create steps. Save as template |
| `Images.dc.html` — advanced | Images the stack uses, with "Brought by"; Containerfiles; versions (chains, pins, Change…); built images per worktree; the "Add an option…" dialog |
| `Environment.dc.html` — advanced | Per-service effective env with a layer column; the seven layers |
| `Templates.dc.html` — Stack library | Templates / Packs / Options tabs; "Built from" with lock status; projects using a template; updates as diffs |
| `Adopt.dc.html` | Scan of a hand-built workspace (Filial: 12 unit templates, 2 Containerfiles, 5 entrypoints, `.env` with 29 entries…) and a mapping table (Matches / Kept as override / This machine only / Changes / Core). **Four decisions:** PG* vs DATABASE_URL, volume names, rustfs-init as a seed, proxy and hostnames. A dry-run diff of every unit. Adopt is disabled until every decision is made. Backing out = delete `project.toml` and render again |
| `Machine.dc.html` — This machine | Projects, hostnames and short names, the shared proxy with per-project networks and a "who can reach whom" grid, the port registry, the dashboard, migration steps |
| `Install.dc.html` — Review before installing | Example: a django-celery update from v2 to v3. Capability checks, file diffs, `stack.lock` preview, trust rules |
| `Settings.dc.html` — existing project | Prefix fixed after creation. Slot changes are *staged*, each applied to every worktree or to one (as an override), and labelled No data affected / Check before applying / Data not carried over. Apply steps with a restart checkbox. Worktree links. Updates list. Remove project |
| `Dashboard.dc.html` | Project switcher (one project at a time) and the colour palette (below) |

### Dashboard: project switcher

- **The switcher** is a button at the top left showing the current project's name and prefix. The shortcut `P` opens it.
  - A search box finds projects, *and worktrees in any project*. Picking a worktree switches to its project.
  - Each project row shows its number key (`1`–`9`), worktree and running counts, ◆ agents needing input, ● agents working, and its stack.
  - Footer: New project…, Adopt a workspace….
- **The list** shows one project at a time, with a bar above it: name, prefix, stack, running count, and a settings link.
- **Which project opens:** the one used last, remembered in the browser. A worktree URL (`/billing/main`) opens its own project.
- **Other projects stay visible when they need you:**
  - a strip above the list ("Other projects need you") links to each waiting agent in another project;
  - the header's "◆ N need input" counts every project;
  - notifications fire for every project.
- **Tasks and jobs:** the task palette (`:`) runs in the selected worktree's project, and project-wide tasks say which project they act on. Each job in the actions panel is labelled with its project.
- **Links on the worktree page** come from that project's Worktree links setting.

### Dashboard: colour palette (⚙ in the header)

- **Presets:** Flexoki (default), Paper, Solarized (darkened to pass contrast) and High contrast. All of them pass 4.5:1 against their own background.
- **Eight editable colours,** each named by its use:

  | Colour | Used for |
  |---|---|
  | background | the page |
  | panels | header, headings |
  | text | names, values |
  | muted | secondary text |
  | accent | ◆ needs you, selection |
  | working | ● working, links |
  | running | ● services up |
  | your turn | warnings |

- **Editing.** Changing any colour turns the palette into "Custom". A live preview shows the header and agent states. If a text colour falls under 4.5:1 against the background, a contrast warning appears; it doesn't block saving.
- **Saving.** Save, Reset to the preset, and Copy as TOML. The palette is stored machine-wide, not in browser storage:

  ```toml
  # ~/.config/wt/dashboard.toml
  [colors.light]
  base  = "flexoki"
  muted = "#5F5E59"     # only the colours changed from the base
  # [colors.dark] is separate; the editor edits whichever the system is in
  ```

## 6. Carry over vs drop

**Port nearly as they are** (generic, hard-won):

| From `rails-dev-podman` | What |
|---|---|
| `.container-config/firewall/` (`hook.sh`, `hook.json`, `*.allow`) + `.scripts/firewall.sh` | OCI createRuntime egress firewall, profiles `none`/`deps`/`agent`, enforce/log/off modes, `fw:show`/`fw:allow` |
| `.dashboard/lib/dashboard/git_safety*` | Sanitised shadow `GIT_COMMON_DIR` for reading untrusted worktrees |
| `.dashboard/lib/dashboard/runner/` (`log_tail.rb`, `spawn.rb`), `child_env.rb`, `session_env.rb` | Jobs in `systemd-run --user --scope`, logs to files, size caps, clean child env |
| `.dashboard/lib/dashboard/safe_file.rb`, `guard.rb` | Reading container-writable files as hostile; Host/Origin/token checks, no framing |
| `.scripts/newterm.sh` | Terminal detection and new tab/window escalation |
| `.scripts/create-worktree.sh`, `remove-worktree.sh` | Name sanitising, ID allocation, *scoped* removal |
| `.scripts/share-worktree.sh` | Tailnet sharing via loopback-published ports |
| `claude-status-hook.sh` + dashboard agent states | permission/question/reply/working/your turn/idle/off |
| `.container-config/Dockerfile.playwright` + entrypoint | Playwright server, Xvfb + VNC, CDP bridge, in-container firewall |
| `.dashboard/test/` | Fixture-based tests: the approach, and the fixtures where they still apply |

**Drop or replace:**

- **mise tasks as the command surface:** the tool's own CLI replaces them.
- **The `.unit-env/<slug>.env` cache and the staleness checks around it:** render from `project.toml` and `stack.lock` instead. Values still need to reach systemd under `[Service]` (§7).
- **Rails-named services, `PG*` in `.env`, `rustfs-init`, the `filial_*` names, and per-service Dockerfile stages:** these become the Rails pack and its options.
- **One Traefik per project:** replaced by the machine-wide proxy.
- **`ports.registry` per workspace:** replaced by the machine-wide registry.

## 7. Gotchas: each becomes a check or a test

| Gotcha (see AGENTS.md) | Make it |
|---|---|
| Never name a service with a TLD in Chromium's HSTS preload list (`app`, `dev`, `page`, `new`…): `http://app:3000` is forced to https (commit `ed5f5d3` renamed `app` → `rails`) | Validation error on service names |
| Quadlet doesn't expand `${VAR}`; systemd does, so the env file must load under `[Service]` too | Render-time lint (today `lint_env_expansion`) |
| `Label=`/`Environment=` values with spaces must be quoted, or a Traefik rule is silently cut in half | Render-time lint |
| A Traefik regex backreference must be written `$${1}` | Render-time lint |
| A bare `Network=` creates no dependency; `.network` generates `<name>-network.service` | Renderer always emits `Requires=`/`After=` |
| podman won't create missing bind-mount sources | Preflight before `up` |
| The firewall must read subnets from netavark's `<name>.json`, not the netns (keep-id containers have no routes yet at createRuntime) | Port as is; test |
| Without a hook descriptor, podman starts containers *unfirewalled* without a word | `up` refuses; doctor checks; `fw:show` names unfirewalled containers |
| A unit joining a new network must add it to the firewall networks annotation | Renderer derives the annotation from `Network=` |
| `git worktree prune` from a partial view deletes every worktree's admin entry | Only scoped `git worktree remove`; abort on a partial view; test |
| Name filters like `name=^<prefix>-<slug>-` are unanchored regexes (api vs api-v2) | Always remove by exact name; test |
| Shared objects use `_` (`<prefix>_proxy`) so per-worktree matching can't reach them | Naming rule in the renderer |
| `deps` must not `RemainAfterExit`, so every app start re-runs it; its checks must be offline | Pack contract + test |
| Containers can't reach loopback-bound host ports; Traefik needs a static IP on the proxy net, and `--add-host` points worktree hosts at it | Machine layer design (§4) |
| Rootless podman can't publish :80 below `ip_unprivileged_port_start` | doctor + `allow-ports` |
| Host ports bind 127.0.0.1 only (else LAN reaches every worktree and password-less VNC) | Renderer never emits a non-loopback publish |
| `--env-file` on a missing path is fatal; truncate, never delete | Rule in the share code |
| `tailscale serve reset` clears *every* serve on the machine; there is only one :443 | Must be redesigned for many projects (§9) |
| A stale `$SSH_AUTH_SOCK` makes podman refuse the container (`statfs`) | Validate; fall back to `/dev/null` |
| The dashboard must never run `mise env` or read worktree files trustingly (container-writable) | Port `SafeFile`/GitSafety; test |
| Browse at the worktree host on :80, never `rails:3000` (wrong-port `*_url` links) | Pack sets the link host; docs |
| Logs: `k8s-file` means container stdout isn't in the journal; oneshots log to the journal | `logs` command picks the right source |
| Firewall allowlists resolve hostnames once per start; CDNs rotate | `fw:allow`/restart; document |

## 8. Build order

**Stage 0 — Set up.** Confirm the tool name and repo with the user. Then lay out the Go module, as sketched in §2a.

**Stage 1 — Engine and lint.**
- Parse `project.toml`, resolve one built-in pack (rails) and its options, and write `stack.lock`.
- Render Quadlet units and the env.
- Every render-time gotcha from §7 is a lint with a test.
- No UI.
- *Done when:* rendering Filial's choices produces units equivalent to today's `.container-config/quadlet/*` (diff reviewed with the user).

**Stage 2 — Lifecycle CLI.**
- `up`/`stop`/`down`/`restart`/`status`/`logs`/`exec`, worktree add/remove (scoped), the firewall (ported) and doctor.
- *Done when:* a fresh Rails project runs end to end on one machine.

**Stage 3 — Machine layer.** The shared proxy, per-project networks and subnet pool, hostnames with short names, the machine-wide ID/port registry.
- *Done when:* two projects run at once without collisions.

**Stage 4 — Adopt.**
- Scan, map, the four decisions, and a dry-run diff.
- *Done when:* the Filial workspace is adopted with matching units, no data lost, and existing URLs still working. **This is the v1 acceptance test.**

**Stage 5 — A second, different pack.** Phoenix or Django.
- *Done when:* it runs without adding framework knowledge to the core. Anything that leaks gets moved into packs.

**Stage 6 — UI (Go, `internal/web` + embedded `web/`).**
- The dashboard, ported from Ruby: switcher, palette, worktree links.
- Then Settings and New project, then Library and Install review.
- The UI only calls the CLI.

**Stage 7 — Library and trust.** Library repos, signing and signers, update review.

Keep the old repo running for daily work until Stage 4 passes.

## 9. Known gaps and open questions (not designed yet)

- **Joining a project:** a teammate cloning an existing project. Where `project.toml` and `stack.lock` live: the app repo, a separate repo, or the workspace only.
- **Transactional create/apply and undo:** what happens when step 3 of 5 fails.
- **Per-worktree overrides:** rendered as systemd instance drop-ins (`<unit>@<slug>.service.d/`)?
- **Sharing over the tailnet:**
  - `tailscale serve reset` clears other projects' serves;
  - there is only one :443 per machine;
  - share needs per-project serve handling.
- **Traefik router and service names** need the project prefix to avoid collisions in the shared proxy.
- **Port list:** the machine's port list should come from packs and options (which ports each publishes), not a fixed set.
- **Signers:** trust, rotation and revocation.
- **Upgrading the tool itself,** and what that does to the built-in packs locked to its version.
- **Pack contract details:**
  - the dev server bind address;
  - HMR/websocket ports and their routing;
  - which browser protocol each pack's test driver uses (Playwright server vs CDP);
  - the database name;
  - generating secrets.
- **Storage's two addresses** (`s3.`, `s3-ui.`) in the option model.
- **Tools image layer:** how nvim/claude are layered on a pack's app image, for any stack.
- **Screens and UI pieces not designed yet:**
  - a tasks screen (pack tasks plus project tasks);
  - the firewall in the UI;
  - a preview of generated files;
  - "Add a service" (a second web app, or a custom container);
  - monorepos: a working folder inside the repo.
- **Letter opener:** the Rails pack suggests the link only if the app uses `letter_opener_web`. Plain `letter_opener` opens files on the host, and Filial sets `LAUNCHY_DRY_RUN`, so check which one Filial uses.
- **Mock-up inconsistencies** between boards (example values, labels): fix them as the real screens are built, not in the mock-ups.

## 10. Glossary

| Term | Meaning |
|---|---|
| **Pack** | Knowledge of one framework (`rails@1`) |
| **Option** | One choice for a slot (`postgres@1` for database) |
| **Slot** | A place beside the app: database, cache, storage, jobs, mail, browser |
| **Template** | A pack, its options and overrides, shareable, with questions for the project-specific parts |
| **Prefix** | The project's short unique name; names objects (`<prefix>-<slug>-<svc>`, `<prefix>_proxy`) and hosts |
| **Slug** | A worktree's sanitised name |
| **Worktree ID** | A machine-wide number that sets the worktree's ports |
| **Render** | Turning `project.toml` + `stack.lock` into units (today: `units:install`) |
| **Seed** | Data imported into a volume when it is first created |
