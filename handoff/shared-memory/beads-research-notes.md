# beads (`bd`) research notes, v1.3.2

Repo: https://github.com/gastownhall/beads. `steveyegge/beads` redirects to it: `gh api repos/steveyegge/beads` returns `full_name: gastownhall/beads`. The Go module path is still `github.com/steveyegge/beads` (go.mod:1).
Clone: `repo/`, checked out at tag **v1.3.2** = commit `9b65b9e198acbcc98e7fd3a87a5d55baa065a572`.
All `path:lines` below refer to that commit unless marked otherwise.

"VERIFIED" means I ran it: the real linux_amd64 release binary in a `debian:trixie-slim` container, on an `--internal` (no internet) podman network, against `dolthub/dolt-sql-server:2.2.0` started with `-e DOLT_ROOT_HOST=%`. Two clients used separate `BEADS_DIR`s, `BEADS_ACTOR=agent1` and `agent2`. Script: `scripts/bd.sh`.

---

## 1. Release, assets, signature, cgo/glibc, embedded dolt

- Latest stable release: **v1.3.2**, published **2026-10-24T00:43:03Z**, `isPrerelease=false` (from `gh release view v1.3.2 -R gastownhall/beads`). The tag commit is dated 2026-10-23 17:22 -0700. Release candidates are tagged `-rc.N`, and goreleaser sets `prerelease: auto` (.goreleaser.yml:193).
- Asset names come from `name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"`, tar.gz (.goreleaser.yml:150-160). Each archive holds `bd`, LICENSE, README.md and CHANGELOG.md:
  - https://github.com/gastownhall/beads/releases/download/v1.3.2/beads_1.3.2_linux_amd64.tar.gz
  - https://github.com/gastownhall/beads/releases/download/v1.3.2/beads_1.3.2_linux_arm64.tar.gz
  - https://github.com/gastownhall/beads/releases/download/v1.3.2/checksums.txt
- Checksums: `checksums.txt`, sha256 (.goreleaser.yml:162-164), in sha256sum format (`<hex>  <file>`). Downloaded values:
  - `c6ea3281f6ff82628351b2d0967c9eb85862c8a0ec0ebc24e63e645c8b02462e  beads_1.3.2_linux_amd64.tar.gz` (matches the download, VERIFIED)
  - `39b34a1246b7216031a98ae838c785a7dbba29cf9f50f109114ac462a4b85e7c  beads_1.3.2_linux_arm64.tar.gz`
- Signature: there is no `.sig`/`.asc`/cosign file among the assets. The release instead carries GitHub artifact attestations (sigstore) for every file in checksums.txt, plus an SPDX SBOM `beads-v1.3.2.spdx.json`. Both come from the `attest-release` job, which uses `actions/attest` with `subject-checksums` (.github/workflows/release.yml:329-374). VERIFIED: `gh attestation verify beads_1.3.2_linux_amd64.tar.gz -R gastownhall/beads` exits 0 and returns a sigstore bundle.
- cgo/glibc: the linux amd64 and arm64 builds use `CGO_ENABLED=1` with tag `gms_pure_go` (.goreleaser.yml:14-55). A post-build hook fails the build when CGO is off or `libicu` is linked (scripts/verify-cgo.sh). VERIFIED with `readelf` and `objdump -T` on the amd64 binary: dynamically linked, interpreter `/lib64/ld-linux-x86-64.so.2`, only `NEEDED libc.so.6`, highest symbol version **GLIBC_2.34**. VERIFIED: it runs on `debian:trixie-slim` (glibc 2.41): `bd version 1.3.2 (9b65b9e19: HEAD@9b65b9e198ac)`. It will not run on musl/Alpine (my inference: it links glibc).
- Embedded dolt: yes. The CGO build embeds the Dolt engine (beads_cgo.go:15-25: embedded is the default, server is chosen from metadata.json). docs/architecture/dolt.md:51-55 says embedded mode links the Dolt engine at the go.mod version (tagged v2.2.0 upstream).
- Builds without embedded dolt: the `!cgo` build has server mode only (beads_nocgo.go:15-17). Release assets built `CGO_ENABLED=0` exist only for android_arm64, windows_arm64 and freebsd_amd64 (.goreleaser.yml:57-148). No linux non-cgo asset exists. The release header's `go install` line without CGO yields a server-only build (.goreleaser.yml:211-216, which describes the RC case).
- Server mode from the normal release binary: works (VERIFIED, see §2).
- Size: `bd` is 154 MB uncompressed.

