# Stage 9f acceptance: db restore

Host run on 2026-10-29 with the branch build installed, on Mastodon.

| Check | Result |
|---|---|
| `db dump`: `masto_development.dump` (456 kB) | PASS |
| A probe table created in `masto_development`, then `db restore --yes` before accepting the update | Refused, with the reason: "the database option postgres@1 declares no [dump] restore in the copy this project accepted; an update of it waits for review" (the project runs its accepted copy, D97) |
| `lock --review`: `postgres@1` changed, 0 flagged; accepted | PASS |
| `db restore --yes`: "restored masto_development from …/masto_development.dump"; the probe table is gone; `/`, `/about`, `/api/v1/instance` 200 | PASS |
