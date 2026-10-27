# The dashboard

RailSwitch's dashboard is a browser page for one project at a time. It
shows what is running, where each branch's pull request stands, and
whether an agent is waiting on you, and it gets you to the app, VNC or
any project shortcut in one click. Interactive work (`console`, `nvim`,
`agent`) stays in the terminal: the page runs only commands that need no
terminal, and opens a terminal for the rest.

The page changes nothing itself. Every action is a `railswitch` command,
and every read is a `railswitch … --json` command, so a script sees
exactly what the page sees (docs/json.md).

## Starting and stopping

- The dashboard is the plain systemd user unit
  `railswitch-dashboard.service` at
  `$XDG_CONFIG_HOME/systemd/user/railswitch-dashboard.service`. Every
  render rewrites it; its `ExecStart` is the `railswitch` that rendered
  (symlinks resolved) with `dashboard serve`.
- It has no `[Install]` section. Every app unit `Wants=` it, so the first
  `up` on the machine starts it. It keeps running after the last worktree
  stops (so its up buttons stay reachable) until you log out or run
  `systemctl --user stop railswitch-dashboard.service`. The next `up`
  starts it again.
- A dashboard that fails to start never fails `up`: `up` prints a warning
  and carries on.
- `railswitch machine` shows its unit, state and address.
- Reinstalling the binary restarts it: `dashboard serve` checks its
  executable's size and mtime every 5 s and, when they change and no job
  is running, exits 0; systemd starts the new binary 2 s later. When the
  unit file itself changes, `up` reloads systemd and restarts an active
  dashboard.
- It listens on `127.0.0.1:7700` only, at
  `http://railswitch.localhost:7700/` (browsers resolve `*.localhost` to
  loopback). It has no Traefik route.

## Logging in

```sh
railswitch dashboard
```

