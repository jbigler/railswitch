# AGENTS.md: railswitch

railswitch is a Go CLI (module `github.com/jbigler/railswitch`) that runs Rails development environments on rootless podman through Quadlet.
This file lists the rules a change can break. To learn how a feature behaves, read its doc:

- Decisions D1 and later, each with its reason: `docs/superpowers/plans/2026-10-25-railswitch-roadmap.md`. Search for `| D<n> |`.
- The spec, and in its §7 the gotchas that render lints check: `handoff/HANDOFF.md`.
- The project's own terms: `GLOSSARY.md`. Add a term there when a change names a new concept.
- `--json` schemas: `docs/json.md`.
- Features: `docs/dashboard.md`, `docs/packs.md`, `docs/templates.md`, `docs/libraries.md`, `docs/firewall.md`, `docs/nvim.md`, and `docs/completion.md`. The user guide is `docs/guide/`.

## Commands
- `make check` runs `gofmt -l` (must print nothing), `go vet ./...`, `staticcheck ./...`, `go test ./...`, and `node --test internal/web/jstest/*.test.js`. Staticcheck runs through `go run` at the Makefile's `STATICCHECK_VERSION`, so there is nothing to install. The JS tests need Node 18 or later and no packages.
- The dashboard's JS logic that needs no page lives in `internal/web/static/logic.js` (an ES module `app.js` imports); test it in `internal/web/jstest/` with `node:test`. No npm, no `package.json`.
- `make build` writes `./railswitch`. `VERSION=` sets `buildinfo.Version`.
- `--help`: every `e.flags(name)` has a line in `internal/cli/help.go` `synopses` with its arguments (`TestEveryFlagSetHasSynopsis` checks). Flags print with `--`; one dash still parses.
- Integration tests carry `//go:build integration`. The full end-to-end test needs a systemd user session, so you run it by hand.

