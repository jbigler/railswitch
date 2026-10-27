# railswitch Stage 6a — the dashboard

Status: design, 2026-10-27. Brainstormed with the user; after the layout,
links, PR, actions, start-up, dark mode, colour editor and task palette
answers, the user asked to continue without further input and review the
result. Everything below marked **(my call)** was decided without them.

## Intent

A browser page that answers, at a glance, for the project in front of you:
what is running, where each branch's pull request stands, and whether an
agent is waiting on you — and gets you to the app, VNC or any shortcut in one
click. The terminal stays the place for interactive work (`console`, `nvim`,
`agent`); the dashboard runs only commands that need no terminal, and opens a
terminal for the rest.

The display name is **RailSwitch**. The binary, paths, units and every
identifier stay `railswitch`.

## Where Stage 6 goes (user, 2026-10-27)

The user wants the whole project configurable from the browser eventually.
Stage 6 is split; each part is its own design and plan:

| Part | Scope |
|---|---|
| **6a Dashboard** (this document) | status, PRs, agents, links, actions, task palette, switcher, notifications, colours |
| 6b Project settings | stack slots, options, services, env, links, routes, secrets (values on stdin) |
| 6c New project | repo, detection, options, review, create |
| 6d Packs and Containerfiles | library, "New pack" form, Containerfile editor with the read-it acknowledgement |
| Stage 7 | library repos and trust; Install review moves here |

## Goals

1. One page per project (`/p/<prefix>`, `/p/<prefix>/<slug>`), layout A of the
   brainstorm: a sortable worktree list on the left, the selected worktree on
   the right.
2. Each worktree row: name, branch, PR (number, checks, state, review), stack
   state, agent state.
3. The selected worktree: name, branch, PR line; **Links first**; services;
   agent message; actions; its jobs.
4. Links: App, then the project's own `[[links]]`, then the stack's (pack,
   options, tools), then PR and Logs in a quieter style. Suggested links can
   be hidden per project.
5. Sorting by name, running, PR or agent.
6. Agents waiting on you in other projects stay visible (strip + header
   count); browser notifications when an agent starts waiting.
7. Actions: up, stop, restart, new worktree (with "up after adding"), remove
   (confirmed; dirty needs a typed confirmation), the task palette, refresh
   PRs.
8. Light and dark themes following the system, Flexoki by default, with a
   colour editor (four presets, eight editable colours per mode).
9. Starts with the first worktree `up` on the machine.

## Non-goals (6a)

- Editing `project.toml`, secrets, packs or Containerfiles (6b–6d).
- New project, adopt, machine board.
- Showing logs in the page (Logs links to Dozzle).
- Desktop notifications without a browser tab (`notify-send`): left out
  unless the open-tab requirement bites (user agreed to the recommendation).
- Anything that runs an interactive program inside the page.

## Anti-goals

- The page changes state itself. It never writes a file or runs podman,
  systemctl or git for a state change: every action is a `railswitch`
  command (HANDOFF §2 "Source of truth"). It reads only through `railswitch`
  commands too, so scripts see exactly what the page sees.
- A secret, token or podman secret value in a page, an SSE event, a job log
  or argv.
- Another local user, another web page open in the same browser, or a DNS
  rebinding page able to read the dashboard or press its buttons.
- A worktree file read trustingly (worktrees are container-writable).

## Decided with the user

- Layout A (list and detail, as the old dashboard), name RailSwitch.
- Links in the selected worktree, first, App prominent; user links are
  `project.toml [[links]]` and apply to every worktree
  (`{worktree.host}/flipper`, `/jobs`, `/lookbook`, `/letter_opener`).
- Sorting, at least by running and PR number.
- PR at a glance: checks passing, draft / ready / merged, approved / pending.
  Colours: draft gray, ready green, merged purple; review pending yellow,
  approved blue.
