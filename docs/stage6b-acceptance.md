# Stage 6b acceptance: project settings

Host run on 2026-10-28, on the machine that ran Stage 6a (podman 6.1.4,
Omarchy). The page was driven with Playwright (headless Chromium) against
the live dashboard at `http://railswitch.localhost:7700`, on two of the
Stage 6a projects: Mastodon (`masto`, running) and Plausible (`plaus`,
stopped). Both `project.toml` files were saved first and are back to their
exact original bytes at the end.

## Results

| Check | Result |
|---|---|
| The page shows Project, Stack and options, Environment, Worktree links, Secrets and the staged panel | PASS |
| `link.add` stages as "No data affected" | PASS |
| Mastodon cache → none: Blocked, "options.jobs: sidekiq needs cache, but options.cache is none"; Apply disabled | PASS |
| Undo removes the card and its preview | PASS |
| A new env var offers "Restart the 1 affected running worktree now main"; Apply with it ticked ends `ok`, the list clears | PASS |
| After apply: `project.toml` differs only by the two staged statements; the link is in `overview`; rails restarted with the new env; `/health` 200 | PASS |
| After apply with restart: `overview` `stale: false` | FAIL, fixed (`9a129e9`), then PASS |
| Removing the env var stages "Check before applying: RS_6B_PROBE is no longer given to rails"; apply with restart; the var is gone from the container | PASS |
| Add then remove a link leaves `project.toml` as it was | FAIL (one blank line left), fixed (`9daf226`), then PASS |
| Add a secret (`rs_probe` for rails) from the page: row shows set and today's date; the value is in no file of the workspace (worktrees included), railswitch's data and config, the units, the journal or the page | PASS |
| Set value on that row: podman holds the exact value (with quotes and `$(x)`); the field clears; no file or journal hit | PASS |
| Raw editor: save writes the file | PASS |
| Raw editor after an outside edit: "project.toml changed since you opened it, so nothing was written", text kept, file has only the outside edit | PASS |
| Plausible `analytics` → none: "Data not carried over" naming each worktree's `clickhouse-data` volume and the two env names; apply | PASS |
| Put ClickHouse back from the page | FAIL (no way to add an extra key), fixed (`d099ad8`), then PASS |
| The secrets table fits its card | FAIL (buttons cut off), fixed (`558c713`), then PASS |

## Gaps found and fixed

| Gap | Fix | Commit |
|---|---|---|
| Edits after a blocked edit reported `"reasons": null` | always a list | `ae26f6d` |
| The raw editor had no way to read the file | `project show` gives `file` | `4b951be` |
| `settings apply --restart` left every container on its old unit (`stale: true`): systemd keeps `NeedDaemonReload=no` for a changed Quadlet file, so `up` after a render started the old generated service. This also hit any `render` followed by `up` or `restart` | `up` compares the hash in the loaded service's `ExecStart` with the rendered file's and reloads when they differ | `9a129e9` |
| Adding then removing a link left a blank line each time | `RemoveLink` takes the blank line `AddLink` put before the table | `9daf226` |
| An extra key set to `none` could not be added back from the page | "Add service" form; `project show` lists `add_choices` | `d099ad8` |
| The secrets table overflowed its card | CSS | `558c713` |

## Known limits

- Version pins (`[versions]`) have no edit op: they are changed in the raw
  editor, without a data-impact label.
- `secret rm` keeps the `[secrets.<name>]` declaration (by design); the
  page has no remove button for a secret.
- A worktree named `settings` is shadowed by the settings page.
- `--git-signing` secrets are added on the CLI only.

## Done when (roadmap)

Stack slots, options, extra services, env, links and secrets (values on
stdin) are editable from the browser, through the CLI, with the data
impact of each change shown before it is applied; everything else through
the raw editor. Met.
