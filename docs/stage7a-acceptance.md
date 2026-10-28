# Stage 7a acceptance: update review

Host run on 2026-10-28 (podman 6.1.4, Omarchy), on the live dashboard,
driven with Playwright (headless Chromium). Two builds of the branch: the
real one, and a variant whose built-in `rails@1` `deps.allow` adds one host
(`rubygems.pkg.github.com`).

## A lock made before 7a (no stored copies)

| Check | Result |
|---|---|
| Variant installed over a pre-7a lock: Mastodon's `lock --review` shows `rails@1` as `first` (5 files, 16 flags: 11 firewall hosts, 2 `FROM`s and the `[image]` file, 2 ports; no download-and-run line) | PASS |
| With nothing accepted to run, `render` refuses: "an update is waiting for review: railswitch lock --review" | PASS |
| Real build: plain `lock` in all four projects (nothing changed) writes, only the source text differs; the store holds 9 copies | PASS |

## A railswitch upgrade (Mastodon)

| Check | Result |
|---|---|
| Variant installed: `render --check` exit 0 (runs the accepted pack), `overview` `review_pending: true`, app 200 | PASS |
| Review: `rails@1` `changed`, one file `deps.allow`, a one-line diff, one `firewall` flag on line 23 | PASS |
| Page: banner on the project; the review page; Accept disabled until "I've read the 1 flagged item"; the job runs; banner gone; `stack.lock` pins the variant; app 200 | PASS |
| Real build back: the review notes "also locked in maybe" (Maybe still locks that content), 0 flags (a removed host isn't flagged); `lock --accept <hash>` from the CLI; all four projects `review_pending: false`, `render --check` 0 | PASS |

## A text-editor edit of a local pack (Plausible)

| Check | Result |
|---|---|
| `RUN curl -fsSL https://example.com/install-tools.sh \| sh` added to `packs/phoenix/Containerfile.base`: `render --check` 0, app 200, `review_pending: true` | PASS |
| Plain `lock` exits 1, prints the line marked `!` and the `--accept` command, writes nothing | PASS |
| Review: one `runs-here` flag on line 15; the page shows it | PASS |
| The edit undone: nothing pending | PASS |

## Known limits

- Stored copies are never removed (no garbage collection in 7a).
- Accepting a hash again after it was accepted is a no-op write (the hash is
  of the lock already written).