## Every change
- Run `make check` before every commit.
- A behaviour change updates its doc in `docs/`. A new design choice gets a Dnn row in the roadmap.
- Every read command takes `--json` and prints one JSON document (D40). Its schema goes in `docs/json.md`.
- A read the dashboard makes has a file in `testdata/contract/` (the dashboard contract, D197). A new or changed read: add the file with its arguments on line 1, run `go test ./internal/cli -run TestDashboardContract -update`, then `go test ./internal/web`.
- Dependencies are the standard library, `github.com/BurntSushi/toml` and `github.com/coder/websocket` (the dashboard's terminals, D149: a WebSocket parser is security-sensitive); a new one needs as good a reason. The no-echo prompt uses `syscall` TCGETS and TCSETS.
- Unit tests need no podman, systemd, gh, or network. `git` and `bash` are allowed. Fake commands with `internal/sys/systest`.
- `project.toml`, `stack.lock`, `registry.toml`, `config.toml`, and `dashboard.toml` carry `schema = 1`. A file with a higher schema is refused as "written by a newer railswitch" (D39).

## Security
- Secret values travel only on stdin (D33). They never appear in argv, written files, env files, logs, or job output.
- Containers never get the host SSH agent, `~/.ssh`, or the host gitconfig.
- Commands run from argv, never through a shell. Every call goes through `sys.Runner` with a timeout (`sys.Cmd.Timeout`, 30 s by default) or an explicit `sys.NoTimeout`. Don't use the podman Go bindings.
- Git calls that reach a remote pass `sys.GitProtocols` (https, ssh, git, and file only). Check each user value with `sys.GitArg`, which refuses a leading `-` and a `<transport>::` prefix.
- Read a file inside a worktree only with `safefile.Read`. Pass text from a worktree or a status file through `safefile.Printable` before you print it on a terminal. Branch names count.
- Cloned repositories, library checkouts, and imported folders are hostile. Read them only through the existing readers: `detect.Fetch`, `libraries.Fetch`, `template.ReadIncoming`, and `readIncomingPackage`. These refuse symlinks, paths that `pack.CheckFilePath` or `template.CheckPath` refuse, and files over their size caps.
- A tool that reads a worktree's git directory, such as `gh stack`, runs through `internal/gitshadow`. Containers can write the worktree's `.git`.
- Ports publish only on `127.0.0.1:<host>:<ctr>`.
- Remove objects by exact name. Never use `--filter` or a regex, and never run `git worktree prune`.
- Every container has `NoNewPrivileges=true`, `Memory=`, and `PidsLimit=` (D34). App, deps, worker, tool, and agent containers also have `DropCapability=all`. The browser keeps `AddCapability=NET_ADMIN` and `NET_RAW`, with no `DropCapability`.
- `hook.sh` and `firewall.sh` run with `umask 077` (D117). The hook keeps `state/` at 0700 and its files and `hook.log` at 0600.
- Every dashboard request needs the token cookie, and every POST also needs `Origin`. Pages have no inline script or style. The dashboard reads only through `railswitch --json`, never a package's files. One exception (D153): it serves an installed office set's files, those its `index.json` lists, from `<data>/office/`, which only `office set add` writes after its checks.

## Code conventions
- Build every worktree path with `naming.WorktreeDir(ws, slug)`, never `filepath.Join(ws, slug)` (D143).
- Write project files through `env.commit` in `internal/cli/settings.go`. It writes every changed file, then `stack.lock`, then renders. If any step fails, it puts each file back byte for byte.
- Copy ejected files from `workspace.Accepted`, never from the raw catalog.
- Never remove a directory in `render.Plan.StatusDirs`. These hold agent status, shared agent memory, and shared notes.
- Templates hold no personal choices (D112). A template never carries `[tools]`, `[agents]`, or the editor's or agents' Containerfiles.
- Agent names in `config.reservedAgents` are refused. Add a name there when you add an `agent` subcommand.
- Only `internal/pack/builtin/agents/claude/` may name `claude`. Only `internal/term/` may name a terminal, except `internal/render/agentsession.go` and `Containerfile.agent-base`, which run tmux inside the container, and the vendored xterm.js in `internal/web/static/term/`. No non-test code names `filial` or Phoenix. `TestGenericity` in `internal/` enforces this.
- Every Quadlet unit railswitch writes ends with `[Quadlet] DefaultDependencies=false` (D130). The line goes after the unit-hash stamp, so it changes no hash. The minimum podman version is 5.3.
- Paths honour `XDG_CONFIG_HOME` and `XDG_DATA_HOME`. Config is in `~/.config/railswitch/`, data is in `~/.local/share/railswitch/`, and Quadlet output is in `~/.config/containers/systemd/`.
- Data services (role `data`: database, cache, and storage) get `StopTimeout=60`, `TimeoutStopSec=90`, and `TimeoutStartSec=300`.
- Long-running units log with `LogDriver=journald`, and oneshots with `passthrough` (D64).
- railswitch's own CI lives in `.github/workflows/`. railswitch never generates or touches a project's CI (D41).

## Pinned values
- Subnets come from the pool `10.214.0.0/16`, one `/24` per project. Allocation skips a `/24` that any podman network already uses. Gateway is `.1`, Traefik is `.2`, and the dynamic range is `10.214.<n>.128/25`.
- `10.214.255.0/24` is reserved for the machine network `railswitch_machine`: gateway `.1`, Traefik `.2`, Dozzle `.3`.
- Every railswitch network (proxy, dev, and machine) is `isolate=strict`. The dev network MTU is 1400.
- Port bases: DB 55432, Ruby debug 33000, Neovim 17000, Playwright 18888, app share 13000, and storage share 19000, each plus the worktree ID. Two port bases are at least `MaxWorktreeID + 1` (100) apart. A worktree slug is at most 40 characters.
- The dashboard is `railswitch-dashboard.service`, a plain systemd user unit on `127.0.0.1:7700` and `[::1]:7700` (`http://railswitch.localhost:7700/`). Every app unit `Wants=` it, and it never blocks `up`. Its token is `~/.local/share/railswitch/dashboard/token`, mode 0600.

## Package map
- `cmd/railswitch`: entry point, calls `cli.Main`.
- `internal/cli`: flag dispatch, one file per command group.
- `internal/sys` and `systest`: `Runner`, `Cmd`, `Result`, and the fake runner. `Cmd.KillGrace` sends SIGTERM, then SIGKILL, to its own process group.
- `internal/config`: `project.toml`, `stack.lock`, and the line editor that settings edits use.
- `internal/pack` and `builtin`: packages and the built-ins. The format is in `docs/packs.md`.
- `internal/workspace`: loads a workspace, locks it, and keeps accepted package content.
- `internal/render`: model, env, images, Quadlet units, lints, diff, and apply.
- `internal/lifecycle`: `up`, `down`, agents, databases, and the other runtime commands.
- `internal/machine`: dirs, registry, proxy, machine config, and the dashboard unit.
- `internal/settings`: `settings preview`. It judges each edit by rendering before and after in memory.
- `internal/web`: the dashboard. `internal/jobs` runs its actions, and `internal/dashconf` holds its colours.
- `internal/web/static/office/`: the Virtual Office view (`docs/dashboard.md`); `kit/` its loader, `sets/cyberpunk/` the built-in set's models, built by the Blender scripts in `tools/office-kit/` (`docs/office-kit.md`). Regenerate the models; never edit them.
- `internal/office`: the Virtual Office's sets (D153): the piece list, glTF bounds, set checks and the store of installed sets.
- `internal/prs`: `pr ls` and `stack ls`, through `gh`.
- `internal/safefile`: hostile-file reads and `Printable`.
- `internal/naming`: sanitized names, object and unit names, and worktree paths.
- `internal/agentstatus`: the agents' status files (D58), read as hostile.
- `internal/agentqueue`: a worktree's agent work queue (D159), host-side; `lifecycle.QueueNext` delivers it.
- `internal/gitguard`: git-guard (D60), which checks hooks and git config that containers can write.
- `internal/gitshadow`: a host-private stand-in for a repository's common git dir.
- `internal/detect`: `project detect`.
- `internal/lockstore`: accepted package content by sha256 (D96).
- `internal/review` and `internal/textdiff`: review diffs and the D98 flags.
- `internal/signing` and `internal/libraries`: library signing, trust, and fetches (D99 to D106).
- `internal/template`: templates (D108 to D113).
- `internal/term`: `--new-term`, which detects the terminal and opens a window; `BrowserEnv`, the browser terminal's `TERM`.
- `internal/pty`: Linux pseudo-terminals (`Open`, `Resize`) for the dashboard's terminals (`GET /p/{prefix}/tty/{slug}/{kind}`, `internal/web/terminal.go`; `sys.Cmd.TTY` runs a command on one, in its own session; `docs/dashboard.md` Terminals).
- Also `internal/placeholder`, `internal/version`, `internal/unithash`, `internal/tailscale`, `internal/podman`, `internal/firewall`, `internal/worktree`, and `internal/doctor`.
