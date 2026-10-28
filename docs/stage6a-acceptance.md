# Stage 6a acceptance

The run procedure for Stage 6a: the dashboard (docs/dashboard.md) on a
real host, against the `ftest` project (workspace `~/workspace/filial-test`,
prefix `ftest`). The unit tests cover the guard, jobs, sorting,
notifications and pages without a browser; these checks cover what only a
desktop session, a browser and GitHub can show. The reference host is
CachyOS/Arch.

## Prerequisites

- Everything in docs/stage4-acceptance.md "Prerequisites": podman ≥ 5,
  netavark ≥ 1.7.0, a systemd user session, `railswitch doctor` all OK.
- `gh` installed and logged in (`gh auth status`), and the `ftest` repo
  with at least one draft PR, one ready PR, one merged PR, and one PR
  each with a review pending and approved, on branches that have
  worktrees.
- Build and install from the `stage-6a` branch:
  `cd ~/workspace/railswitch && git switch stage-6a && git pull && make build && install -m755 railswitch ~/.local/bin/`.
- Upgrading: nothing to do. The next render writes the dashboard unit and
  adds `Wants=railswitch-dashboard.service` to every app unit.

## 1. The dashboard starts with the first `up`

```sh
systemctl --user stop 'ftest-*' railswitch-dashboard.service
cd ~/workspace/filial-test/master && railswitch up
systemctl --user is-active railswitch-dashboard.service
railswitch machine | grep dashboard
cat ~/.config/systemd/user/railswitch-dashboard.service
```

Expected: `up` succeeds; `active`; `railswitch-dashboard.service
active/running http://railswitch.localhost:7700/`; the unit's `ExecStart`
is `/home/<you>/.local/bin/railswitch dashboard serve` and it has no
`[Install]`. `systemctl --user cat ftest-master-rails.service | grep Wants`
lists `railswitch-dashboard.service` (Quadlet accepted the `Wants=`).

## 2. Login

```sh
railswitch dashboard
ls -l ~/.local/share/railswitch/dashboard/token
curl -s -o /dev/null -w '%{http_code}\n' http://railswitch.localhost:7700/p/ftest
```

Expected: the first line is
`http://railswitch.localhost:7700/login?token=<64 hex>`; the token file is
`-rw-------`; curl without the cookie prints `401`. Opening the link in
the browser lands on `/` (then `/p/ftest`) with the token gone from the
address bar.

## 3. Layout in light and dark

Open `http://railswitch.localhost:7700/p/ftest`, then switch the desktop
theme between light and dark.

Expected: header (RailSwitch, `ftest ▾`, `◆ N need input`, `tasks :`,
`logs`, `⚙`), the list on the left, the selected worktree on the right
with Links before Services. Light is Flexoki light on `#FFFCF0`, dark
Flexoki dark on `#100F0F`; the page follows the theme without a reload.
Nothing overlaps or is unreadable in either mode. Keep the browser's
developer console open through sections 3–11: it shows no errors.

## 4. Sorting and keys

Click `running`, `pr`, `pr` again, then `agent`.

Expected: the orders in docs/dashboard.md "Sorting"; clicking the active
`pr` reverses it (URL `?sort=-pr`), `agent` then sorts forward
(`?sort=agent`). Reload: the sort stays (per project). ↑/↓ move
the selection and the URL follows (`/p/ftest/<slug>`); `P` opens the
switcher, `1` jumps to the first project; Esc closes.

## 5. Links

Add at the top level of `~/workspace/filial-test/project.toml` (above the
first table):

```toml
hide_links = ["Routes"]

[[links]]
label = "Flipper"
url = "{worktree.host}/flipper"

[[links]]
label = "Jobs"
url = "{worktree.host}/jobs"
```

```sh
cd ~/workspace/filial-test && railswitch render
railswitch --json overview | jq '.worktrees[] | select(.slug == "master") | .links'
```

Expected: links App, Flipper, Jobs, then the stack's: the pack's (Mailer
previews), then each option's in slot order (database, cache, storage,
jobs, mail, browser; with rustfs, sidekiq and playwright: Storage console,
Sidekiq, Browser (VNC)), and no Routes. In the page: App, Flipper, Jobs, stack
links, then PR and Logs quieter; each opens its page in a new tab. On a
stopped worktree the links are greyed out. `hide_links = ["Rotues"]` makes
`render` fail with `hide_links: no stack link labelled "Rotues"`.

