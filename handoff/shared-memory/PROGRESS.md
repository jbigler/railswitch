# Shared agent memory: progress log

## Summary (read this first)

All ten milestones are done; the end-to-end acceptance run passes. Branch
`shared-memory` in `~/railswitch`, 14 commits on 12e724b, nothing pushed.

**Review first**
1. The decisions: `docs/superpowers/plans/2026-10-25-railswitch-roadmap.md`
   D136–D145, plus the smaller calls in `DECISIONS.md` here. D137 (the memory
   network *is* the firewall rule), D138 (bd only for `[memory]` projects) and
   D141 (root without a password) are the ones to argue with.
2. `internal/render/memory.go` (units, env, mounts) and
   `internal/lifecycle/memory.go` (start, init, report, backup, doctor check).
3. `internal/pack/builtin/agentbase/Containerfile.agent-base`: the download and
   checksum lines. Every project with agents gets a pending review of the
   agent base after this lands (any built-in change does).

**Run the end-to-end check**
```bash
RAILSWITCH_E2E_REPO=$HOME/rs-demo/src/demo RAILSWITCH_E2E_PREFIX=s9mem \
  go test -tags integration ./internal/lifecycle/ -run E2EMemory -timeout 60m -v
```
(`~/rs-demo/src/demo` is a `rails new -d postgresql` app; any Rails repo
works. About 5 minutes with images cached.) The hand-driven demo is
`~/rs-demo/ws` (prefix `rsmem`, worktree `main`; `cd ~/rs-demo/ws/main &&
railswitch agent shell`).

**What I'd do next**
1. A real model run: Claude with a token in two worktrees, to see whether
   agents actually follow the label and claim conventions from the prime
   text, and tune the wording. (Needs agent credentials, which this VM
   doesn't have.)
2. Prime for agents other than claude: every agent of a `[memory]` project
   gets bd, the env and `/usr/local/bin/railswitch-prime`, but only claude@1
   runs it at session start; another package needs its own hook or
   entrypoint line.
3. A `memory migrate` dry run in doctor, for the first real bd upgrade.

Done since the first handoff (2026-11-01): the Memory page reads its live
part every 15 s while shown and highlights activity after its load cursor
(checked in headless Chromium over the DevTools protocol); removing
`[memory]` already stopped the server through render (D28), so only the
docs changed (checked on the demo: off stopped it, on brought every
memory back).

Not done or not verified: `memory migrate` never ran against a real newer bd;
the arm64 checksum is taken from `checksums.txt` but its tarball was never
downloaded; the bd backup is JSONL (no Dolt history, no activity journal,
D142); a guidance edit restarts every running worktree (the existing
asset rule).

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

## M6: the end-to-end run, reproducible

- `internal/lifecycle/e2e_memory_integration_test.go` (`TestE2EMemory`), the
  repo's form for acceptance runs (see DECISIONS.md). **PASS** in 271 s on a
  fresh project (`s9mem`), cleanup by `project rm --purge` leaves nothing:
  - without `[memory]`: no units, no network, no `bd`/`BEADS_DIR`/`railswitch-prime`
    in the agent; the hook allows exactly the dev and proxy subnets;
  - A's memory and issue show in B's prime and `bd list` (`created_by` A);
  - the hook allows exactly dev, proxy and the memory subnet; the memory
    network is internal; the app can't resolve `memory`; nothing published;
  - two agents claim one issue at the same second: exactly one exits 0, the
    other says "already claimed";
  - `memory --json`: counts, the claim, activity with worktree/agent; `--since`
    the cursor returns nothing new;
  - `wt rm --force` of B: everything still there;
  - backup (0600), stop, `podman volume rm`, restore: 2 issues, 1 in
    progress, 1 memory.
- The first run failed one assertion of mine (it read B's hook line as A's);
  fixed in the test, not the code.

## M8: doctor version check, backup and restore

- `doctor` → `memory version` (D145). VERIFIED on the demo: ok normally; with a
  `schema_migrations` row one past bd 1.3.2's it fails with bd's own line
  ("schema version mismatch: database is at v67, binary knows up to v66") and
  the fix; the row was removed after. A pin change can't leave stale *built*
  images (the tag covers the Containerfile), so the check looks at running
  agent containers and at whether the pin is installed at all.
- Backup/restore: see M6. `memory migrate` (`bd migrate schema`) is unit-tested
  only: there was no newer bd to migrate to.

## M9: docs and tests

- Docs: `docs/guide/agents.md` (Shared memory), `docs/json.md` (`memory`,
  `doctor`'s new check, the Settings ops, `project show`'s and `overview`'s new
  fields), `docs/firewall.md`, `docs/packs.md` (what an agent package does with
  `railswitch-prime`), `docs/dashboard.md` (Memory page), AGENTS.md (Stage 9w),
  roadmap D136–D145.
- Settings: `memory.on`, `memory.off`, `memory.guidance` (preview/apply),
  `project show --json` `memory`. VERIFIED through the real `settings preview`.
- `make check` (gofmt, vet, staticcheck, all unit tests): green.
- `doctor` after: every check ok in the demo workspace (two unrelated
  warnings as before: dashboard not enabled, linger off).
- Real Claude Code (2.1.316, a dummy token for `--version`) in the demo
  registered `railswitch-prime` as its SessionStart hook. Claude itself was
  never run against a model (no credentials, by design).
- Regression: `TestE2EAgents` fails at its `lock` step, **on `main` too**
  (checked in a worktree at 12e724b): since D97 a plain `lock` refuses a new
  package and prints the review. Pre-existing, not touched. Its cleanup left
  projects `s4a`/`s4b` registered; removed by hand.

## M10 (stretch): dashboard Memory page

- `/p/<prefix>/memory`, read-only, one `memory --json` per load (not polled,
  D140); header link only for `[memory]` projects, from a new `overview`
  boolean read from project.toml. VERIFIED: served from this build against the
  demo (then the VM's own dashboard unit, v0.1.6, was started again).