## 2. Server mode

**How bd connects to an external server**
- Flags on `bd init`: `--server`, `--server-host` (default 127.0.0.1), `--server-port` (default 3307), `--server-socket`, `--server-user` (default root), `--database`, `--external`, `--shared-server` (docs/cli-reference/init.md:25-28, 56, 60, 82-88). The password comes only from the environment, `BEADS_DOLT_PASSWORD` (init.md:28).
- Environment variables, read in internal/configfile/configfile.go. Env always beats metadata.json:
  - `BEADS_DOLT_SERVER_MODE=1` forces server mode (:368, also internal/doltserver/servermode.go:62-65 → `ServerModeExternal`).
  - `BEADS_DOLT_SERVER_HOST` (:488-499). A non-localhost value also implies server mode (:416-418, `HostImpliesServerMode`). Localhost strings: "", localhost, 127.0.0.1, ::1, 0.0.0.0 (:442-447).
  - `BEADS_DOLT_SERVER_PORT`, then legacy `BEADS_DOLT_PORT` (:508-523; real resolution order in internal/doltserver/doltserver.go:791-851 and 906-955). Default 3307 (:333).
  - `BEADS_DOLT_SERVER_SOCKET` (:527-532), `BEADS_DOLT_SERVER_USER` (:536-545, default `root`, :334), `BEADS_DOLT_SERVER_DATABASE` (:560-568, default `beads`, :335), `BEADS_DOLT_SERVER_TLS` (:615-621).
  - `BEADS_DOLT_PASSWORD`, else the `[host:port]` section of a credentials file at `BEADS_CREDENTIALS_FILE` or `~/.config/beads/credentials` (:583-610).
  - `BEADS_DOLT_CREDENTIAL_COMMAND` (:547-556), env only, for a token gateway.
  - Docs table: docs/architecture/dolt.md:197-206 and 548-560.
- metadata.json keys (`.beads/metadata.json`, struct at configfile.go:20-69): `dolt_mode` ("embedded"|"server"|proxied), `dolt_server_host`, `dolt_server_port` (deprecated: it warns about cross-project leakage, doltserver.go:836-851), `dolt_server_socket`, `dolt_server_user`, `dolt_database`, `dolt_server_tls`, `dolt_data_dir`, `project_id`.
- config.yaml keys: `dolt.mode`, `dolt.host`, `dolt.port`, `dolt.user`, `dolt.shared-server` (docs/architecture/dolt.md:188-195 and 528-546).
- VERIFIED: `bd init --server -p demo --non-interactive --skip-hooks --skip-agents`, with the env vars above and host `bdr-dolt`, wrote this metadata.json:
  `{"database":"dolt","backend":"dolt","dolt_mode":"server","dolt_database":"demo","project_id":"…"}`
  Host and port were not persisted, so every client needs the env vars. Init printed "Server: root@127.0.0.1:3306", but the connection went to `bdr-dolt` (cosmetic display bug).
- Auto-start: bd never starts a local server in external mode (internal/storage/dolt/open.go:193-238). `BEADS_DOLT_AUTO_START=0` or `dolt.auto-start: false` also turns auto-start off. Without `BEADS_DOLT_SERVER_MODE=1`, a non-localhost host, or a port in metadata, the mode is "owned" and bd may spawn its own dolt (servermode.go:105-111). So **always set `BEADS_DOLT_SERVER_MODE=1`**.