## 6. PRs match GitHub

```sh
cd ~/workspace/filial-test && railswitch pr ls
gh pr list --state all
```

Expected: every worktree's PR number, state and review match GitHub. In the
page: draft gray, ready green, merged purple; review pending yellow,
approved blue; checks ✓/◌/✗. `Refresh PRs` after changing a PR on GitHub
(mark ready, approve) shows the change within seconds. A merged PR's
worktree shows "Remove worktree".

## 7. A notification on a permission prompt

Click `notifications` and allow. In another worktree run `railswitch agent`
and give the agent something that asks for permission. Switch to another
browser tab.

Expected: one notification "ftest / <worktree> needs permission" with the
prompt as body; clicking it focuses the dashboard and selects the
worktree; the tab title is `(1) RailSwitch`; on another project's page the
strip shows `◆ ftest / <worktree> · needs permission`. Answering the prompt
clears the count; no second notification while it stays waiting.

Then let the agent go back to work (it shows `working`), and give it
something that asks for permission again.

Expected: a second desktop notification for the same worktree appears,
even though the first one (same tag) may still be in the notification
centre: the new one replaces it and alerts again (`renotify`).

## 8. up, stop, restart

On a worktree: `stop`, then `up`, then `restart`.

Expected: each shows a job with live output and ends `ok`; the stack column
goes stopped → up; `systemctl --user is-active ftest-<slug>-rails.service`
agrees. Clicking `up` while a job runs shows "busy: …". Cancel on a running
`up` ends it `cancelled`.

## 9. New and removed worktree

`+ New worktree`: branch `s6a-check`, "up after adding" ticked → Add.
Then create a dirty file:
`echo x > ~/workspace/filial-test/s6a-check/dirty.txt`; `remove…`.

Expected: a project job runs `wt add --up s6a-check`; the row appears and
comes up. The remove dialog says `s6a-check` has uncommitted changes;
Remove refuses until "Discard changes" is ticked and `s6a-check` typed;
then the row disappears and `railswitch wt ls` no longer lists it.

