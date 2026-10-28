# railswitch Stage 6d: Containerfiles and packages from the browser

Design for Stage 6d (D72): the Images page, the Containerfile editor and
the Library. Agreed with the user on 2026-10-28, section by section;
decisions D91–D94.

## Intent

After 6b (edit a project) and 6c (create one), the browser still can't
touch what gets built: the Containerfiles in `containerfiles/` and the
workspace's own packages (`packs/<name>/`, `options/<name>/`, e.g.
Plausible's `local:phoenix@1`, Metabase's `local:clojure@1`). Today they are
edited in a text editor, then `railswitch lock`. 6d brings that into the
dashboard. Every `RUN` line runs on this machine during the build, outside
the firewall, so a save needs an "I've read this file" acknowledgement, and
the contract checks (D31, D36) run before anything is written.

The page changes nothing itself: it runs `railswitch` commands (6a
anti-goal).

## Scope (D94)

| In 6d | Out of 6d |
|---|---|
| Images: the build chain per worktree, "New addon" | The mock-up's "Add an option" dialog (registry tag lookup) |
| Containerfile editor: diff against the ejected default, "default changed" notice, Reset to default, Check, acknowledgement, Rebuild and restart | Version pin changes and major-version volume moves (a settings concern) |
| Library: built-in and local packs, options, agents; local package files in the same editor; Add and delete file | A structured `pack.toml` form (D92) |
| New pack / New option: copy of a built-in or a blank skeleton | Templates, import, export, library repositories (Stage 7) |
| | A diff of a local package against the built-in it was copied from |

## CLI (D91)

Every command works on the current workspace (`-C <ws>`); every read prints
one JSON document under `--json` (`docs/json.md`). Hashes are
`workspace.Hash` (`sha256:<hex>` of the bytes).

### `containerfile show <name> --json`

```json
{"name": "pg-client", "file": "containerfiles/Containerfile.pg-client",
 "kind": "addon", "text": "…", "hash": "sha256:…",
 "default": {"ref": "postgres@1", "text": "…"},
 "ejected": {"ref": "postgres@1", "sha256": "…"},
 "default_changed": false,
 "diff": "--- default\n+++ containerfiles/Containerfile.pg-client\n…"}
```

- `kind`: `base`, `addon`, `tool`, `standalone`, `agent-base`, `agent`, or
  `user` (a `containerfile new` addon, no default).
- `default`: the built-in default the stack has for `name` (`lookup`), or
  `null`. `ejected`: the file's first line parsed as the eject header
  (`# Ejected from <ref> Containerfile.<name> (sha256 <hex>) …`), or
  `null`. `default_changed`: both present and the default's sha256 differs
  from the header's.
- `diff`: a unified diff (3 lines of context) from the default to the file
  with its header line dropped; `""` when equal or no default.
- The file is read through `safefile.Read`; a missing file is an error
  (`eject` or `new` first). Without `--json` the command prints as now.

### `containerfile write <name> --if-hash <h> [--dry-run] [--restart]`

The new text on stdin (≤ 1 MiB, else refused).

1. `<name>` matches `cfNameRe` and is a step of the project's build (a
   `containerfile list` step's file); else refused ("not built by this
   project; use containerfile new or eject").
2. The current file's hash must be `<h>`; else exit 1 "changed on disk
   since you opened it".
3. Every worktree is rendered in memory with the new text (the D31/D36
   checks: `ARG BASE_IMAGE` before the first `FROM`, last `FROM
   ${BASE_IMAGE}`, fully qualified `FROM`s, final `USER` appuser/1000). Any
   error refuses; nothing is written.
4. `--dry-run --json`:
   `{"hash": "<new>", "steps": [{"worktree": "main", "name": "addon pg-client", "old_tag": "…", "new_tag": "…"}], "restart": ["main"], "errors": []}`;
   `steps` lists only changed tags; `restart` the running worktrees
   (one `systemctl show`, as `settings preview`) with a changed tag.
   A refusal at step 3 is `errors` with exit 0 under `--dry-run`, so the
   page can show it; steps 1–2 exit 1.
