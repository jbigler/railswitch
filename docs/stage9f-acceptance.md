# Stage 9f acceptance: db restore

Host run on 2026-10-29 with the branch build installed, on Mastodon.

| Check | Result |
|---|---|
| `db dump`: `masto_development.dump` (456 kB) | PASS |
| A probe table created in `masto_development`, then `db restore --yes` before accepting the update | Refused, with the reason: "the database option postgres@1 declares no [dump] restore in the copy this project accepted; an update of it waits for review" (the project runs its accepted copy, D97) |
| `lock --review`: `postgres@1` changed, 0 flagged; accepted | PASS |
| `db restore --yes`: "restored masto_development from …/masto_development.dump"; the probe table is gone; `/`, `/about`, `/api/v1/instance` 200 | PASS |

## A new worktree restores its database (Stage 9g, D122)

| Check | Result |
|---|---|
| Mastodon accepts `postgres@1` (its initdb mount removed), 0 flagged | PASS |
| `wt add --up rs-restore-test`: "restoring masto_development into the new database", "restored … from …/masto_development.dump", then "starting rs-restore-test: deps, worker, db, cache, rails"; its `accounts` count is main's (2); the app answers 200 | PASS |
| `wt add rs-restore-test --up` (flags after the branch, as the guide shows) | FAILED at first (usage error); `wt add` and `db restore` now take flags after their arguments (`parseMixed`) |
| Cleanup: `down --yes`, `wt rm`, `git branch -D rs-restore-test`; main back up, 200 | PASS |