Remove the selected worktree: add `s6a-check` again (without "up after
adding"), select it (the address bar shows `/p/ftest/s6a-check`), then
`remove…` → Remove.

Expected: when its row is gone, the detail shows another worktree and the
address bar changes to that worktree's URL (no longer `/s6a-check`).
Reloading works: no 404. Opening
`http://railswitch.localhost:7700/p/ftest/s6a-check?sort=pr` by hand lands
on `/p/ftest?sort=pr`.

## 10. Tasks

Press `:` on `master`, type `test`, Enter. Then `:`, `console`,
Shift+Enter.

Expected: `test` runs as a job with its output streamed in the page and
ends `ok` or `failed` with the exit code. `console` opens a new terminal
window running the Rails console in the app.

## 11. Colours survive a restart

`⚙`, preset `solar`, change `accent`, Save; then:

```sh
railswitch dashboard colors
systemctl --user restart railswitch-dashboard.service
```

Expected: the CLI shows `solar` and the changed accent; after the restart
and a reload the page still uses them. A low-contrast `text` shows the
contrast warning and still saves. "Reset to preset" keeps `solar` and drops
the change.

## 12. Reinstalling the binary restarts the dashboard

```sh
systemctl --user show -p MainPID railswitch-dashboard.service
cd ~/workspace/railswitch && make build && install -m755 railswitch ~/.local/bin/
sleep 10
systemctl --user show -p MainPID railswitch-dashboard.service
```

Expected: a different PID; the open page reconnects on its own.

## 13. Foreign Host is refused

```sh
curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: evil.example:7700' http://127.0.0.1:7700/healthz
curl -s http://127.0.0.1:7700/healthz
```

Expected: `403`, then `ok <version>`.

## 14. The page says when it is no longer live

With a dashboard page open:

```sh
rm ~/.local/share/railswitch/dashboard/token
systemctl --user restart railswitch-dashboard.service
```

Expected: a red line under the header says "Logged out — run `railswitch
dashboard` for a new link" (the new run made a new token, so the cookie is
refused). `railswitch dashboard` prints the new link; opening it removes
the line.

Then:

```sh
systemctl --user stop railswitch-dashboard.service
sleep 10
systemctl --user start railswitch-dashboard.service
```

Expected: while it is stopped the line says "Disconnected — retrying";
a few seconds after the start it disappears and the page updates again.

## Clean up

Remove the `[[links]]` and `hide_links` you added if you don't want them,
and `railswitch render`.

## Run log

**2026-10-27, sandbox smoke (no browser): PASS.** A throwaway test served
the dashboard with fake CLI data on `127.0.0.1:7700`; curl checked
`/healthz`, login, the page (Links before Services, Refresh PRs, the sort
bar), `/theme.css`, the event stream, a POST with and without `Origin`, and
a foreign Host. No browser exists in the sandbox, so every visual check
above is for the host.

**2026-10-28, host run (Omarchy/Arch VM, podman 6.1.4, headless
Chromium 155 via Playwright): partial.** The `ftest` project is not on this
host. The run used three open-source apps instead: Mastodon (`masto`,
`rails@1` with postgres, redis and sidekiq), Plausible (`plaus`, a local
`phoenix` pack and a local `clickhouse` option) and Metabase (`mb`, a local
`clojure` pack). Mastodon and Plausible run end to end. Metabase stopped at
deps: the VM has 28 GB of disk (full) and 3 GB of RAM. PR rows came from
Plausible worktrees added by PR number (#6809 draft, #6812 ready and
approved, #6808 merged).

- PASS: 1 (unit, `Wants=`, no `[Install]`), 2 (login link, 401 without
  the cookie), 4 (every sort order, `?sort=-pr`, reload, ↑/↓, `P`, `1`,
  Esc), 6 (after the `pr ls` fix below), 12 (new PID after reinstall), 13
  (403, then `ok <version>`). 3 in part: both schemes render, but nobody
  has compared them against the mock-ups.
- Not run: 5, 7–11, 14. They need the resources above or a desktop session.
- Cosmetic: the page has no favicon (Chromium's `/favicon.ico` gets 404).

**2026-10-28, host run, continued (80 GB disk, 8 GB RAM): every section but
7 PASS.** All three apps run end to end: Metabase's backend
(`/api/health` ok, 202 tables in its Postgres) and its frontend (`bun run
build` in `railswitch exec --fresh`, 1 min 20 s; `/` serves the built app).

- 5 PASS on Mastodon (`[[links]]` Admin and PgHero, `hide_links =
  ["Routes"]`): App, Admin, PgHero, Mailer previews, Sidekiq, then Logs
  (`quiet`); every link opens a new tab; on a stopped worktree each but
  Logs is `dim`; `"Rotues"` fails render with the documented message.
- 8 PASS on Metabase: stop, up, restart each end `ok` with live output and
  the stack column follows; a second click while a job runs shows `busy:
  …`; Cancel on a running `up` ends it `cancelled` with no `railswitch up`
  left. A stop first ended the unit `failed`, for two reasons, both fixed
  (gap log): Mastodon's `bin/dev` and the JVM's exit 143.
- 9 PASS on Mastodon (`s6a-check`): the `wt add s6a-check` job brings the
  row up; the dirty remove dialog names the uncommitted changes and a bare
  Remove is refused until "Discard changes" and the typed name; the
  selected row's removal moves the address bar to `/p/masto/main`, a reload
  works, and `/p/masto/s6a-check?sort=pr` lands on `/p/masto/main?sort=pr`
  (the project page selects the base worktree). A worktree added on the CLI
  shows up within the 10 s idle poll.
- 10 PASS: `:` opens the palette; `test` runs as a job and ends `ok`
  (Mastodon is RSpec: 0 runs); Shift+Enter on `console` opens foot running
  `bin/rails console` in the app. `TERMINAL=xdg-terminal-exec` (Omarchy) is
  not a terminal `internal/term` knows, so the fallback list chose foot,
  here the only one installed.
- 11 PASS: `solar` and an `accent` override survive a dashboard restart
  (`/theme.css`); a near-white `text` warns `contrast 1.0:1` and saves;
  Reset keeps `solar` and drops both overrides. Restored to `flexoki`.
- 14 PASS: `Logged out — run railswitch dashboard for a new link` after a
  new token, gone with the new link; `Disconnected — retrying` while the
  dashboard is stopped, gone after its start.
- Refresh PRs: POST `/p/plaus/prs/refresh` answers 202; the rows keep
  their PRs.
- Not run: 7 needs an agent with a real token (claude@1) and a desktop
  notification. 3's visual comparison with the mock-ups is for the user.

## Done checklist (roadmap, Stage 6a)

- [x] one page per project (`/p/<prefix>`, `/p/<prefix>/<slug>`), list and detail;
- [x] each row: name, branch, PR (number, checks, state, review), stack, agent;
- [x] the selected worktree: name, branch, PR line, Links first, services, agent message, actions, jobs;
- [x] links: App, `[[links]]`, stack links, then PR and Logs; `hide_links`;
- [x] sorting by name, running, PR, agent;
- [ ] waiting agents in other projects visible (strip, header count); browser notifications;
- [x] up, stop, restart, new worktree (up after adding), remove (dirty needs the typed name), task palette, Refresh PRs;
- [x] light and dark following the system, Flexoki by default, the colour editor;
- [x] the dashboard starts with the first worktree `up`.

## Gap log

| Gap | Generic feature | Commit |
|-----|-----------------|--------|
| Node 25+ has no corepack: a yarn 4 app (Mastodon, Node 26) had no yarn | rails base image installs corepack from npm | `8f51191` |
| `FROM …:${V%%-otp-*}` pre-pulled `…:-otp-28-slim` | FROM/ARG expansion with buildah's modifiers; an unknown form is an error | `c64c267` |
| `up` on a running stack reran deps twice; under Phoenix, mix touched `compile.lock` and the code reloader refused every request | with the app active, services start with `--job-mode=ignore-requirements` | `3c86317` |
| `pack new` needed a project, `project new --pack local:` needed the pack | `pack new --dir <workspace>` | `4d1590c` |
| `MB_DB_CONNECTION_URI = "{env.DATABASE_URL}"` silently dropped | `{env.X}` holding a generated value expands in env only | `9bbcaf8` |
| Dashboard "PRs unavailable: mise ERROR" (mise-shimmed `gh` refused the workspace's `mise.local.toml`); `pr ls` HTTP 504 on busy repos | `gh` runs in `/`; one `--head=<branch>` query per worktree branch | `593399a` |
| Metabase pins `java = "temurin-25.0.3+9.0.LTS"` (mise); `.java-version` was no source | Java values lose their distribution and `+build` | `ae68b01` |
| `db dump` wrote `{db}.dump` only: a second database of the server was lost on a round trip | `[dump] list`; `postgres@1` dumps every database | `5f64059` |
| ClickHouse beside Postgres had no slot (D3) | any other `[options]` key adds a service of that name (D81) | `9251f61` |
| Moving ClickHouse to `analytics` left the old `storage` container running on its volume; the new one exited 76 | render stops a container whose unit it removes | `3c32b98` |
| Metabase never creates its database (`mb_dev does not exist`); a dump of a database `POSTGRES_DB` made aborted the restore | `postgres@1` sets `POSTGRES_DB = "{db}"`; restore-dump.sh restores into an existing database | `e3a3d3a`, `2a8c9f8` |
| `stop` on Mastodon SIGKILLed everything after 10 s and left the unit `failed` (exit 137): its `bin/dev` runs foreman as a child of PID 1 `sh` | the rails app entrypoint passes a stop to the container's process group and exits 0 | `943bb0f` |

Open: `internal/term` does not know `xdg-terminal-exec`, the session's
`TERMINAL` on Omarchy; with several terminals installed it would not pick
the user's default.

Settings only, in the projects' own files: Mastodon `[env] BIND`,
`LOCAL_DOMAIN`, `STREAMING_API_BASE_URL`, `[services.rails] health =
/health`, a `streaming` route; Plausible `[env] LISTEN_IP`, `BASE_URL`,
`CLICKHOUSE_DATABASE_URL`, `[firewall.allow] deps = download.db-ip.com`
for `mix download_country_database`, and three lines removed from the
worktree's tracked `config/.env.dev` (Envy overwrites the environment
with it); Metabase `MB_*` env, `/api/health`, two Maven hosts. In the
local packs (the user's own, like a user would write): phoenix strips an
asdf `-otp-NN` suffix in `FROM`; clojure declares `success_exit_status =
143` (a JVM's clean stop).
