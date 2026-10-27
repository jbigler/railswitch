# AGENTS.md — railswitch

Go CLI (`railswitch`, module `github.com/jbigler/railswitch`) that runs Rails dev environments on rootless podman via Quadlet.
Spec: `handoff/HANDOFF.md`; decisions D1–D62: `docs/superpowers/plans/2026-10-25-railswitch-roadmap.md`.

## Commands
- `make check` before every commit: `gofmt -l` empty, `go vet ./...`, `staticcheck ./...`, `go test ./...`.
- `make build` → `./railswitch` (`VERSION=` sets `buildinfo.Version`).
- Integration tests carry `//go:build integration`; the full end-to-end test needs a systemd user session and is a manual run.

## CLI (Stage 4 additions)
- `agent [--new-term] [<name>] [-- args]` (runs `up` first; on-demand agents get a transient container, service agents are started then attached to; refused while another agent of the worktree runs, D62), `agent stop [<name>]`, `--json agent ls` (rows carry `running`); `up` never starts an agent; `project new --agent name=ref` (repeatable) or `--agent none` (default: the built-in package with `default = true`).
- `nvim`, `nvim update`, `nvim reset-config [--yes]`; `hooks check | backup [<hook>…] | restore [--force]`; `db dump` (files 0644 in `<ws>/db-dumps/`, dir 0755); `secret add --git-signing` (key piped on stdin).
- `project.toml`: `[tools.nvim] config_repo`/`config_ref` (replaces `[tools] nvim = true`, which is refused); `[agents.<n>] use` (`<name>@N` or `local:<name>@N` → `<ws>/agents/<name>/`), `run` (`on-demand`|`service`), `env`, `allow`.
- `config.toml` `[[mount]]` (D57): machine-local mounts per service (`relabel = false` drops `z`; see docs/nvim.md); `doctor` checks them (`machine mounts`).
- `doctor` checks carry `warn` (JSON `"warn"`): a warning prints `warn` and doesn't change the exit code; `git-guard` findings are warnings.
- Agents (D55, C7, C10): service `agent-<n>`, own firewall profile `agent-<n>` (allow file `agent-<n>.allow`), always `enforce`. git-guard: every writable `{base.git}` mount gets `{base.git}/hooks` mounted `ro,z` on top.
- Genericity: no `filial` in non-test code or built-in packs, `claude` only under `internal/pack/builtin/agents/claude/`, terminal names only in `internal/term/` (`TestGenericity` in `internal/`).

## Global constraints
- Paths honour `XDG_CONFIG_HOME`/`XDG_DATA_HOME`: config `~/.config/railswitch/`, data `~/.local/share/railswitch/`, Quadlet output `~/.config/containers/systemd/`.
- Commands: argv only, never a shell; every call through `sys.Runner` with a timeout (`sys.Cmd.Timeout`, default 30 s) or explicit `sys.NoTimeout` (`logs -f`, `exec`, `deps`, `agent`, `nvim`). No podman Go bindings.
- Secrets (D33): values only on stdin — never argv, written files, env files, logs or job output. No host SSH agent, `~/.ssh` or host gitconfig mounted into containers. Commit signing is opt-in (D61): a file-delivered secret with `git_signing = true` (`secret add --git-signing`) holds a signing-only SSH key; its services get `gpg.format=ssh`, `user.signingkey=/run/secrets/<name>`, `commit.gpgsign`/`tag.gpgsign` via `GIT_CONFIG_*`.
- Dependencies: stdlib + `github.com/BurntSushi/toml` only. No-echo prompt uses `syscall` TCGETS/TCSETS.
- Hardening (D34): every container has `NoNewPrivileges=true`, `Memory=`, `PidsLimit=`; app/deps/worker/tool/agent also `DropCapability=all`; browser keeps `AddCapability=NET_ADMIN`/`NET_RAW`, no `DropCapability`.
- Schema (D39): `project.toml`, `stack.lock`, `registry.toml`, `config.toml` carry `schema = 1`; a higher schema is refused ("written by a newer railswitch").
- `--json` (D40): every read command (`env`, `status`, `wt ls`, `project ls`, `secret ls`, `containerfile list`, `doctor`, `machine`, `agent ls`) prints one JSON document; schemas in `docs/json.md`.
- Unit tests need no podman, systemd, gh or network (`git`, `bash` allowed; use `internal/sys/systest` Fake).
- Ports publish only on `127.0.0.1:<host>:<ctr>`.
- Remove objects by exact name, never `--filter`/regex. Never `git worktree prune`.
- Files inside a worktree are read only via `safefile.Read` (regular files, ≤ 1 MiB).
- Pinned: subnet pool `10.214.0.0/16`, one `/24` per project (skipping /24s any existing podman network uses), gateway `.1`, Traefik `.2`, dynamic `10.214.<n>.128/25`; `10.214.255.0/24` reserved for the machine network `railswitch_machine` (gateway `.1`, Traefik `.2`, Dozzle `.3`), never a project's; every railswitch network (proxy, dev, machine) `isolate=strict`; dev network MTU 1400; ports DB 55432+ID, Ruby debug 33000+ID, Neovim 17000+ID, Playwright 18888+ID, App share 13000+ID, Storage share 19000+ID; two different port bases on one machine ≥ 100 (`MaxWorktreeID + 1`) apart; worktree slug ≤ 40 chars; `LogOpt=max-size=20m`.
- railswitch's own CI lives in `.github/workflows/`; railswitch never generates or touches a project's CI (D41).

## Package map
- `cmd/railswitch` — entry point → `cli.Main`.
- `internal/cli` — flag dispatch, one file per command group.
- `internal/sys` (+ `systest`) — Runner/Cmd/Result, Fake runner.
- `internal/safefile` — hostile-file reads. `internal/naming` — sanitize, object/unit names.
- `internal/placeholder`, `internal/config`, `internal/pack` (+ `builtin`), `internal/version`.
- `internal/machine` — dirs, registry, proxy, machine config.
- `internal/render` — model, env, images, quadlet, lint, diff, apply.
- `internal/unithash` — the `<prefix>.unit-hash` label that tells `up` a container runs an older unit.
- `internal/tailscale` — the tailscale CLI for `share`/`unshare` (`serve --bg`, `serve … off`, never `reset`).
- `internal/agentstatus` — the agents' status files (D58), read as hostile.
- `internal/gitguard` — git-guard (D60): installed hooks vs. their `githooks/` backups, key names (never values) in `.git/config`, `.git/config.worktree`, `.git/worktrees/*/config.worktree` and `.git/modules/**/config`; no `.git/commondir` (nor in `modules/**`); `.git/worktrees/*/commondir` is `../..` and `*/gitdir` a registered worktree's `.git`; worktree `.git` pointers name such an entry; any hook in `.git/modules/**/hooks/` (`submodule-hook`).
- `internal/term` — `--new-term`: detects the Linux terminal (C5) and opens a detached window through `sys.Runner`.
- `internal/podman`, `internal/firewall`, `internal/worktree`, `internal/workspace`, `internal/lifecycle`, `internal/doctor`.

## Rules
- Render lints are the gotchas from HANDOFF §7.
- Every behaviour change updates the relevant doc in `docs/`.
