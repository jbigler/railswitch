# railswitch Stage 7c: library repositories

Design for Stage 7c (D95), written on the user's instruction to continue
without questions (2026-10-28: "use your own judgement"); decisions
D103–D107. Builds on 7a (update review, the store) and 7b (signed index,
trust).

## Intent

HANDOFF §3.9/§3.11: the library "can add a library git repository"; nothing
from a library is installed or updated until it has been reviewed; an
unsigned library, or one whose signer isn't trusted, is shown but can't be
installed. 7c adds library repositories to a machine, fetches and verifies
them, shows their packages, and installs and updates library packages in a
project through 7a's review (the Install review, screen 6). It also brings
the nvim tool and the agent base image into `stack.lock`, the gap 7a left.

## Libraries on a machine (D103)

`~/.config/railswitch/libraries.toml` (`schema = 1`):

```toml
schema = 1
[[library]]
alias = "acme"                                # the ref prefix: acme:django@1
url = "https://github.com/acme/stack-library" # or a local path (file://)
branch = "main"                               # "" = the remote's default
id = "github.com/acme/stack-library"          # from the first verified index
```

- `alias`: `[a-z][a-z0-9-]{0,19}`, not `local`; unique.
- `library add <url> --alias <a> [--branch <b>]`: records it and fetches.
- `library fetch [<alias>]`: fetches one or all (below).
- `library rm <alias>`: refused while a registered project's
  `project.toml` names `<alias>:`; removes the entry and its checkout.
- `library ls [--json]`: alias, url, branch, id, the last fetch's status
  (a 7b status), serial, commit, fetched time, package count.

## Fetching (D104)

A library repository is hostile input, fetched like `project detect` (D88):
`git clone --depth 1 --filter=blob:none --no-checkout --branch <b>` into a
temp dir under `$XDG_RUNTIME_DIR`, `sys.GitProtocols`, `sys.GitArg`,
`core.hooksPath=/dev/null`, `protocol.ext.allow=never`,
`GIT_LFS_SKIP_SMUDGE=1`, no prompt, one 5 min deadline.

- `git ls-tree -r -z HEAD -- library.toml library.toml.sig packs options
  agents`: only mode 100644/100755 blobs count; a symlink (120000) or a
  submodule anywhere under those paths fails the fetch; paths must match
  the package path allowlist (D93) under `<kind>s/<name>/`; at most 2,000
  files, each ≤ 1 MiB, 64 MiB in all.
- Blobs are written (0644, `.sh` 0755) into a staging dir
  `<data>/libraries/<alias>/staging-<rand>/`, then `signing.Verify` runs on
  it with the machine's trust.
- `trusted`: staging is renamed over `<data>/libraries/<alias>/current/`
  (the previous `current` removed after), `last_serial` saved, the id
  recorded on first fetch (later: must equal it, else refused).
- `unknown-signer` or `rotation`: staging is kept as
  `<data>/libraries/<alias>/pending/` (replacing an older pending) for
  "Trust this key" / "Confirm rotation", which run `library trust
  <pending dir> --fingerprint <fp> [--rotation]` and then promote pending
  to current.
- Anything else (`rolled-back`, `revoked`, `bad-signature`): staging
  removed; `current` stays as it was; the status is recorded.
- The temp clone is always removed.

## Library refs (D105)

`<alias>:<name>@N` names a library package, in every place a ref goes
(`[stack] pack`, `[options]`, `[agents.<n>] use`, `from`). `pack.Ref`
gains `Library string` (the alias; exclusive with `Local`); `config`'s ref
check accepts it.

- The catalog resolves a library ref from `<data>/libraries/<alias>/current/`
  only, and only when that copy's `library.toml` lists the ref with the
  sha256 its directory hashes to now (`signing.PackageHash` per load; no
  `ssh-keygen` on load: the checkout is in the data dir, out of every
  container's reach, and was verified when fetched).
- `stack.lock` entries for library packages: `source = "library <id>"`,
  `commit = <fetched commit>`, plus `signer = "<fingerprint>"` (new field,
  library entries only).
- 7a applies unchanged: a library package runs what its lock entry pins,
  from the store; a fetch that changes a package's content leaves the
  project running the accepted copy with `review_pending`.

## Installing and updating through the review (D106)

A library package's content is never locked without being reviewed, even
the first time:

- `lock --review` lists a library ref new to the lock as `added`, every
  file shown and flagged (compared against nothing), as an Install review.
- Writes that don't accept (`settings apply`, `project write`, `agent add`,
  `project new`) refuse a library ref new to the lock: "installing
  acme:django@1 needs review", and restore, as every refused commit does.
