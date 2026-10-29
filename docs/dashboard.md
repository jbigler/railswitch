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
  `$XDG_CONFIG_HOME/systemd/user/railswitch-dashboard.service`. Its
  `ExecStart` is a `railswitch` (symlinks resolved) with `dashboard
  serve`: the one that last ran `railswitch dashboard`. Every render
  rewrites the unit but keeps that path (D129), so another copy on the
  machine (a clone's build) never takes the dashboard over; a render
  writes its own path only when there is no unit or its `railswitch` is
  gone or unsafe to keep (not an absolute path to a regular, executable
  file you own that no one else can write). `railswitch dashboard`
  restarts a running dashboard when it points the unit at itself.
- `railswitch dashboard` (D118) prints the login link, writes the unit and,
  unless it is already enabled and running, runs `systemctl --user enable
  --now railswitch-dashboard.service`: it starts now and at every login
  (`[Install] WantedBy=default.target`), on a machine with no project yet
  and after a reboot. Every app unit also `Wants=` it, so `up` starts it
  too. It keeps running after the last worktree stops (so its up buttons
  stay reachable) until you log out, unless linger is on (`loginctl
  enable-linger`; `doctor` warns when it is off), or you run `systemctl
  --user stop railswitch-dashboard.service`.
- A dashboard that fails to start never fails `up`: `up` prints a warning
  and carries on. Nor does a unit file render can't write (or railswitch's
  own path it can't find): `warning: railswitch-dashboard.service: <why>`.
- `railswitch machine` shows its unit, state and address.
- Reinstalling the binary restarts it: `dashboard serve` checks its
  executable's size and mtime every 5 s and, when they change and no job
  is running, exits 0; systemd starts the new binary 2 s later. When the
  unit file itself changes, `up` reloads systemd and restarts an active
  dashboard.
- It listens on `127.0.0.1:7700` and `[::1]:7700` only (`::1` when IPv6
  is on as it starts; turned on later, the next `railswitch dashboard` or
  `doctor` names anything else holding it), at
  `http://railswitch.localhost:7700/` (browsers resolve `*.localhost` to
  loopback). It has no Traefik route.

## Logging in

```sh
railswitch dashboard
```

writes the dashboard's unit and, unless it is enabled and running,
enables and starts it (D118). Then it asks `http://127.0.0.1:7700/healthz`
as the browser would (Host `railswitch.localhost:7700`) with a fresh
random nonce, and `http://[::1]:7700/healthz` too (browsers try IPv6
loopback first for `*.localhost`; the dashboard listens there as well,
so no other program can, and without IPv6 nothing is there); railswitch's dashboard answers `ok <version> <proof>`, an
HMAC of the nonce keyed by the login token (D127). Only when the proof is
right does it print `http://railswitch.localhost:7700/login?token=<token>`:
the link carries the token, so it never goes to another program on the
port. It waits up to 10 s while nothing answers or a dashboard answers
without the proof (one just started, or an older one restarting onto a
new binary). Another program answering (an older dashboard holding the
port), a dashboard with another token (another user's, an `ssh -L`
forward's), or nothing answering is reported on stderr with what to do,
no link, exit 1. `doctor` makes the same check (`dashboard answers`, a
warning). Open the link once: the dashboard sets the cookie
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

- **Loopback only** (127.0.0.1 and ::1, port 7700), and the `Host`
  header must be exactly
  `railswitch.localhost:7700`, `localhost:7700` or `127.0.0.1:7700`;
  anything else is 403 for every request (DNS rebinding), a text reply
  that quotes the `Host` it got and names the addresses that work.
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
- `/healthz` needs only the Host check and answers `ok <version>`; with
  `?nonce=<32 hex>` it adds the proof, `ok <version> <hmac>` (D127),
  which shows nothing that logs in.

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
  switcher); `◆ N need input`; `tasks :`; `images`, `library` and
  `settings` (the project's Images, Library and Settings pages, below); `logs` (Dozzle at
  `http://logs.localhost/`); a `notifications` button until the browser
  permission is decided; `⚙` colours. The tab title is `(N) RailSwitch`,
  N counting agents in permission, question or waiting-for-reply across
  all projects.
- **Banner** (under the header): a project's read errors ("Status
  unavailable", "PRs unavailable") and, while `overview`'s
  `review_pending` is true, "Package update waiting for review. Review
  update" (the Review update page, below). The switcher marks such a
  project `update`. One line per `overview` `signer_warnings` entry, in
  red: "The signer of <ref> (<library>) is no longer trusted. The project
  keeps running what it has; replace it or re-check the library." While
  `overview`'s `template_update` is true: "The template <ref> changed
  since this project was made from it. See it in Settings" (the
  Settings page's Template changed notice, below; `<ref>` is
  `overview`'s `template.ref`).
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
- **Secrets to set** (above the detail, only while a secret `up` needs
  is unset: `secret ls`'s `up`, read once per page load with 5 s to
  answer; none when it can't be read): "Set these secrets to start
  <base>", one row per unset secret (name, services, delivery, a value
  field and Set value, the Settings page's `secret set` job: the value
  goes only to its stdin and the field clears once the job has started);
  an on-demand agent's comes last, marked "when it runs; not needed to
  start". Then **Start <base>**, off until the last secret `up` needs is
  set. Secret jobs are the project's, one at a time, so every Set value
  waits while one runs. Each secret set leaves the card; once none that
  `up` needs is left, the heading says "Ready to start <base>" and Start
  turns on. Start selects the base worktree, runs `up` there and
  says where its output is; the card goes when it ends ok. `up` refuses an unset
  secret, so a new project with such secrets opens with this card.
- **Detail** (the selected worktree): name, `⎇ branch`, the PR line
  (`PR #2141 ready · checks ✓ · approved`), then Links, Services (one aligned row per service: a
  glyph, the name and a plain state word, `running`, `starting`,
  `stopping`, `reloading`, `busy`, `failed`, `stopped`, `on demand` or
  `unknown`, in the glyph's colour;
  the systemd state is the row's tooltip), Agent (state and message), Actions and Jobs.

## Settings

`/p/<prefix>/settings` (`settings` in the header) edits the project's
`project.toml`. Changes are staged, not applied: the right-hand panel
shows what each one does before anything is written.

**Left column**

- **Template changed** (first, only while `overview`'s `template_update`
  is true): the template's ref and the recorded hash, and that nothing
  is applied (`railswitch template check` in the workspace shows the
  diff). **Accept** posts the template's sha256 as `railswitch --json
  template show <ref>` reads it when the page opens. The server reads
  `overview` and `template show` again and refuses (400, no job) a sha
  that isn't the template's now, or a project with no template change
  waiting; then it runs `railswitch template check --accept <sha>` as a
  project-level job. When the job ends ok the page reloads and the
  notice is gone.
- **Project:** name, repository, base branch and prefix. Read-only; the
  prefix is fixed because it names every container, volume, network and
  hostname.
- **Stack and options:** first the pack (D115), when `project show`'s
  `pack.choices` offers another (a local copy, a library's, another
  version: each with the same app service, and only one the project's
  options work with); then one row per slot and
  extra `[options]` key with its current package. "Change" opens its choices (the current one tagged
  `current`); picking another stages it and the row shows `→ <new>`.
  Picking the current one again unstages it. "Add service" (a key and an
  option from `project show`'s `add_choices`) stages a new extra key (D81);
  setting an extra key to `none` removes it.
- **Firewall** (D119, docs/firewall.md): the mode (Default, with what it
  is now, Enforce, Log only, Off) and the `app`, `deps` and `agent` allow
  lists, each host with Remove and an add row; all staged like the other
  edits (`firewall.mode`, `firewall.allow.add`, `firewall.allow.remove`).
  "Blocked, or would be under enforce" loads after the page from
  `GET /p/<prefix>/settings/blocked` (`railswitch --json fw show`): each
  rejected address with its host and an "Allow in <list>" button (staging
  `firewall.allow.add`) when it resolves and the container has a list; a
  data container has nothing to allow; a container with no hook is
  named.
- **Hooks** (D126): a box per `[hooks]` list (`deps`, `start`, `create`),
  one command per line, in order; blank lines are dropped. Leaving a
  changed box stages `hooks.set` with the whole list (an empty box removes
  it); changing it back unstages it. The preview's restart list shows
  what a `deps` or `start` change restarts (`create` restarts nothing).
- **Environment:** the project's `[env]`. Editing a value stages it when
  you leave the field; Remove stages its removal; the add form (name,
  value) stages a new one, shown as a highlighted row with Undo until
  Apply writes it. The card's note says nothing is saved before Apply.
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
- **Save as template:** a name (`[a-z][a-z0-9-]*`) and a version (a
  whole number from 1) run `railswitch template export --name <n>
  --version <N>` as a project-level job. It writes
  `~/.config/railswitch/templates/<n>.stack/`; the CLI refuses a version
  already saved. The server refuses (400, no job) another name or
  version.
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
- **Install review:** when the edits install a library package
  (`settings preview`'s `install`, a `lock --review` document), the
  panel shows it under the cards with the Review update page's component
  (one card per package, every file as a diff from nothing, flagged
  lines marked). Apply then needs "I've read the N flagged items" ticked
  when N > 0, and posts the review's hash as `accept_install` with the
  tick. The server runs the preview again and refuses (400, no job) a
  hash that no longer matches, an `accept_install` when the edits no
  longer install anything, or a missing tick; the job then gets
  `--accept-install <hash>`. Without `--accept-install` the CLI installs
  no library package, so a form without it needs no second preview.
- Apply runs `railswitch settings apply --if-hash <hash>
  [--accept-install <hash>] [--restart]` as a project-level job, the
  edit list on its stdin. It writes all the
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

## Images

`/p/<prefix>/images` (`images` in the header) shows what a worktree
builds. Every `RUN` line in these Containerfiles runs on this machine
during the build, outside the firewall.

- **Worktree:** one chip per worktree; the base worktree is shown first
  (`?wt=<slug>` picks another; an unknown slug is a 404).
- The project's warnings from `railswitch --json -w <slug> containerfile
  list`, then the **build chain** in build order: step, kind (`base`,
  `addon`, `agent-base`, `agent`, or `image` for a tool or an option's
  image), file, `FROM` images and tag, and Edit.
- **New addon:** a name (lower-case letters, digits and `-`) runs
  `railswitch containerfile new <name>` as a project-level job; when it
  ends ok the editor opens on the new file.

**Editor** (`/p/<prefix>/images/edit/<name>`): the file from `railswitch
--json containerfile show <name>` in a text area. "Load from file…" reads
a local file into it, in the browser only (1 MiB at most).

- **Changes from the default (<ref>):** the diff from the built-in default
  to the file (its eject header left out); "Same as the default." when
  there is none to show. A notice says when the default changed since the
  file was ejected. "Reset to default" (after a confirm) runs `railswitch
  containerfile eject <name> --force` as a job; it renders, so the
  running worktrees show "restart to apply". A file `containerfile new`
  made has no default and no panel.
- **Check** posts the text to `POST /p/<prefix>/images/check/<name>`,
  which runs `railswitch --json containerfile write <name> --if-hash <h>
  --dry-run` with the text on stdin: every worktree is rendered with it
  and nothing is written. The panel shows the contract errors, or "Check
  passed" with each image tag that changes, and "Rebuild and restart:
  <slugs>" (off) when running worktrees build a changed tag.
- **Save** is on only when "I've read this file. Every RUN line runs on
  this machine during the build, outside the firewall." is ticked and the
  last Check passed for the text as it is now; an edit after Check turns
  it off again and says so. It runs `railswitch containerfile write
  <name> --if-hash <h> [--restart]` as a project-level job, the text on
  its stdin. The server refuses a Save without the acknowledgement (400).
  When the job ends ok the editor reloads the file; when only its
  restart failed (exit 3) it reloads with "Saved. The rebuild and restart
  failed" above the job's output. If the file changed
  on disk since the page read it, nothing is written and the page offers
  Reload; your text stays until you reload.

## Library

`/p/<prefix>/library` (`library` in the header) lists the packages from
`railswitch --json pack ls`: Packs, Options (with their slot) and Agents,
each `built in`, `local` (the workspace's `packs/<name>/` and
`options/<name>/`) or `library <alias>` (a library package,
`<alias>:<name>@N`; from `pack ls`'s `library`, else the ref's prefix).
"used by this project" marks the refs the project's stack uses
(`used` of `railswitch --json project show`). A library package is
read-only like a built-in (View; a write is refused, 422), can't be the
start of a New pack or New option, and its page says which library it
comes from, with the library's id and last fetch status.

**Import a package** (D114): a folder field and an optional name (`As`)
open `/p/<prefix>/library/import?dir=<path>&as=<name>`, which reads
`railswitch --json -C <workspace> pack import <dir> [--as <name>]` and
shows the package's status, its files with the review component and,
for a used package, "Restart after importing". **Import** needs the tick
when anything is flagged; it posts to `/p/<prefix>/library/import`,
which reads the review again and refuses (400, no job) a path that isn't
absolute and clean, a bad name, a hash that isn't the review's now, a
missing tick or nothing to import; then it runs `railswitch -C
<workspace> pack import <dir> [--as <name>] --accept <hash> [--restart]`
as a project job. A selected package's pane has **Export**: a new
folder's full path, posted to `/p/<prefix>/library/export`, which runs
`railswitch -C <workspace> pack export <ref> --to <dir>` as a job.

**Libraries** (first on the page) lists the machine's package libraries
from `railswitch --json library ls`: alias, status chip (`trusted`
green; `unknown-signer`, `rotation` yellow; anything else red; "not
fetched"), repository and branch, library id, and the last fetch (time,
serial, commit, package count). A failed read shows "Libraries
unavailable" and the rest of the page.

- **Fetch** posts the alias to `/machine/libraries/fetch`:
  `railswitch library fetch <alias>`.
- **Trust this key** (a fetch whose `pending.status` is
  `unknown-signer`) shows the fingerprint and "check it with the
  publisher"; after a browser confirm it posts the alias, the
  fingerprint and `kind=trust` to `/machine/libraries/trust`:
  `railswitch library trust <alias> --fingerprint <fp>`. When the
  library already trusts other keys (`known_library`) the box is red,
  says no trusted key vouched for this one, and needs the second
  confirmation "The publisher confirmed this new signer through another
  channel" (`new_signer=1`), which adds `--new-signer`.
- **Confirm rotation** (`pending.status` `rotation`) shows the trusted
  key and the new key it vouches for, and posts the new key's
  fingerprint with `kind=rotation`: `--rotation`.
- The server reads `library ls` again and refuses (400, no job) a
  fingerprint or kind that isn't what waits for that alias now, a
  library with nothing waiting, an unknown alias, and a `known_library`
  trust without the second confirmation.
- **Add library** (URL, alias, optional branch) posts to
  `/machine/libraries/add`: `railswitch library add <url> --alias <a>
  [--branch <b>]`. The server refuses (400) an empty URL, one over 2 KiB,
  starting with `-` or holding a line break; an alias that isn't
  `[a-z][a-z0-9-]{0,19}` or is `local`; a branch with a space or a
  leading `-`. The CLI checks the URL's transport.
- All three are machine-level jobs (`~machine`, like the Signers jobs),
  from your home directory; the Library page shows them in its jobs
  beside the project's, and reloads when one ends ok.

- Picking one (`?ref=<ref>`; an unknown ref is a 404) shows its
  `railswitch --json pack show <ref>`: kind, path, app, slot, runtimes,
  ports, pin, the env it gives, its README, and its files. A local
  package's files open in the editor (Edit); a built-in's open read-only
  (View). A package that doesn't load (no file list) offers its manifest.
- **Add file** (a local package) opens the editor on a new, empty file
  at the path typed: a top-level `Containerfile.<name>`, `deps.allow`,
  `README.md` or `<name>.sh`, or `entrypoints/<file>` or `initdb/<file>`.
- **New pack** and **New option**: a name, and either "blank" or a
  built-in to copy. A copy runs `railswitch pack eject <ref> --as <name>`;
  a blank pack `railswitch pack new <name>`; a blank option `railswitch
  pack new --option <slot> <name>` with the slot picked. When the job
  ends ok the new package's manifest opens in the editor.

**Templates** lists `railswitch --json template ls`: ref, description
and source (`machine`, or `library <alias>`). A row whose ref isn't
`<name>@N` or `<alias>:<name>@N` is left out. A failed read shows
"Templates unavailable" and the rest of the page. Picking one
(`?template=<ref>`; an unlisted ref is a 404) shows its `railswitch
--json template show <ref>`: name and version, the packages it uses, its
content hash, secret names, Containerfiles, one row per question (the
prompt, the `project.toml` path it sets, the default), its `stack.toml`
and README. A template is read-only here; save one from a project's
Settings.

**Package file editor** (`/p/<prefix>/library/<ref>/<path>`): the file
from `railswitch --json pack file show <ref> <path>` in the same editor as
the Images page, without the default panel.

- A built-in's file is read-only: no Save, Check or Delete, and the
  server refuses a write to it (422).
- Save needs "I've read this file. A package's RUN lines run on this
  machine during the build, outside the firewall, and its scripts run in
  the project's containers." for every file but `README.md`. It runs
  `railswitch pack file write <ref> <path> --if-hash <h> [--restart]` as a
  project-level job, the text on its stdin; a new file goes with
  `--if-hash ""` (the file must not exist yet).
- Save also needs a passing Check (`railswitch --json pack file write …
  --dry-run`, the text on stdin), as on the Images page: for any local
  package it says whether the package still loads; for one the project
  uses, also the changed tags and "Rebuild and restart".
- **Delete file** (after a confirm; not for the manifest, nor a file not
  saved yet) runs `railswitch pack file rm <ref> <path> --if-hash <h>` and
  goes back to the package.

The page refuses a ref or path of the wrong shape (404) before anything
runs; the CLI checks the exact list of files a package may have.

## Review update

`/p/<prefix>/review` (the banner's "Review update") shows a package
update waiting under `stack.lock` (a railswitch upgrade changed a
built-in, or a local package was edited outside the dashboard), from
`railswitch --json lock --review`. The project keeps running what it last
accepted until the update is accepted.

- One card per package: ref, kind, status (`changed`, `added`, `removed`,
  `first`), the old and new content hash (12 characters), and "Also
  locked in" the other projects that lock the same content. `first` says
  there is no stored copy of the old content: every file shows as added.
- Each changed file is a `<details>` with its status and flag count: a
  file with flags is open, the rest collapsed. Its unified diff marks
  each flagged line (the flag's line in the new file) in red, with the
  check's name as its title. Flags no diff line shows (a manifest flag,
  line 0, or a binary file's) are listed above the diff.
- **Accept** needs "I've read the N flagged items" ticked when N > 0, and
  offers "Restart after accepting" with the review's `restart`
  worktrees. It posts only the review's hash, the tick and the restart
  box, never file text. The server reads `lock --review` again and
  refuses (400, no job) a hash that no longer matches ("the update
  changed since you opened this page"), a missing tick while anything is
  flagged, or an empty review. Then it runs `railswitch lock --accept
  <hash> [--restart]` as a project-level job. When the job ends ok the
  page goes to the project page. When it exits 3 (accepted, restart
  failed) the page goes there too, with a notice.

## Machine

`/machine` (the header's "machine" link; no project needed) has a
**Templates** section (D113): the templates from `railswitch --json
template ls`, and **Import from a folder**, a path field that opens
`/machine/templates/import?dir=<path>`. That page reads `railswitch
--json template import <dir>` and shows the template's status (new, or
which version it replaces), its warnings and its files with the review
component. **Import** needs "I've read the N flagged items" when
anything is flagged; it posts the folder and hash to
`/machine/templates/import`, which reads the review again and refuses
(400, no job) a path that isn't absolute and clean or holds a control
character, a hash that isn't the review's now, a missing tick, or a
template with nothing to import (`same`, `older`); then it runs
`railswitch template import <dir> --accept <hash>` as a machine job
(`~machine`), the path one argv element. New project's template picker
links here.

It also has a **Signers** section: the SSH keys trusted to sign each package library,
from `railswitch --json signer ls`. Each library lists its trusted keys
(fingerprint, key type, added date; never the key material, and no
comment: `signers.toml` keeps none) and its revoked fingerprints. Library
ids, key types and fingerprints are escaped like any other text.

- **Distrust** asks for confirmation in the browser, then posts the
  library and fingerprint to `/machine/signers/rm`, which runs
  `railswitch signer rm <library> <fingerprint>` as a job. The server
  answers 400, no job, for a library id that doesn't match
  `[a-z0-9][a-z0-9./_-]{0,199}` (or holds `..`) or a fingerprint that
  isn't `SHA256:` and 43 base64 characters. A distrusted key can be
  trusted again.
- **Revoke** asks for confirmation (the key is never trusted again for
  the library, and that can't be undone from the CLI), then posts to
  `/machine/signers/revoke`, which runs
  `railswitch signer revoke <library> <fingerprint>` as a job. Same 400
  checks as Distrust.
- **Add key** posts a library id and one pasted public key line to
  `/machine/signers/add`, which runs `railswitch signer add <library> -`
  with the key as the job's stdin, never argv. A key over 16 KiB, with
  more than one non-empty line or a control character is refused (400).
- These are machine-level jobs, keyed `~machine` (no valid prefix, so no
  project's jobs mix in); one runs at a time, and the page shows them
  below the forms.

## New project

`/new` creates a project. Open it from the switcher ("New project…", the
last entry) or from `/` when no project is registered. The page has two
panels: step 1 on the left, step 2 on the right. Nothing is created
until you press Create project.

**Step 1: Stack and options**

- **Repository**, **Base branch** (empty: the repository's default
  branch), **Project name** (empty: the folder's name), **Prefix**
  (lowercase letters and digits, 2–16, starting with a letter, unused on
  this machine) and **Workspace folder**. The folder starts as
  `~/workspace/<prefix>` and follows the prefix until you edit it.
- **Start from a template** (above Read the repository) offers the
  templates of `railswitch --json template ls`. Picking one posts it to
  `POST /new/template`, which reads `railswitch --json template show
  <ref>` and answers the Stack card for it: the template, its packages,
  and one field per question (`answer-<key>`), prefilled with its
  default; the Containerfiles it brings. Picking a template unticks the
  Tools and agents boxes (the template's own stand) and turns Read the
  repository off; picking "None" puts the empty Stack card back.
- **Read the repository** runs `railswitch --json project detect <repo>
  [--branch <b>]` while you wait (it fetches only the files the packages'
  detection rules name, up to 2 minutes; the dashboard stops a read that
  runs too long with SIGTERM, then SIGKILL 3 s later, so its temp dir is
  removed). git's error shows under Repository. An empty Base branch is filled with the branch it read.
- **Stack:** the pack it matched, with the files that matched, and a
  picker of every pack (`railswitch --json pack ls`). When no pack
  matched, the card says "No pack matched; pick one". Picking a pack
  reads the repository again with `--pack <ref>`, which resets the slots.
- One row per slot, labelled by where its package comes from: "Read from
  the repository · <evidence>" or "Pack default". "Change" opens the
  choices: the options of that slot that work with the pack (`works_with`
  `*` or the pack's name), and `none`. A changed row shows `→ <new>` and
  "Changed by you · the repository suggests <ref>" (or "the pack default
  is <ref>"). "Add service" adds an extra key (D81) with any option that
  works with the pack.
- **Tools and agents:** Neovim, and one box per agent package `pack ls`
  lists; the package with `default = true` starts ticked. With no box
  ticked the project gets no agent (`--agent none`), from a template
  too: a template never brings an editor or agents (D112).
- A slot changed away from what the repository suggests adds a warning:
  "<Slot>: you picked <X>, but the repository suggests <Y>."
- **Next: review** runs the dry run (step 2). It is off until a pack or
  template is chosen and the prefix and folder are filled in.

**Step 2: Review and create**

- `railswitch --json project new <dir> --repo <repo> --prefix <p> [--name
  <n>] [--branch <b>] --pack <ref> --option <key>=<ref>… [--tool nvim]
  (--agent <name>=<ref>… | --agent none) --dry-run`; from a template,
  `--template <ref> --answer <key>=<value>…` in place of `--pack` and
  `--option`, one `--answer` argv entry per question in the template's
  order. The server reads `template show <ref>` first and refuses (400,
  nothing run) an `answer-<key>` the template doesn't ask, an answer
  without a template, a value with a line break, and a malformed or
  `local:` template ref. A refusal (a prefix in
  use, an existing `project.toml`, an option that doesn't work with the
  pack) shows at the top, and Create project is off.
- From a template (the dry run's `template`): its ref and sha256, and
  each question with the answer the project gets, its key and, when it
  differs, the default.
- Each worktree gets: its routes, its containers with their roles, and
  the app's env names. Then the render warnings, the `project.toml` it
  writes (read-only) and the steps Create runs.
- Any change to step 1 after a review marks the review stale and turns
  Create project off until you press Next: review again.
- When the project installs a library package (the dry run's
  `install`), step 2 shows its Install review, as in Settings, and
  Create project needs "I've read the N flagged items" when N > 0.
  Create posts the review's hash as `accept_install`; the server runs the
  dry run again and refuses (400, no job) another hash or a missing
  tick, then adds `--accept-install <hash>`.
- "Start <slug> when it's created" (on) adds `--up`. A project with
  secrets is created but not started (`up` would refuse them): the
  project page opens with its Secrets to set card.
- **Create project** runs the same `project new` command without
  `--dry-run` as a project-level job of the new prefix, from your home
  directory; a second create of that prefix is refused while the first
  runs. Every value is its own argv entry: a repository URL with spaces
  or `;` is passed as typed. When the job ends ok the page opens
  `/p/<prefix>`. When it fails, the page asks `GET /new/registered?prefix=<p>`
  (a fresh `project ls`) whether the project exists; if it does (exit 3:
  created, but `up` failed), the page opens `/p/<prefix>` and says that
  `up` failed, with its output in the project's jobs. Otherwise the form
  keeps its values and the job's output below says what was left and the
  command that removes it.

The page refuses (400, nothing run) a repository, branch, name, prefix,
folder or pack that starts with `-` or holds a line break, a prefix that
breaks the rule above, and a folder that is neither absolute nor under
`~/`.

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
  A request for a prefix the project list lacks reads `project ls` again
  only when the list is 5 s old or older; until then it is a 404.

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

- `railswitch --json signer ls` (machine): each library's trusted keys and
  revoked fingerprints. `signer add <library> -` (key on stdin),
  `signer rm <library> <fingerprint>` and
  `signer revoke <library> <fingerprint>` run as jobs.
- `railswitch --json library ls` (machine): each library with its last
  fetch and the fetch waiting on its signer (`pending`). `library add
  <url> --alias <a> [--branch <b>]`, `library fetch <alias>` and `library
  trust <alias> --fingerprint <fp> [--rotation | --new-signer]` run as
  jobs: the Library page's Libraries section.
- `settings preview`'s and `project new --dry-run`'s `install` (a `lock
  --review` document, or null) and `--accept-install <hash>`: the Install
  review. `overview`'s `signer_warnings`: the banner.
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
  one branch an open or draft one wins, then the highest number; a merged
  or closed PR counts only while the worktree is still at its head commit,
  so a branch that moved on (`master`, say) shows none; a PR from a fork
  never matches (its head branch is the fork's). Fails with gh's
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
- `railswitch [--json] pack ls`: the built-in packs, options (with slot
  and `works_with`) and agent packages, for the new page.
- `railswitch [--json] -w <slug> containerfile list`, `--json containerfile
  show <name>`, `containerfile write <name> --if-hash <h> [--dry-run]
  [--restart]` (text on stdin), `containerfile eject <name> --force`,
  `containerfile new <name>`: the Images page and its editor.
- `railswitch [--json] pack ls`, `--json pack show <ref>`, `--json pack file
  show <ref> <path>`, `pack file write <ref> <path> --if-hash <h>
  [--dry-run] [--restart]` (text on stdin), `pack file rm <ref> <path>
  --if-hash <h>`, `pack eject <ref> --as <name>`, `pack new <name>`, `pack
  new --option <slot> <name>`: the Library and its editor.
- `railswitch [--json] lock --review`, `lock --accept <hash>
  [--restart]`: the Review update page; `overview`'s `review_pending`
  shows its banner.
- `railswitch --json template ls` and `--json template show <ref>`
  (machine): the Library's Templates and the new page's Start from a
  template. `project new … --template <ref> --answer <key>=<value>…
  [--dry-run]` (the dry run's `template`): New project.
  `template export --name <n> --version <N>` and `template check
  --accept <sha>` (project) run as jobs: Settings. `overview`'s
  `template` and `template_update`: the banner and the Template changed
  notice.
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
while the order and selection stay), and `: ping` every 15 s. A settings,
images, editor or review page connects with `&view=settings`: it gets no
`list` or `detail`, and its `jobs` are the project-level ones only. The
Library page connects with `&view=library`: the same, with the
machine-level jobs (`~machine`) among its `jobs`. The new page connects when
Create starts, with `&view=new` and the new prefix, which need not be
registered: it gets the `header`, `projects`, `strip` and `banner` of no
project and that prefix's project-level `jobs`. Each `data:` line is one
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