**Database name and sharing**
- Database name: `dbNameFromPrefix(prefix)` = prefix with `-` replaced by `_` (cmd/bd/init.go:2775-2777). Without a prefix, the default is the directory name or "beads" (init.go:1158-1178). `--database` overrides it (init.go:1173-1178, init.md:16-18 and 56).
- Shared servers: yes, one database per project on one server. That is the design of `--shared-server` ("Each project uses its own database (prefix-based)", docs/architecture/dolt.md:548-552). VERIFIED: two clients with separate BEADS_DIRs can share one database. The second ran `bd init --server -p demo --database demo` and printed "Adopted project identity from existing database". It got the same `project_id`.
- Database creation: `bd init` creates the database on the server with a bare `CREATE DATABASE` (internal/storage/uow/dolt_sql_provider.go:545-600). If the database already exists, it adopts it. A fresh database is migrated without consent ("creating a database is consent for its schema", internal/storage/schema/remote_migrate_gate.go:823-824).

**User and password**
- Default user `root` with no password: an empty password is used when none is set (configfile.go:603-610). VERIFIED: this works against `dolthub/dolt-sql-server:2.2.0` with `DOLT_ROOT_HOST=%`.
- Caveat: the image entrypoint defaults `DOLT_ROOT_HOST` to `localhost` (image `/usr/local/bin/docker-entrypoint.sh`:311 and 405, extracted to `image/docker-entrypoint.sh`). Root from another container then fails unless you set `DOLT_ROOT_HOST=%` or create a user. beads' own tests set this (internal/testutil/testdoltserver.go:146-150).

**Dolt version**
- beads pins the standalone dolt to **2.2.0** (docs/architecture/dolt.md:45-75). It says not to use 2.3.0/2.3.1, which regress `DOLT_RESET('--hard')`. 2.2.0 is also the CI image (internal/testutil/testdoltcommon.go:14, scripts/ci/pull-dolt-image.sh:5).
- bd enforces no hard minimum version for server mode. `doltversion.RecommendedMin = 2.0.0` is a warning only, and only for managed proxied mode (internal/doltversion/policy.go:9-22).

**Image `dolthub/dolt-sql-server:2.2.0`** (from `podman image inspect` and the extracted entrypoint)
- Ports: 3306/tcp, 33060/tcp, 7007/tcp. Volume: `/var/lib/dolt`. Entrypoint `tini -- docker-entrypoint.sh`.
- Server command: `dolt sql-server --host=0.0.0.0 --port=3306` (entrypoint:356).
- Env: `DOLT_ROOT_PASSWORD`, `DOLT_ROOT_HOST` (default localhost), `DOLT_DATABASE`, `DOLT_USER`/`DOLT_PASSWORD` (MYSQL_* aliases accepted, entrypoint:140-175 and 272-311), `DOLT_SERVER_TIMEOUT` (default 300, :87).
- Config dirs: `/etc/dolt/doltcfg.d` (json), `/etc/dolt/servercfg.d` (yaml) (:125-126, 259-266, 395). Init scripts: `/docker-entrypoint-initdb.d/`.
- Doc page: https://hub.docker.com/r/dolthub/dolt-sql-server (not fetched; the facts above come from the image itself).

## 3. BEADS_DIR