5. Real run: atomic write (same-dir temp file, rename), render, print each
   changed tag. `--restart`: restart (stop, then `up`, which builds the
   new tags) each `restart` worktree in order, stopping at the first
   failure (6b's `restartAll`).

Reset to default stays `containerfile eject <name> --force`.

### Local package files

- `pack show <ref> --json` gains `files: [{"path", "size", "executable"}]`
  for a local package (sorted; `[]` for a built-in).
- `pack file show <ref> <path> [--json]`: `{"path", "text", "hash"}`; built-in
  refs too (read-only view). Text output prints the file.
- `pack file write <ref> <path> --if-hash <h> [--dry-run] [--restart]`:
  stdin ≤ 1 MiB; `<ref>` must be `local:`. `--if-hash ""` means the file
  must not exist (Add file).
- `pack file rm <ref> <path> --if-hash <h>`: the manifest can't be
  removed.
- Allowed paths (D93): the manifest (`pack.toml` or `option.toml`),
  `Containerfile.<name>`, `deps.allow`, `README.md`, `<name>.sh` at the
  top; `entrypoints/<file>` and `initdb/<file>`. `<file>` and `<name>`
  match `[A-Za-z0-9][A-Za-z0-9._-]*`. Anything else, `..`, a symlinked
  file or a symlinked parent (the package dir, `entrypoints/`, `initdb/`)
  is refused. `.sh` files are written 0755, others 0644.
- Checks: the package must still load (`pack.LoadCatalog` of the
  workspace sees it without error). When the project uses the package
  (`stack.lock` names its ref), the write also locks and renders; a
  failure restores the file (or removes a new one), `project.toml` and
  `stack.lock` (the commit step shared with 6b's `commitProject`).
  `--dry-run --json` gives `{"hash", "used": bool, "steps", "restart",
  "errors"}` as above; `--restart` as above.

### `pack new --option <slot> <name>`

Writes `options/<name>/option.toml` (every field commented, `slot =
"<slot>"`, `version = 1`) and a `README.md`. The skeleton loads. `pack new
<name>` (a pack) is unchanged.

## Pages (D91)

Per project, linked from the project header beside Settings. Reads through
`--json`; writes as project-level jobs (one at a time per project, as
`settings apply`), the text as job stdin (`StartInput`), never argv. No
inline script or style.

**`/p/{prefix}/images`.** A worktree picker (default: the base worktree)
and its build chain from `containerfile list --json -w <slug>`: name, file,
kind, `FROM`s, tag, Edit. Warnings above. "New addon": a name, then the job
`containerfile new <name>`, then the editor.

**`/p/{prefix}/images/{name}`.** A monospace textarea with the file; "Load
from file…" reads a local file into it in the browser. "Changes from the
default (<ref>)" shows `diff`; when `default_changed`, a notice says the
default changed since it was ejected. "Reset to default" (confirm) runs
`containerfile eject <name> --force`. **Check** posts the text to the dry
run and shows changed tags, errors and the restart list. **Save** needs the
acknowledgement ("I've read this file. Every RUN line runs on this machine
during the build, outside the firewall.") and a passing Check of the
current text; an edit after Check disables Save. "Rebuild and restart:
<slugs>" (off by default) adds `--restart`. A hash mismatch shows "Changed
on disk since you opened it" with Reload; the text stays until reloaded.
The server refuses a Save without the acknowledgement field.

**`/p/{prefix}/library`.** `pack ls -C <ws>`: packs, options, agents,
built-in and local; "Used by this project" marks refs in the project's
lock (`project show`). Selecting one shows `pack show --json` (app,
runtimes, ports, pin, gives, readme) and, for a local package, its files
(Edit) and Add file; a built-in's files open read-only. **New pack / New
option**: name, (option) slot, start from a built-in copy (`pack eject
<ref> --as <name>`) or blank (`pack new <name>` / `pack new --option
<slot> <name>`); then the manifest opens in the editor.

**`/p/{prefix}/library/{ref}/{path...}`.** The same editor without the
default panel. The acknowledgement for every file but `README.md`; Check
and the restart list only when `used`. Delete (confirm) runs `pack file
rm`.

## Errors

- Contract or load error: shown under Check; Save stays disabled.
- Hash mismatch: refused, nothing written, Reload offered.
- Failed write job: file, `project.toml` and `stack.lock` restored; output
  shown; the text stays.
- Failed restart: the save stays; the job output names the failing
  worktree.

## Security

- File text is escaped by `html/template`; it's a file, not a secret, so
  it may appear in job logs.
- Reads through `safefile.Read`; writes refuse > 1 MiB, symlinks and
  off-list paths, and write atomically without following links.
- Every new POST goes through the guard; the dashboard never reads a
  workspace file itself.

## Testing

- CLI: `containerfile show --json` (kind, default, diff, `default_changed`
  against an altered header sha); `containerfile write` refuses a hash
  mismatch, a contract error, a non-chain name and > 1 MiB with nothing
  written; a good write changes the tag; `--dry-run` writes nothing and
  `restart` holds only running worktrees with changed tags; `pack file
  write` refuses off-list paths, `..`, symlinks, a write that breaks the
  load; a used package re-locks, a failing render restores all three
  files; `.sh` 0755; `rm` of the manifest refused; `pack new --option`
  loads.
- Web (CLI faked): exact argv for every job, text only on stdin; Save
  without the acknowledgement refused by the server; the guard on every
  new POST; a hostile Containerfile is escaped; one headless Chromium run.

Host acceptance, `docs/stage6d-acceptance.md`: through the page, edit
Mastodon's `Containerfile.pg-client` (Check, Save with restart; `stale:
false`, the app answers); reset it to the default; edit Plausible's
`packs/phoenix` entrypoint (re-locks), restart, the app answers; a new
option copied from redis.

## Decisions (roadmap)

- D91: Containerfiles and local package files are read and written through
  domain commands (`containerfile show --json`, `containerfile write`,
  `pack file show|write|rm`) with `--if-hash`, `--dry-run` and
  `--restart`; the page only runs them.
- D92: new packages start from a built-in copy or a commented skeleton and
  are edited as files; no structured `pack.toml` form.
- D93: package file paths are an allowlist of the package format's files;
  symlinks refused; `.sh` executable.
- D94: 6d scope as above; saves offer "Rebuild and restart", off by
  default.
