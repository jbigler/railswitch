# Stage 6d acceptance: Containerfiles and packages from the browser

Host run on 2026-10-28 (podman 6.1.4, Omarchy), on the live dashboard,
driven with Playwright (headless Chromium).

## The Images page and editor (Mastodon)

| Check | Result |
|---|---|
| `Containerfile.pg-client`: add `less` to the install line; Check lists the changed tags (the addon step, `agent-base`, `agent claude`) and "Rebuild and restart: main" | PASS |
| Save with the acknowledgement and restart ticked: the job rebuilds the chain from the addon on, restarts `main`; `stale: false`, the app answers 200, `less` is in the container | PASS |
| The editor shows "Changes from the default (postgres@1)" | PASS |
| Reset to default: the file is the default again (its body byte-identical) | PASS |
| After the reset, the running worktree shows as behind | FAIL, fixed (`2cca7bb`): `eject` and `new` render a locked project; then `stale: true`, and a restart rejoins the default image (200) |

## The Library (Plausible, Mastodon)

| Check | Result |
|---|---|
| `local:phoenix@1` `entrypoints/entrypoint-app.sh` (a comment line): Check says "No image changes" and still lists `master` to restart (the mounted script changed) | PASS |
| Save with restart: `stack.lock`'s phoenix sha changes, the asset is rewritten, `master` restarts, answers 200, `stale: false`; the container's `/usr/local/bin/entrypoint-app.sh` has the line | PASS |
| New option, a copy of `redis@1` as `valkey`: the job runs `pack eject redis@1 --as valkey`, then the editor opens on `local:valkey@1/option.toml` | PASS |
| Agents: `claude@1` shows kind agent and its five files, read-only; "used by this project" marks the pack, options and agent the stack uses | PASS (after `381d3b5`, `dec10dd`) |

The test changes were undone afterwards: the entrypoint written back
through `pack file write`, `options/valkey/` removed.

## Gaps found and fixed

| Gap | Fix | Commit |
|---|---|---|
| The page marked "used" refs from a `project show` field that did not exist; built-in packages listed no files; Check was hidden for unused local packages | `project show` `used`; `pack show` lists every package's files; Check for every local file | `dec10dd` |
| Review: `local:rails@2` for a pack at `@1` wrote `pack.toml` without the lock or render | a ref at a version its manifest doesn't declare is refused | `381d3b5` |
| Review: a used package broken on disk couldn't be repaired (the current project had to load first) | "used" read from `project.toml`; the change judged on `workspace.LoadEdited` | `381d3b5` |
| Review: the Library's agent rows failed (`pack show` had no agents) | agents in `pack show`, `pack file`, `pack eject` | `381d3b5` |
| Review: a save whose restart failed said "not written" | exit 3; the editor reloads with "Saved. The rebuild and restart failed" | `381d3b5` |
| Review: a copied package opened at `@1` whatever the built-in's version | the copied version | `381d3b5` |
| Reset to default left a running worktree marked up to date | `eject` and `new` render a locked project | `2cca7bb` |

## Known limits

- A local package copied from a built-in records no origin, so it has no
  diff against a default (Containerfiles do).
- Version pins and the mock-up's "Add an option" registry dialog are not
  in 6d (D94).

## Done when (roadmap)

Containerfiles and local packages are edited from the browser with the
read-it acknowledgement, checks before any write and an optional rebuild
and restart; new packs and options start from a copy or a skeleton. Met.