- It points at the `.beads` directory itself, not its parent (internal/beads/beads.go:537 "points to .beads directory"; docs/reference/worktrees.md:58-66 `export BEADS_DIR=~/project-beads/.beads`).
- It is authoritative. When set, bd does not walk up from cwd. If BEADS_DIR has no project files, bd returns "not found" instead of finding another workspace (beads.go:740-790). A `.beads/redirect` file inside it is followed (beads.go:749-751).
- `$BEADS_DIR/config.yaml` has the highest config priority (docs/reference/configuration.md:22-29).
- No git repo: works. VERIFIED: init and all commands run with no git binary at all. Side effects are harmless stderr warnings: "could not compute repository ID: not a git repository", and on every command "warning: beads.role not configured (GH#2950)…" (internal/routing/routing.go:59-62). Role falls back to a URL heuristic, maintainer when there is no remote.
- Git repos bd must not touch. What `bd init` does to the cwd repo:
  - If BEADS_DIR is under cwd, it writes/updates `<cwd>/.gitignore` (init.go:1090-1115). VERIFIED: init with cwd=/w and BEADS_DIR=/w/.beads created `/w/.gitignore`.
  - Unless `--stealth` or `--skip-agents`, it writes AGENTS.md and Claude/Codex/Cursor project files (init.go:2088-2148).
  - Unless `--skip-hooks` or `--stealth`, it installs git hooks (init.go:1022-1024, 2049).
  - Only when not stealth, in a git repo, and with BEADS_DIR local, it runs `git add .beads/ …` and `git commit -m "bd init: initialize beads issue tracking"` (init.go:2150-2195).
  - When not stealth and in a git repo, it reads `origin` and probes it for Dolt data refs (network) (init.go:876 and 1263-1270).
  - Safest: run `bd init` with cwd outside any git worktree (e.g. the BEADS_DIR parent). Pass `--skip-hooks --skip-agents --non-interactive`, and set BEADS_DIR outside the repo. `useLocalBeads` is false only when BEADS_DIR is explicit and outside (init.go:944 and 1035).
- Non-init commands: actor lookup calls `git config user.name` read-only unless BEADS_ACTOR is set (cmd/bd/main.go:813-841). Commit identity uses `GIT_AUTHOR_NAME`/`GIT_AUTHOR_EMAIL` if set (internal/storage/dolt/store.go:1555-1566). I did not audit every subcommand for git writes.

## 4. Stealth mode

- It is a flag on `bd init`: `--stealth` (init.md:20-23 and 91; cmd/bd/init.go:2312).
- What it does:
  - `setupStealthMode` adds `.beads/` and `.claude/settings.local.json` to **`.git/info/exclude`** of the cwd repo (it *writes into .git*), and skips silently when not in a repo (cmd/bd/init_stealth.go:23-63).
  - It routes the Dolt ignore patterns to `.git/info/exclude` instead of `.gitignore`, and removes any beads section from a tracked `.gitignore` (init.go:1094-1110).
  - It forces `skipHooks = true` (init.go:1017-1025).
  - It persists `no-git-ops: true` in config.yaml (init.go:1797-1806).
  - It skips AGENTS.md and the Claude/Codex/Cursor setup (init.go:2092-2093 and 2128).
  - It skips the auto `git add`/`git commit` and the origin-remote wiring (init.go:2152, 876, 1263).
- The flag help text says "global gitattributes and gitignore". The code does per-repo `.git/info/exclude`, so the help text is stale.
- Config key `no-git-ops` (yaml-only; configuration.md:91 and 105) makes `bd prime` print the stealth close protocol (prime.go:164). `bd prime --stealth` does the same per call.
- No env var for stealth was found.

## 5. `bd prime`

- It prints an AI-oriented markdown workflow guide: session close protocol, command reference, git-authority policy, and persistent memories (docs/cli-reference/prime.md:10-27; cmd/bd/prime.go:95-128). VERIFIED: about 5.8 KB in CLI mode on this workspace. It began with "[bd prime] If this output is truncated…", "# Beads Workflow Context", then "## Persistent Memories (1)". The output still mentions `git status` steps unless no-git-ops/stealth is set.
- Flags (prime.go:218-228):
  - `--full`, `--mcp`, `--stealth`
  - `--export` (dump the default and ignore the override)
  - `--memories-only`, `--no-memories`
  - `--hook-json` (SessionStart hook JSON envelope for Claude Code, Gemini CLI, Codex)
  - `--max-memories N`, `--max-memory-chars N` (config `prime.max-memories` / `prime.max-memory-chars`, env `BD_PRIME_MAX_MEMORIES` / `BD_PRIME_MAX_MEMORY_CHARS`, configuration.md:107-108)