- Worktree add and remove from the dashboard; up/stop/restart.
- Browser notifications plus the tab title count (recommended, accepted).
- The dashboard starts when the first worktree comes up.
- Dark mode follows the system, Flexoki dark.
- The colour editor stays.
- The task palette stays.

## Design

### Process and address

- **Unit:** `railswitch-dashboard.service`, a plain systemd user unit (not
  Quadlet: it runs on the host, as the user) at
  `$XDG_CONFIG_HOME/systemd/user/railswitch-dashboard.service`:

  ```ini
  # railswitch: machine unit, rewritten by railswitch
  [Unit]
  Description=RailSwitch dashboard

  [Service]
  ExecStart=<absolute path of the railswitch that rendered> dashboard serve
  Restart=always
  RestartSec=2
  ```

  No `[Install]`: like Traefik and Dozzle it is started by `Wants=` from every
  app unit, so the first `up` starts it and it runs until logout or
  `systemctl --user stop railswitch-dashboard.service` **(my call: it keeps
  running after the last worktree stops, so its start buttons stay
  reachable)**.
- **Written** wherever the machine units are refreshed (every render). The
  `ExecStart` path is `os.Executable()` with symlinks resolved. When the file
  changes, `up` reloads systemd and restarts the dashboard if it is active
  (the same rule as a stale Traefik).
- **Binary updates:** `dashboard serve` checks its executable's size and
  mtime every 5 s; when they change and no job is running, it exits 0 and
  systemd starts the new binary **(my call)**.
- **Listens** on `127.0.0.1:7700` only. The address is
  `http://railswitch.localhost:7700/` (browsers resolve `*.localhost` to
  loopback); no Traefik route.
- **Not hardened like containers:** `NoNewPrivileges=` would break rootless
  podman's setuid `newuidmap` in the commands it runs, so the unit sets none.

### Access (guard)

- **Host:** exactly `railswitch.localhost:7700`, `localhost:7700` or
  `127.0.0.1:7700`; anything else is 403 for every method (DNS rebinding).
- **Token:** 32 random bytes, hex, in
  `$XDG_DATA_HOME/railswitch/dashboard/token` (0600, directory 0700), created
  by `dashboard serve` when missing and kept across restarts.
  `railswitch dashboard` prints `http://railswitch.localhost:7700/login?token=<token>`.
- **Login:** `GET /login?token=` compares in constant time, sets cookie
  `railswitch_dashboard=<token>; Path=/; HttpOnly; SameSite=Strict;
  Max-Age=31536000` and redirects (303) to `/`, so the token leaves the
  address bar. A wrong token is 403 with a page saying to run
  `railswitch dashboard`.
- **Every other request** (GET included, `/events` included) needs the
  cookie; without it: 401 with the same page. Other local users can't read
  the 0600 token, so they can't read the dashboard **(my call: stricter than
  the old dashboard, whose GETs were open to any local process)**.
- **Unsafe methods** (POST) also need `Origin` equal to `http://` + the
  request's allowed Host; missing or different is 403. No command runs.
- **Every response:** `X-Frame-Options: DENY`,
  `Content-Security-Policy: default-src 'self'; frame-ancestors 'none';
  base-uri 'none'; form-action 'self'`, `Referrer-Policy: same-origin`
  (ruling, final review: `no-referrer` makes a form POST's Origin `null`,
  which the Origin check refuses), `X-Content-Type-Options: nosniff`,
  `Cache-Control: no-store` on pages and
  events.
- `/healthz` (Host check only): `ok <version>`.

### Reading: new CLI read commands

The dashboard learns everything from `railswitch … --json`. The read
commands below are new and are documented in `docs/json.md`.

**`railswitch overview [--json]`** — one project (`-C`), every worktree:

