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
  and carries on. Nor does a unit file render can't write (or railswitch's
  own path it can't find): `warning: railswitch-dashboard.service: <why>`.
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
  `Referrer-Policy: same-origin` (so a form posted without the script
  still carries the page's Origin) and `X-Content-Type-Options: nosniff`;
  pages and events are `Cache-Control: no-store`.
- No secret value ever reaches a page, an event, a job log or argv.
- `/healthz` needs only the Host check and answers `ok <version>`.

## Layout

Pages are `/p/<prefix>` and `/p/<prefix>/<slug>`; `/` opens the last
project you looked at. The URL of a worktree that no longer exists
(removed) redirects to its project's page, keeping `?sort=`; the address
bar follows the worktree the page shows.

When the live stream drops, a red line under the header says so:
"Disconnected — retrying" (the page retries every few seconds), or
"Logged out — run `railswitch dashboard` for a new link" when the cookie
is no longer accepted. What the page shows meanwhile may be stale.

- **Header:** "RailSwitch"; the project button (the prefix; opens the
  switcher); `◆ N need input`; `tasks :`; `settings` (the project's
  Settings page, below); `logs` (Dozzle at
  `http://logs.localhost/`); a `notifications` button until the browser
  permission is decided; `⚙` colours. The tab title is `(N) RailSwitch`,
  N counting agents in permission, question or waiting-for-reply across
  all projects.
- **Strip:** one entry, `◆ <project> / <worktree> · <phrase>` (`needs
  permission`, `has a question`, `is waiting for your reply`), for every
  other project's worktree that needs input; click it to go there.
- **List:** a toolbar with the sort bar, `+ New worktree` and `Refresh
  PRs`, then one row per worktree: name, branch, PR (number, checks,
  state, review), stack state, agent state. A worktree whose running
  containers were created from older units than the rendered ones
  (`overview`'s `stale`, after a settings change, say) shows "restart to
  apply" next to its stack state, in the row and in the detail; `restart`
  clears it.
- **Detail** (the selected worktree): name, `⎇ branch`, the PR line
  (`PR #2141 ready · checks ✓ · approved`), then Links, Services (a glyph
  and state per service), Agent (state and message), Actions and Jobs.

## Settings

`/p/<prefix>/settings` (`settings` in the header) edits the project's
`project.toml`. Changes are staged, not applied: the right-hand panel
shows what each one does before anything is written.

**Left column**

- **Project:** name, repository, base branch and prefix. Read-only; the
  prefix is fixed because it names every container, volume, network and
  hostname.
- **Stack and options:** one row per slot and extra `[options]` key with
  its current package. "Change" opens its choices (the current one tagged
  `current`); picking another stages it and the row shows `→ <new>`.
  Picking the current one again unstages it.
- **Environment:** the project's `[env]`. Editing a value stages it when
  you leave the field; Remove stages its removal; the add form (name,
  value) stages a new one.
- **Worktree links:** every link with Show, Label, Address and From
  (`project`, or the stack part that brings it). Unticking Show on a stack
  link stages adding it to `hide_links`; Remove is offered on the
  project's own `[[links]]` only. The add form needs both a label and an
  address. Use `{worktree.host}`, `{worktree.slug}` or `{branch}`; they
  are filled in per worktree.
- **Secrets:** one row per declared secret: services, delivery (`file` or
  `env <VAR>`), set (with its date) or not set; never the value. "Set
  value" runs `railswitch secret set <name>` as a job; "Add a secret"
  (name, the services that receive it, an optional env variable, value)
  runs `railswitch secret add <name> --service <id>… [--env VAR]`. The
  value goes only to the job's stdin; the field clears as soon as you
  press the button. Secrets act at once; they are not staged.
- **Edit project.toml** opens the raw editor (below), for everything
  else: services, routes, tasks, firewall, agents, image, tools, versions.

**Staged changes**

- "Staged changes · N", one card per edit: what changes (`cache: kv@1 →
  none`, `Env BIND: "0.0.0.0" → "127.0.0.1"`, `Link Admin: added`), its
  label and the reasons, and "Undo this change".

  | Label | Means |
  |---|---|
  | No data affected (green) | nothing is lost and the app's env is the same |
  | Check before applying (yellow) | no data is lost, but an env variable the app gets is gone or changes |
  | Data not carried over (red) | a worktree's volume is no longer mounted (left behind) or starts empty; the reasons say which, and suggest `railswitch db dump` first when a database goes |
  | Blocked (red) | the edit can't be placed or the edited project doesn't load, validate, resolve or render; the error says why |

- "When you apply": project.toml is written; the units are rendered;
  running worktrees keep their containers until restarted.
- "Restart the N affected running worktrees now" (off; shown only when a
  running worktree's units change) restarts them after the write, one at
  a time.
- "Apply N changes" is off while any card is blocked. "See the
  project.toml diff" shows the whole change.
- Apply runs `railswitch settings apply --if-hash <hash> [--restart]` as
  a project-level job, the edit list on its stdin. It writes all the
  edits or none. When it ends ok the list is cleared and the page
  reloads. If project.toml changed after the preview (edited by hand,
  say), the job is refused and nothing is written; the page previews the
  list again and says so.
- The staged list is kept in the browser's `sessionStorage`
  (`rs-staged-<prefix>`), so a reload keeps it; closing the tab drops it.
  Every change posts the whole list to `POST
  /p/<prefix>/settings/preview`, which runs `railswitch --json settings
  preview` (list on stdin) and answers the panel.

**Raw editor** (`/p/<prefix>/settings/file`): the whole `project.toml`
(from `railswitch --json project show`'s `file`) and its hash. Save runs
`railswitch project write --if-hash <hash>` as a project-level job with
the text on stdin: it checks the file loads and validates, writes it,
locks and renders, and puts the old file back if any step fails. If the
file changed since the page read it, nothing is written and the page
offers to reload the file; your text stays in the editor until you do.

Every settings action is a job of the project, so it waits for (and
blocks) the project's other jobs. Its output shows under the panel.

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

To hide stack links you don't use, list their labels at the top level of
`project.toml` (above the first table):

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
| pr | PR number, highest first; worktrees without a PR last |
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
  "Remove worktree"): the dialog reads `railswitch --json wt ls` for the dirty flag
  and lists what goes: the checkout, its containers and volumes, its nvim
  and agent homes. A clean worktree → `wt rm <slug>`. A dirty one needs
  "Discard changes" ticked and the worktree name typed →
  `wt rm --force <slug>`.
- **Refresh PRs** runs `pr ls` now instead of waiting for the next minute.
- **Task palette** (`:`): `railswitch --json task ls` for the selected worktree,
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
  Its stdin is `/dev/null`, except for the settings jobs: their edit
  list, secret value or project.toml text is written to the child's stdin
  and kept nowhere else (not the job's label, log, page or events).
- One job at a time per worktree; a project-level job (`wt add`) blocks the
  whole project. A second request answers "busy: <running job>".
- A worktree's Jobs list shows its own jobs, the project-level ones and
  those of removed worktrees (so a `wt rm`'s output stays visible).
- Cancel sends SIGTERM to the job's process group, SIGKILL 3 s later.
- States: running, ok, failed (with the exit code), cancelled.
- The hub keeps the last 5000 lines and 4 MiB per job (lines cut at 64 KiB,
  ANSI escapes stripped); the page shows the last 500 with "… N earlier
  lines". The full output is in
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
- Polling: `railswitch --json overview` every 2 s for a project with a page open,
  every 10 s for the others (so their waiting agents reach the strip);
  `railswitch --json pr ls` every 60 s for a project with a page open.

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

- `railswitch [--json] overview` (project): every worktree with its stack
  state (`up`, `partial`, `stopped`, `failed`), services, agent and links.
  No git call and no health check; `status` has health.
- `railswitch [--json] pr ls` (project): each worktree's pull request via
  the host's `gh` (`gh pr list --head=<branch>` per worktree branch on the
  repo from `project.toml`, never a worktree's git config, run in `/`: a mise-shimmed `gh` started in a
  workspace would read its `mise.local.toml` as mise config and refuse it
  as untrusted): number, title, url, state (`draft`, `open`,
  `merged`, `closed`), checks (`pass`, `pending`, `fail`, `none`), review
  (`approved`, `changes_requested`, `pending`, `none`). For several PRs on
  one branch an open or draft one wins, then the highest number; a PR from
  a fork never matches (its head branch is the fork's). Fails with gh's
  message when gh is missing or not logged in; the page then shows "PRs
  unavailable".
- `railswitch [--json] task ls` (worktree): the pack's `[tasks]` merged
  with the project's `[tasks]` in `project.toml`; a project task replaces a
  pack task of the same name and `""` removes one; no task may be named
  `ls`:

  ```toml
  [tasks]
  test = "bin/rails test"
  lint = ""
  ```

- `railswitch task [--new-term] <name> [args…]` (worktree): runs the task
  in the app image as `exec` does, as `sh -c '<command> "$@"' <name>
  <args…>`; stdin and the terminal pass through; the exit code is the
  task's. `--new-term` opens it in a new tab or window of the terminal
  railswitch runs in; failing that, a window of `$TERMINAL`, then the
  freedesktop default terminal through `xdg-terminal-exec`, then the first
  known terminal installed.
- `railswitch wt add [--name <n>] [--up] <branch>`: `--up` runs `up` in the
  new worktree.
- `railswitch dashboard`: prints the login link.
- `railswitch [--json] dashboard colors`,
  `dashboard colors set [--dark] [--base <preset>] [<token>=#rrggbb…]`,
  `dashboard colors reset [--dark]`: the colours, above.
- `railswitch dashboard serve`: the dashboard itself (the unit's
  `ExecStart`); not for running by hand.

## Events

`GET /events?p=<prefix>&w=<slug>&sort=<key>` (cookie required) is a
Server-Sent Events stream. On connect it sends the current fragments
(`header`, `projects`, `strip`, `banner`, `list`, `detail`, `jobs`);
afterwards only fragments that changed (a single list row by its own id
while the order and selection stay), and `: ping` every 15 s. A settings
page connects with `&view=settings`: it gets no `list` or `detail`, and
its `jobs` are the project-level ones only. Each `data:` line is one
JSON object:

```json
{"type":"html","id":"list","html":"<div id=\"list\" class=\"list\">…</div>"}
{"type":"job","id":"jobs","html":"…","job":{"id":"20261027-101500-3","slug":"feat-login","label":"up","state":"running"}}
{"type":"notify","title":"ftest / feat-login needs permission","body":"Bash: rm -rf tmp","url":"/p/ftest/feat-login","tag":"ftest/feat-login"}
```

- `html`: replace the element with that `id` by `html` (server-rendered).
- `job`: the same, for the `jobs` fragment, with the job that changed
  (`state`: `running`, `ok`, `failed`, `cancelled`).
- `notify`: an agent started waiting (Notifications above).
