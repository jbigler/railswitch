# Shared agent memory: decisions made without asking

The design decisions are rows D136–D145 in
`docs/superpowers/plans/2026-10-25-railswitch-roadmap.md`, where this
repo keeps them. One line each here, then the smaller calls that don't
deserve a D number.

## In the roadmap

- **D136** One Dolt server per project, opt-in `[memory]`, private (no
  remote); started by `railswitch agent`, never by `up`; `down`/`wt rm`
  never touch it; `project rm --purge` removes its volume. *Only agents use
  it, and `up` never starts an agent.*
- **D137** The memory network is the agents' one firewall rule. *The hook
  resolves allow-list names on the host, which can't resolve a container;
  network membership is how every in-project service is already reached.*
  Conflict with the brief ("an allow rule via the profile mechanism") noted.
- **D138** bd in the agent base, downloaded and sha256-checked, only when
  `MEMORY=1`. *The binary is 154 MB; projects without memory shouldn't
  carry it.*
- **D139** Prime through `railswitch-prime` and the package's own hook
  (claude: SessionStart). *`[agents.<n>] setup` runs once per home and its
  output never reaches the agent's context.*
- **D140** `overview` says nothing about memory beyond a boolean from
  project.toml. *Every number needs a container run.*
- **D141** Root without a password. *Only agents reach it, and each may read
  and write all of it anyway.*
- **D142** Backup is `bd export --all` / `bd import --allow-stale`. *Verified
  end to end; Dolt-native `bd backup` wasn't.*
- **D143** Activity from bd's events journal (`BD_EVENTS_JOURNAL=1`, `seq`
  cursor). *Dolt commits name `beads`, not the agent.*
- **D144** Templates don't carry `[memory]`. *Already true of the code: the
  template's `stack.toml` struct has no such table.*
- **D145** doctor's `memory version`. *Running agents and the database's
  schema are what can disagree with the pin.*

## Smaller calls

| Call | Why |
|---|---|
| Cloned to `~/railswitch` as the brief says, though `~/Work/railswitch` already held a checkout | The brief named the path; the other checkout is untouched |
| The reproducible e2e is a Go integration test (`TestE2EMemory`), not a shell script | Every acceptance run in this repo is an `e2e_*_integration_test.go` with `RAILSWITCH_E2E_REPO`; "conventions win" |
| The test agent lives in `internal/lifecycle/testdata/agents/shell`, beside the stage 4 `probe` | Same convention |
| Demo app: `rails new -d postgresql` in a ruby:3.4 container, as docs/stage2-acceptance.md does | The lightest valid project the rails pack builds; nothing lighter exists |
| Demo project created with `--agent none`, then `agent add shell` | No credentials in the VM; claude was added later only to see its hook registered (`--version` with a dummy token) |
| Names: unit `<prefix>-memory.{network,container}`, network/container/volume `<prefix>_memory`, alias `memory`, database `beads`, issue prefix = project prefix | D6's naming: `_` for shared objects; a prefix has no `-`, so `<prefix>-memory` can't be a worktree container's name |
| `BEADS_DIR` read-only at `~/.railswitch-memory/beads` inside the agent's home | bd writes a lock *beside* `BEADS_DIR`; the home is writable, the config isn't |
| `beads.role` through `GIT_CONFIG_*`, without adding the host's git identity to agents that had none | bd warns on every command otherwise; nothing may be written into `.git` |
| `BEADS_ACTOR=<slug>/<agent>` | Gives `created_by`, the claim's assignee and the journal's actor the worktree for free |
| Server run as `tini -- dolt sql-server`, not the image's entrypoint | Found on the real run: the entrypoint exits 1 on SIGTERM without waiting for dolt |
| `memory` report: one container running every bd query (`sh -c`, one JSON line each) | One `podman run` (≈1.4 s) instead of six; the "argv only, never a shell" rule is for host commands (`task` runs `sh -c` in its container too) |
| Activity capped at 200 per report; the page shows the newest 50 | Bounded output; the cursor gets the rest |
| Unlabelled issues are counted but not listed per worktree | The brief scopes work by the `worktree:` label |
| Backups in `<workspace>/memory-backups/`, 0600 in a 0700 dir | Beside `db-dumps/`, but private: agents' notes |
| `memory restore` asks, as `db restore` does; `--yes` without a terminal | Same command shape as `db restore` (D121) |
| Settings ops `memory.on`, `memory.off`, `memory.guidance`; no dashboard Settings card | The JSON ops are the settings flow; a card is UI work beyond the brief's "where the patterns call for it" |
| The Memory page is read-once, not SSE-polled | A read costs a container run; polling it at overview's 2 s would be exactly what D140 avoids |
| `[memory]` without a schema bump | Optional tables have never bumped `schema` here; an older railswitch refuses the unknown table by name |
| Didn't fix `TestE2EAgents` (fails at `lock` on `main` too) | Pre-existing and outside the brief; noted in PROGRESS.md |
| Left the stale `rhdemo` registry entry (another session's) alone | Not mine; nothing of it runs |
