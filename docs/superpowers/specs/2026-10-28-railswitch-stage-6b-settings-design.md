# railswitch Stage 6b: project settings

Design for Stage 6b (D72): editing a project's settings from the dashboard.
Agreed with the user on 2026-10-28, section by section; decisions D82–D86.

## Intent

Configuration happens in the browser. The user (2026-10-28): "Most
configuration is not going to be done via CLI, that will be used to
start/stop run one off commands. But most of the configuration should be
done in the browser UI when creating the project and later when editing
it." 6b is the editing half; 6c is creating.

The page still changes nothing itself (6a anti-goal): every change is a
`railswitch` command the page runs as a job, and every read is a
`railswitch … --json` command. The commands below are shaped for the page
(JSON on stdin), not for typing.

Changes are staged, not applied. Before anything happens the page shows
what each change does to data and to running worktrees; Apply writes them
all or none.

## Scope (D82)

| In 6b | Out of 6b |
|---|---|
| Stack slots and extra option keys (D81), with data-impact labels | Per-worktree option overrides ("only pg17-trial") |
| Project `[env]` | The Updates card, Install review (Stage 7) |
| `[[links]]` and `hide_links` | Remove project from the browser |
| Secrets: declared rows, set a value, add one | New project (6c); packs and Containerfiles (6d) |
| A raw `project.toml` editor for everything else (services, routes, tasks, firewall, agents, image, tools, versions) | |
| A "restart to apply" marker on worktrees running old units | |

From the mock-up (`handoff/design/Settings.dc.html`) 6b keeps the layout,
the staged-changes panel, the three labels, the restart checkbox and the
links table. It drops: short names, "Started from" a template, the
Updates card, the Remove card, the "Apply to" override menu.

## CLI

All new commands work on the project (`-C <workspace>`); none takes `-w`.
JSON schemas go in `docs/json.md`.

### Edits (D84)

An edit list is a JSON array on stdin:

```json
[
  {"op": "option", "key": "cache", "ref": "valkey@1"},
  {"op": "option", "key": "analytics", "ref": "none"},
  {"op": "env.set", "name": "BIND", "value": "0.0.0.0"},
  {"op": "env.unset", "name": "OLD"},
  {"op": "link.add", "label": "Flipper", "url": "{worktree.host}/flipper"},
  {"op": "link.remove", "label": "Flipper"},
  {"op": "link.hide", "label": "Mailer previews"},
  {"op": "link.show", "label": "Mailer previews"}
]
```

- `option`: `key` is a slot or any other `[options]` key (D81). `ref` is a
  package ref or `"none"`. On a slot, `"none"` writes `key = "none"`; on an
  extra key it removes the line. A new extra key is inserted.
- `env.set` / `env.unset`: the project `[env]` table only.
- `link.add` / `link.remove`: the project's `[[links]]`, keyed by label
  (labels must be unique among project links; a duplicate is refused).
- `link.hide` / `link.show`: add to / remove from `hide_links`; the label
  must be a stack link (the D80 render check applies).
- An unknown op or field is an error (strict JSON decode).

Edits apply in order to the file's text with the line-level editor of
`internal/config/edit.go`, which keeps every other byte (comments, order,
spacing). It gains set over an existing value, remove a key, and remove an
`[[links]]` entry. A shape it cannot edit by line (an inline table, a dotted
key, a value split across statements) makes that edit **blocked** with the
line and "edit project.toml directly", as `agent add` does today.

### `railswitch settings preview --json`

Reads an edit list, writes nothing, prints:

```json
{
  "hash": "sha256:…",
  "diff": "--- project.toml\n+++ project.toml\n@@ …",
  "edits": [
    {"index": 0, "impact": "none|check|data|blocked",
     "reasons": ["REDIS_URL is no longer given to rails"],
     "error": ""}
  ],
  "restart": ["main", "feat-x"]
}
```

