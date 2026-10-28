# railswitch Stage 6c: new project from the browser

Design for Stage 6c (D72): creating a project from the dashboard. Agreed
with the user on 2026-10-28, section by section; decisions D87–D90.

## Intent

Configuration happens in the browser (the user, 2026-10-28: "most of the
configuration should be done in the browser UI when creating the project
and later when editing it"). 6b edits a project; 6c creates one. You paste
a repository, railswitch reads it and suggests a pack and options with the
evidence for each, you correct what is wrong, review the `project.toml` it
will write, and Create runs `project new` as a job.

The page changes nothing itself: it runs `railswitch` commands (6a
anti-goal). No framework is named in core: detection rules belong to the
packages (D87).

## Scope (D90)

| In 6c | Out of 6c |
|---|---|
| `/new`: repository, base branch, name, prefix, workspace folder | The Environment and Images "advanced" pages (env is edited in 6b Settings after creation; Containerfiles are 6d) |
| Detected pack and slot options, each with its source and evidence; pack picker; Add service (D81 extra keys) | Templates and Save as template (Stage 7 library) |
| Tools (nvim) and agents | Adopt a workspace |
| Review: the `project.toml` to be written, what each worktree gets, the steps | Secret values during create (set on Settings afterwards) |
| Create as a job, optionally starting the base worktree | |

## Detection rules in packages (D87)

`pack.toml`:

```toml
[detect]
files = ["Gemfile", "config/application.rb"]   # all must exist
```

`option.toml` (any rule matching is enough):

```toml
[[detect]]
file = "Gemfile"
match = '''^\s*gem ['"]pg['"]'''   # RE2, tried on each line
evidence = "Gemfile: pg"            # optional; default "<file>: <match>"
```

- Paths are relative, clean, inside the repo (no `..`, no leading `/`);
  `match` must compile; checked at load (`internal/pack/load.go`).
- Built-in rules: `rails@1` `Gemfile`, `config/application.rb`;
  `postgres@1` Gemfile `pg`; `redis@1` Gemfile `redis`; `rustfs@1`
  Gemfile `aws-sdk-s3`; `sidekiq@1` Gemfile `sidekiq`; `playwright@1`
  Gemfile `capybara-playwright-driver` or `playwright`.
- `docs/packs.md` documents both tables.

## `railswitch project detect <repo> [--branch <b>] [--pack <ref>] --json` (D88)

1. Base branch: `--branch`, else the remote's HEAD branch (`remoteHead`,
   as `project new`).
2. Paths: the union of every pack's `[detect] files` and every option's
   `[[detect]] file` in the catalog (built-in; local packages of the
   workspace dir when it exists are not considered, there is none yet).
3. Fetch: into a new temp dir under `$XDG_RUNTIME_DIR` (else `os.TempDir`),
   `git clone --depth 1 --filter=blob:none --no-checkout --branch <b>
   <repo> <tmp>`, with `GIT_LFS_SKIP_SMUDGE=1`,
   `-c core.hooksPath=/dev/null`, `GIT_TERMINAL_PROMPT=0`; every git call
   through `sys.Runner`, one 2 min deadline for the whole fetch. No working
   tree is ever written. The temp dir is removed on every path (defer);
   the dashboard stops a read with SIGTERM (3 s grace before SIGKILL) so
   the defer runs, and each detect first removes this user's
   `railswitch-detect-*` dirs (real dirs, not symlinks) older than 10 min.
4. Read each path from the object store: `git ls-tree -z HEAD -- <paths…>`
   gives each entry's mode and blob id; only mode 100644 or 100755 counts
   (a symlink, a path under a symlinked directory, which has no entry, a
   directory or a submodule counts as absent). `git cat-file -s <oid>`
   (the promisor fetches the one blob, compressed) over 1 MiB counts as
   absent; else `git cat-file blob <oid>` into a buffer capped at 1 MiB.
5. Choose:
   - pack: `--pack` when given; else the first pack (by ref string) whose
     `files` all exist; others that matched go in `others`; none → `null`.
   - each slot (`config.Slots` order), among options with that slot that
     work with the pack (`works_with`): the pack's `[defaults]` value if it
     matched; else the first match by ref; `others` lists the rest.
     No match → the pack's default, `source: "default"`.
6. Print:

```json
{"branch": "main",
 "pack": {"ref": "rails@1", "evidence": ["Gemfile", "config/application.rb"], "others": []},
 "options": [
   {"key": "database", "ref": "postgres@1", "source": "repo", "evidence": "Gemfile: pg", "others": []},
   {"key": "mail", "ref": "none", "source": "default", "evidence": "", "others": []}
 ]}
```

`pack` `null` → `options` is `[]`. A git failure exits 1 with git's error.

## `project new --dry-run --json` and `--up` (D89)

`--dry-run` takes the same flags and writes nothing: no clone, no
directory, no registry entry, no units, no podman.

1. The same refusals as a real run, in the same words: `project.toml`
   already in `<dir>`, bad name, prefix in use (`prefixFree`), bad branch,
   unknown ref, an option that doesn't work with the pack, env clashes.
   The branch, when not given, is read from the remote (`remoteHead`).
