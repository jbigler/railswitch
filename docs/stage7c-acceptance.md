# Stage 7c acceptance: library repositories

Host run on 2026-10-29 (podman 6.1.4, OpenSSH 10.6p1, Omarchy) with the
branch build installed, on the live dashboard driven with Playwright
(headless Chromium). The library was a local bare git repository
(`file://`) holding `options/redis/` (a copy of the built-in redis option
with its own README), indexed as `example.test/acme-lib` and signed with a
fresh ed25519 key.

## Upgrade

| Check | Result |
|---|---|
| The 7c build over 7b locks (no `[tool]` / `[base]` entries): all four projects `review_pending: false`, `render --check` 0 (the nvim tool and agent base adopted) | PASS, after `aa2ba89` (the first cut made every project with nvim or agents refuse `up` until a review) |

## Adding and trusting a library

| Check | Result |
|---|---|
| `library add file://…/acme.git --alias acme`: `unknown-signer`, the fetch waits as pending with the fingerprint and the trust command | PASS |
| Library page: the row shows "Trust this key" with the fingerprint; the job `library trust acme --fingerprint …` runs; the library is `trusted`, serial 1, 1 package | PASS |

## Installing through the review (Mastodon)

| Check | Result |
|---|---|
| Settings offers `acme:redis@1` for the cache slot | PASS, after `pack ls` and `project show` list library packages (two gaps between the CLI and the page, fixed in this branch) |
| Choosing it shows the Install review: `acme:redis@1` option `added`, its files, 2 `image` flags (`pull`, `pin.default`); Apply disabled until "I've read the 2 flagged items" | PASS |
| Apply runs `settings apply --accept-install <hash>`; `stack.lock` pins the option with `source = "library example.test/acme-lib"`, its commit and `signer`; the app answers 200 | PASS |

## An update from the library

| Check | Result |
|---|---|
| Serial 2 changes the option's image to `redis:{redis}` (debian); `library fetch acme`: trusted, current | PASS |
| Mastodon: `review_pending: true`, `render --check` 0 (still runs the accepted copy); the review shows `option.toml` changed with one `image` flag and lists `main` to restart | PASS |
| Review page: Accept with restart; the cache container runs the debian redis image (by digest); app 200; nothing pending | PASS |

## Distrusting the signer

| Check | Result |
|---|---|
| `signer rm` of the library's key: `overview` `signer_warnings` names `acme:redis@1`; the project header shows the warning; the app keeps running (200, `render --check` 0) | PASS |
| A serial 3 fetch after the distrust doesn't become current: the library is known (a verified serial), so its signer waits for `--new-signer` | PASS |

## Cleanup

Mastodon was put back on `redis@1` through `settings apply --restart`
(200), `library rm acme` removed the library and its checkout, and the test
entry in `signers.toml` (kept by design: a verified serial) was removed by
hand. All four projects: nothing pending, `render --check` 0.

## Known limits

- An accept after a distrust is refused by the re-verify
  (`TestAcceptReverifies`); on the host the distrusted library's next
  fetch never became current, so there was nothing new to accept.
- Library tools (nvim from a library) are not in 7c.