prints `http://railswitch.localhost:7700/login?token=<token>` (and
`start it: systemctl --user start railswitch-dashboard.service` when the
unit isn't active). Open the link once: the dashboard sets the cookie
`railswitch_dashboard` (HttpOnly, SameSite=Strict, one year) and
redirects to `/`, so the token leaves the address bar. Without the cookie
every page answers 401 and tells you to run `railswitch dashboard`; a
wrong token answers 403.

The token is 32 random bytes in hex, in
`~/.local/share/railswitch/dashboard/token` (`$XDG_DATA_HOME`; file 0600,
directory 0700), kept across restarts. To rotate it, which logs out every
browser:

```sh
rm ~/.local/share/railswitch/dashboard/token
systemctl --user restart railswitch-dashboard.service
railswitch dashboard
```

## Security model

- **Loopback only**, and the `Host` header must be exactly
  `railswitch.localhost:7700`, `localhost:7700` or `127.0.0.1:7700`;
  anything else is 403 for every request (DNS rebinding).
- **Cookie on everything** except `/login` and `/healthz`, reads included.
  Another local user can't read the 0600 token file, so they can't read
  the page either.
- **Origin on POST:** a POST needs `Origin: http://<the request's Host>`;
  missing or different is 403 and nothing runs. Your worktree apps at
  `*.localhost` are same-site with the dashboard, so the browser sends
  them the cookie, but their Origin differs, so they cannot press a
  button.
- Every response carries `X-Frame-Options: DENY`, a strict
  `Content-Security-Policy` (no inline script or style, no framing),
  `Referrer-Policy: no-referrer` and `X-Content-Type-Options: nosniff`;
  pages and events are `Cache-Control: no-store`.
- No secret value ever reaches a page, an event, a job log or argv.
- `/healthz` needs only the Host check and answers `ok <version>`.

## Layout

Pages are `/p/<prefix>` and `/p/<prefix>/<slug>`; `/` opens the last
project you looked at.

- **Header:** "RailSwitch"; the project button (the prefix; opens the
  switcher); `◆ N need input`; `tasks :`; `logs` (Dozzle at
  `http://logs.localhost/`); a `notifications` button until the browser
  permission is decided; `⚙` colours. The tab title is `(N) RailSwitch`,
  N counting agents in permission, question or waiting-for-reply across
  all projects.
- **Strip:** one entry, `◆ <project> / <worktree> · <state>`, for every
  other project's worktree that needs input; click it to go there.
- **List:** the sort bar, then one row per worktree: name, branch, PR
  (number, checks, state, review), stack state, agent state;
  `+ New worktree` and `Refresh PRs` below.
- **Detail** (the selected worktree): name, `⎇ branch`, the PR line
  (`PR #2141 ready · checks ✓ · approved`), then Links, Services (a glyph
  and state per service), Agent (state and message), Actions and Jobs.

## Links

The detail's Links section comes first, in this order:

1. **App**, the main button: `http://<worktree host>/`.
2. The project's own `[[links]]` from `project.toml`, in file order. They
   apply to every worktree:

   ```toml
   [[links]]
   label = "Flipper"
   url = "{worktree.host}/flipper"
   ```

3. The stack's links: the pack's, then each option's in slot order, then
   enabled tools', each in file order (VNC, mail previews, routes …).
4. **PR** (when the branch has one) and **Logs** (Dozzle), in a quieter
   style.

Placeholders `{worktree.host}`, `{worktree.slug}` and `{branch}` are
expanded; a URL without a scheme gets `http://`. Links are greyed out
while the stack is stopped. All open in a new tab.

To hide stack links you don't use, list their labels in `project.toml`:

```toml
hide_links = ["Routes", "Mailer previews"]
```

A label no pack, option or tool link of the project carries is an error
at render (`hide_links: no stack link labelled …`), which catches typos.
Run `railswitch render` (or `up`) after editing `project.toml`.

## Sorting

Click a key in the sort bar; clicking the active one reverses it.

| Key | Order (ties by name) |
|---|---|
| name | the base worktree first, then by slug |
| running | failed, up, partial, stopped |
| PR | PR number, highest first; worktrees without a PR last |
| agent | permission, question, reply, your turn, working, idle, off |

The choice is remembered per project in the browser's `localStorage` and
sent as `?sort=`: `name`, `running`, `pr` or `agent`, with a leading `-`
when reversed (`?sort=-pr`). An unknown value is `name`.

## Keys

| Key | Does |
|---|---|
| ↑ / ↓ | move the selection; the URL follows |
| `P` | the project switcher: projects with worktree, running and ◆ counts; typing filters projects and every project's worktrees |
| `1`–`9` | in the switcher (filter empty): jump to that project |
| `:` | the task palette for the selected worktree |
| Esc | close a dialog or a message |
| Enter | in the palette: run the highlighted task as a job |
| Shift+Enter | in the palette: open the task in a new terminal |

## Actions

Every action is a job (below).

- **up, stop, restart** run `railswitch up|stop|restart` in the worktree.
- **New worktree:** branch (required), name (optional) and "up after
  adding" (ticked by default) → `railswitch wt add [--name <n>] [--up]
  <branch>`.
- **remove…** (not offered for the base worktree; a merged PR also shows
  "Remove worktree"): the dialog reads `wt ls --json` for the dirty flag
  and lists what goes: the checkout, its containers and volumes, its nvim
  and agent homes. A clean worktree → `wt rm <slug>`. A dirty one needs
  "Discard changes" ticked and the worktree name typed →
  `wt rm --force <slug>`.
- **Refresh PRs** runs `pr ls` now instead of waiting for the next minute.
- **Task palette** (`:`): `task ls --json` for the selected worktree,
  filtered as you type, with an "args" field split like a shell (quotes and
  backslash, no expansion). Enter runs `railswitch task <name> <args…>` as
  a job with its output in the page; Shift+Enter runs `railswitch task
  --new-term <name> <args…>`, a terminal window for interactive tasks such
  as `console`.
- **Cancel** on a running job stops it.

## Jobs

- A job is a child `railswitch -C <workspace> [-w <slug>] <args>` in a
  `systemd-run --user --scope`, so restarting the dashboard doesn't kill a
  running `up`. It gets the session's display, SSH agent and terminal
  variables from `systemctl --user show-environment`, plus `NO_COLOR=1`.
  Its stdin is `/dev/null`.
- One job at a time per worktree; a project-level job (`wt add`) blocks the
  whole project. A second request answers "busy: <running job>".
- Cancel sends SIGTERM to the job's process group, SIGKILL 3 s later.
- States: running, ok, failed (with the exit code), cancelled.
- The page keeps the last 5000 lines and 4 MiB per job (lines cut at 64 KiB,
  ANSI escapes stripped). The full output is in
  `~/.local/share/railswitch/dashboard/jobs/<id>.log` (0600), truncated past
  8 MiB with "… output dropped".
- The last 20 jobs are kept; older finished ones and their logs are
  dropped. Logs from a previous dashboard run are deleted at start.

## Notifications

- Click `notifications` in the header once to let the browser decide; the
  button disappears after that.
- A notification fires when a worktree's agent moves into needs
  permission, has a question, is waiting for your reply, or your turn, from
  any other state: title `<project> / <worktree> needs permission` (or
  `has a question`, `is waiting for your reply`, `your turn`), body the
  agent's message (200 characters). Clicking it focuses the tab and selects
  the worktree. One notification per transition; an agent that stays
  waiting doesn't repeat.
- The first poll of a project after the dashboard starts is the baseline:
  agents already waiting then don't notify (the strip and the header count
  show them).
- Notifications and the tab title count need an open dashboard tab; there
  are no desktop notifications without one.
- Polling: `overview --json` every 2 s for a project with a page open,
  every 10 s for the others (so their waiting agents reach the strip);
  `pr ls --json` every 60 s for a project with a page open.

## Colours

The page follows the system's light or dark setting. Each mode has a
preset and up to eight changed colours.

- Presets: `flexoki` (the default), `paper`, `solar`, `contrast`.
- Editable tokens: `bg`, `bg2`, `text`, `muted`, `accent`, `blue`,
  `green`, `yellow`. `sel`, `border`, `purple` and `red` are fixed by the
  preset.
- `⚙` opens the editor for the current mode: preset, eight colour fields,
  a live preview, and a warning for any text colour below 4.5:1 against
  the background (saving is still allowed). "Reset to preset" drops the
  mode's changed colours and keeps its preset.
- The editor runs the CLI, which you can use directly:

  ```sh
  railswitch dashboard colors                       # both modes, warnings
  railswitch dashboard colors set --dark --base solar accent=#CB4B16
  railswitch dashboard colors reset --dark          # keeps solar
  ```

- They live in `~/.config/railswitch/dashboard.toml` (format in
  docs/json.md, `dashboard.toml`), so they survive a dashboard restart.
- The page loads them as `GET /theme.css` (CSS custom properties for both
  modes; the content policy forbids inline styles).

State colours: stack up/partial/stopped/failed are green/yellow/muted/red;
PR draft/ready/merged/closed muted/green/purple/red; review
pending/approved/changes requested yellow/blue/red; checks
pass/pending/fail green/yellow/red; agents waiting on you use the accent,
working blue, your turn yellow, idle and off muted.

## Commands

The commands the dashboard reads and runs. Their `--json` output is in
docs/json.md.

- `railswitch overview [--json]` (project): every worktree with its stack
  state (`up`, `partial`, `stopped`, `failed`), services, agent and links.
  No git call and no health check; `status` has health.
- `railswitch pr ls [--json]` (project): each worktree's pull request via
  the host's `gh` (`gh pr list` on the repo from `project.toml`, never a
  worktree's git config): number, title, url, state (`draft`, `open`,
  `merged`, `closed`), checks (`pass`, `pending`, `fail`, `none`), review
  (`approved`, `changes_requested`, `pending`, `none`). For several PRs on
  one branch an open one wins, then the highest number. Fails with gh's
  message when gh is missing or not logged in; the page then shows "PRs
  unavailable".
- `railswitch task ls [--json]` (worktree): the pack's `[tasks]` merged
  with the project's `[tasks]` in `project.toml`; a project task replaces a
  pack task of the same name and `""` removes one:

  ```toml
  [tasks]
  test = "bin/rails test"
  lint = ""
  ```

- `railswitch task [--new-term] <name> [args…]` (worktree): runs the task
  in the app image as `exec` does, as `sh -c '<command> "$@"' <name>
  <args…>`; stdin and the terminal pass through; the exit code is the
  task's. `--new-term` opens it in a new terminal window.
- `railswitch wt add [--name <n>] [--up] <branch>`: `--up` runs `up` in the
  new worktree.
- `railswitch dashboard`: prints the login link.
- `railswitch dashboard colors [--json]`,
  `dashboard colors set [--dark] [--base <preset>] [<token>=#rrggbb…]`,
  `dashboard colors reset [--dark]`: the colours, above.
- `railswitch dashboard serve`: the dashboard itself (the unit's
  `ExecStart`); not for running by hand.

## Events

`GET /events?p=<prefix>&w=<slug>&sort=<key>` (cookie required) is a
Server-Sent Events stream. On connect it sends the current fragments
(`header`, `projects`, `strip`, `banner`, `list`, `detail`, `jobs`);
afterwards only fragments that changed (a single list row by its own id
while the order and selection stay), and `: ping` every 15 s. Each
`data:` line is one JSON object:

```json
{"type":"html","id":"list","html":"<div id=\"list\" class=\"list\">…</div>"}
{"type":"job","id":"jobs","html":"…","job":{"id":"20261027-101500-3","slug":"feat-login","label":"up","state":"running"}}
{"type":"notify","title":"ftest / feat-login needs permission","body":"Bash: rm -rf tmp","url":"/p/ftest/feat-login","tag":"ftest/feat-login"}
```

- `html`: replace the element with that `id` by `html` (server-rendered).
- `job`: the same, for the `jobs` fragment, with the job that changed
  (`state`: `running`, `ok`, `failed`, `cancelled`).
- `notify`: an agent started waiting (Notifications above).
