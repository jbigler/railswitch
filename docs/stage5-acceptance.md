# Stage 5 acceptance

The run procedure for Stage 5: a Phoenix app runs from a pack the user wrote
(`local:phoenix@1`), with no Phoenix knowledge in railswitch; local packs are
locked by hash; the rails pack installs JavaScript with bun. Run it on a real
host, not in a sandbox. The reference host is CachyOS/Arch.

## Prerequisites

- Everything in docs/stage4-acceptance.md "Prerequisites": podman ≥ 5,
  netavark ≥ 1.7.0, a systemd user session, `railswitch doctor` all OK.
- Build and install from the `stage-5` branch:
  `cd ~/workspace/railswitch && git switch stage-5 && git pull && make build && install -m755 railswitch ~/.local/bin/`.
- No project with prefix `s5p`, `hello` or `bun` registered (`railswitch project ls`).
- Upgrading: nothing to do for existing projects. New behaviour reaches an
  existing project only through files it ejected at `project new`:
  `railswitch containerfile eject nvim --force` brings nvim's own Node (D71),
  `railswitch containerfile eject base --force` the rails base with bun
  (D69). Both overwrite your edits to those files.

## 1. A Phoenix app, without Elixir on the host

```sh
mkdir -p ~/tmp/rs-phx
podman run --rm -it --init --name rs-phx-new -v "$HOME/tmp/rs-phx":/w:Z -w /w docker.io/library/elixir:1.19-otp-28 \
  sh -c 'mix local.hex --force && mix archive.install --force hex phx_new && mix phx.new hello --inside-docker-env --no-install'
cd ~/tmp/rs-phx/hello
printf 'elixir 1.19.6\nerlang 28\n' > .tool-versions
sed -i -e '/^config :hello, Hello.Repo,/,/pool_size/{/username: "postgres",/d;/password: "postgres",/d;/hostname: "localhost",/d;s/database: "hello_dev"/database: System.get_env("PGDATABASE", "hello_dev")/}' config/dev.exs
grep -n -A6 '^config :hello, Hello.Repo' config/dev.exs
git init -q && git add -A && git -c user.email=t@t -c user.name=t commit -qm init
```

Expected: the `grep` shows the Repo config with
`database: System.get_env("PGDATABASE", "hello_dev")` and no `username`,
`password` or `hostname`; `config/dev.exs` has `http: [ip: {0, 0, 0, 0}`
(`--inside-docker-env`). These are the app-side changes the pack's
`README.md` lists.

## 2. The end-to-end test (`TestE2EPhoenix`)

```sh
cd ~/workspace/railswitch
RAILSWITCH_E2E_PHX_REPO=$HOME/tmp/rs-phx/hello RAILSWITCH_E2E_PREFIX=s5p \
  go test -tags integration ./internal/lifecycle/ -run E2EPhoenix -timeout 60m -v
```

It copies `testdata/fixtures/phoenix/packs/phoenix` into a temp workspace,
runs `project new --pack local:phoenix@1 --option database=postgres@1
--agent none`, `up` (the first start compiles: allow several minutes), GETs
`http://<branch>.s5p.localhost/` (200), adds worktree `feat-x`, brings it up
(200) and checks that each worktree runs its own database container, then
edits the pack's `README.md` and expects `up` to refuse with
`stack.lock is out of date for local:phoenix@1` until `railswitch lock`.
It removes the project at the end. Expected: `PASS`.

## 3. By hand: a Phoenix project to keep

```sh
mkdir -p ~/workspace/hello/packs
cp -r ~/workspace/railswitch/testdata/fixtures/phoenix/packs/phoenix ~/workspace/hello/packs/
railswitch project new ~/workspace/hello --repo ~/tmp/rs-phx/hello --prefix hello --pack local:phoenix@1 --tool nvim
cd ~/workspace/hello/master && railswitch up
railswitch pack show local:phoenix@1
```

(`master` is the branch `git init` created; use `main` if that is yours.)

- **The app:** `http://master.hello.localhost/` shows the Phoenix welcome page.
- **Live reload:** edit `lib/hello_web/controllers/page_html/home.html.heex`
  (change a heading); the open page reloads by itself.
- **nvim's Node (D71):** `railswitch nvim`, then in nvim
  `:!node --version && npm --version` prints both (nvim's own Node, the
  Phoenix image has none). On a rails project (e.g. `ftest`, after
  `railswitch containerfile eject nvim --force` and `railswitch up`),
  `:!node --version` prints the app's Node version (`.nvmrc`), not 24.
- **An agent:** `project new` added the default agent (`claude@1`):
  `railswitch secret set claude_token` (the token on stdin), then
  `railswitch agent`. The agent base builds on the Phoenix image (Debian).
- **Firewall:** `railswitch fw show` lists the running containers:
  `hello-master-web` with `profiles=none mode=log`, `hello-master-nvim` with
  `profiles=agent,deps` (deps ran and exited, so it is not listed).

## 4. A Rails app on bun (D69)

