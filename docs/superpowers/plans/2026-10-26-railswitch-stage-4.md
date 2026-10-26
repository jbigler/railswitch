# railswitch Stage 4 (agents, nvim, git-guard; v1 acceptance) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Filial repo runs end to end from a fresh `railswitch project new`, using only generic features, with several agents per project, the nvim config repo, git-guard, opt-in commit signing and `db dump`.

**Architecture:**
- **New package kind.** Agents are a fourth package kind next to packs, options and tools. They are loaded from the built-in FS or the workspace's `agents/` dir (`local:`), and locked in `stack.lock`.
- **Rendering agents.**
  - Render builds an `agent-base` step on top of the base chain, then each agent package's image on top of agent-base.
  - Each configured agent becomes a `RoleAgent` service `agent-<name>`, with its own firewall profile `agent-<name>` in enforce mode, its own allow file and its own status file.
  - `run = "service"` agents get a Quadlet unit that the app `Wants`. `run = "on-demand"` agents stay in the model (env file, secrets, volumes) but get no unit; `railswitch agent` runs them as transient `podman run --rm -it` containers.
- **Interactive commands.** `lifecycle` gains the interactive agent, nvim, hooks and dump commands. They use the existing `L.interactive`/`transient` path.
- **Machine-local mounts** come from `machine.Config` and reach render through `render.Inputs`.

**Tech Stack:** Go (stdlib + `github.com/BurntSushi/toml`), rootless podman 5+, Quadlet, systemd user units, POSIX sh + `jq` in agent images.

**Spec:**
- `docs/superpowers/plans/2026-10-25-railswitch-roadmap.md`: the Stage 4 section and decisions D23, D30–D33, D43, D54, D55, D57–D61;
- `handoff/HANDOFF.md` §6 (agent states, newterm, git safety) and §7 (bind-source gotcha);
- old tool at `/tmp/rails-dev-podman` (`fd7336b`), used for the ports:
  - `.container-config/Dockerfile` (`claude-cli` stage);
  - `.container-config/entrypoint-claude.sh`;
  - `.container-config/claude-status-hook.sh`;
  - `.container-config/CLAUDE.md`;
  - `.container-config/firewall/agent.allow`;
  - `.scripts/{podman-claude,newterm,git-guard,podman-db-dump}.sh`;
  - `.mise/config.toml` `[tasks.nvim]`;
  - `.dashboard/test/fixtures/hook-payloads/`.

**User decisions, 2026-10-26, binding:** D57 (machine-local mounts in `config.toml [[mount]]`), D58 (minimal status v1), D59 (`BROWSER_CDP_URL` to agents only), D60 (read-only `.git/hooks` plus checks), D61 (opt-in SSH signing secret).

