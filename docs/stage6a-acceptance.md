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
`[Install]`. `systemctl --user cat ftest-master-web.service | grep Wants`
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
Nothing overlaps or is unreadable in either mode.

## 4. Sorting and keys

Click `running`, `PR`, `agent`, `name`, and the active key again.

Expected: the orders in docs/dashboard.md "Sorting"; the second click
reverses (URL `?sort=-pr`). Reload: the sort stays (per project). ↑/↓ move
the selection and the URL follows (`/p/ftest/<slug>`); `P` opens the
switcher, `1` jumps to the first project; Esc closes.

## 5. Links

Add to `~/workspace/filial-test/project.toml`:

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
railswitch overview --json | jq '.worktrees[] | select(.slug == "master") | .links'
```

Expected: links App, Flipper, Jobs, then the stack's (Mailer previews,
Browser (VNC) …) and no Routes. In the page: App, Flipper, Jobs, stack
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
approved blue; checks ✓/…/✗. `Refresh PRs` after changing a PR on GitHub
(mark ready, approve) shows the change within seconds. A merged PR's
worktree shows "Remove worktree".

## 7. A notification on a permission prompt

Click `notifications` and allow. In another worktree run `railswitch agent`
and give the agent something that asks for permission. Switch to another
browser tab.

Expected: one notification "ftest / <worktree> needs permission" with the
prompt as body; clicking it focuses the dashboard and selects the
worktree; the tab title is `(1) RailSwitch`; on another project's page the
strip shows `◆ ftest / <worktree> · permission`. Answering the prompt
clears the count; no second notification while it stays waiting.

## 8. up, stop, restart

On a worktree: `stop`, then `up`, then `restart`.

Expected: each shows a job with live output and ends `ok`; the stack column
goes stopped → up; `systemctl --user is-active ftest-<slug>-web.service`
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

## Done checklist (roadmap, Stage 6a)

- [ ] one page per project (`/p/<prefix>`, `/p/<prefix>/<slug>`), list and detail;
- [ ] each row: name, branch, PR (number, checks, state, review), stack, agent;
- [ ] the selected worktree: name, branch, PR line, Links first, services, agent message, actions, jobs;
- [ ] links: App, `[[links]]`, stack links, then PR and Logs; `hide_links`;
- [ ] sorting by name, running, PR, agent;
- [ ] waiting agents in other projects visible (strip, header count); browser notifications;
- [ ] up, stop, restart, new worktree (up after adding), remove (dirty needs the typed name), task palette, Refresh PRs;
- [ ] light and dark following the system, Flexoki by default, the colour editor;
- [ ] the dashboard starts with the first worktree `up`.

## Gap log

| Gap | Generic feature | Commit |
|-----|-----------------|--------|