- MCP auto-detection reads `~/.claude/settings.json` (prime.go:280-300).
- `BD_AGENT_PROFILE` / `agent.profile` (`conservative`|`minimal`|`team-maintainer`) changes the git-authority wording (prime.go:101-105).
- Customization: a `PRIME.md` replaces the default text. Lookup order: `./.beads/PRIME.md` (relative to **cwd**), then `$beadsDir/PRIME.md`, then `~/.config/beads/PRIME.md` (prime.go:231-261). Memories are still appended. VERIFIED: `$BEADS_DIR/PRIME.md` containing "custom prime" was printed, followed by the memories section.
- It is meant for agent hooks: "Designed for Claude Code, Gemini CLI, and Codex SessionStart hooks" (prime.md:16-17). `bd init` installs those hooks unless `--skip-agents` or `--stealth`.

## 6. Labels

- At create time: `bd create -l a,b` (`-l, --labels strings`, docs/cli-reference/create.md:44). VERIFIED: `bd create "First task" -l railswitch,wt-a`.
- Filter with `bd list` / `bd ready`: `-l, --label` (AND), `--label-any` (OR), `--exclude-label`, `--label-pattern` (glob), `--label-regex`, `--no-labels` (list.md:32-50; ready.md:37-45). VERIFIED: `bd list --label railswitch --json` and `bd ready --label wt-b --claim --json`.
- Other label commands: `bd label …` (docs/cli-reference/label.md, not read in detail).

## 7. Claiming

- Commands:
  - `bd update <id> --claim`: sets assignee to the actor and status to in_progress. It is idempotent if you already hold it. Issues assigned to a `claim.pools` alias are claimable too (cmd/bd/update.go:993).
  - `bd ready --claim [filters]`: atomically claims the first ready match (docs/cli-reference/ready.md:23-24 and 36). It prints `[]` with exit 0 when nothing is claimable (cmd/bd/ready.go:146).
  - `--claim` cannot be combined with `--if-assignee`/`--if-status` (update.go:960-961).
- Semantics: compare-and-swap inside a transaction. It runs `UPDATE … SET assignee=?, status='in_progress' … WHERE id=? AND row_lock=? AND status IN (claimable)`, then reads back the winner. Losers get `ErrAlreadyClaimed` ("issue already claimed by <assignee>") or `ErrNotClaimable` (internal/storage/issueops/claim.go:24-196).
- Leases: a claim grants a 5-minute lease (`DefaultLeaseTTL`, internal/storage/issueops/lease.go:19-26). It is shown as `lease_expires_at` / `heartbeat_at`. Expired claims are reverted only by an explicit `bd reclaim` (`--older-than` default 10 min, cmd/bd/reclaim.go:227) or by `bd heartbeat` refreshes. I did not see anything automatic.
- Losing the race, VERIFIED with two concurrent containers:
  - The winner exits 0 and prints a JSON array with the updated issue on stdout.
  - The loser exits **1** with empty stdout. Its stderr carries a plain line `Error updating demo-h9l: issue already claimed by agent1`, then the JSON `{"error":"1 of 1 issues failed to update","failed":[{"id":"demo-h9l","error":"updating issue: issue already claimed by agent1"}],"schema_version":1}` (update.go:552-559 and 864-906).
  - Exit 13 (`ExitGuardMismatch`) is only for `--if-assignee`/`--if-status` mismatches (update.go:824-831).

## 8. JSON output