```json
{
  "prefix": "ftest",
  "workspace": "/home/u/workspace/filial-test",
  "worktrees": [
    {
      "slug": "feat-login", "id": 2, "branch": "jb/feat-login", "base": false,
      "stack": "up",
      "services": [{"service": "web", "role": "app", "state": "active/running"}],
      "agent": {"name": "claude", "state": "permission", "message": "Bash: rm -rf tmp", "ts": 1793115142, "reply": false, "running": true},
      "links": [
        {"label": "App", "url": "http://feat-login.ftest.localhost/", "group": "app"},
        {"label": "Flipper", "url": "http://feat-login.ftest.localhost/flipper", "group": "project"},
        {"label": "Browser (VNC)", "url": "http://vnc.feat-login.ftest.localhost", "group": "stack"}
      ]
    }
  ]
}
```

- `stack`: over the long-running services that make the stack (roles `app`,
  `worker`, `data`; not `deps`, `tool`, `agent`): `failed` if any unit is
  failed, `up` if all are active, `stopped` if none is active or activating,
  otherwise `partial`.
- `services`: service, role and state as in `status --json`, without
  `health` (one `podman` health call per container on a 2 s poll costs
  too much), from one batched `systemctl --user show` for the whole project
  **(my call)**.
- `agent`: the worktree's running agent (D62), else the first configured agent
  whose status is not `off`, else `null`; fields as `agent ls --json`.
- `links`, in order: `app` (App, `http://{worktree.host}/`); `project` (the
  project's `[[links]]`, in file order); `stack` (pack, then options in slot
  order, then enabled tools, each in file order), minus labels in
  `hide_links`. Placeholders expanded; a URL without a scheme gets `http://`.
- No git call: `overview` runs on the dashboard's 2 s poll, and git in a
  container-writable worktree is a cost and a risk the page doesn't need.

**`railswitch pr ls [--json]`** — one project, via the host's `gh`:

- Runs `gh pr list --repo <owner/name> --state all --limit 200 --json
  number,title,url,headRefName,isCrossRepository,isDraft,state,reviewDecision,statusCheckRollup`
  with `GH_PROMPT_DISABLED=1`, in the workspace root, 30 s timeout. The repo
  comes from `project.toml`'s repo URL (never a worktree's `.git/config`,
  which containers can write). A non-GitHub repo: error "pr ls: <url> is not
  a GitHub repo".
