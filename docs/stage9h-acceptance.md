# Stage 9h acceptance: MariaDB

Host run on 2026-10-29 with the branch build installed: a fresh Rails 8
app made with `rails new shopdb -d mysql` (the `mysql2` gem), project
`shopdb`.

| Check | Result |
|---|---|
| `project detect`: `database` = `mariadb@1`, evidence "Gemfile: mysql2" | PASS |
| `project new` from the CLI with no `--option database=…` | Took the pack default (postgres@1): the CLI doesn't detect (the New project page does); `mysql2` then failed to build without the MariaDB client |
| Settings `option database = mariadb@1` | FAILED at first: `[image] addons` kept `Containerfile.pg-client` and never got the MariaDB client. Fixed: an option edit syncs the addons (D123) |
| `up`: the base image built with `libmariadb-dev`; `/up` 200 | PASS |
| `db dump`: only `shopdb_development.sql` (the system databases left out) | PASS |
| `wt add feature-x --up`: "restoring shopdb_development into the new database", the probe row "from main" there, `/up` 200 | PASS |
| A row changed in feature-x, `db restore --yes`: back to "from main", `/up` 200 | PASS |