- `--json` is a global flag (also `BD_JSON`, configuration.md:99).
- Without `BD_JSON_ENVELOPE=1`, arrays print bare. Objects get a `"schema_version":1` key merged in, *including the `bd memories` map*. With `BD_JSON_ENVELOPE=1` the output is `{"schema_version":1,"data":…}` (cmd/bd/output.go:23-25 and 31-80). VERIFIED both ways. A stderr note says the format will change in v2.0, printed only when stderr is a terminal (output.go:103-112). **Use `BD_JSON_ENVELOPE=1`.**
- `bd ready --json`, `bd list --json`:
  - Both return an array of `IssueWithCounts` = Issue + `dependency_count`, `dependent_count`, `comment_count`, `parent` (internal/types/types.go:1139-1145).
  - Issue fields (types.go:20-175): `id`, `title`, `description`, `notes`, `status`, `priority`, `issue_type`, `assignee`, `owner`, `created_at`, `created_by` (types.go:50), `updated_at`, `started_at`, `closed_at`, `close_reason`, `lease_expires_at`, `heartbeat_at`, `labels` (types.go:137), `metadata`, ….
  - Status filter: `bd list -s/--status open,in_progress,blocked,deferred,closed`, comma-separated. Repeated `-s` overwrites (list.md:68). `bd list --ready` uses ready semantics (ready.md:15).
- `bd blocked --json`: an array of `BlockedIssue` = Issue + `blocked_by_count`, `blocked_by` (types.go:1705-1709; ready.go:298-325).
- `bd status --json` (alias `bd stats`, cmd/bd/status.go:33-35) returns `{"summary":{total_issues, open_issues, in_progress_issues, closed_issues, blocked_issues, deferred_issues, ready_issues, pinned_issues, epics_eligible_for_closure, average_lead_time_hours}}`. With `--no-blocked`, `blocked_issues`/`ready_issues` may be null. Optional `recent_activity` (status.go:15-30; types.go:1875-1884). VERIFIED output. There is also a `bd count` command (count.go, not examined).
- Notes vs. memories:
  - Per-issue `notes` field: `bd update --notes`, `--append-notes`, `bd note <id> text` (docs/cli-reference/note.md).
  - Persistent memories: `bd remember "<text>" [--key k]`, `bd memories [search]`, `bd recall <key>`, `bd forget <key>` (docs/cli-reference/remember.md, memories.md, recall.md, forget.md).
  - Memories are stored as `config` table rows `kv.memory.<key>` (memoryops/doc.go:11-19). VERIFIED.
  - `bd memories --json` returns a `{key: value}` map (cmd/bd/memory.go:150-154). Count = number of keys under `data` with the envelope. Or use SQL: `SELECT COUNT(*) FROM config WHERE \`key\` LIKE 'kv.memory.%'`.

## 9. History, events, actor

- Dolt commits:
  - `dolt.auto-commit` (`on|off|batch`, default `on`, internal/config/config.go:260; flag `--dolt-auto-commit`, env `BD_DOLT_AUTO_COMMIT`, configuration.md:109).
  - In server mode each write unit runs `CALL DOLT_COMMIT('-Am', <message>)`. With nothing pending it does a plain `COMMIT` instead (internal/storage/uow/doltserver_tx.go:104-140).
  - The CLI-level auto-commit helper is embedded-only (cmd/bd/dolt_autocommit.go:148-151).
  - docs/architecture/dolt.md:535 says "default: on for embedded, off for server", which contradicts config.go:260 and what I observed.
- VERIFIED `dolt_log`: messages like `bd: create issue`, `bd: update demo-h9l`, `bd: claim ready demo-c2a`, `bd init`, `schema: apply migration …`.
  - **Committer is `beads <beads@local>`, not the actor.** The defaults are `GIT_AUTHOR_NAME`/`GIT_AUTHOR_EMAIL`, else beads/beads@local (store.go:1555-1566). Migration commits showed `root <root@%>`, the SQL user.
  - VERIFIED: `bd remember` left `config` *modified but uncommitted* in `dolt_status`. Config rows are excluded from ordinary commits (store.go:3319-3340).