```sh
mkdir -p ~/tmp/rs-bun
podman run --rm -it --init --name rs-bun-new -v "$HOME/tmp/rs-bun":/w:Z -w /w docker.io/library/ruby:3.4 \
  sh -c 'apt-get update -qq && apt-get install -y -qq unzip >/dev/null && curl -fsSL https://bun.sh/install | bash && export PATH=$HOME/.bun/bin:$PATH && gem install rails && rails new demo -d postgresql -j bun && cd demo && bun --version > .bun-version && git add -A && git -c user.email=t@t -c user.name=t commit -qm init'
railswitch project new ~/workspace/rs-bun --repo ~/tmp/rs-bun/demo --prefix bun --agent none
cd ~/workspace/rs-bun/main && railswitch up
journalctl --user -u bun-main-deps.service -o cat --no-pager | grep -i 'bun install'
```

Expected: `up` reports `http://main.bun.localhost/ ready` (use `master` if
`rails new` created that branch); the deps journal shows the bun install;
the page answers 200. Without `.bun-version` (and no `packageManager`), the
deps service fails with `deps: bun.lock needs bun, which is not in the image`
and the fix.

## Clean up

```sh
railswitch project rm hello --yes --purge
railswitch project rm bun --yes --purge
```

## Done checklist (roadmap, Stage 5)

- [x] a user-written `packs/phoenix/` (a test fixture, not shipped) runs a fresh `mix phx.new` app (`TestE2EPhoenix`, 2026-10-27);
- [x] it uses `mix phx.server` on :4000, `mix deps.get --check-locked` in deps, elixir/erlang from `.tool-versions`, hex registries in `deps.allow`, and a health command the pack declares;
- [x] no change to `internal/render` or `internal/config` for Phoenix (`TestGenericity` refuses `phoenix`, `elixir`, `erlang`, `mix ` in core; the Phoenix fixture task changed no core code);
- [x] live reload, nvim (with its own Node) and an agent work on the Phoenix project (2026-10-27);
- [x] a `rails new -j bun` app installs with bun (2026-10-27).

## Run log

**2026-10-27, `TestE2EPhoenix` run 1: FAIL** at the second worktree. The
app-side edits held (section 1). `master`: deps 44 s (tailwind downloaded
in 3 s), `up` ready, 200. `feat-x`: deps compiled in 35 s, then the tailwind
download stalled until systemd killed deps at its 15 min limit (1 min 47 s
CPU in 15 min).

On the user's host the network was slow at the time, so the stall was a
slow transfer with no timeout rather than a dead connection.

**2026-10-27, `TestE2EPhoenix` run 2: PASS** (175.36 s), after `8b62dc7`.

```
=== RUN   TestE2EPhoenix
    e2e_phoenix_integration_test.go:49: workspace /tmp/rs-e2e5-3906379711/rs-s5p
--- PASS: TestE2EPhoenix (175.36s)
PASS
ok     github.com/jbigler/railswitch/internal/lifecycle   175.361s
```

**2026-10-27, Phoenix project `hello` by hand: works.** `pack show
local:phoenix@1` prints the summary and README; the app answers, a template
edit live-reloads the page, `railswitch agent` runs, nvim attaches and
`:!node --version && npm --version` prints `v24.22.1` / `11.19.1` (nvim's
own Node; the Phoenix image has none). `fw show`: web and db
`profiles=none mode=log`, nvim `profiles=agent,deps mode=log`, nothing
rejected. The host's own mise warns `missing: elixir@1.19.6 erlang@28` in
the worktree (it reads `.tool-versions`); that is the host, not railswitch.

**2026-10-27, nvim on a rails project (`ftest`) keeps the app's Node: PASS.**
After `containerfile eject nvim --force` and `up`, `:!node --version` in
nvim prints `v24.20.0`, Filial's `.nvmrc`, not nvim's own Node (`v24.22.1`
from `node:24-slim` on the Phoenix project).

**2026-10-27, Rails app on bun (`bun` project): PASS.** `rails new demo -d
postgresql -j bun` with `.bun-version`, `project new --prefix bun`, `up`:
the deps journal shows `bun install v1.4.4 (38afee6c8)` (the bun the base
image installed from `BUN_VERSION`), and the page answers in the browser.
The bootstrap `podman run` ignored Ctrl-C (no `-it`, `sh` as PID 1); the
commands here now pass `-it --init --name` (`883479b`).

Stage 5 acceptance: complete.

## Gap log

| Gap | Generic feature | Commit |
|-----|-----------------|--------|
| A stalled binary download (Erlang's `:httpc` sets no timeout) hung the deps service until its 15 min limit; every worktree downloaded tailwind (~110 MB) again | None needed in core: the Phoenix pack bounds `mix assets.setup` (`timeout 300`, 3 tries) and keeps tailwind's version-named binaries (tailwind hex 0.5+, `tailwind-<target>-<version>`) in a shared cache volume (`phoenix_assets`); older releases' unversioned binary is not cached, since it would go stale on an update | `8b62dc7` |
