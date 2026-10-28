# Stage 6c acceptance: new project from the browser

Host run on 2026-10-28 (podman 6.1.4, Omarchy), on the live dashboard,
driven with Playwright (headless Chromium).

## Detection on real repositories

| Repository | Read | Result |
|---|---|---|
| mastodon/mastodon | `main` | `rails@1` (Gemfile, config/application.rb); postgres (pg), redis, rustfs (aws-sdk-s3), sidekiq, playwright (capybara-playwright-driver) from the repo; mail the pack default |
| maybe-finance/maybe | `main` | `rails@1`; postgres, redis, rustfs, sidekiq from the repo; mail and browser the pack default |
| plausible/analytics | `master` | no pack (its `local:phoenix@1` lives in its workspace, which a new project doesn't have) |
| metabase/metabase | `master` | no pack (same: `local:clojure@1`) |

Each read took 2–4 s and left no `railswitch-detect-*` dir.

## The page

| Check | Result |
|---|---|
| Read the repository fills the branch, the pack with its evidence and every slot with "Read from the repository · …" or "Pack default" | PASS |
| The folder follows the prefix (`~/workspace/<prefix>`) | PASS |
| Review with the prefix `masto`: "prefix "masto" is already used by /home/jbigler/workspace/mastodon", Create disabled | PASS |
| Dry run into a folder holding `project.toml`: refused, nothing written | PASS |
| Review with a new prefix: routes, containers, app env names and the `project.toml`; nothing written | PASS |
| Create maybe-finance/maybe as `maybe` with Start ticked: clone, images, register, render (390 s) | PASS |
| Its `up` | FAIL, fixed (`a0450c5`, `d710e06`), then PASS: `main.maybe.localhost` → 200 at `/sessions/new`; puma, sidekiq and the tailwind watcher run under foreman; `stale: false` |
| A failed `up` after a create is shown as a failed create | FAIL, fixed (`df9f742`): exit 3, and the page opens the project |
| An `ext::` repository: refused before git runs | PASS |

## Gaps found and fixed

| Gap | Fix | Commit |
|---|---|---|
| A Procfile's `rails tailwindcss:watch` ends at stdin's EOF, and foreman then stopped the app before it was ready (`result 'protocol'`); every Rails app with Tailwind | the rails pack's app runs with `--interactive` (new `[unit] interactive`), and its entrypoint passes stdin to the backgrounded server through fd 3 (dash gives a background command `/dev/null` otherwise; checked in the image) | `a0450c5` (entrypoint), `d710e06` |
| Review: a hostile repository could fill `$XDG_RUNTIME_DIR` (a 3.8 MB repo wrote 390 MB to tmpfs) | detect reads blobs from the object store (`ls-tree`, `cat-file -s`, `cat-file blob`), never a working tree | `a0450c5` |
| Review: a cancelled detect left its temp dir; 2 min per git call, not per read | one deadline for the read; the dashboard's reads get SIGTERM before SIGKILL; stale dirs swept | `34292d5` |
| Review: created but `up` failed looked like a failed create | `project new --up` exits 3; the page checks registration and opens the project | `df9f742` |
| Review: the dry run approved folders the real run refuses after cloning | `planNew` refuses them first, in both modes | `4ba84e5` |
| Review: git transports not allowlisted (`fd::` hung, `ext::` reached git) | `protocol.allow=never` plus https, ssh, git, file on every remote call; `<transport>::` refused | `60f000e` |
| Review: an unknown prefix re-read the project list on every request | at most once per 5 s | `469d347` |
| `make check` failed while an agent's worktree sat under `.claude/` | gofmt only `cmd` and `internal` | `e0941a1` |

## Known limits

- A workspace-local pack (Plausible's phoenix, Metabase's clojure) can't be
  detected or chosen at create: it doesn't exist until the workspace does.
- The detection rules changed the built-in packages' content, so every
  existing project needs `railswitch lock` once after upgrading.
- Secret values are set on the Settings page after creating.

## Done when (roadmap)

A project is created from the browser: repository, detection with
evidence, options, review of the exact `project.toml`, create as a job.
Met.
