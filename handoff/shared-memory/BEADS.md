# beads (bd) as railswitch uses it

Pinned: **bd 1.3.2** (tag `v1.3.2`, commit `9b65b9e198acbcc98e7fd3a87a5d55baa065a572`,
released 2026-10-24) from https://github.com/gastownhall/beads (`steveyegge/beads`
redirects there). Server: **dolthub/dolt-sql-server:2.2.0**, the Dolt version beads
pins (`docs/architecture/dolt.md:45-75`: 2.3.0/2.3.1 regress `DOLT_RESET('--hard')`).

Paths below are in the beads repo at that commit. The full research, with
everything that was read and run, is `beads-research-notes.md`; "verified" means
run against the real release binary and server, in the research run or on the
demo project (`PROGRESS.md`).

## Download (agent base, D138)

| What | Value | Source |
|---|---|---|
| Asset | `beads_1.3.2_linux_{amd64,arm64}.tar.gz`, `bd` at the archive root | `.goreleaser.yml:150-160`; verified (`tar tzf`) |
| Checksums | `checksums.txt`, sha256sum format; amd64 `c6ea3281…462e`, arm64 `39b34a12…5e7c` | `.goreleaser.yml:162-164`; amd64 verified against the download |
| Signature | none; GitHub/sigstore attestations (`gh attestation verify … -R gastownhall/beads` passes) | `.github/workflows/release.yml:329-374`. railswitch checks the sha256 pinned in the Containerfile, which the lock review shows |
| libc | linked against glibc ≤ 2.34 (cgo, embeds Dolt); runs on Debian trixie; not on musl | `.goreleaser.yml:14-55`; verified with readelf |

## Environment every agent gets (`render.MemoryEnv`)