**Controller calls in this plan (flag for the user's review):**
- **C1. nvim config, `[tools] nvim = true` → `[tools.nvim]` table (D54).** This is a clean cutover with no schema bump, because railswitch has no release tag yet. The old form is refused with a message naming the new one. `project new --tool nvim` writes an empty `[tools.nvim]` table.
- **C2. `local:<name>@N` refs are implemented for agents only.** Packs and options stay built-in until Stage 5 (D43). An agent ref of that form reads `<workspace>/agents/<name>/`, which is tracked (`!/agents/` in the workspace `.gitignore`). It is locked by content hash with `source = "local"`.
- **C3. `railswitch db dump` contains no postgres knowledge.** An option declares `[dump] command`/`file`; postgres sets `pg_dump -Fc -U postgres {db}` → `{db}.dump`. The command also writes each worktree volume that declares a `seed` (D18), via `podman volume export` (gzip when the seed name ends in `.gz`). The old dump script did both.
- **C4. `railswitch nvim` attaches the host's `nvim --remote-ui` to the worktree's loopback nvim port, as the old tool did.** If no `nvim` is on the host PATH, it falls back to `podman exec -it <ctr> nvim --remote-ui --server 127.0.0.1:7777`. Terminal integrations (kitty) live in optional user scripts `~/.config/railswitch/hooks/nvim-attach` and `nvim-detach`. These run with `RAILSWITCH_NVIM_SERVER=127.0.0.1:<port>`. No terminal name appears in core.
- **C5. `--new-term` targets Linux terminals only:** tmux, kitty, wezterm, konsole, gnome-terminal, xfce4-terminal, terminator, ghostty, alacritty, foot and xterm. The macOS (osascript) and vscode paths are dropped, because railswitch needs systemd.
- **C6. Agents are env consumers.** They get the same layers as tools: pack env, option `[gives]`, project and worktree env. On top of that they get the new option `[agent_gives]`, the package's `[env]`, `[agents.<n>] env`, `RAILSWITCH_AGENT` and `RAILSWITCH_STATUS_FILE`. They join the dev and proxy networks (no routes), like tools, so routed hosts (`s3.`) resolve through `AddHost`.
- **C7. Agent firewall mode is always `enforce` (D55), whatever `[firewall] mode` is.** Each agent's allow file is the package `allow` plus `[agents.<n>] allow` plus the project's shared `[firewall] allow.agent`.
- **C8. `doctor` gains a warn level.** A `Check` gets `warn bool` (JSON `"warn"`); a warning prints `warn` and doesn't change the exit code. git-guard findings are warnings (D60 "nothing refuses").
- **C9. Default agent from the package.** `project new` takes its default agent from the built-in package marked `default = true` (`claude@1`), so no agent name is written in Go.
- **C10. One firewall profile per agent.** D55 says "under the `agent` profile"; each agent instead gets its own profile `agent-<name>` with its own `agent-<name>.allow`. The shared `agent` profile stays for nvim, and its hosts are added to every agent's file.
- **C11. Worktree paths for git.** Every service that mounts the base `.git` writable (tools and agents) also mounts `{worktree.dir}` at its own host path, besides `/app`. The paths git records in `<base>/.git/worktrees/<slug>/gitdir` then resolve, and git never sees the worktree as prunable (D30's core guarantee; HANDOFF §7). The working dir stays `/app`.
- **C12. `railswitch agent` runs `up` first**, as `share` does, so the network, images, env file, units and preflight are in place. The old tool started rails and playwright before every Claude run.

## Global Constraints

Every Global Constraint of the Stages 0–2 and Stage 3 plans still holds:
- names, argv-only runner and timeouts;
- secrets on stdin only;
- hardening and schema versions;
- `--json` (a global flag before the command: `railswitch --json agent ls`);
- loopback-only publishing;
- exact names, no prune;
- `safefile`;
- isolation;
- `make check` before every commit.

Added here:
- **Genericity:**
  - `grep -rli filial` over every non-test `.go` file and over `internal/pack/builtin/` finds nothing.
  - `grep -rli claude` over every non-test `.go` file finds nothing; it may match only under `internal/pack/builtin/agents/claude/`.
  - No terminal emulator name appears in core outside `internal/term/`.
  - Task 12 adds `TestGenericity` to enforce this.
- **Agent names:** `^[a-z][a-z0-9-]{0,24}$`, so that `agent-<name>` fits `naming.serviceRe` (≤31). The names `ls` and `base` are reserved. Where an agent name becomes part of a secret name, `-` becomes `_`.
- **Agent objects:**
  - service id `agent-<name>`;
  - container and unit `<prefix>-<slug>-agent-<name>`;
  - home volume `<prefix>-<slug>-agent-<name>-home` at `/home/appuser`, with `:U`;
  - status dir `<ws>/.railswitch/status/<slug>/`, mode 0700, bind-mounted `rw,z` at `/run/railswitch/status`;
  - `RAILSWITCH_STATUS_FILE=/run/railswitch/status/<name>.json`;
  - images `localhost/<prefix>/agent-base:<ver>-<hash8>` and `localhost/<prefix>/agent-<pkg>:<ver>-<hash8>` (D9).
- **Firewall profiles:**
  - `none`, `deps`, `agent` (shared extra hosts, used by nvim) and `agent-<name>`;
  - `hook.sh` accepts a profile only when `[[ $p =~ ^agent-[a-z][a-z0-9-]{0,24}$ ]]` (no glob), and reads `<profile>.allow`;
  - an unknown profile still fails the container start.
- **git-guard:** every mount of `{base.git}` that is writable is followed by `{base.git}/hooks` → `{base.git}/hooks` with `ro,z`. In the base worktree, `/app` *is* the base checkout, so every service that mounts `{worktree.dir}` there (app, deps and workers included) also gets `{base.git}/hooks` → `/app/.git/hooks` with `ro,z`. Tools and agents also get it at the host path (C11). `Preflight` creates `<base>/.git/hooks` (0755) when it is missing.
- **Status contract (D58), exact:**
  - `{"v":1,"state":"…","message":"…","ts":<int>}` with `state ∈ {permission, question, working, done, idle, off}`;
  - readers read through `safefile.Read(path, 65536)` (the dir is container-writable: symlinks and FIFOs are refused), and treat missing, refused, oversized, invalid, `v != 1` or an unknown state as `off`;
  - `reply` is `done` with a message matching the old dashboard's regex ``\?[\s)\]"'*_`”’]*\z``.
- **Interactive paths** use `sys.NoTimeout` and `L.interactive`, never a timed `Run`. `L.interactive` takes the program name, so host `nvim` uses it too.
- **Detached processes** (`--new-term` windows) go through `sys.Runner` too: `sys.Cmd` gains `Detach bool`. `sys.Exec` then starts the process in its own session with `/dev/null` stdio, releases it and returns; `systest.Fake` records it.
- **Docs:** every new command or field updates `docs/json.md` (for `--json` output), `docs/completion.md`, `AGENTS.md` (decisions list D1–D61, commands) and `docs/stage4-acceptance.md`.

## Review Focus

1. **On-demand agents with no unit.**
   - `up` must not reload systemd on every run because an on-demand agent's unit is "not found".
   - `status` shows the agent as `on-demand` (or `running` while a run is live). `down` removes a leftover container and the home volume. `stop` ignores it.
   - An unset secret of an on-demand agent does not block `up`, only `railswitch agent <name>`.
   - *Pinned in Tasks 3 and 5.*
2. **Two agents in one worktree.** Each sees only its own secrets (`/run/secrets/<name>` present in one, absent in the other), its own allow file and annotation `<p>.firewall=agent-<name>`, and its own status file. A secret listing `agent-a` never reaches `agent-b`. *Pinned in Tasks 3, 4 and 12.*
3. **Arguments to `railswitch agent`.**
   - `agent -- --version` with one agent runs it with `--version`.
   - `agent claude -- -p "a b"` passes `-p` and `a b` verbatim.
   - `agent ls` is the subcommand and never an agent named `ls`.
   - With two agents and no name, the error lists both.
   - *Pinned in Task 5.*
4. **Missing bind sources.**
   - A machine mount with `optional = true` whose source is gone renders no `Volume=`, and the container still starts.
   - A non-optional missing source makes `Preflight` name the path and its `[[mount]]` entry.
   - `<base>/.git/hooks` missing is created before start, never reported.
   - *Pinned in Tasks 8 and 9.*
5. **nvim config repo drift.**
   - `[tools.nvim] config_repo` changed after the clone: `nvim update` refuses, naming both URLs and `railswitch nvim reset-config`.
   - A dirty clone: `nvim update` refuses and lists the changed paths.
   - The entrypoint never deletes or overwrites a non-empty config dir.
   - *Pinned in Task 7.*

---

## File Structure

```
internal/config/project.go          # Agents, Agent; Tools.Nvim *NvimTool; Secret.GitSigning
internal/config/lock.go             # Lock.Agents [agent.*]
internal/pack/format.go             # Agent kind; Option.AgentGives, Option.Dump
internal/pack/ref.go                # Ref.Local; ParseRef accepts local:
internal/pack/load.go               # agents/ loading; LoadLocalAgents(root)
internal/pack/resolve.go            # Stack.Agents []*ChosenAgent
internal/pack/builtin/agentbase/Containerfile.agent-base
internal/pack/builtin/agents/claude/{agent.toml,Containerfile.claude,CLAUDE.md,entrypoints/entrypoint-claude.sh,claude-status-hook.sh}
internal/pack/builtin/options/{postgres,playwright}/option.toml   # [dump]; [agent_gives]
internal/pack/builtin/tools/nvim/{tool.toml,entrypoints/entrypoint-nvim.sh,Containerfile.nvim}
internal/render/{model.go,env.go,images.go,quadlet.go,runargs.go,apply.go}   # RoleAgent, agent images, on-demand, mounts, signing
internal/firewall/{descriptor.go,assets/hook.sh}   # agent-<name>.allow, profile pattern, per-service mode
internal/machine/config.go          # [[mount]]
internal/agentstatus/status.go      # Read, Status, Reply (D58)
internal/gitguard/gitguard.go       # Check, Backup, Restore (D60)
internal/term/term.go               # Detect, Launch (--new-term)
internal/lifecycle/{agent.go,nvim.go,dump.go,up.go,preflight.go,status.go,down.go,exec.go}
internal/cli/{agent.go,nvim.go,hooks.go,db.go,new.go,secret.go,completion.go,doctor.go}
internal/doctor/doctor.go           # warn level; git-guard; mounts
docs/{json.md,completion.md,nvim.md,stage4-acceptance.md}, AGENTS.md
```

---

### Task 1: Agent package kind, `[agents]`, refs and lock

**Files:**
- Modify:
  - `internal/config/project.go` (+ `project_test.go`);
  - `internal/config/lock.go` (+ test);
  - `internal/pack/{format,ref,load,resolve}.go` (+ tests);
  - `internal/workspace/workspace.go` (`lockEntries`, `CheckLock`; `Load` and `Lock` use `LoadCatalog`);
  - `internal/cli/containerfile.go` (`projectStack` and `containerfile new` use `LoadCatalog`), `internal/cli/new.go` (`workspaceGitignore` gains `!/agents/`).

  `embed.go` is not touched here: `//go:embed` fails on a pattern with no files, and the agent dirs arrive in Task 2. Task 1 tests `Load` on a MapFS and `LoadLocalAgents` on a temp dir.

**Interfaces:**
- Produces:
  ```go
  // config
  type Agent struct {
      Use   string            `toml:"use"`   // "claude@1" or "local:<name>@N"
      Run   string            `toml:"run"`   // "", "on-demand", "service"
      Env   map[string]string `toml:"env"`
      Allow []string          `toml:"allow"`
  }
  // Project gains: Agents map[string]Agent `toml:"agents"`
  // Lock gains:    Agents map[string]LockEntry `toml:"agent"`   // key = ref string, e.g. "claude@1", "local:probe@1"

  // pack
  type Ref struct { Name string; Version int; Local bool }        // String(): "local:" prefix when Local
  type AgentImage struct { File string `toml:"file"` }            // Containerfile.<pkg>
  type AgentSecret struct { Name, Env string }                   // declared into [secrets.<agent>_<name>] by project new
  type Agent struct {
      Name string; Version int; Run string                       // default run mode; "" = on-demand
      Default bool                                               // `default = true`: project new adds it unless --agent is given; at most one built-in
      Image AgentImage; Entrypoint string; Allow []string
      Shared []Volume; Mounts []Mount; Env map[string]string
      Secrets []AgentSecret; Unit Unit; Files fs.FS
  }
  type ChosenAgent struct { Name, Service, Run string; Agent *Agent; Ref Ref; Env map[string]string; Allow []string }
  // Catalog gains Agents map[Ref]*Agent; Stack gains Agents []*ChosenAgent (sorted by Name)
  func LoadLocalAgents(c *Catalog, root string) error   // <root>/agents/<name>/agent.toml, Ref{Local:true}; no agents/ dir is fine
  func LoadCatalog(root string) (*Catalog, error)       // LoadBuiltins + LoadLocalAgents(root): the ONE entry point for a workspace's catalog
  func (c *Catalog) DefaultAgent() (Ref, bool)          // the built-in agent with Default; LoadBuiltins errors when two set it
  ```
- Consumes: the `Load` loop and `checkTool` patterns in `load.go`, and `claim` in `resolve.go`.

**Rules:**
- `[agents.<n>].use` accepts both forms. `[stack] pack` and `[options]` refuse `local:`. `config.Validate`'s `validator.ref` checks for the prefix first and emits `local: refs are supported for agents only until Stage 5 (D43)`; `pack.Resolve` keeps the same guard.
- Every place that resolves a workspace's stack uses `LoadCatalog(root)`: `workspace.Load`, `workspace.Lock`, `cli` `projectStack`/`containerfile new` and `project new`. Test helpers that build the shared fixture's stack switch in Task 3, when the fixture gains a local agent.
- `CheckLock` prints an agent key as `Ref.String()` (`local:probe@1`), never `name@version` appended again.
- A local agent's `agent.toml` `name` must equal its directory name, and its version must equal the ref's `@N`.
- `run` resolves in this order: `[agents.<n>].run`, then the package's `run`, then `on-demand`.
- `Allow` is the package's `allow` plus the project's `[agents.<n>].allow`, deduplicated in order.
- Containerfile names are one namespace (base, addons, tools, agent packages, `agent-base`, standalone). A collision is a resolve error that names both owners.
- The lock key is `Ref.String()`. Builtin entries get `source = "built in (railswitch <ver>)"`; local ones get `source = "local"`. `SHA256` is always `ContentHash` of the package dir.

- [ ] **Step 1: Write failing tests**
  - `TestAgentConfigValidate` (table):
    - accepted: `use="claude@1"`, `use="local:probe@1"`, `run` ∈ {"", "on-demand", "service"};
    - refused, with the field named:
      - names `Bad`, `ls`, `base`, `a_b`;
      - a 26-character name;
      - `run="always"`;
      - env name `1X`;
      - `allow=["a b"]`;
      - `use="local:x@0"`.
  - `TestStackLocalRefused` (config): `[options] database = "local:pg@1"` → error containing `agents only until Stage 5`.
  - `TestLoadAgents`: a MapFS with `agents/a/agent.toml` (`name="a"`, `version=1`, `[image] file="Containerfile.a"`, `allow=["x.example"]`) loads as `Catalog.Agents[Ref{"a",1,false}]`. An unknown key in `agent.toml` is an error naming the file.
  - `TestLoadLocalAgents`: in a temp dir, `agents/probe/agent.toml` loads as `Ref{"probe",1,true}`. A directory named `probe` with `name="other"` is an error. A root without `agents/` loads nothing and no error.
  - `TestResolveAgents`: `[agents.zed] use="a@1" run="service" allow=["y.example"]` and `[agents.alpha] use="a@1"` give `Stack.Agents` names `[alpha zed]`, services `agent-alpha`/`agent-zed`, runs `on-demand`/`service`, and zed's allow `[x.example y.example]`. `[agents.x]` with `use="nope@1"` → `unknown agent package nope@1`.
  - `TestLockAgents`: `lockEntries` includes `agent."a@1"` and `agent."local:probe@1"` (source `local`). Editing `agents/probe/agent.toml` makes `CheckLock` fail with `ErrStaleLock` naming `local:probe@1`.
- [ ] **Step 2:** `go test ./internal/config/ ./internal/pack/ ./internal/workspace/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Same command. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -m "feat(pack): agent package kind, [agents], local agent refs, lock entries"`

### Task 2: Built-in `claude@1` agent package and the agent base Containerfile

**Files:**
- Create:
  - `internal/pack/builtin/agentbase/Containerfile.agent-base`. It contains `ARG BASE_IMAGE` / `FROM ${BASE_IMAGE}` / `USER root`, then `apt-get install --no-install-recommends -y git curl ca-certificates jq ripgrep fd-find socat openssh-client less procps`, then `USER appuser`. Its header says it extends the base for every agent, and that a non-Debian base needs an edit.
  - Under `internal/pack/builtin/agents/claude/`:
    - **`agent.toml`:**
      - `name="claude"`, `version=1`, `default=true`, `run="on-demand"`, `entrypoint="entrypoints/entrypoint-claude.sh"`, `[image] file="Containerfile.claude"`;
      - `allow = ["claude.ai","api.anthropic.com","statsig.com","sentry.io","registry.npmjs.org","meta:github","github.com","api.github.com","codeload.github.com"]`;
      - `[[shared]]`: `claude_plugins_cache` → `/home/appuser/.claude/plugins/cache` and `claude_plugins_marketplaces` → `/home/appuser/.claude/plugins/marketplaces`, both chown;
      - `[[mounts]]`: `CLAUDE.md` → `/opt/railswitch/agent/CLAUDE.md` `ro,z`, and `claude-status-hook.sh` → `/usr/local/bin/claude-status-hook.sh` `ro,z`;
      - `[[secrets]] name="token" env="CLAUDE_CODE_OAUTH_TOKEN"`;
      - `[env] DISABLE_AUTOUPDATER="1"`;
      - `[unit] tty=true`.
    - **`Containerfile.claude`:** `ARG BASE_IMAGE`, a builder stage `FROM docker.io/library/debian:trixie-slim` that downloads the native binary (ported verbatim from the old `claude-cli` stage, with `ARG CLAUDE_CODE_VERSION=latest`, and the arch mapped from `uname -m` to `x64|arm64`), then `FROM ${BASE_IMAGE}`. It copies `/opt/claude-code` and writes the `/usr/bin/claude` wrapper with `DISABLE_AUTOUPDATER=1`. `USER appuser`.
    - **`entrypoints/entrypoint-claude.sh`:** ported from the old one (steps 1, 2, 4, 5, 6 and the `exec`), with these changes:
      - trust `/app`;
      - the CDP bridge and chrome-devtools MCP only when `BROWSER_CDP_URL` is set: parse host:port, `socat TCP-LISTEN:9222,bind=127.0.0.1,fork,reuseaddr TCP:<host>:<port> &`;
      - copy `/opt/railswitch/agent/CLAUDE.md` to `~/.claude/CLAUDE.md`;
      - no Pencil, rtk or gh-stack (personal tools; the user adds them through a D13 entrypoint override).
    - **`claude-status-hook.sh`:** ported from the old v2 `phase` mapping. It writes D58 v1 to `$RAILSWITCH_STATUS_FILE` with temp + rename (`mktemp` in the same dir). `offline` becomes `off`. `message` is the summary or question text, and for `Stop` it is the last 600 characters of the flattened `last_assistant_message`. `ts` is `date +%s`. It exits 1 on any failure, never 2, and exits 0 when the variable is unset.
    - **`CLAUDE.md`:** only the old file's generic sections:
      - Browser (the CDP bridge; use `http://$WORKTREE_HOST`);
      - Network egress (refused means the firewall; ask the human to run `railswitch fw allow agent-claude <host>` or add the host to `[agents.claude] allow`);
      - Git worktrees: never prune, add or remove from inside the container.

      Nothing personal, Rails-specific or Filial-specific.
- Create: `internal/pack/claude_hook_test.go`, which replays `internal/pack/testdata/hook-payloads/*.json` (copied from the old repo's `.dashboard/test/fixtures/hook-payloads/`) through `bash` and `jq`. Neither goes under `builtin/`: everything there is embedded and hashed into the lock.
- Modify: `internal/pack/builtin/embed.go` (`all:agents all:agentbase`), `internal/pack/pack_test.go` (`TestLoadBuiltins` hashes the agent dirs; `TestBuiltinFromQualified` covers the new Containerfiles), `internal/cli/containerfile_test.go` (`TestBuiltinContainerfilesSelfContained` covers them).

**Interfaces:**
- Consumes: the `agent.toml` format and `LoadBuiltins` agents (Task 1).
- Produces: `claude@1` in `LoadBuiltins().Agents`; the embedded file `agentbase/Containerfile.agent-base`, which Task 3 ejects and reads.

- [ ] **Step 1: Write failing tests**
  - `TestHookMapping` (skip only when `jq` isn't on PATH). For each fixture → expected `state`/`message`:
    - SessionStart → idle;
    - UserPromptSubmit → working;
    - PreToolUse.Bash → working;
    - PermissionRequest.Bash → permission, `Permission: Bash: …`;
    - Notification.permission_prompt → permission;
    - Stop.long → done, message ≤ 602 characters, ending as the payload does;
    - SessionEnd → off.

    Each output parses as D58 v1 (`v==1`, `ts>0`). With `RAILSWITCH_STATUS_FILE` unset, exit 0 and no file. With an unwritable dir, exit 1.
  - `TestClaudeBuiltin` (pack): `LoadBuiltins()` has `claude@1`; `DefaultAgent()` returns it. Its allow contains `api.anthropic.com`.
  - `TestDefaultAgentUnique` (pack): a MapFS with two agents marked `default = true` makes `Load` fail, naming both.
  - `TestBuiltinFromQualified` and `TestBuiltinContainerfilesSelfContained` pass for `Containerfile.claude` and `Containerfile.agent-base`. The `checkDerived` check (`ARG BASE_IMAGE`, last stage `FROM ${BASE_IMAGE}`) runs in Task 3's render tests, because the method lives in `render`.
- [ ] **Step 2:** `go test ./internal/pack/... ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Same command. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -m "feat(agents): built-in claude@1 package and agent base"`

### Task 3: Render agents (agent-base, images, services, env, on-demand)

**Files:**
- Modify:
  - `internal/render/{model,env,images,quadlet,apply}.go` (+ tests);
  - `internal/pack/{format,load}.go` (`Option.AgentGives`; load checks its env names like `deps_env`), `internal/pack/builtin/options/playwright/option.toml` (`[agent_gives]`);
  - `internal/cli/containerfile.go` (kinds `agent-base` and `agent`; `stackBuiltins`, `builtinNames` and `lookup` include built-in and local agent packages);
  - `internal/cli/new.go` (`ejectNames` inserts `agent-base`, then each distinct agent package, after the tools);
  - `internal/testfixture/testfixture.go` (writes `Containerfile.agent-base` and the fixture agents' Containerfiles);
  - `testdata/fixtures/filial/project.toml`: add `[agents.claude] use="claude@1"`, `[agents.probe] use="local:probe@1" run="service"` and `[secrets.probe_key] services=["agent-probe"]`, plus the test-only package `testdata/fixtures/filial/agents/probe/` (`agent.toml` with `name="probe"`, `version=1`, `allow=["probe.example"]`, `[image] file="Containerfile.probe"`; `Containerfile.probe` = `ARG BASE_IMAGE` / `FROM ${BASE_IMAGE}` / `CMD ["sh"]`);
  - every test helper that builds the shared fixture's catalog switches from `LoadBuiltins()` to `LoadCatalog(root)`: `render/model_test.go:43,594`, `firewall/firewall_test.go:34`, `lifecycle/preflight_test.go:28`, `cli/containerfile_test.go:212,277`, and any other caller `go test ./...` shows;
  - existing count tests the new builds and volumes change, set to their new values: `lifecycle_test.go:227,376,397` (7 builds, playwright last), `lifecycle_test.go:583` (`Delete 5 volumes of feat?`), `cli/containerfile_test.go:319,328` (the 8-step list), and `TestUpOrder`'s build expectations.

**Interfaces:**
- Consumes: `pack.ChosenAgent` (Task 1).
- Produces:
  ```go
  const RoleAgent Role = "agent"           // roleLimits: 4g / 4096; quadlet --cpus = CPUs/2
  // Service gains:
  OnDemand bool                            // no Quadlet unit, not in app.Wants, not in Names.Units
  FirewallMode string                      // "" = project mode; agents "enforce"
  Workdir string                           // "/app" for worktree services (transient -w)
  // Model gains: agentBase *containerfile; agents map[string]*containerfile (by package name)
  func (m *Model) Agents() []*Service      // RoleAgent services in Stack.Agents order
  // Instance values: "image.agent-base", "image.agent-<name>", "status.dir" = <ws>/.railswitch/status/<slug>
  ```
- Env (C6) adds a block for agent services:
  - `RAILSWITCH_AGENT=<name>`;
  - `RAILSWITCH_STATUS_FILE`;
  - the package's `[env]`, then `[agents.<n>].env` (layer project);
  - every chosen option's `AgentGives`, at layer option, with `svc` set to the giver so `{service}` resolves.

  Option gains `AgentGives map[string]string `toml:"agent_gives"``. The playwright option sets `BROWSER_CDP_URL = "http://{service}:9223"` (D59) in this task.
- Mounts per agent:
  - `worktreeMounts("z")`, which gains the hooks mounts in Task 9. For writable `.git` (tools and agents) it now also mounts `{worktree.dir}` → `{worktree.dir}` with `z` (C11), after `/app`;
  - `{status.dir}` → `/run/railswitch/status` with `rw,z`;
  - package `[[mounts]]` (relative paths are package assets);
  - the entrypoint (D13 override applies);
  - volumes: `agent-<n>-home` → `/home/appuser` (Chown), the pack's worktree volumes, the pack caches, and the package `[[shared]]`.
- Image steps: chain → tools → `agent-base` (`BASE_IMAGE` = chain end) → one step per distinct agent package (`BASE_IMAGE` = agent-base; the step is named `agent <pkg>`) → standalone. `image.agent-<name>` is set for every agent that uses the package.

**Rules:**
- `Plan` gains `StatusDirs []string`, modelled on `Plan.Homes`: `.railswitch/status/<slug>` for each worktree when the project has at least one agent. `Apply` creates them at 0700 and never deletes them; Task 5 removes one on `wt rm`.
- `NamesFor`: `Units` skips `OnDemand` services. `Containers` and `Volumes` keep them, so `down` and `wt rm` clean them up. (`startTimeout` already skips a missing `.container`; no change there.)
- `Quadlet()` skips `OnDemand` services. A service agent's unit has `--tty --interactive` in its `PodmanArgs` (after `--cpus=`) and joins `app.Wants`.

- [ ] **Step 1: Write failing tests**
  - `TestImageChain` (update) expects these build names, in order: `[base, addon pg-client, addon vips, nvim, agent-base, agent claude, agent probe, playwright]`. Changing `Containerfile.agent-base` changes the tags of agent-base, claude and probe, and nothing earlier. `Containerfile.agent-base` and `Containerfile.claude` (the built-in defaults) pass `checkDerived`.
  - `TestAgentServices`:
    - `service(t,m,"agent-claude")`: Role `agent`, `OnDemand` true, Networks `[dev proxy]`, Firewall `["agent-claude"]`, FirewallMode `enforce`.
    - `agent-probe`: `OnDemand` false.
    - The rails unit `Wants=` includes `filial-feat-agent-probe.service` and not `-agent-claude.service`.
    - No file `filial-feat-agent-claude.container` exists. `filial-feat-agent-probe.container` has `PodmanArgs=--cpus=4 --tty --interactive` (the fixture host has 8 CPUs).
  - `TestWorktreeHostPath`: the nvim and probe units have `Volume=<ws>/feat:<ws>/feat:z` (built with `filepath.Join`, not the golden's `/WORKSPACES`); the rails unit doesn't.
  - `TestAgentEnv`:
    - `envVar(in,"agent-claude","RAILSWITCH_STATUS_FILE")` is `/run/railswitch/status/claude.json`;
    - `BROWSER_CDP_URL` is `http://browser:9223` for both agents and absent for `rails`, `nvim` and `deps`;
    - `DATABASE_URL` is present for agents.
  - `TestAgentSecretsScoped`: `in.SecretArgs("agent-probe")` contains `filial_probe_key`; `in.SecretArgs("agent-claude")` doesn't.
  - `TestAgentNames`: `NamesFor("feat")`.
    - `Units` contains `filial-feat-agent-probe.service` and not `-agent-claude.service`.
    - `Containers` contains both.
    - `Volumes` contains `filial-feat-agent-claude-home` and `filial-feat-agent-probe-home`.
  - `TestPlanStatusDirs`: the plan has `.railswitch/status/master` and `/feat`. With no agents, it has neither.
  - `TestGolden -update`: review the diff. Only new agent units, the nvim host-path mount, and `Wants=` additions change.
  - `TestStackBuiltinsAgents` (cli): `containerfile list` names the steps `agent-base`, `agent claude` and `agent probe`. After removing the fixture's `containerfiles/Containerfile.agent-base` (the `removeFile` pattern), `containerfile eject agent-base` writes it again; `containerfile eject probe` copies the local package's `Containerfile.probe`.
- [ ] **Step 2:** `go test ./internal/render/ ./internal/cli/ ./internal/pack/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test ./internal/render -run TestGolden -update`, review the diff, then `go test ./...`. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -m "feat(render): agent base, agent images and services (on-demand/service)"`

### Task 4: Per-agent firewall (allow files, profile, enforce)

**Files:**
- Modify: `internal/firewall/{descriptor.go,assets/hook.sh}` (+ `firewall_test.go`), `internal/render/runargs.go`, `internal/cli/fw.go` (`fw show agent-<n>`; + `cli` test).

**Interfaces:**
- `func Files(prefix, ws, networkDir string, st *pack.Stack, fw config.Firewall) (map[string]render.File, error)` keeps its signature (it reads `st.Agents`). It also writes `agent-<name>.allow` = `allowlist(header, agent.Allow, fw.Allow["agent"])`.
- `FirewallAnnotations` writes `<p>.firewall.mode=` `s.FirewallMode` when that is set, otherwise the project mode.
- `hook.sh`: profiles `none`, `deps` and `agent` as today, plus any `$p` with `[[ $p =~ ^agent-[a-z][a-z0-9-]{0,24}$ ]]` (a regex test, never a `case` glob: the name becomes a path). Each profile reads `$HERE/<p>.allow`, and a missing file dies (fail closed).

- [ ] **Step 1: Write failing tests**
  - `TestAgentAllowFiles`: with `[firewall] allow.agent=["shared.example"]`, Files writes `agent-claude.allow` containing the claude package's hosts plus `shared.example`, and `agent-probe.allow` containing `probe.example` plus `shared.example`. Neither file contains the other agent's own hosts.
  - `TestAgentAnnotations`: the probe unit has `Annotation=filial.firewall=agent-probe` and `Annotation=filial.firewall.mode=enforce`, while `[firewall] mode` is `log`. The nvim unit keeps `mode=log`.
  - `TestHookAgentProfile` (`runHook` shims): profile `agent-probe` loads `agent-probe.allow`. Profiles `agent-missing` (no file), `agentx` and `agent-a/../x` exit non-zero.
  - `TestFwShowAgent` (cli): `fw show agent-probe` prints the probe's allow file.
  - The descriptor (`TestDescriptorGolden`) lists no profiles, so it doesn't change.
- [ ] **Step 2:** `go test ./internal/firewall/ ./internal/render/ ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Same command. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -m "feat(firewall): per-agent allow files and enforce mode"`

### Task 5: `railswitch agent` and `agent ls`; `project new --agent`; lifecycle handling of agents

**Files:**
- Create: `internal/agentstatus/status.go` (+ test), `internal/lifecycle/agent.go` (+ test), `internal/cli/agent.go` (+ test)
- Modify:
  - `internal/lifecycle/{exec.go,up.go,preflight.go,status.go,stop.go,down.go,project.go}`;
  - `internal/worktree/{add,remove}.go`, so the status dir is removed on `wt rm` and in the add rollback;
  - `internal/cli/{cli.go,completion.go}` (`agent` with sub `ls` and agent-name args; `--agent=`). `--new-term` is not registered here; Task 6 adds the flag with its behaviour.
  - `internal/cli/new.go`:
    - `--agent name=ref` (repeatable) or `--agent none`. Without `--agent`, the default is `[agents.<n>] use="<n>@<N>"` for `Catalog.DefaultAgent()`; no agent name is written in Go (Global Constraints, genericity).
    - `projectTOML` emits `[agents.<n>]`.
    - Package `[[secrets]]` are declared as `[secrets.<agent>_<name>] services=["agent-<agent>"] env=…`, with `-` in the agent name turned into `_` (`my-agent` → `my_agent_token`).
    - `github_token` (github.com repos) gets `services` = every `agent-<n>` plus `nvim` when nvim is chosen, with `git_host="github.com"`.
    - `printNext` lists the secrets to set;
  - `docs/json.md` (`agent ls`; status role `agent`; status `state` may be `on-demand` or `running` for on-demand agents instead of `<ActiveState>/<SubState>`), `docs/completion.md`.

**Interfaces:**
- Produces:
  ```go
  // agentstatus
  type Status struct { V int `json:"v"`; State string `json:"state"`; Message string `json:"message"`; TS int64 `json:"ts"` }
  func Read(path string) Status            // D58 reader rules (Global Constraints); never errors
  func (s Status) Reply() bool             // done + trailing "?" rule

  // lifecycle
  type AgentRow struct { Name, Package, Run, State, Message string; TS int64; Reply bool } // json: name, package, run, state, message, ts, reply
  func (l *L) Agent(ctx context.Context, slug, name string, argv []string) error  // l.Up first (C12); on-demand: transient run; service: start if inactive, then attach
  func (l *L) AgentList(ctx context.Context, slug string) ([]AgentRow, error)
  func (l *L) agentName(m *render.Model, name string) (string, error)            // "" → the only agent, else error listing all
  ```
- `transient` gains `opts runOpts{Full bool; Name, Entrypoint string}`.
  - With `Full`, it uses every network from `in.Networks(s)` and the same `AddHost` entries as the unit. Extract `(*Instance).AddHosts(s *Service) []string` from `quadlet.go` so the unit and the run share one source.
  - `--name <Name>` only when `Name != ""`; `--entrypoint <Entrypoint>` only when it is set; `-w s.Workdir`.
  - `exec` and `deps` pass the zero `runOpts`: no `--name` (a running app container already holds it), no entrypoint change, dev network only.
- On-demand run: `l.Up(ctx, slug)` first (C12), then `CheckSecrets` for that one service, then `podman --hooks-dir … run --rm -i [-t] --name <prefix>-<slug>-agent-<n> --entrypoint <s.Entrypoint> … <image> <argv…>`. `s.Entrypoint` is the package entrypoint mounted at `/usr/local/bin/<file>` (the D13 override is the mount), as the unit's `Entrypoint=` uses.
- Service mode: `l.Up` first, `systemctl --user start <unit>` when the unit isn't active, then `podman attach <ctr>` through `interactive(ctx, sys.NoTimeout, …)`. Extra argv for a service agent is a usage error: `agent <n> runs as a service; arguments go in its package or [agents.<n>]`.
- `project rm --purge`: when the workspace loads, the shared volumes to purge come from the model's volume names for every service (on-demand agents have no unit file to read them from); the unit-file path stays for a missing workspace.
- `up`:
  - The unit-state loop iterates `names.Units`, so it never sees on-demand agents.
  - `CheckSecrets` skips `OnDemand` services.
  - `Preflight` creates the status dir when missing.
- `status`: on-demand rows have State `on-demand`, or `running` when `podman().Running` reports the container. Role `agent`.
- CLI:
  - `agent [<name>] [-- args…]`. Split raw args at the first `--` *before* `flag` parsing.
  - `railswitch --json agent ls` (global `--json`) prints the rows; text columns are `NAME PACKAGE RUN STATE MESSAGE`. `STATE` shows `reply` when `Reply()`.
  - Completion: the first word after `agent` offers `ls` and the `[agents]` names together (merge `completionSubs["agent"] = {"ls"}` with `completionArgs["agent"]`; `completions` today treats a command with subs as sub-only).

- [ ] **Step 1: Write failing tests**
  - `TestStatusRead` (table, temp files):
    - a valid working file gives `working`;
    - `{"v":2,…}` gives `off`;
    - `state:"bogus"` gives `off`;
    - a 70000-byte file gives `off`;
    - a missing file gives `off`; a symlink to a valid file gives `off`; a FIFO gives `off` without blocking;
    - extra fields are ignored;
    - `Reply()` is true for done with `"Ship it?"` and for done with `"ok?)"`, and false for done with `"ok"` and for question with `"x?"`.
  - `TestAgentOnDemandArgv`, Fake: `l.Agent(ctx,"feat","claude",["--version"])` makes the `up` calls, then one `podman … run` call whose args contain (not in a fixed order) `--rm`, `-i`, `--name filial-feat-agent-claude`, `--entrypoint /usr/local/bin/entrypoint-claude.sh`, `--network filial-feat-dev`, `--network filial_proxy`, `--add-host s3.feat.filial.localhost:<machine.TraefikIP(setup's subnet)>` (10.214.0.2 in `setup`), `--annotation filial.firewall=agent-claude`, `--annotation filial.firewall.mode=enforce`, `-w /app`, and end with the claude image and `--version`. Its `Timeout == sys.NoTimeout`.
  - `TestExecNoName`: `exec --fresh` runs a transient with no `--name` and no `--entrypoint`.
  - `TestAgentServiceAttach`: the probe unit is inactive, so the calls are `systemctl --user start filial-feat-agent-probe.service`, then `podman attach filial-feat-agent-probe`. When the unit is active, only attach runs. `Agent(…,"probe",["x"])` gives a usage error that names `[agents.probe]`.
  - `TestAgentNameChoice`: with two agents and name `""`, the error contains `claude` and `probe`. With a single-agent fixture, name `""` picks it.
  - `TestUpNoReloadForOnDemand`: with every unit `loaded` and no on-demand unit present, `up` makes no `daemon-reload` call.
  - `TestUpIgnoresOnDemandSecret`: `[secrets.claude_token] services=["agent-claude"]` is unset, and `up` succeeds. `Agent(…,"claude",…)` fails with `claude_token`.
  - `TestStatusOnDemand`: the claude row has State `on-demand`, or `running` when Fake `Running` returns true.
  - `TestDownRemovesAgentHome`: `down --yes` calls `podman volume rm -f filial-feat-agent-claude-home`.
  - `TestProjectRmPurgesOnDemandShared`: `project rm filial --yes --purge` removes `filial_claude_plugins_cache` and `filial_claude_plugins_marketplaces`.
  - `TestWtRmRemovesStatusDir`.
  - `TestAgentArgs` (cli): `agent -- --version` → name `""` and argv `[--version]`. `agent claude -- -p "a b"` → argv `[-p, "a b"]`. `--json agent ls` reaches `AgentList`.
  - `TestCompleteAgent` (cli): `__complete agent ""` offers `ls`, `claude` and `probe`.
  - `TestAgentLsJSON` (cli) checks the fields from `docs/json.md`.
  - `TestCompletionFlagsMatch` stays green.
  - `TestNewDefaultAgent` (cli): `project new` (github repo) writes `[agents.claude] use = "claude@1"`, `[secrets.claude_token] services = ["agent-claude"] env = "CLAUDE_CODE_OAUTH_TOKEN"` and `[secrets.github_token] services = ["agent-claude"]` with `git_host`, and ejects `Containerfile.agent-base` and `Containerfile.claude`.
  - `TestNewAgentNone`: no `[agents]`, no `agent-base`, and no `github_token` unless `--tool nvim` is given.
  - `TestNewAgentFlag`: `--agent a=claude@1 --agent b-c=claude@1` gives two agents, one `Containerfile.claude`, secrets `a_token` and `b_c_token`, and `github_token` services `[agent-a agent-b-c]`.
- [ ] **Step 2:** `go test ./internal/agentstatus/ ./internal/lifecycle/ ./internal/cli/ ./internal/worktree/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Same command. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -m "feat: railswitch agent, agent ls, project new --agent"`

### Task 6: `--new-term` (port of `newterm.sh`)

**Files:**
- Create: `internal/term/term.go` (+ `term_test.go`)
- Modify:
  - `internal/sys/runner.go` (+ test): `Cmd.Detach bool`. `Exec.Run` with `Detach` starts the process with `Setsid`, stdio on `/dev/null` and `Dir`, calls `Process.Release` and returns at once (no wait, no timeout). `systest.Fake` records it like any call;
  - `internal/cli/agent.go` (+ test): the `--new-term` flag, which re-execs `os.Executable()` as `railswitch -C <ws> -w <slug> agent <name> [-- args]` through `term.Launch` and returns 0;
  - `internal/cli/completion.go` (`completionFlags["agent"] = {"--new-term"}`), `docs/completion.md`.

**Interfaces:**
- Produces:
  ```go
  type Launcher struct { R sys.Runner; Env func(string) string; LookPath func(string) (string, error); ProcRoot string; TTY bool; Err io.Writer }
  func (l Launcher) Detect() string                  // env fingerprints, then the parent chain via <ProcRoot>/<pid>/{comm,stat}
  func (l Launcher) Launch(ctx context.Context, title, dir string, argv []string) error
  ```
- Order and exact command lines: copy `/tmp/rails-dev-podman/.scripts/newterm.sh` (C5 terminals only).
  1. Tab in the detected terminal, through `R.Run` with a 10s timeout. These are tmux, kitty `@ launch --type=tab --copy-env=yes`, `wezterm cli spawn`, konsole `--new-tab`, gnome-terminal `--tab`, xfce4-terminal `--tab --command=<quoted>`, and terminator `--new-tab -x`.
  2. Window of the detected terminal, through `R.Run` with `Detach: true`, only when `DISPLAY` or `WAYLAND_DISPLAY` is set and the binary is on `LookPath`.
  3. `$TERMINAL`, then the fallback list `kitty wezterm ghostty alacritty foot konsole gnome-terminal xfce4-terminal terminator xterm`, each as a window. Stderr gets `railswitch: <detected> gave no tab or window; opened <term> instead`.
  4. When `TTY` (stdin and stdout are terminals), run in the current window (`R.Run` with `sys.NoTimeout` and the caller's stdio) and print `railswitch: no terminal emulator could be launched; running here`.
  5. Otherwise the error is `no terminal emulator could be launched (detected: <x>)`.
- Quoting for xfce4-terminal and xterm uses a POSIX single-quote quoter (`'` → `'\''`), not `%q`.

- [ ] **Step 1: Write failing tests**
  - `TestDetectEnv` (table over the fingerprints): `TMUX` → tmux, `KITTY_PID` → kitty, `WEZTERM_PANE` → wezterm, `KONSOLE_VERSION` → konsole, `TERMINATOR_UUID` → terminator, `ALACRITTY_WINDOW_ID` → alacritty, `GHOSTTY_RESOURCES_DIR` → ghostty.
  - `TestDetectParents`: a fake procfs chain `bash` → `foot` → `systemd` gives foot. A chain with no known name gives `unknown`.
  - `TestLaunchKittyTab`: the Fake `R` sees `kitty @ launch --type=tab --cwd=/w --tab-title=T --copy-env=yes -- railswitch …`, with `Detach` false and a 10s timeout.
  - `TestLaunchFallbackWindow`: detected tmux, `tmux new-window` fails (Fake exit 1), and there is no tmux window. With `DISPLAY` set and only `tmux` and `foot` on `LookPath`, `R` then sees `foot -D /w railswitch …` with `Detach: true`, and stderr names foot.
  - `TestAgentNewTerm` (cli): `agent --new-term claude -- -p x` calls `Launch` with argv `[<exe> -C <ws> -w feat agent claude -- -p x]` and exits 0.
  - `TestLaunchNoDisplayNoTTY` → the error contains `detected: tmux`.
  - `TestQuote`: `it's` → `'it'\''s'`.
- [ ] **Step 2:** `go test ./internal/term/ ./internal/sys/... ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Same command. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -m "feat(term): --new-term opens the agent in a new tab or window"`

### Task 7: nvim config repo, `railswitch nvim`, `nvim update`, `nvim reset-config`

**Files:**
- Create: `internal/lifecycle/nvim.go` (+ test), `internal/cli/nvim.go` (+ test), `docs/nvim.md` (attach hooks, plus a kitty example script, documented here only)
- Modify:
  - `internal/config/project.go` (C1: `NvimTool.UnmarshalTOML` decodes the table itself and refuses unknown keys, because BurntSushi marks every key under a custom unmarshaler as decoded);
  - `internal/pack/resolve.go`;
  - `internal/render/{env.go,runargs.go}` (nvim env and firewall extra);
  - `internal/naming` (+ test): `GitHost(url string) (string, bool)` moved from `cli/new.go`'s `repoHost`, which now calls it;
  - `internal/lifecycle/{up.go,exec.go}`: `L` gains `LookPath func(string) (string, error)` and `ConfigDir string` (the hooks dir parent; `machine.Dirs.Config`), set by `cli`; `interactive` takes the program name (`podman` or the host `nvim`);
  - `internal/pack/builtin/tools/nvim/entrypoints/entrypoint-nvim.sh`;
  - `internal/cli/new.go`;
  - the `Tools.Nvim` bool's other callers: `pack/resolve_test.go:16`, `render/apply_test.go:340`, `render/images_test.go:139`, `cli/new_test.go:172,244`;
  - the fixture `project.toml` and goldens;
  - `internal/cli/completion.go`, `docs/completion.md`.

**Interfaces:**
- Produces:
  ```go
  type NvimTool struct { ConfigRepo string `toml:"config_repo"`; ConfigRef string `toml:"config_ref"` }
  type Tools struct { Nvim *NvimTool `toml:"nvim"` }   // NvimTool.UnmarshalTOML refuses a bool: "[tools] nvim = true is now the table [tools.nvim] (D54)"
  func (l *L) Nvim(ctx context.Context, slug string) error          // start the unit if inactive; attach
  func (l *L) NvimUpdate(ctx context.Context, slug string) error
  func (l *L) NvimResetConfig(ctx context.Context, yes bool) error  // project-wide: <prefix>_nvim_config
  ```
- Env: the nvim service gets `NVIM_CONFIG_REPO` and `NVIM_CONFIG_REF` (layer project, source `tools.nvim`) when set. The repo's host (`naming.GitHost`: `https://host/…` or `git@host:…`) is added to nvim's `<p>.firewall.extra` annotation.
- Entrypoint, when `NVIM_CONFIG_REPO` is set:
  - empty `~/.config/nvim`: `git clone [--branch "$NVIM_CONFIG_REF"] "$NVIM_CONFIG_REPO" ~/.config/nvim`. A failure prints the error and continues to the editor;
  - non-empty: when `git -C ~/.config/nvim remote get-url origin` differs, print `nvim config is from <origin>, project.toml says <repo>: railswitch nvim reset-config` and continue;
  - it never deletes anything.
- Attach (C4):
  - run `<ConfigDir>/hooks/nvim-attach` if it is executable (env `RAILSWITCH_NVIM_SERVER=127.0.0.1:<NVIM_PORT>`, 30s timeout, failure only warns);
  - then `nvim --remote-ui --server 127.0.0.1:<port>` via `interactive` (NoTimeout) when `LookPath("nvim")` finds it, otherwise `podman exec -i [-t when l.TTY] <ctr> nvim --remote-ui --server 127.0.0.1:7777`;
  - then `nvim-detach` the same way.
- `nvim update`:
  - errors when `config_repo` is unset;
  - starts the unit if needed;
  - `podman exec <ctr> git -C /home/appuser/.config/nvim status --porcelain` → non-empty refuses, listing the paths;
  - origin ≠ repo refuses (Review Focus 5);
  - then `git pull --ff-only`, or with `config_ref`: `git fetch origin <ref>` + `git checkout --detach FETCH_HEAD`;
  - timeout 5m.
- `nvim reset-config`:
  - prompt (Down pattern, `--yes`);
  - stop every registered worktree's nvim unit (`StopLoaded`);
  - `VolumeRemove <prefix>_nvim_config` (no `-f`);
  - print `the next nvim start clones <repo>`.

- [ ] **Step 1: Write failing tests**
  - `TestToolsNvimTable`: `[tools.nvim]` (empty) gives non-nil `Nvim`. `[tools.nvim] config_repo="https://github.com/u/cfg"` decodes. `[tools] nvim = true` gives an error containing `[tools.nvim]`. `[tools.nvim] config_rpo = "x"` gives an error naming `config_rpo`.
  - `TestGitHost` (naming): `https://github.com/u/c` → github.com; `git@gitlab.com:u/c.git` → gitlab.com; `ssh://git@h.example:22/x` → h.example; `/local/path` → not ok.
  - `TestNvimEnvAndFirewall`: the nvim unit env has `NVIM_CONFIG_REPO`, and `Annotation=filial.firewall.extra=github.com`. With `git@gitlab.com:u/c.git`, the extra is `gitlab.com`.
  - `TestNvimAttachArgv` (Fake, injected `LookPath` finds nvim): the calls are the hook (when present under a temp `ConfigDir`), then `nvim --remote-ui --server 127.0.0.1:17002`, then `nvim-detach`. Without host nvim and with `TTY` set: `podman exec -i -t filial-feat-nvim nvim --remote-ui --server 127.0.0.1:7777`.
  - `TestNvimUpdateRefusesDirty`: Fake `status --porcelain` → ` M init.lua` gives an error containing `init.lua`, and there is no pull call.
  - `TestNvimUpdateRefusesOrigin`: origin `https://x/old` gives an error containing `https://x/old`, the configured URL and `nvim reset-config`.
  - `TestNvimUpdateRef`: `config_ref="v2"` → `git fetch origin v2`, then `git checkout --detach FETCH_HEAD`.
  - `TestNvimResetConfig`: with `--yes`, the calls are `systemctl --user stop` for each nvim unit, then `podman volume rm filial_nvim_config`. Without a TTY and without `--yes`, it refuses.
  - `TestEntrypointNvimClone` (bash, with git passthrough in a temp HOME): `NVIM_CONFIG_REPO` = a local bare repo and an empty config dir → cloned. Run again with a different repo → the message contains `reset-config` and the dir is unchanged.
- [ ] **Step 2:** `go test ./internal/config/ ./internal/naming/ ./internal/render/ ./internal/lifecycle/ ./internal/cli/ ./internal/pack/...`. Expected: FAIL.
- [ ] **Step 3: Implement.** Update the goldens (`-update`) and review the diff.
- [ ] **Step 4:** Same command. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -m "feat(nvim): config repo, railswitch nvim, update and reset-config"`

### Task 8: Machine-local mounts (D57)

**Files:**
- Modify:
  - `internal/machine/config.go` (+ test);
  - `internal/render/model.go` (`Inputs.Mounts`; applied per service);
  - `internal/workspace/workspace.go` (+ test) (`Model` loads the config and filters by prefix; `ReadHost` stats optional sources);
  - `internal/lifecycle/preflight.go` (the missing-source message names the `[[mount]]`);
  - `internal/doctor/doctor.go` (a `machine mounts` check: the config parses and each non-optional source exists);
  - `docs/json.md` (doctor names).

**Interfaces:**
- Produces:
  ```go
  // machine
  type Mount struct { Service, Source, Target string; RO *bool `toml:"ro"`; Optional bool; Projects []string } // toml: service, source, target, ro, optional, projects
  // Config gains Mounts []Mount `toml:"mount"`
  // render
  type MachineMount struct { Service, Source, Target string; RO, Optional bool; Index int } // Index: position in config.toml, for messages
  // Inputs gains Mounts []MachineMount; Host gains Missing map[string]bool (optional sources absent at render time)
  // render.Mount gains Origin string: "" for package mounts, `[[mount]] #<i> service = "<svc>"` for machine mounts
  ```
- `LoadConfig` validates each mount:
  - `service` passes `naming.ValidService`;
  - `source` is `~/`-expanded and absolute;
  - `target` is absolute and not `/app`;
  - `projects` entries match the prefix grammar;
  - otherwise the error names the entry index and field.
- Each mount applies to every service whose ID equals `Service`, whether a unit or a transient container. It renders as `Mount{Source: literal(src), Target, Options: "ro,z"|"z"}`. Optional mounts whose source is in `Host.Missing` are left out.

- [ ] **Step 1: Write failing tests**
  - `TestLoadConfigMounts`: `~/.local/state/kitty` is expanded against HOME; `ro` defaults to true; a relative target errors with `mount[0].target`.
  - `TestMachineMountsRender`:
    - `{service="nvim", source=<tmp dir>, target="/kitty-sock", ro=false}` → the nvim units get `Volume=<tmp>:/kitty-sock:z`, and the rails units don't;
    - `projects=["other"]` → no line;
    - `optional=true` with a missing source → no line.
  - `TestPreflightNamesMount`: a non-optional missing source fails with the path and `[[mount]] #0 service = "nvim"` (from `Mount.Origin`).
  - `TestDoctorMachineMounts`: a missing non-optional source → `FAIL` with a fix line.
- [ ] **Step 2:** `go test ./internal/machine/ ./internal/render/ ./internal/workspace/ ./internal/lifecycle/ ./internal/doctor/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Same command. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -m "feat(machine): machine-local mounts from config.toml [[mount]]"`

### Task 9: git-guard (D60)

**Files:**
- Create: `internal/gitguard/gitguard.go` (+ test), `internal/cli/hooks.go` (+ test)
- Modify:
  - `internal/render/model.go` (`worktreeMounts` appends the hooks mounts per the git-guard Global Constraint: `{base.git}/hooks` after a writable `.git`, and for the base worktree `/app/.git/hooks` for every service mounting `{worktree.dir}`) and the goldens;
  - `internal/lifecycle/{preflight.go,up.go}` (mkdir hooks; warning after `warnNotIsolated`);
  - `internal/doctor/doctor.go` (C8 warn level; a `git-guard` check per workspace);
  - `internal/cli/{doctor.go,new.go,completion.go}` (`!/githooks/`; subs `check backup restore`);
  - `docs/json.md` (doctor `warn`).

**Interfaces:**
- Produces:
  ```go
  type Finding struct { Kind, Path, Detail string }   // Kind: hook-missing-backup | hook-differs | hook-not-installed | config-key | pointer
  func Check(ctx context.Context, r sys.Runner, ws, baseDir string, slugs []string) ([]Finding, error) // slugs: linked worktrees only (the base slug's .git is a directory)
  func Backup(ws, baseDir string, names []string) ([]string, error)  // copies <base>/.git/hooks/<name> (all non-.sample when names empty) to <ws>/githooks/
  func Restore(ws, baseDir string, force bool) ([]string, error)     // keeps a differing installed hook unless force
  var ConfigAllowed = regexp.MustCompile(`^(core\.(repositoryformatversion|filemode|bare|logallrefupdates|ignorecase|precomposeunicode|symlinks)|branch\..+\.(remote|merge|rebase|pushremote|description)|remote\..+\.(url|pushurl|fetch|tagopt|prune|gh-resolved)|remote\.pushdefault|rerere\.(enabled|autoupdate)|extensions\.worktreeconfig|user\.(name|email))$`)
  ```
- The config key list comes from `git -c core.hooksPath=/dev/null -c core.fsmonitor=false config --file <base>/.git/config --name-only --list`. It never prints values.
- Pointers: each linked worktree's `<ws>/<slug>/.git` must read `gitdir: <base>/.git/worktrees/<name>`, where `<name>` contains no `/` and isn't `.` or `..`.
- Hook comparison is byte for byte plus the executable bit. `*.sample` files are ignored. Pointer files and installed hooks are read with `safefile.Read` (containers could write them before the guard).
- CLI: `hooks check` (exit 1 with findings, text `<kind> <path>: <detail>`), `hooks backup [<hook>…]`, `hooks restore [--force]`.
- `up` prints `WARNING: git-guard: <n> finding(s): …; railswitch hooks check`.
- `doctor` gets the check `git-guard` with `warn` set.

- [ ] **Step 1: Write failing tests**
  - `TestHooksMountReadOnly` (expected lines built with `filepath.Join(ws, …)`, not the golden's `/WORKSPACES`):
    - in feat, the nvim and agent units have `Volume=<ws>/master/.git/hooks:<ws>/master/.git/hooks:ro,z` after the `.git` line; the feat rails unit (whose `.git` is `ro`) has no hooks line;
    - in master, the rails, deps, nvim and agent units all have `Volume=<ws>/master/.git/hooks:/app/.git/hooks:ro,z`.
  - `TestPreflightCreatesHooks`: when `<base>/.git/hooks` is missing, Preflight creates it with mode 0755.
  - `TestCheck` (real git in temp repos):
    - an installed `pre-commit` with no backup → `hook-missing-backup`;
    - backed up, with installed content differing → `hook-differs`;
    - `git config core.hooksPath x` → `config-key core.hookspath`, and the value never appears in any `Detail`;
    - a linked worktree whose `.git` reads `gitdir: /tmp/evil` → `pointer`;
    - a clean repo → no findings.
  - `TestBackupRestore`:
    - backup writes `githooks/pre-commit` with the same mode;
    - restore over a differing installed hook without `--force` keeps it and reports it;
    - with `--force` it replaces it.
  - `TestUpWarnsGitGuard`: the warning appears and `up` succeeds.
  - `TestDoctorWarnLevel`: a finding gives `warn: true`, `ok: false`, exit code 0 when it is the only problem, and text status `warn`.
- [ ] **Step 2:** `go test ./internal/gitguard/ ./internal/render/ ./internal/lifecycle/ ./internal/doctor/ ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Update the goldens and review the diff.
- [ ] **Step 4:** Same command. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -m "feat(gitguard): read-only .git/hooks, hooks check/backup/restore"`

### Task 10: Opt-in commit signing (D61)

**Files:**
- Modify:
  - `internal/config/{project.go,edit.go}` (`Secret.GitSigning`; `AddSecret` writes the bool `git_signing = true`, a non-string key);
  - `internal/render/env.go` (`gitWiring` collects signing pairs even when no `git_host` exists, and then also writes the host's `GIT_AUTHOR_*`/`GIT_COMMITTER_*`: a signed commit needs an identity);
  - `internal/cli/secret.go` (`secret add … --git-signing`);
  - `internal/pack/builtin/tools/nvim/Containerfile.nvim` (adds `openssh-client`), which changes the nvim image tag in the goldens;
  - `AGENTS.md`, `docs/json.md` (`secret ls` gains `git_signing`).

**Interfaces:**
- Validation: `git_signing` requires `env == ""` (file delivery) and is incompatible with `git_host`, which needs its own secret. The error names the secret.
- Pairs, in order, for each service that receives the secret: `gpg.format=ssh`, `user.signingkey=/run/secrets/<name>`, `commit.gpgsign=true`, `tag.gpgsign=true`. They are appended after the credential pairs in the same `GIT_CONFIG_COUNT`.

- [ ] **Step 1: Write failing tests**
  - `TestGitSigningEnv`: `[secrets.sign] services=["agent-claude"] git_signing=true` gives `agent-claude` the env `GIT_CONFIG_KEY_n`/`VALUE_n` with these four pairs, a correct `GIT_CONFIG_COUNT`, and `GIT_AUTHOR_NAME`. `rails` gets none of them.
  - With `github_token` (git_host) on the same service, the count covers both sets, and the credential pairs come first.
  - `TestGitSigningValidate`: `env="X"` → an error naming `git_signing`. `git_host` together with `git_signing` → an error.
  - `TestSecretAddGitSigning` (cli): `project.toml` gains `git_signing = true`, and the value arrives through stdin only.
- [ ] **Step 2:** `go test ./internal/config/ ./internal/render/ ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Run `go test ./internal/render -run TestGolden -update` and check the diff: only the nvim `Image=` tags change.
- [ ] **Step 4:** Same command. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -m "feat(secrets): opt-in SSH commit signing key"`

### Task 11: `railswitch db dump` (C3)

**Files:**
- Create: `internal/lifecycle/dump.go` (+ test), `internal/cli/db.go` (+ test)
- Modify:
  - `internal/pack/format.go` (`Option.Dump`);
  - `internal/pack/load.go` (check: `command` non-empty, `file` a bare name ending in no `/`);
  - `internal/pack/builtin/options/postgres/option.toml`;
  - `internal/cli/{cli.go,completion.go}`, `docs/completion.md`.

**Interfaces:**
- Produces:
  ```go
  type Dump struct { Command []string `toml:"command"`; File string `toml:"file"` } // values expand {db}, {service}
  func (l *L) DBDump(ctx context.Context, slug string) ([]string, error)          // written paths
  ```
- Postgres: `[dump] command = ["pg_dump", "-Fc", "-U", "postgres", "{db}"]`, `file = "{db}.dump"`.
- Flow:
  1. The `database` slot's option must have `[dump]`, otherwise the error is `the database option <ref> declares no [dump]`.
  2. Its container must be running, otherwise `<ctr> is not running: railswitch up`.
  3. `podman exec <ctr> <command…>` (timeout 30m) streams stdout into `<ws>/db-dumps/.<file>.tmp-*` (0600), then is renamed over `<file>`.
  4. Then, for each worktree volume with `Seed`: `podman volume export <vol>` → `<seed>` (gzipped when it ends in `.gz`), temp file plus rename too.
  5. It prints each path with its size.
- On failure the temp file is removed and the old dump is untouched.

- [ ] **Step 1: Write failing tests**
  - `TestDBDump` (Fake writes `PGDMP…` to stdout): `<ws>/db-dumps/filial_dev.dump` holds exactly those bytes with mode 0600. The call is `podman exec filial-feat-db pg_dump -Fc -U postgres filial_dev` (the fixture's `config/database.yml` names `filial_dev`). `rustfs_data.tar.gz` is a valid gzip of the Fake's export bytes.
  - `TestDBDumpFailureKeepsOld`: an existing dump, then a pg_dump exit 1 → the old bytes are unchanged and no `.tmp-` file remains.
  - `TestDBDumpNotRunning` → the error contains `railswitch up`.
  - `TestDBDumpNoOption`: with `database = "none"` → an error.
- [ ] **Step 2:** `go test ./internal/lifecycle/ ./internal/cli/ ./internal/pack/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Same command. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -m "feat: railswitch db dump"`

### Task 12: Genericity, agent e2e, docs, Stage 4 acceptance

**Files:**
- Create:
  - `internal/genericity_test.go`: package `internal_test`. It walks the repo's `internal/` and `cmd/`, skipping `_test.go` files and `testdata/`. Every non-test file that matches `(?i)filial` is a failure. A file that matches `(?i)claude` fails unless it is under `internal/pack/builtin/agents/claude/`. A terminal name from C5 outside `internal/term/` fails.
  - `internal/lifecycle/e2e_agents_integration_test.go` (`//go:build integration`; `RAILSWITCH_E2E_REPO`): see Steps.
  - `internal/lifecycle/testdata/agents/probe/{agent.toml,Containerfile.probe}`: `ARG BASE_IMAGE` / `FROM ${BASE_IMAGE}` / `CMD ["sh"]`, `allow=["example.com"]`.
  - `docs/stage4-acceptance.md`: prerequisites; the agent e2e command; the **Filial run** (user, host); the gap log table (gap → generic feature → commit); the run log.
- Modify:
  - `AGENTS.md` (decisions D1–D61; new commands; agents, nvim, hooks, db, mounts), `docs/json.md`, `docs/completion.md`;
  - the non-test files `TestGenericity` already flags before this stage, using neutral example names: `internal/naming/naming.go:79-109` (comments `filial-feat-rails` …), `internal/render/quadlet.go:148` (`s3.master.filial.localhost`), `internal/firewall/assets/hook.sh:7` (`claude`), and any other the test lists.

- [ ] **Step 1: `TestGenericity`.** Run `go test ./internal/ -run TestGenericity`. Expected: PASS. A failure lists the files; fix them by moving the knowledge into packages or settings.
- [ ] **Step 2: Write `TestE2EAgents`.** It creates a project from `RAILSWITCH_E2E_REPO` (default agent `claude@1`), copies `testdata/agents/probe` into `<ws>/agents/probe`, adds `[agents.probe] use="local:probe@1" run="service"`, then runs, in order: `railswitch lock`; `containerfile eject probe`; `secret add probe_key --service agent-probe` (value on stdin); `secret set claude_token` (a dummy value on stdin: `claude --version` needs no valid token); `up`. It asserts:
  - `systemctl --user is-active <p>-main-agent-probe.service` is `active`, and no `<p>-main-agent-claude.container` unit file exists;
  - `podman exec <p>-main-agent-probe test -f /run/secrets/probe_key` succeeds;
  - `railswitch agent claude -- --version` exits 0 and prints a version. `railswitch agent claude -- sh -c 'test ! -e /run/secrets/probe_key'` exits 0: a first argument not starting with `-` takes the entrypoint's `exec "$@"` branch;
  - the probe's annotation `<p>.firewall=agent-probe` with mode `enforce`;
  - `podman exec <p>-main-agent-probe curl -sS -m 5 https://api.anthropic.com` fails (exit 7 or 28), and `https://example.com` succeeds;
  - `railswitch --json agent ls` lists both agents, with `claude` run `on-demand` and `probe` run `service`;
  - cleanup: `project rm <p> --yes --purge`.
- [ ] **Step 3: Docs.** Write `docs/stage4-acceptance.md`:
  - **Filial steps (user):** stop the old stack; `railswitch project new <ws> --repo <filial repo> --prefix filial --option …` with the slots Filial uses; `[image] addons` and env settings in its own `project.toml`; `wt add` for the needed branches; `up`; the app at `http://master.filial.localhost/`; `agent`; `nvim`; `db dump`, then delete the db volume and `up` again to restore.
  - **Gap log** (gap → generic feature → commit).
  - **Done checklist:** the roadmap's Stage 4 "Done" bullets, verbatim.
- [ ] **Step 4:** `make check && git commit -m "test: Stage 4 genericity and agent e2e; acceptance doc"`
- [ ] **Step 5 (user, host):** `RAILSWITCH_E2E_REPO=/tmp/rs-demo/demo go test -tags integration ./internal/lifecycle/ -run E2EAgents -timeout 60m -v`, then the Filial steps. Every gap goes through a fix task (generic feature + test) and into the gap log. The run log is recorded as in Stage 3.
