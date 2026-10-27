# AGENTS.md — railswitch

Go CLI (`railswitch`, module `github.com/jbigler/railswitch`) that runs Rails dev environments on rootless podman via Quadlet.
Spec: `handoff/HANDOFF.md`; decisions D1–D53: `docs/superpowers/plans/2026-10-25-railswitch-roadmap.md`.

## Commands
- `make check` before every commit: `gofmt -l` empty, `go vet ./...`, `staticcheck ./...`, `go test ./...`.
- `make build` → `./railswitch` (`VERSION=` sets `buildinfo.Version`).
- Integration tests carry `//go:build integration`; the full end-to-end test needs a systemd user session and is a manual run.

## Global constraints
- Paths honour `XDG_CONFIG_HOME`/`XDG_DATA_HOME`: config `~/.config/railswitch/`, data `~/.local/share/railswitch/`, Quadlet output `~/.config/containers/systemd/`.
- Commands: argv only, never a shell; every call through `sys.Runner` with a timeout (`sys.Cmd.Timeout`, default 30 s) or explicit `sys.NoTimeout` (`logs -f`, `exec`, `deps`, `agent`, `nvim`). No podman Go bindings.
- Secrets (D33): values only on stdin — never argv, written files, env files, logs or job output. No host SSH agent, `~/.ssh` or host gitconfig mounted into containers.
- Dependencies: stdlib + `github.com/BurntSushi/toml` only. No-echo prompt uses `syscall` TCGETS/TCSETS.
- Hardening (D34): every container has `NoNewPrivileges=true`, `Memory=`, `PidsLimit=`; app/deps/worker/tool also `DropCapability=all`; browser keeps `AddCapability=NET_ADMIN`/`NET_RAW`, no `DropCapability`.
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
- `internal/podman`, `internal/firewall`, `internal/worktree`, `internal/workspace`, `internal/lifecycle`, `internal/doctor`.

## Rules
- Render lints are the gotchas from HANDOFF §7.
- Every behaviour change updates the relevant doc in `docs/`.