- `hash`: of the current `project.toml` bytes; `apply` checks it.
- `diff`: unified diff of the file before and after all edits.
- Each edit is judged on its own against the current file (edits 0…i-1
  applied first), so a card's label belongs to that card:
  - **blocked**: the edit cannot be placed, or the edited project does not
    load, validate, resolve or render. `error` is the message.
  - **data** ("Data not carried over"): for some registered worktree a
    volume the current render mounts is not mounted by the edited one, and
    that volume is not `data = "cache"`. Reasons name the volumes left
    behind and the ones that start empty; an option losing its service
    that has `[dump]` adds "run `railswitch db dump` first".
  - **check** ("Check before applying"): no data lost, but an env name the
    current render gives the app service is gone or has another value.
  - **none** ("No data affected"): otherwise.
- `restart`: registered worktrees with a running app unit whose rendered
  unit hash (the `<prefix>.unit-hash` label) differs between the current
  and the edited render, for the edits as a whole.
- Exit 0 even when an edit is blocked; the page reads `impact`.

The judgement is generic: it compares two renders' volumes and env. No
package is named in core.

### `railswitch settings apply [--restart] --if-hash <h>`

Reads the same edit list.

1. Refuses if `project.toml`'s hash is not `<h>` ("project.toml changed
   since the preview").