- `settings preview` reports the Install review: an `install` section, the
  `lock --review` document of the project as the edits would leave it.
  `settings apply --if-hash <h> --accept-install <review-hash>` passes that
  hash to `Lock`, which accepts exactly those library refs (`Accept{Install:
  refs, IfHash}`) and refuses a different hash. `project new --dry-run`
  reports `install` the same way, and `project new --accept-install <hash>`
  accepts it.
- Accepting (install or update) also re-verifies the library's current
  checkout against today's trust (`signing.Verify`, one `ssh-keygen` call):
  a library whose key was distrusted or revoked since the fetch can't be
  installed or updated ("the signer of acme is no longer trusted").
- Built-in and local refs are unaffected (7a's rules).

## Tools and the agent base in the lock (D107)

`stack.lock` gains `[tool.<name>]` (the nvim tool when `[tools.nvim]` is
set) and `[base.agent]` (the agent base image's files) entries, hashed,
stored and run from the store like packages; `stackPackages` and the
review include them (`kind` `tool`, `base`). Built-ins only (no library
tools in 7c). A lock without these entries (written before 7c) is not
stale: they ran built in and unlocked before, keep running as resolved,
and the next lock write adds them, so an upgrade never stops `up`.

## Revocation's effect, filled (D102)

`overview --json` `signer_warnings`: one entry per locked library package
whose lock `signer` is no longer trusted for its library (distrusted or
revoked): `{"ref": "acme:django@1", "library": "<id>", "fingerprint": "…"}`.
The project keeps running; the dashboard shows the warning on the project.

## Page

- Library page (`/p/{prefix}/library`): a Libraries section (from `library
  ls --json`): each library with status, serial, fetched time, Fetch
  (job), and for a pending fetch "Trust this key" (shows the fingerprint,
  "check it with the publisher"; job `library trust … --fingerprint`) or
  "Confirm rotation" (both fingerprints). When the library already trusts
  other keys (`known_library`), "Trust this key" says that no trusted key
  vouched for this one and needs a second confirmation (`--new-signer`).
  "Add library" (URL, alias,
  branch; job `library add`). Library packages are listed with built-ins
  and local ones, read-only, with their library and signer.
- Settings and New project: library packages appear in the pickers (with
  their alias). When the staged change installs one, the preview shows the
  Install review (the 7a review component: files, flags) and Apply needs
  "I've read the N flagged items"; the request carries `accept_install`
  (the review hash, re-checked by the CLI).
- A project with `signer_warnings` shows them in its header.

## Errors

- A fetch of a hostile repository: refused paths, symlinks or sizes fail
  the fetch; nothing replaces `current`.
- A library ref whose library isn't fetched, or whose checkout no longer
  hashes to its index: the catalog reports it missing; a locked one runs
  from the store (7a).
- `library rm` of a library in use: refused, naming the projects.

## Testing

- Fetch from a local bare repo (git allowed in tests): a trusted library
  becomes current; a symlink, a submodule, a disallowed path, an oversized
  file each fail with current unchanged; unknown signer → pending; trust
  promotes; a rolled-back or revoked fetch leaves current; the temp clone
  is gone in every case; the id can't change.
- Refs: `acme:django@1` parses, round-trips, refuses `local` as an alias
  and an unknown alias.
- Catalog: a library package loads only when its dir hashes to the index's
  sha.
- Review: a library ref new to the lock is `added` with flags; `settings
  apply` without `--accept-install` refuses and restores; with the right
  hash locks and stores; with a stale one refuses; accepting after the
  signer was distrusted refuses.
- Lock includes `tool.nvim` and `base.agent`; a changed built-in nvim
  Containerfile is pending review and runs the accepted copy.
- `signer_warnings` after `signer rm`.
- Web: Libraries section, Add/Fetch/Trust jobs (argv exact), the Install
  review in Settings with the ack, guard.

Host acceptance, `docs/stage7c-acceptance.md`: a library repository in a
local bare repo (and pushed to a scratch path): add (unknown signer),
trust on the page, fetch (current); install a library option into Maybe
or Mastodon through Settings with the Install review, restart, the app
answers; publish an update (serial + 1), fetch, the project shows the
review, accept; distrust the signer: `signer_warnings` shows, the project
keeps running.

## Decisions (roadmap)

- D103: libraries are machine-wide (`libraries.toml`), each with an alias
  used as the ref prefix.
- D104: a library fetch is a hardened shallow clone of only the package
  paths; only a `trusted` verify replaces the current checkout; an
  untrusted one waits as pending for "Trust this key".
- D105: library refs are `<alias>:<name>@N`, resolved only from a verified
  checkout whose files still hash to the signed index.
- D106: installing a library package is a 7a review (`added`), accepted
  with the review hash through `settings apply --accept-install` /
  `project new --accept-install` / `lock --accept`, after a re-verify
  against today's trust.
- D107: the nvim tool and the agent base image are locked, stored and
  reviewed like packages.
