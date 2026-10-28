# AGENTS.md — railswitch

Go CLI (`railswitch`, module `github.com/jbigler/railswitch`) that runs Rails dev environments on rootless podman via Quadlet.
Spec: `handoff/HANDOFF.md`; decisions D1–D80: `docs/superpowers/plans/2026-10-25-railswitch-roadmap.md`.

## Commands
- `make check` before every commit: `gofmt -l` empty, `go vet ./...`, `staticcheck ./...`, `go test ./...`.
- `make build` → `./railswitch` (`VERSION=` sets `buildinfo.Version`).
- Integration tests carry `//go:build integration`; the full end-to-end test needs a systemd user session and is a manual run.

## CLI (Stage 4 additions)
- `agent [--new-term] [<name>] [-- args]` (runs `up` first; on-demand agents get a transient container, service agents are started then attached to; refused while another agent of the worktree runs, D62), `agent stop [<name>]`, `agent add <name> [--use <ref>] [--run on-demand|service]` (D63: adds `[agents.<name>]`, declares its package secrets, adds it to git-host secrets' services, locks and ejects missing agent Containerfiles; workspace-wide), `--json agent ls` (rows carry `running`); `up` never starts an agent; `project new --agent name=ref` (repeatable) or `--agent none` (default: the built-in package with `default = true`).
- `nvim`, `nvim update`, `nvim reset-config [--yes]`; `hooks check | backup [<hook>…] | restore [--force]`; `db dump` (every database the option's `[dump] list` prints, one `<db>.dump` each; files 0644 in `<ws>/db-dumps/`, dir 0755); `secret add --git-signing` (key piped on stdin).
- `project.toml`: `[tools.nvim] config_repo`/`config_ref` (replaces `[tools] nvim = true`, which is refused); `[agents.<n>] use` (`<name>@N` or `local:<name>@N` → `<ws>/agents/<name>/`), `run` (`on-demand`|`service`), `env`, `allow`.
- `config.toml` `[[mount]]` (D57): machine-local mounts per service (`relabel = false` drops `z`; see docs/nvim.md); `doctor` checks them (`machine mounts`).
- `doctor` checks carry `warn` (JSON `"warn"`): a warning prints `warn` and doesn't change the exit code; `git-guard` findings are warnings.
- Agents (D55, C7, C10): service `agent-<n>`, own firewall profile `agent-<n>` (allow file `agent-<n>.allow`), always `enforce`. Home: the bind dir `<ws>/.railswitch/agent-home/<slug>/<n>` at `/home/appuser` (`z`), apart from nvim's `.railswitch/home/<slug>`; render pre-creates every mount point under a home (else the runtime makes them root-owned); `down` keeps it, `wt rm --force` deletes it. git-guard: every writable `{base.git}` mount gets `{base.git}/hooks` mounted `ro,z` on top.
- Genericity: no `filial` in non-test code or built-in packs, `claude` only under `internal/pack/builtin/agents/claude/`, terminal names only in `internal/term/` (`TestGenericity` in `internal/`).

## CLI (Stage 6a additions)
- `pr ls` (project; `gh pr list` with the repo from `project.toml`, never a worktree's git config), `task ls` and `task [--new-term] <name> [args…]` (worktree; `sh -c '<command> "$@"' <name> <args…>` in the app as `exec` runs, the task's exit code; a `--` right after the name is dropped), `wt add [--name <n>] [--up] <target>` (`--up`: `up` in the new worktree).
- `dashboard` (prints the login link), `dashboard colors [set [--dark] [--base <p>] [<tok>=#rrggbb…] | reset [--dark]]` (`~/.config/railswitch/dashboard.toml`; reset keeps the preset), `dashboard serve` (the dashboard unit's ExecStart): `internal/web` on `127.0.0.1:7700`; jobs get the session's display/agent/terminal variables from `systemctl --user show-environment`; exits 0 when its executable's size or mtime changes while no job runs. The dashboard as a whole: `docs/dashboard.md`.
- `project.toml`: `[tasks]` (`name = "command"`, `""` removes a pack task; `ls` is refused here and in a pack's `[tasks]`), `hide_links = [labels]` (an unknown stack-link label is a render error), `[[links]]`.

## CLI (Stage 6b additions)
- `settings preview` (`--json`: hash, diff, per-edit `impact`, `restart`) and `settings apply --if-hash <h> [--restart]`: an edit list (JSON array, D84) on stdin; apply refuses on a hash mismatch or a blocked edit, writes `project.toml`, locks, renders, and on failure restores `project.toml` and `stack.lock`; `--restart` restarts the previewed worktrees in order, stopping at the first failure. `project show` (`--json`: options with choices, env, links with `from`/`hidden`) and `project write --if-hash <h>` (the whole file on stdin, validated and resolved first, same commit). Preview reads app unit states with one `systemctl show`.

## CLI (Stage 6c additions)
- `project detect <repo> [--branch <b>] [--pack <ref>]` (D88): `internal/detect`; a shallow blobless sparse clone of only the paths the built-ins' rules name (`detect.Paths`), git with `core.hooksPath=/dev/null`, `protocol.ext.allow=never`, `GIT_LFS_SKIP_SMUDGE=1`, no prompt, 2 min per call; temp dir under `$XDG_RUNTIME_DIR` always removed; a symlinked file or parent, or a file over 1 MiB, is absent.
- `pack ls` (built-ins newest per name, then the workspace's `local:` ones; for the new-project pickers).
- Packages: `[detect] files` (pack, all must exist), `[[detect]] file/match/evidence` (option, any rule); paths are `[A-Za-z0-9._+-/]` only (passed as `/<path>` no-cone sparse patterns). Framework knowledge stays in the rules (D87).

## Global constraints
- Paths honour `XDG_CONFIG_HOME`/`XDG_DATA_HOME`: config `~/.config/railswitch/`, data `~/.local/share/railswitch/`, Quadlet output `~/.config/containers/systemd/`.
- Commands: argv only, never a shell; every call through `sys.Runner` with a timeout (`sys.Cmd.Timeout`, default 30 s) or explicit `sys.NoTimeout` (`logs -f`, `exec`, `deps`, `agent`, `nvim`). No podman Go bindings.
- Secrets (D33): values only on stdin — never argv, written files, env files, logs or job output. No host SSH agent, `~/.ssh` or host gitconfig mounted into containers. Commit signing is opt-in (D61): a file-delivered secret with `git_signing = true` (`secret add --git-signing`) holds a signing-only SSH key; its services get `gpg.format=ssh`, `user.signingkey=/run/secrets/<name>`, `commit.gpgsign`/`tag.gpgsign` via `GIT_CONFIG_*`.
- Dependencies: stdlib + `github.com/BurntSushi/toml` only. No-echo prompt uses `syscall` TCGETS/TCSETS.
- Hardening (D34): every container has `NoNewPrivileges=true`, `Memory=`, `PidsLimit=`; app/deps/worker/tool/agent also `DropCapability=all`; browser keeps `AddCapability=NET_ADMIN`/`NET_RAW`, no `DropCapability`.
- Schema (D39): `project.toml`, `stack.lock`, `registry.toml`, `config.toml`, `dashboard.toml` carry `schema = 1`; a higher schema is refused ("written by a newer railswitch").
- `--json` (D40): every read command (`env`, `status`, `overview`, `wt ls`, `project ls`, `secret ls`, `containerfile list`, `pack show`, `pack ls`, `doctor`, `machine`, `agent ls`, `pr ls`, `task ls`, `dashboard colors`, `settings preview`, `project show`, `project detect`) prints one JSON document; schemas in `docs/json.md`.
- Unit tests need no podman, systemd, gh or network (`git`, `bash` allowed; use `internal/sys/systest` Fake).
- Ports publish only on `127.0.0.1:<host>:<ctr>`.
- Remove objects by exact name, never `--filter`/regex. Never `git worktree prune`.
- Files inside a worktree are read only via `safefile.Read` (regular files, ≤ 1 MiB); text from them (or from a status file) printed on a terminal goes through `safefile.Printable` (branch names included).
- Pinned: subnet pool `10.214.0.0/16`, one `/24` per project (skipping /24s any existing podman network uses), gateway `.1`, Traefik `.2`, dynamic `10.214.<n>.128/25`; `10.214.255.0/24` reserved for the machine network `railswitch_machine` (gateway `.1`, Traefik `.2`, Dozzle `.3`), never a project's; every railswitch network (proxy, dev, machine) `isolate=strict`; dev network MTU 1400; ports DB 55432+ID, Ruby debug 33000+ID, Neovim 17000+ID, Playwright 18888+ID, App share 13000+ID, Storage share 19000+ID; two different port bases on one machine ≥ 100 (`MaxWorktreeID + 1`) apart; worktree slug ≤ 40 chars; `LogDriver=journald` for long-running units, `passthrough` for oneshots (D64).
- railswitch's own CI lives in `.github/workflows/`; railswitch never generates or touches a project's CI (D41).
- Dashboard (D73, D74): unit `railswitch-dashboard.service` (plain systemd user unit, `Wants=` from every app unit, never blocks `up`) on `127.0.0.1:7700` (`http://railswitch.localhost:7700/`); token `~/.local/share/railswitch/dashboard/token` 0600; every request needs the cookie, POST also `Origin`.

## Package map
- `cmd/railswitch` — entry point → `cli.Main`.
- `internal/cli` — flag dispatch, one file per command group.
- `internal/sys` (+ `systest`) — Runner/Cmd/Result, Fake runner; `Cmd.KillGrace` (own process group, SIGTERM then SIGKILL on cancel).
- `internal/jobs` — the dashboard's job hub: one child per job in a `systemd-run --user --scope`, log file 0600, kept output capped, one job per worktree (project-level jobs block the project), last 20 kept.
- `internal/prs` — `pr ls`: `gh pr list` JSON → per-branch PR state, checks and review; the repo from `project.toml`'s URL; fork PRs (`isCrossRepository`) ignored.
- `internal/settings` — `settings preview`: the edit list (strict JSON), applied by `config`'s line editor; each edit judged against the project with the edits before it, by rendering both in memory (`workspace.WithProject`, no stack.lock write) and comparing every worktree's non-cache volumes and the app's env; restart = running worktrees whose stamped units change.
- `internal/dashconf` — `dashboard.toml`: presets, per-mode token overrides, set/reset, contrast warnings.
- `internal/web` — the dashboard: guard (Host, token cookie, Origin on POST, CSP), pollers (`overview` 2 s with a page open, else 10 s; `pr ls` 60 s), SSE fragment diffs and notifications, actions as `internal/jobs` jobs. Reads only through `railswitch --json` (its `CLI`), never a package's files; templates and static files embedded; no inline script or style (colours at `/theme.css`).
- `internal/safefile` — hostile-file reads; `Printable` (control characters → spaces). `internal/naming` — sanitize, object/unit names.
- `internal/placeholder`, `internal/config`, `internal/pack` (+ `builtin`; the package format and local packs: `docs/packs.md`), `internal/version`.
- `internal/machine` — dirs, registry, proxy, machine config.
- `internal/render` — model, env, images, quadlet, lint, diff, apply.
- `internal/unithash` — the `<prefix>.unit-hash` label that tells `up` a container runs an older unit.
- `internal/tailscale` — the tailscale CLI for `share`/`unshare` (`serve --bg`, `serve … off`, never `reset`).
- `internal/agentstatus` — the agents' status files (D58), read as hostile.
- `internal/gitguard` — git-guard (D60): installed hooks vs. their `githooks/` backups, key names (never values) in `.git/config`, `.git/config.worktree`, `.git/worktrees/*/config.worktree` and `.git/modules/**/config`; no `.git/commondir` (nor in `modules/**`); `.git/worktrees/*/commondir` is `../..` and `*/gitdir` a registered worktree's `.git`; worktree `.git` pointers name such an entry; any hook in `.git/modules/**/hooks/` (`submodule-hook`); `.git/worktrees`, `.git/modules` and the dirs under them are real directories, never symlinks (`not-a-dir`); `core.worktree` is allowed only in `modules/**/config`.
- `internal/detect` — `project detect`: `Paths`, `Fetch` (hostile repository: named files only), `Choose` (pure).
- `internal/term` — `--new-term`: detects the Linux terminal (C5) and opens a detached window through `sys.Runner`.
- `internal/podman`, `internal/firewall`, `internal/worktree`, `internal/workspace`, `internal/lifecycle`, `internal/doctor`.

## Rules
- Render lints are the gotchas from HANDOFF §7.
- Every behaviour change updates the relevant doc in `docs/`.
