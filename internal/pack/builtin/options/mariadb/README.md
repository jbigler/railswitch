# mariadb

MariaDB for the `database` slot. MariaDB speaks MySQL's protocol, so an
app written for MySQL (the `mysql2` gem) uses it unchanged.

- `DATABASE_URL` uses the pack's scheme for it (`mysql2://` for Rails).
  An app on the `trilogy` gem sets its own in Settings, with the same
  placeholders (the password stays generated):
  `DATABASE_URL = "trilogy://root:{generated.DB_PASSWORD}@db{db_path}"`.
- `railswitch db dump` writes one `<database>.sql` per database;
  `db restore` and a new worktree's first `up` load them back.
- Pin a version with `[versions] mariadb = "11.4"`; the data volume is per
  major version (`db-data-mariadb11`). A dump from a newer MariaDB doesn't
  restore into an older one (11.x dumps use collations 10.x lacks).
