# Shared agent memory: progress log

(Summary at the top is written last.)

## M1: environment, repo, conventions (2026-10-31)

- VM: KVM guest (Omarchy/Arch, kernel 7.2.8), 6 CPUs, 7 GiB RAM, 13 GiB free
  on /home. Already installed: Go 1.27.3 (go.mod: 1.27.3), podman 6.1.4
  with Quadlet, nft, jq, git, curl, ssh-keygen. Nothing had to be installed
  for `railswitch doctor`.
- Repo: cloned https://github.com/jbigler/railswitch to `~/railswitch`
  (at 12e724b), branch `shared-memory`. `make build` builds `./railswitch`.
- `./railswitch doctor` before any change: every check `ok` except three
  warnings that don't fail it: dashboard not enabled, dashboard not
  answering (not started), linger off. VM-specific: none of these matter
  for the work; the dashboard was not started.
- VM state found: the registry (`~/.local/share/railswitch/registry.toml`)
  holds a project `rhdemo` whose workspace (under another session's /tmp
  scratchpad) no longer exists, with its unit files still in
  `~/.config/containers/systemd/` and no containers or volumes. Left alone.
- Conventions noted: decisions are rows `| Dnn | Topic | Decision | Why |` in
  `docs/superpowers/plans/2026-10-25-railswitch-roadmap.md` (last: D135);
  per-stage additions are a section in `AGENTS.md`; every `--json` read
  command is listed in AGENTS.md's D40 line and has a `## ` section in
  `docs/json.md` (example, then a field table); built-in packages under
  `internal/pack/builtin/` are locked and reviewed (D96, D107: the agent base
  is `[base.agent]` in `stack.lock`), so a change to them shows up as a
  pending review in every project that uses them; hostile files go through
  `safefile`; every command through `sys.Runner` with a timeout; unit tests
  use `systest.Fake` (no podman, systemd or network).

## M2: beads researched and pinned

- bd **v1.3.2** (2026-10-24, commit 9b65b9e1), Dolt server **2.2.0** (the
  version beads pins). Full research with file:line citations:
  `beads-research-notes.md`; what railswitch uses and where each name is
  confirmed: `BEADS.md`.
- Surprises that shaped the design: bd sends usage metrics by default
  (`BD_DISABLE_METRICS=1` everywhere); without `BEADS_DOLT_SERVER_MODE=1` bd
  may start its own Dolt; `bd init` writes into the cwd's git repo unless run
  outside one; the binary is 154 MB (so it is installed only for [memory]
  projects); a newer bd refuses to auto-migrate a shared server's database.

## M3: Dolt server as a project-level unit

- `<prefix>-memory.network` (`<prefix>_memory`, Internal, isolate=strict) and
  `<prefix>-memory.container` (`<prefix>_memory`, volume `<prefix>_memory`).
  Started by `railswitch agent` (and memory backup/restore/migrate), never by
  up; down and wt rm never touch it; project rm stops it, `--purge` removes
  the volume. VERIFIED on the demo: wt rm of a worktree left server, volume
  and data intact.
- Found on the real run: the image's entrypoint exits 1 on SIGTERM without
  waiting for dolt; the unit now runs `tini -- dolt sql-server` (clean stop,
  exit 0). VERIFIED.

## M4: bd in the agent base, env, stealth, firewall

- Agent base installs bd only with `MEMORY=1` (railswitch passes it for
  [memory]), sha256-checked; labels `railswitch.memory`,
  `railswitch.bd.version`. VERIFIED: `bd version` in the demo's agent prints
  1.3.2.
- Env at layer option; BEADS_DIR read-only inside the agent's home (bd puts a
  gate lock beside it); `no-git-ops: true` in config.yaml (what `--stealth`
  sets); beads.role via `GIT_CONFIG_*` (no warnings, nothing written).
  VERIFIED: prime shows "Git workflow: stealth mode (no git ops)", stderr empty.
- Firewall: the agent's hook log shows exactly one more local subnet, the
  memory network's (10.89.2.0/24, Internal, only server + agents); the app
  cannot resolve `memory`; nothing published (ports exposed, unbound).

## M5: opt-in, prime, labels, guidance

- `[memory]` (optional `guidance`, ≤ 4 KiB). Prime: `railswitch-prime`
  (guidance, the `worktree:<slug>` convention with the real slug, then
  `bd prime`), registered by claude@1 as a SessionStart hook. VERIFIED with
  the shell test agent: prime output in worktree B lists the memory A wrote.
- Not done: no Settings op / dashboard card for [memory] yet (see M9).

## M6 (manual, on the demo): every property VERIFIED by hand

- Demo: `~/rs-demo/src/demo` (rails new, postgres), workspace `~/rs-demo/ws`,
  prefix `rsmem`, worktrees main and feat-b, test agent `shell`
  (`testdata/fixtures/memory/agents/shell`).
- A remembers + creates; B's prime shows the memory, `bd list` shows A's issue
  (`created_by: main/shell`). Race: both claim at once, B exit 0, A exit 1
  ("issue already claimed by feat-b/shell"). Backup → stop → `podman volume
  rm` → restore: 2 issues and 1 memory back. wt rm feat-b: memory intact.
- The reproducible script is the integration test (next entry).

## M7: `railswitch --json memory`

- One `podman exec` (schema version) plus one transient container that runs
  every bd query; 1.4 s on the demo. Activity from `bd_events_journal`
  (`BD_EVENTS_JOURNAL=1` in every agent) with a `--since` cursor. VERIFIED.
- `overview` is unchanged: nothing about memory there (see D140).
