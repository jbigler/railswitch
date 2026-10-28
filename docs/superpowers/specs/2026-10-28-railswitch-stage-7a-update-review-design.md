# railswitch Stage 7a: update review

Design for Stage 7a, the first part of Stage 7 (D95). Agreed with the user
on 2026-10-28, section by section; decisions D95–D98.

## Intent

A change to a package a project uses is never accepted unseen. Today two
things change one: a railswitch upgrade (the built-ins change) and a text
editor edit of a local package. Either leaves `stack.lock` stale; `up` and
`render` refuse ("run railswitch lock"), and `railswitch lock` accepts the
new content without showing it. After 6c every project needed exactly that.

7a puts a review in between: every changed file as a diff, risky lines
flagged by the "What it can do" checks (HANDOFF §3.11), and nothing
re-locked until "I've read N flagged items". Meanwhile the project keeps
running what it last accepted.

Saves through the 6d editor are already reviewed (the read-it
acknowledgement and Check) and keep re-locking at once.

## Split of Stage 7 (D95)

7a update review; 7b signers (trust, rotation, revocation; SSH signatures);
7c library repositories (add, browse, install and update through 7a's
review, signed per 7b); 7d templates (export `<name>.stack/`, start a
project from one). Each has its own design, plan and PR.

## The store (D96)

`internal/lockstore` owns `<data>/locked/<sha256>/` (`<data>` is
`~/.local/share/railswitch`, honouring `XDG_DATA_HOME`): one directory per
package content hash (`pack.ContentHash`), outside every workspace, so no
container can reach it.

- `Put(files fs.FS) (sha string, err error)`: copies every regular file
  (a symlink or other entry refuses, as `ContentHash`) to a temp dir beside
  the target, then renames it into place; an existing `<sha>` dir is left as
  it is. Dirs 0755, files 0644, `.sh` 0755.
- `Get(sha string) (fs.FS, error)`: reads the copy into memory
  (`fstest.MapFS`; regular files only, each ≤ 1 MiB, at most 256 files),
  recomputes its hash; missing, unreadable or mismatched is `ErrMissing`.
- Every `stack.lock` write stores its packages: `workspace.Lock` and
  `WithProject`'s callers that commit (`commit` in `internal/cli/settings.go`,
  `project new`) call `Put` for each pack, option and agent the new lock pins.
  A `Put` failure fails the write (and `commit` restores).

Nothing removes copies (they are small; garbage collection is out of 7a).

## Running on accepted content (D97)

`workspace.load` compares the resolved stack with `stack.lock`:

- Same refs (name, version, locality) and same content: as now.
- Same refs, different content for some packages: each such package is
  replaced in the catalog by the store's copy at the lock's sha
  (`Catalog.WithPackage(ref, files fs.FS)`, loaded and checked like any
  package). The project renders and runs exactly what was accepted.
  `Workspace.Pending` lists the packages with an update waiting.
- A store copy that is missing, or that this railswitch can't load: the
  package stays as resolved, `CheckLock` fails as now, and its error says
  "an update is waiting for review: railswitch lock --review".
- Different refs (a package added, removed or at another version; only a
  hand edit of `project.toml` does that): `CheckLock` fails as now, same
  hint.

The replacement happens in the catalog, before anything else reads the
stack, so every in-memory render (`settings preview`, `containerfile
write --dry-run`, `pack file write --dry-run`) sees the accepted content
too.

**Other writes never accept a waiting update.** `workspace.Lock(ctx,
root, accept)` takes what it may accept: `lock --accept` passes every
package, `pack file write` only the package it edits (reviewed in the
editor), every other caller (`settings apply`, `project write`, `agent
add`, `project new`) none. A package not accepted keeps its current lock
entry when that entry's content is usable from the store, and the write
refuses ("an update is waiting for review") when it isn't; a ref that is
new to the lock (an option added in Settings) is locked as resolved, and
stored. `lock --accept` passes its hash down, and the lock refuses before
writing bytes that hash differently.

`overview --json` gains `review_pending: bool` (`status --json` is an array
of service rows and stays as it is).

## CLI (D97)

- `railswitch lock --review [--json]`: the review of what a lock would
  write against the current `stack.lock`:

  ```json
  {"hash": "sha256:…", "flags": 3,
   "packages": [{"ref": "rails@1", "kind": "pack", "status": "changed",
     "from": "<sha>", "to": "<sha>", "also_locked_in": ["maybe"],
     "files": [{"path": "deps.allow", "status": "changed", "diff": "…",
       "flags": [{"check": "firewall", "line": 4, "text": "+ objects.githubusercontent.com"}]}]}],
   "restart": ["main"]}
  ```

  - `status`: `changed`, `added`, `removed`, or `first` (changed, no
    stored copy of the old content: compared against nothing).
  - `files`: every file that differs (unchanged files omitted); `status`
    `added`, `removed`, `changed`; `diff` a unified diff (3 lines of
    context); every file is diffed and checked as text, a NUL or invalid
    UTF-8 byte shown as `\xNN` (and itself a `runs-here` flag), so one
    stray byte hides nothing; `flags` as in the checks.
  - `also_locked_in`: other registered projects whose `stack.lock` pins
    the same ref at the new sha.
  - `hash`: `workspace.Hash` of the exact `stack.lock` bytes the accept
    would write.
  - `restart`: running worktrees whose units or mounted assets change
    (`settings.Env.Compare`), as 6d.
  - Nothing to review: `packages` `[]`, `flags` 0.