2. Applies the edits; refuses if any is blocked (preview's rules).
3. Keeps the old bytes, writes `project.toml` once (temp + rename), then
   runs `lock` and `render`. If either fails it writes the old bytes back
   and exits non-zero with the error.
4. With `--restart`: restarts, one at a time, the worktrees preview would
   list (`restart` in each). The first failure stops it; settings stay
   applied, the rest keep their marker.

### `railswitch project show --json`

What the page needs, in one read:

```json
{
  "prefix": "masto", "name": "…", "repo": "…", "base_branch": "main",
  "hash": "sha256:…",
  "options": [
    {"key": "database", "slot": true, "ref": "postgres@1",
     "choices": ["postgres@1", "local:mariadb@1", "none"]}
  ],
  "env": [{"name": "BIND", "value": "0.0.0.0"}],
  "links": [
    {"label": "Admin", "url": "{worktree.host}/admin", "from": "project", "hidden": false},
    {"label": "Mailer previews", "url": "{worktree.host}/rails/mailers", "from": "pack rails@1", "hidden": true}
  ]
}
```

- `choices`: for a slot, the built-in and workspace-local options that fill
  that slot, then `"none"`; for an extra key, every option, then `"none"`.
- `links`: project links, then stack links (pack, options, tools), with
  placeholders unexpanded, as `overview` orders them.

### `railswitch project write --if-hash <h>`

The whole file on stdin. Refuses on a hash mismatch; else validates
(`Load`, `Validate`), writes, locks and renders with apply's restore on
failure.

### Secrets

Unchanged commands, `secret add <name> --service <id>… [--env VAR]` and
`secret set <name>`, value on stdin. The page reads `secret ls --json`.

### Packages: `data` on volumes (D83)

`[[volumes]]` and `[[caches]]` entries take `data = "persistent" | "cache"`
(default `persistent`). A cache volume can be lost without a "data" label.
`docs/packs.md` documents it; built-ins with only rebuildable content mark
it.

### `overview`: `stale` (D85)

Each worktree gains `"stale": true` when a running container's
`<prefix>.unit-hash` label differs from its rendered unit's. One
`podman ps -a --format json` per `overview` call reads every container's
labels.

## Page

`/p/<prefix>/settings`, linked from the project header. Its jobs are
project-level (they block the project like `wt add`).

**Left column**

- Project: name, repo, base branch, prefix with "fixed: it names every
  container, volume, network and hostname". Read-only.
- Stack and options: one row per slot and extra key: current ref and
  "Change"; open, the choices as radio buttons with the current one tagged.
  A staged row shows `old → new`.
- Env: name and value rows; add, edit, remove (each stages an edit).
- Links: Show checkbox (hide/show), label, address, From, Remove on project
  links; the add form (Label, Address) with Add disabled until both are
  filled; the help line about `{worktree.host}`, `{worktree.slug}`,
  `{branch}`.
- Secrets: one row per declared secret: services, delivery (file or env
  name), set or unset and its date, never the value; a "Set value" password
  field per row; "Add a secret" (name, services, optional env, value).
- "Edit project.toml" → `/p/<prefix>/settings/file`.

**Right column: staged changes**

- "Staged changes · N"; empty: "Nothing staged. Change a setting on the
  left to see what it does."
- One card per edit: title (`Cache: redis@1 → valkey@1`, `Env BIND`, …),
  label (No data affected green, Check before applying amber, Data not
  carried over red, Blocked red with the error), reasons, Undo.
- The diff, collapsed. "Restart the N affected running worktrees now"
  (off; hidden when N is 0). "Apply N changes", disabled while any edit is
  blocked.
- "When you apply": project.toml is written; units are rendered; running
  worktrees keep their containers until restarted (or restart now, one at a
  time, when ticked).

**Flow**

- The staged list lives in the page and `sessionStorage` (a reload keeps
  it). Each change POSTs the list to `/p/<prefix>/settings/preview`, which
  runs `settings preview --json` synchronously and returns the panel
  fragment.
- Apply POSTs the list and the previewed hash; a job runs
  `settings apply --if-hash <h> [--restart]` with the list as stdin. The
  panel shows the job; on `ok` the list is cleared and the page reloads. A
  hash refusal re-previews and says the file changed.
- Secrets act at once: Set and Add POST, a job runs `secret set|add` with
  the value as stdin; the field clears.
- Raw editor: a `<textarea>` with the file and its hash. Save runs
  `project write --if-hash`; a mismatch says so, offers a reload, keeps the
  text.
- Worktree rows and the detail show "restart to apply" while `stale`.

**Jobs gain stdin (D86).** A job may carry a stdin payload: held in memory,
written to the child's stdin, never logged; the job log's header line shows
the argv only. Every POST passes the existing guard (token cookie, Origin,
Host).

## Errors

- Blocked edits: shown on their card; Apply disabled.
- Outside change between preview and apply: `--if-hash` refusal, re-preview.
- `lock`/`render` failure after the write: old bytes restored, job failed
  with the error.
- Restart failure: stops; settings applied; marker stays on the rest.

## Testing

Unit tests (fake runner, no podman):

- Edit ops on a commented fixture: each op's result loads and validates,
  every other byte survives; set over a value, remove a key, extra-key
  insert and remove, `[[links]]` add and remove, `hide_links` add and
  remove; blocked shapes name their line.
- Preview on the Filial fixture: redis → none is check (`REDIS_URL`) and
  sidekiq blocks it; postgres pin 16 → 17 is data (`db-data-pg16` left
  behind) with the dump hint; rustfs → none is data; a dropped
  `data = "cache"` volume is none; a link edit is none with no restart.
- Apply: one write, lock, render; restored after an injected render
  failure; `--if-hash` refuses after an outside edit; `--restart` restarts
  only the listed worktrees.
- Secret form (Stage 6 roadmap list): no token or a foreign Origin → 403,
  no command; the value is exactly the child's stdin; it is in no argv, job
  log, `slog` output, response or SSE event; the Secrets HTML holds no
  value.
- Web: preview and apply POSTs run the right argv with the list as stdin;
  the raw editor round-trips and shows the conflict.

Host acceptance, `docs/stage6b-acceptance.md`: Mastodon redis pin 8 → 7
with restart now, the app back; Plausible analytics → none shows red,
apply, put it back; add a link; set a secret and find its value nowhere
but podman's store.

## Decisions (roadmap)

- D82: 6b scope as above.
- D83: impact labels compare two renders' volumes and app env;
  `data = "cache"` on a volume.
- D84: `settings preview|apply` take a JSON edit list on stdin, all or none,
  `--if-hash`; `project write` replaces the whole file; `project show`.
- D85: apply restarts nothing unless `--restart` (one at a time);
  `overview` reports `stale` from the unit hash.
- D86: jobs take an optional stdin payload, never logged.