- Output: one row per worktree:
  `{"slug","branch","pr":{"number","title","url","state","checks","review"}|null}`.
  - `state`: `draft` (open and draft), `open`, `merged`, `closed`.
  - `checks`: `fail` if any check concluded failure, error, timed out,
    cancelled, action required or startup failure; else `pending` if any is
    not completed (or a status context is pending/expected); else `pass`;
    `none` with no checks.
  - `review`: `approved`, `changes_requested`, `pending` (review required),
    `none`.
  - Several PRs for one branch: an open one wins, then the highest number.
    PRs from forks (`isCrossRepository`) are ignored (ruling, final review:
    their head branch is the fork's, not ours).
- `gh` missing or not logged in: exit 1 with gh's message; the dashboard
  shows "PRs unavailable: <message>" once and keeps going.

**`railswitch task ls [--json]`** and **`railswitch task <name> [args…]`**
(worktree, `-w`):

- Tasks are the pack's `[tasks]` plus the project's new `[tasks]` table
  (`name = "command"`); a project task replaces a pack task of the same name;
  `""` removes one. `task ls --json`: `[{"name","command","source"}]`
  (`pack`/`project`).
- `task <name> [args…]` runs the command in the app image exactly as
  `exec` does (in the running app container, else a transient one), as
  `sh -c '<command> "$@"' <name> <args…>`; stdin and TTY pass through; the
  exit code is the task's. `--new-term` opens it in a new terminal like
  `agent --new-term`.

**`railswitch wt add --up`**: add the worktree, then `up` in it.

**`railswitch machine`** gains the dashboard: text line and
`machine.dashboard {unit, state, url}` in `--json`.

### Links config

- `project.toml` top-level `hide_links = ["Routes", "Mailer previews"]`:
  labels of stack links not to show. A label that no pack, option or tool
  link of the project carries is an error at render ("hide_links: no stack
  link labelled …") **(my call: catches typos)**.
- Pack, option and tool `[[links]]` URLs get the same placeholder check as
  project links (`{worktree.host}`, `{worktree.slug}`, `{branch}`), at load.

### Jobs

Every action is a job: a child `railswitch -C <workspace> [-w <slug>] <args>`.

- Run through `sys.Runner`, wrapped in
  `systemd-run --user --scope --collect --quiet --` so a dashboard restart
  doesn't kill a running `up`. stdin `/dev/null`; stdout and stderr to
  `$XDG_DATA_HOME/railswitch/dashboard/jobs/<id>.log` (0600, `O_NOFOLLOW`);
  logs from a previous run are deleted at start.
- Env: the dashboard's environment, plus the session variables from
  `systemctl --user show-environment` read at each start (`DISPLAY`,
  `WAYLAND_DISPLAY`, `XDG_CURRENT_DESKTOP`, `XDG_RUNTIME_DIR`,
  `SSH_AUTH_SOCK`, and the terminal variables `internal/term` reads), plus
  `NO_COLOR=1`.
- No deadline from the dashboard (`sys.NoTimeout`); the commands keep their
  own. Cancel: SIGTERM to the job's process group, SIGKILL 3 s later.
- One job at a time per worktree; a project-level job (`wt add`) blocks the
  project. A second request is 409 "busy: <running job>".
- Output kept for the page: the last 5000 lines and 4 MiB per job, lines cut
  at 64 KiB, ANSI escapes stripped, invalid UTF-8 replaced; a log over 8 MiB
  is truncated with "… output dropped". The last 20 jobs are kept (finished
  ones dropped first, their logs deleted).
- Job states: running, ok, failed (exit code), cancelled.

### Live updates

- `GET /events?p=<prefix>`: Server-Sent Events. On connect, the current
  header, strip, list and detail; afterwards only changes; `: ping` every
  15 s.
- A poller per project: `overview --json` every 2 s while a page for that
  project is open, every 10 s otherwise (so other projects' waiting agents
  show up) **(my call)**; `pr ls --json` every 60 s while a page for the
  project is open, and on "Refresh PRs".
- Events: `{"type":"html","id":"…","html":"…"}` (server-rendered fragment
  replaces the element with that id), `{"type":"notify","title","body",
  "url"}`, `{"type":"job",…}`. The HTML is rendered by Go `html/template`;
  the JS only swaps fragments, so sorting and states are tested in Go.
- Notification: when a worktree's agent moves into permission, question,
  waiting for reply (done + reply) or your turn (done) from any other state.
  Title "<project> / <worktree> needs permission" (or "has a question",
  "is waiting for your reply", "your turn"), body the status message (200
  characters), click focuses the tab and selects the worktree. Once per
  transition.
- Tab title: `(N) RailSwitch` where N counts permission, question and
  waiting-for-reply agents across all projects; `RailSwitch` when 0.

### Page

- **Header:** "RailSwitch"; the project button (name = prefix; switcher on
  `P`); `◆ N need input`; spacer; `tasks :`; `logs` (Dozzle,
  `http://logs.localhost/`); a "notifications" button until the browser
  permission is decided; `⚙` colours.
- **Strip:** "◆ <project> / <worktree> · <state>" for every other project's
  worktree that needs input; click switches there.
- **List:** sort bar (name, running, PR, agent; clicking the active one
  reverses it; kept per project in `localStorage`, sent as `?sort=`);
  columns worktree, branch, PR, stack, agent; "+ New worktree".
  - name: base first, then by slug.
  - running: failed, up, partial, stopped; then name.
  - PR: by number, highest first; worktrees without a PR last.
  - agent: permission, question, reply, your turn, working, idle, off; then
    name.
  - ↑/↓ move the selection; the URL follows (`/p/<prefix>/<slug>`).
- **Detail:** name; `⎇ branch`; PR line ("PR #2141 ready · checks ✓ ·
  approved"). **Links** (App as the main button; project links; stack links;
  then PR and Logs, quieter; greyed while the stack is stopped; all
  `target=_blank rel=noopener`). **Services** (glyph and state per service).
  **Agent** (state and message). **Actions:** up, stop, restart, and apart
  from them `remove…` (none for the base worktree); a merged PR also shows
  "Remove worktree". **Jobs:** this worktree's jobs, newest first, with live
  output and Cancel.
- **New worktree dialog:** branch (required), name (optional), "up after
  adding" (ticked) → `wt add [--name n] [--up] <branch>`.
- **Remove dialog:** loads `wt ls --json` for the dirty flag; lists what
  goes (checkout, containers, volumes, nvim and agent homes); clean →
  "Remove" → `wt rm <slug>`; dirty → explains, needs "Discard changes" ticked
  and the worktree name typed → `wt rm --force <slug>`.
- **Task palette** (`:`): `task ls --json` for the selected worktree;
  filter as you type; an "args" field; Enter runs it as a job
  (`task <name> <args…>`, args split like a shell: quotes and backslash, no
  expansion); Shift+Enter opens it in a terminal (`task --new-term …`) for
  interactive ones like `console`.
- **Switcher** (`P`): projects with worktree count, running count, ◆ count;
  `1`–`9` jump; typing filters projects and worktrees of every project.
  The last project is remembered in `localStorage`; `/` opens it.
- **Colours** (`⚙`): presets, eight colour fields for the current mode,
  live preview, contrast warning (< 4.5:1 against the background, save still
  allowed), "Reset to preset".

### State colours

| Thing | Token |
|---|---|
| stack up / partial / stopped / failed | green / yellow / muted / red |
| agent permission, question, reply | accent, bold |
| agent working | blue |
| agent your turn | yellow |
| agent idle, off | muted |
| PR draft / open (ready) / merged / closed | muted / green / purple / red |
| review pending / approved / changes requested | yellow / blue / red |
| checks pass / pending / fail | green / yellow / red |

### Colours

- `~/.config/railswitch/dashboard.toml`, `schema = 1` (D39):

  ```toml
  schema = 1
  [colors.light]
  base = "flexoki"
  accent = "#BC5215"   # only tokens changed from the preset
  [colors.dark]
  base = "flexoki"
  ```

- Editable tokens: `bg`, `bg2`, `text`, `muted`, `accent`, `blue`, `green`,
  `yellow`. Fixed per preset: `sel`, `border`, `purple`, `red`.
- CLI: `railswitch dashboard colors [--json]`;
  `railswitch dashboard colors set [--dark] [--base <preset>] [<token>=#rrggbb…]`;
  `railswitch dashboard colors reset [--dark]`. The page's editor runs these.
- Presets (light values from the old boards; dark values **my call**,
  from each scheme's own dark palette):

| token | flexoki L | flexoki D | paper L | paper D | solar L | solar D | contrast L | contrast D |
|---|---|---|---|---|---|---|---|---|
| bg | #FFFCF0 | #100F0F | #FFFFFF | #18181B | #FDF6E3 | #002B36 | #FFFFFF | #000000 |
| bg2 | #F2F0E5 | #1C1B1A | #F4F4F5 | #27272A | #EEE8D5 | #073642 | #F0F0F0 | #121212 |
| sel | #E6E4D9 | #282726 | #E4E4E7 | #3F3F46 | #E4DCC4 | #0B4A5A | #DADADA | #2A2A2A |
| border | #CECDC3 | #403E3C | #D4D4D8 | #52525B | #D3CBB2 | #2E5A66 | #7A7A7A | #8A8A8A |
| text | #100F0F | #CECDC3 | #18181B | #F4F4F5 | #073642 | #EEE8D5 | #000000 | #FFFFFF |
| muted | #6F6E69 | #878580 | #62626B | #A1A1AA | #586E75 | #93A1A1 | #3D3D3D | #D0D0D0 |
| accent | #BC5215 | #DA702C | #7C3AED | #A78BFA | #B03F0F | #CB4B16 | #B3261E | #FF8A65 |
| blue | #205EA6 | #4385BE | #1D4ED8 | #60A5FA | #1B6699 | #268BD2 | #0040A8 | #8AB4FF |
| green | #536907 | #879A39 | #15803D | #4ADE80 | #5C6B00 | #859900 | #1E5E00 | #7EE787 |
| yellow | #8E6B01 | #D0A215 | #A16207 | #FACC15 | #7A5C00 | #B58900 | #6B4E00 | #FFD54F |
| purple | #5E409D | #8B7EC8 | #7E22CE | #C084FC | #6C71C4 | #6C71C4 | #6A1B9A | #D2A8FF |
| red | #AF3029 | #D14D41 | #B91C1C | #F87171 | #DC322F | #DC322F | #C62828 | #FF7B72 |

  Flexoki light keeps the old boards' green `#536907` and yellow `#8E6B01`
  rather than Flexoki's `#66800B`/`#AD8301` (shown in the brainstorm
  mockup), because those fall under 4.5:1 on `#FFFCF0` **(my call)**.

### Front end

- `html/template` pages and fragments, one CSS file, one vanilla JS file
  (SSE, fragment swap, keys, dialogs, notifications, palette filter, colour
  preview). Embedded with `go:embed` from `internal/web/static/`; no build
  step, no third-party JS **(my call: the old Turbo/Stimulus pair is
  replaced by ~300 lines that this page needs)**.
- Fonts: Nunito and JetBrains Mono if installed, else `system-ui` and
  `ui-monospace` (nothing downloaded).
- CSS custom properties from the chosen preset and overrides; light/dark by
  `prefers-color-scheme`.

## Testing

Unit tests (no podman, systemd, gh, network; `sh` allowed for jobs):

- Guard: wrong Host → 403 and no command; no cookie → 401; POST without or
  with a foreign Origin → 403 and no command runs; good cookie and Origin →
  the job's argv; login sets the cookie and redirects; token compare.
- Jobs: argv wrapped in `systemd-run … --`; log file mode 0600; line,
  byte and line-length caps; ANSI stripped; busy → 409; retention 20;
  cancel → TERM then KILL to the group.
- `overview`: stack derivation (each case), agent choice, link order,
  `hide_links`, scheme added, placeholders.
- `pr ls`: gh JSON fixtures → each state, checks and review value; branch
  matching preference; non-GitHub repo; gh failure.
- `task ls`/`task`: merge and removal; argv `sh -c '<cmd> "$@"' <name> args`.
- Sorting: each key with ties and reversal.
- Notifications: transitions fire once; staying waiting doesn't repeat.
- Colours: set/reset round trip, unknown token or bad hex refused,
  `schema` check.
- Shell-style args splitting.
- Pages: Links section comes before Services; no remove for base; merged PR
  shows the remove shortcut.

Host acceptance (`docs/stage6a-acceptance.md`): the dashboard starts with the
first `up`, login link, layout in light and dark, sorting, links open, PRs
match GitHub, notification on a permission prompt, up/stop/restart, new and
removed worktree, a task streamed and `console` opened in a terminal, colour
edits survive a restart, a binary reinstall restarts the dashboard.

## Decisions to record (roadmap)

D72 Stage 6 split; D73 dashboard unit and address; D74 access (token file,
cookie, Origin); D75 read commands `overview`, `pr ls`, `task ls`; D76 jobs;
D77 live updates and notifications; D78 colours file and presets; D79
`[tasks]` in `project.toml` and `railswitch task`; D80 `hide_links` and link
placeholder checks.