- `railswitch lock --accept <hash> [--restart]`: recomputes the review,
  refuses a different hash ("the update changed since it was reviewed"),
  then writes through `commit` (lock, store, render; restored on failure);
  `--restart` restarts the `restart` worktrees (exit 3: written, restart
  failed).
- `railswitch lock`: writes as now when there is no `stack.lock` or the
  review is empty; else prints the review as text (each file's diff, flags
  marked `!`) and exits 1 with `accept with: railswitch lock --accept
  <hash>`.

## Checks (D98)

`internal/review`: `Compare(kind string, old, new fs.FS) ([]File, error)`
(nil `old`: every file added), pure, no disk, no framework names
(`TestGenericity`). A flag is `{check, line, text}` (`line` 0 for a
manifest flag, which is listed above the diff).

| Check | Where | Flagged |
|---|---|---|
| `firewall` | `deps.allow` (a host per line, `#` comments); `agent.toml` `allow`; option and tool `firewall` profiles | a host added; a profile changed |
| `runs-here` | `Containerfile.*`, added or changed instructions (continuations and heredoc bodies joined; flagged at the first changed line); any file that is not text | `curl` or `wget` piped (`\|`) into `sh`, `bash`, `zsh`, `dash`, `ash`, `ksh`, `fish`, `python`, `python3`, `perl`, `ruby`, `node` or `php` (any case or spacing), or run by `<(`, `$(` or backticks; `ADD http://` or `https://`; `--security=insecure`, `--network=host`; a NUL or invalid UTF-8 byte |
| `image` | `Containerfile.*` `FROM`; option `[image] pull`; pack `[image]` file | a `FROM` ref or `pull` added or changed |
| `host-mount` | manifest `[[mounts]]` | an added or changed mount whose source is an absolute path or a placeholder (`{…}`); a package file source is not flagged |
| `port` | manifest `[[ports]]` | a port added, or its `base`, `container` or `share` changed |
| `privilege` | `[unit] add_capability`; a top-level `.sh` of a package with `add_capability` | a capability added; such a script changed |

- Manifests are compared field by field after loading both (`toml`
  decode into the format's types), so reordering or reformatting adds no
  flag. A manifest that doesn't load is a `runs-here` flag on the manifest
  ("does not load: …").
- Commented lines are not parsed specially: a commented `curl | sh` is
  flagged too.
- Everything else (entrypoints, README, env, tasks, links, other fields)
  is shown as a diff, not flagged: it runs inside a firewalled container.

## Page

- A project with `review_pending` shows a banner on its dashboard
  ("Package update waiting for review", link) and a mark in the switcher.
- `GET /p/{prefix}/review`: `railswitch --json lock --review -C <ws>`. One
  section per package (ref, status, from → to, "also locked in …"), its
  files with status and flag count; flagged files open, the rest
  collapsed (`<details>`); flagged diff lines highlighted, manifest flags
  listed above.
- `POST /p/{prefix}/review/accept` (guard): fields `hash`, `ack`
  (required when `flags` > 0: "I've read the N flagged items"), `restart`;
  the server re-reads the review and refuses a mismatched hash or a
  missing `ack` (400, no job); starts the project-level job `lock --accept
  <hash> [--restart]`. Job `ok` → the project page.
- The page never sends file text; `html/template` escapes everything; no
  inline script or style.

## Errors

- A tampered or missing store copy: `first` in the review; `up` refuses
  only when nothing accepted can run.
- A failed accept: `stack.lock` restored by `commit`; copies already stored
  stay (content-addressed, harmless).
- Two accepts at once: refused busy (one project job).

## Testing

- `lockstore`: round trip; tampered copy is missing; a second `Put` is a
  no-op; a symlink refuses; size and count limits.
- `review`: each check positive and negative; nil `old` marks all added;
  a reordered manifest adds no flag; a commented `curl | sh` is flagged.
- `workspace`: a built-in changed under the lock renders byte-identical
  units to before, `Pending` names it; a missing copy fails with the hint;
  a version change in `project.toml` fails.
- CLI: `lock` with a change exits 1, writes nothing, prints the review;
  `--accept` with a stale hash refuses; `--accept` writes, stores, renders;
  `project new`, `settings apply`, `pack file write` store copies.
- Web (CLI faked): banner; review page; hostile file text escaped; Accept
  argv exact, no file text; `ack` required when flags > 0; guard.

Host acceptance, `docs/stage7a-acceptance.md`: a build whose built-in
rails pack differs; Mastodon keeps running the accepted pack and shows the
banner; review and accept through the page, restart, 200. Plausible's
`local:phoenix@1` edited in a text editor with a `curl | sh` line: the
review shows the diff with the line flagged.

## Decisions (roadmap)

- D95: Stage 7 is four parts: 7a update review, 7b signers, 7c library
  repositories, 7d templates.
- D96: accepted package content is kept in a machine-wide store,
  `<data>/locked/<sha256>/`, verified by hash on every read; every
  `stack.lock` write stores what it pins.
- D97: a project runs what `stack.lock` names: a package whose content
  changed under an unchanged ref runs from the store until its update is
  reviewed; `lock --review` and `lock --accept <hash>`; plain `lock` refuses
  an unreviewed change; reviews are per project, noting other projects
  that lock the same content.
- D98: the checks (firewall, runs-here, image, host-mount, port,
  privilege) flag; everything else is shown; Accept needs "I've read the N
  flagged items" when N > 0.