2. `project_toml`: the exact bytes a real run writes to `project.toml`
   (after `addNewSecrets`), byte for byte.
3. An in-memory render of that project with its base worktree as the only
   worktree and a placeholder machine entry (next free ID and subnet,
   computed, not saved), giving the summary below.

```json
{"project_toml": "schema = 1\n…",
 "worktree": {"slug": "main",
   "routes": [{"host": "main.masto.localhost", "service": "rails"}],
   "containers": [{"name": "masto-main-db", "role": "data"}],
   "env": ["DATABASE_URL", "REDIS_URL"]},
 "warnings": [],
 "steps": ["Clone <repo> (<branch>) into <dir>/<slug>", "Write project.toml and stack.lock, generate local passwords", "Eject <N> Containerfiles", "Register, lock and render", "Start <slug> (up)"]}
```

- `env`: the app service's env names, sorted; no values (passwords are
  generated only by a real run).
- `warnings`: render lint warnings.
- `steps`: the last only with `--up`.
- A refusal exits 1 with the real run's message.

`--up`: after a successful create, `up` in the base worktree (as `wt add
--up`); not after a failure.

A failed `project new` deletes nothing (it may hold the user's clone): it
prints what it left and the command that removes it (`failLeft`), as now.

## Page

`/new`, linked from the switcher ("New project…") and from the root page
when no project is registered. One page, two panels (step 1, step 2), no
navigation between them.

**Step 1: Stack and options**

- Start a new project: Repository; Base branch (filled from `detect`'s
  `branch` after a read); Project name; Prefix (lowercase a–z0–9, ≤ 12,
  "Must be unused on this machine."); Workspace folder (default
  `~/workspace/<prefix>`, follows the prefix until edited).
- "Read the repository" runs `project detect` synchronously (a short read,
  not a job) and fills the pack and slots. Changing the pack re-runs it
  with `--pack`. A git error shows under Repository.
- Stack: the pack with its evidence; a picker of every pack; "No pack
  matched; pick one" when `pack` is null.
- Beside the app: one row per slot with its label — "Read from the
  repository · <evidence>", "Pack default", or "Changed by you · the
  repository suggests <ref>" — and "Change" → radio choices; Add service
  (key + option, as 6b).
- Tools and agents: nvim checkbox; agent checkboxes (default: the built-in
  agent with `default = true`).
- Warnings: an override that differs from a repo detection: "<Slot>: you
  picked <X>, but the repository suggests <Y>."
- "Next: review" runs the dry run.

**Step 2: Review and create**

- Dry-run refusals at the top; Create disabled.
- Each worktree gets: routes, containers, the app's env names.
- The `project.toml` it will write (read-only) and "Create runs" (steps).
- "Start <slug> when it's created" (on) → `--up`. "Create project" starts
  the job `project new <dir> --repo … --prefix … --name … --branch …
  --pack … --option k=ref… [--tool nvim] [--agent n=ref… | --agent none]
  [--up]`, each value its own argv entry.
- Job `ok` → `/p/<prefix>`. Failed → the form stays filled, the output
  below it (what was left and how to remove it).

**Jobs.** A create job's key is the new prefix (project-level): a second
create of the same prefix is refused busy; `project new` refuses a used
prefix or an existing `project.toml` anyway.

## Errors

- Unreadable repository: git's error under Repository; host git
  credentials only.
- Hostile repository: only named files checked out; `safefile` reads; no
  hooks, LFS or repo code; temp dir always removed; 2 min bound.
- Dry-run refusal: blocks Create.
- Create failure: output kept, nothing deleted.

## Testing

- Rules: a pack needs all its files; an option rule fires on a matching
  line, not on a commented one; the pack default wins when it matched;
  `others`; default source when nothing matched; bad rule paths and regexps
  refused at load.
- Detect against a local bare repo (git allowed in unit tests): only the
  named files are checked out; a symlinked `Gemfile` counts as absent; the
  temp dir is gone after success and after failure; `--branch` and the
  remote's default.
- Dry run: writes nothing (no dir, registry or units); `project_toml`
  equals a real run's `project.toml` for the same flags; same refusals
  (used prefix, existing `project.toml`).
- `--up`: `up` after success, not after failure.
- Web: guard on the new POSTs; Create argv exact, a repo URL with spaces or
  `;` is one argv entry; Read and Next run the right commands; success
  redirects, failure keeps the form.

Host acceptance, `docs/stage6c-acceptance.md`: create a public Rails app
not yet on this host through the page with Start ticked and see it answer;
detect Mastodon, Plausible (with its local pack, chosen by `--pack`) and
Metabase and record what each reads; a used prefix; an existing folder.

## Decisions (roadmap)

- D87: detection rules belong to packages (`[detect] files`;
  `[[detect]] file/match/evidence`); core only runs them.
- D88: `project detect` reads only the named files, from the object store
  of a shallow blobless clone with no working tree, in a temp dir it always
  removes.
- D89: `project new --dry-run --json` gives the exact `project.toml` and an
  in-memory render summary; `project new --up` starts the base worktree.
- D90: 6c scope as above.