| Variable | Value | Source |
|---|---|---|
| `BEADS_DIR` | `/home/appuser/.railswitch-memory/beads` (the `.beads` dir itself; authoritative, no walk up) | `internal/beads/beads.go:537, 740-790` |
| `BEADS_DOLT_SERVER_MODE` | `1`: external server, never a local one | `internal/configfile/configfile.go:368`; `internal/doltserver/servermode.go:61-111` |
| `BEADS_DOLT_AUTO_START` | `0` | `docs/architecture/dolt.md` env table; `internal/storage/dolt/open.go:193-238` |
| `BEADS_DOLT_SERVER_HOST` | `memory` (the server's network alias) | `configfile.go:488-499` |
| `BEADS_DOLT_SERVER_PORT` | `3306` (the image's) | `configfile.go:508-523` |
| `BEADS_DOLT_SERVER_USER` | `root` | `configfile.go:536-545` |
| `BEADS_DOLT_SERVER_DATABASE` | `beads` | `configfile.go:560-568` |
| `BEADS_ACTOR` | `<slug>/<agent>`: `created_by`, claims' `assignee`, journal `actor` | `cmd/bd/main.go:813-841` |
| `BD_DISABLE_METRICS` | `1`: metrics are on by default, to `gastownhall-eventsapi.com` | `cmd/bd/main.go:2430-2446`; `internal/metrics/metrics.go:17-21` |
| `BD_EVENTS_JOURNAL` | `1`: only writers with it are journaled | `internal/config/config.go:218-223`; verified |
| `GIT_CONFIG_*` `beads.role=maintainer` | silences "beads.role not configured" on most commands; env config writes nothing | `internal/routing/routing.go:37-80` (bd runs `git config --get beads.role`) |

No password: `BEADS_DOLT_PASSWORD` unset is an empty password (`configfile.go:603-610`).

## Files railswitch writes (`.railswitch/memory/beads/`, 0600 in a 0700 dir)

- `metadata.json`: `database: dolt`, `backend: dolt`, `dolt_mode: server`,
  `dolt_database: beads` (what `bd init --server --database beads` writes, less
  `project_id`, which bd adopts from the database): struct at
  `internal/configfile/configfile.go:20-69`; verified.
- `config.yaml`: `no-git-ops: true` (what `--stealth` persists, `cmd/bd/init.go:1797-1806`;
  `docs/reference/configuration.md:91, 105`) and `events-journal: true`
  (`internal/config/yaml_config.go:28-34`).
- Read-only is fine for bd in server mode: its gate lock goes beside the dir
  (`internal/workspacegate/gate.go:17-20`), so the mount sits inside the agent's
  writable home. bd warns about a group-readable BEADS_DIR, hence 0700.

## Commands railswitch runs

| Command | Why | Source |
|---|---|---|
| `bd init --server --stealth --non-interactive --skip-hooks --skip-agents -p <prefix> --database beads` | creates the database once (`CREATE DATABASE`, schema migrated without consent on a new database); run with cwd `/tmp` in a throwaway container so no git repo is near | `docs/cli-reference/init.md:20-28, 56-60, 82-91`; `cmd/bd/init.go:1017-1025, 1090-1115, 2088-2195`; `internal/storage/schema/remote_migrate_gate.go:823-824` |
| `bd prime` | the workflow and the memories, for SessionStart hooks | `docs/cli-reference/prime.md:10-27`; `cmd/bd/prime.go:95-128, 218-261` |
| `bd status --json` | counts: `summary.{total,open,in_progress,blocked,ready,closed}_issues` | `cmd/bd/status.go:15-35`; `internal/types/types.go:1875-1884` |
| `bd memories --json` | `{key: value}`; the count is its size | `cmd/bd/memory.go:150-154, 354` |
| `bd ready -n 0 --json` | ready work, no limit (`0 = unlimited`) | `cmd/bd/ready.go:704` |
| `bd list -s in_progress -n 0 --json` | claimed work | `cmd/bd/list.go:398, 412` |
| `bd blocked --json` | blocked work (no limit flag) | `cmd/bd/ready.go:741-744` |
| `bd events tail --since <seq> --limit 200` | the journal, JSON lines | `cmd/bd/events.go:129-130, 200-201` |
| `bd export --all` (stdout) | backup: issues with labels, deps, comments, and memories | `cmd/bd/export.go:66-68, 129` |
| `bd import --allow-stale -` | restore from stdin, the file wins | `cmd/bd/import.go:27-28, 112-115, 154` |
| `bd migrate schema` | apply pending migrations; the verb is the consent a shared database without a remote needs | `docs/cli-reference/migrate.md:12-30`; `internal/storage/schema/remote_migrate_gate.go:29-56` |
| `BD_JSON_ENVELOPE=1` (railswitch's own bd runs) | `{"schema_version":1,"data":…}` around --json output | `cmd/bd/output.go:23-80` |

Agents are told to use `bd create -l worktree:<slug>` (`docs/cli-reference/create.md:44`),
`bd ready -l …` (`docs/cli-reference/ready.md:37-45`) and `bd update <id> --claim`
(compare-and-swap; the loser exits 1 with "issue already claimed by <actor>":
`internal/storage/issueops/claim.go:24-196`, `cmd/bd/update.go:552-559, 864-906`;
verified with two agents).

## Version safety

- Schema: `SELECT MAX(version) FROM schema_migrations` (66 for 1.3.2;
  `internal/storage/schema/schema.go:196, 313-318`).
- A database newer than the binary: "schema version mismatch: database is at vN,
  binary knows up to vM" (`schema.go:82-153`).
- A newer bd refuses to migrate a shared server's existing database
  ("refusing to auto-apply …", `remote_migrate_gate.go:233-235, 807-826`) until
  `bd migrate` (above). Order for an upgrade: every agent image first, then
  `railswitch memory migrate` once.

## Not used, and why

- `bd backup` (Dolt-native `DOLT_BACKUP`): in server mode the path is the
  server's; not verified (D142).
- Dolt commits for activity: their author is `beads <beads@local>`, not the actor,
  and `bd remember` leaves its write uncommitted (`internal/storage/dolt/store.go:1555-1566,
  3319-3340`) (D143).
- `BD_IGNORE_SCHEMA_SKEW`: would hide exactly the mismatch doctor reports.