- `events` audit table: `issue_id, event_type, actor, old_value, new_value, comment, created_at, id CHAR(36)` (internal/storage/schema/migrations/0062_events_dolt_ignore.up.sql:99-112).
  - It is **dolt_ignored**: working set only, not versioned, not replicated (0062:1-22). Rows cascade-delete with their issue.
  - It has no monotonic cursor: the id is a UUID and `created_at` has second granularity.
  - VERIFIED event_types seen: `created`, `label_added`, `claimed`, with actor = BEADS_ACTOR.
- `bd_events_journal` (migrations 0064 and 0066): `seq BIGINT` gapless and commit-ordered (drawn from `bd_events_seq` inside the mutation's transaction; designed for `WHERE seq > cursor` tailing, 0064:16-24), `ts`, `op`, `issue_id`, `actor`, `issue_json`, `dep_json`, `comment_json`.
  - It is **OFF by default** (config.go:218-223). Enable with config `events-journal: true` (yaml-only, internal/config/yaml_config.go:28-34) or `BD_EVENTS_JOURNAL=1`.
  - **Only writers that have it enabled journal their mutations.** VERIFIED: an update from a client without the env var was not journaled.
  - Retention defaults: 7 days and 100k rows, auto-prune on (config.go:224-245).
  - Read it with `bd events tail --since <seq> [--limit] [--follow]` (JSON lines, VERIFIED), `bd events export`, `bd events prune` (cmd/bd/events.go:33-160).
  - Raw `bd sql` DML is not journaled (events.go long help).
- Actor resolution: `--actor`, then `BEADS_ACTOR`, then `BD_ACTOR` (deprecated), then `git config user.name`, then `$USER`, then "unknown" (cmd/bd/main.go:813-841; configuration.md:101 and 266-279). The actor is written to `created_by`, `assignee` (claim), `events.actor`, the journal `actor`, and comment `author`.

## 10. Schema version and refusal

- Stored in table `schema_migrations` (column `version`). There is also `ignored_schema_migrations` for the dolt_ignored tables (internal/storage/schema/schema.go:313-318).
- Cheap read: `SELECT COALESCE(MAX(version),0) FROM schema_migrations` (schema.go:196). VERIFIED: **66** for v1.3.2. The latest migration file is `0066_add_events_journal_actor.up.sql`.
- `bd version --json` returns `{version, build, commit, branch, schema_version}`. Its `schema_version` is the JSON *output* schema (=1), not the DB schema (cmd/bd/version.go:43-56; output.go JSONSchemaVersion=1).
- DB newer than the binary: `SchemaSkewError` "schema version mismatch: database is at v%d, binary knows up to v%d (%d migration(s) ahead)" (schema.go:82-121 and 131-153).
  - JSON form on stderr: `{"error":…, "schema_skew":{"current_version","required_version","delta"}}` (cmd/bd/schema_skew.go:10-22).
  - Bypass: `BD_IGNORE_SCHEMA_SKEW=1` or `--ignore-schema-skew`.
- DB older than the binary, read-only open: `SchemaBehindError` (schema.go:165-210).
- Shared server: a newer bd **refuses to auto-migrate** an existing server database. The error is "refusing to auto-apply %d pending schema %s to a shared server database (v%d -> v%d): migrating would lock out every co-resident bd client still on the old schema (#5920)" (remote_migrate_gate.go:233-235 and 807-826).
  - It proceeds after explicit consent: `bd migrate` (the verb), `bd migrate --force`, or `BD_ALLOW_REMOTE_MIGRATE=1` (remote_migrate_gate.go:14-56).
  - Upgrade order for a shared server: upgrade all clients' bd, then migrate once.

## 11. Backup and restore

- `bd export [-o file] [--include-memories] [--include-infra] [--all] [--scrub]` writes JSONL, one issue per line, with `"_type":"issue"`. Each line embeds `labels`, `dependencies`, `comments`. Memories go out as `{"_type":"memory","key","value"}` only with `--include-memories`/`--all` (docs/cli-reference/export.md). VERIFIED.
  - It is "not a full database backup": no Dolt history, branches, or non-issue tables. The events audit table and journal are not exported, and I saw no export of config other than memories.
- `bd import [file|-] [--dry-run] [--dedup] [--allow-stale] [-i file]` upserts issues and turns memory lines into memories. Rows are applied only when `updated_at` is newer unless `--allow-stale` (docs/cli-reference/import.md). So export→import round-trips issues, labels, deps, comments and memories, but not events, history, or config.
- `bd backup init <path|dolthub-url>`, `sync`, `restore [path] [--force]`, `remove`, `status` (docs/cli-reference/backup.md).
  - These are Dolt-native: `CALL DOLT_BACKUP('add'|'sync'|'restore'|'rm', …)` (internal/storage/versioncontrolops/backup.go:18-49).
  - In server mode a filesystem path is resolved by the *server* (my inference from DOLT_BACKUP running server-side; not tested).
  - Restore requires an initialized database. Auto-backup is `backup.enabled` (default false, configuration.md:123-126).
- Dolt alternatives (outside bd): `dolt dump`, `CALL DOLT_BACKUP(...)` from any SQL client, or copying `/var/lib/dolt/<db>` with the server stopped. Not verified here. Ignored tables (events, journal, leases) are working-set only. I believe Dolt backups carry the working set (backup.md says "working-set data"), but that is unverified.

## 12. Network, telemetry, updates

- **Anonymous usage metrics are ON by default.**
  - Endpoint `https://gastownhall-eventsapi.com/mp/collect` (internal/metrics/metrics.go:17-21; internal/config/config.go:271-272).
  - VERIFIED: the first run wrote `~/.config/bd/config.yaml` with `metrics: {disabled: false, endpoint: …}` and queued `~/.beads/eventsData/*.evtq`. A background flusher process is spawned (internal/metrics/spawn.go).
  - Disable with **`BD_DISABLE_METRICS=1`** (bidirectional override), or `DO_NOT_TRACK=1` (disable-only), or `bd metrics off` (user-global config only) (cmd/bd/main.go:2430-2446). `BD_DISABLE_EVENT_FLUSH` disables the flusher (metrics.go:18).
  - Docs: docs/cli-reference/metrics.md ("which commands get run, plus the bd version and OS platform").
- OpenTelemetry is off unless `BD_OTEL_ENABLED=true` / `BD_OTEL_METRICS_URL` / `BD_OTEL_LOGS_URL` / `BD_OTEL_STDOUT` (internal/telemetry/telemetry.go:94-124).
- Update check: only `bd doctor` queries `https://api.github.com/repos/steveyegge/beads/releases/latest` (cmd/bd/doctor/version.go:229-236). It is skipped in machine-readable/non-interactive contexts (cmd/bd/doctor.go:660-668). I found no startup auto-update check.
- Other default network activity:
  - `bd init` in a git repo, when not stealth, reads `origin` and probes it for Dolt data (init.go:1263-1270).
  - `dolt.auto-push` defaults to false (configuration.md:110 and 246).
  - Federation and remotes are opt-in.
  - Integrations (GitHub/Jira/Linear/…) run only when invoked.
- VERIFIED: all commands worked on an `--internal` network with no internet.

## Not confirmed / caveats
- arm64 asset contents (glibc floor, behaviour) were not downloaded; only the checksum line was read.
- Dolt image docs on Docker Hub were not fetched. Image facts come from inspecting the image and its entrypoint.
- `bd reclaim`/`bd heartbeat` details and whether any path reclaims automatically: only partly read.
- Dolt's `--author` for server-mode `DOLT_COMMIT('-Am')`: observed `beads <beads@local>`. The exact code path that sets the session's commit identity was not traced.
- Whether `bd backup` with a filesystem path works against a remote server container, and whether `dolt dump`/`DOLT_BACKUP` include dolt_ignored tables: untested.
- There may be undiscovered git writes in subcommands other than `init`/`hooks`/`setup`.
