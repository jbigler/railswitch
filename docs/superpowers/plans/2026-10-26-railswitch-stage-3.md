# railswitch Stage 3 (machine layer) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Two projects run at once on one machine without collisions or cross-project reach. The plan adds:
- a shared log viewer;
- tailnet sharing that never touches another project's serves;
- `railswitch machine` and `railswitch project rm`;
- shell completion.

**Architecture:** The machine-wide state stays in `internal/machine`. That covers the registry, the shared Traefik unit, a new reserved machine network and Dozzle. `render` keeps owning every unit text. `lifecycle` reconciles live state:
- Traefik's network attachments;
- tailnet serves.

Sharing is a new env layer (7) plus `tailscale serve` on each worktree's own HTTPS port. Isolation between projects is a Traefik router rule: a request is accepted only from the project's own subnet or from the host.

**Tech Stack:** Go (stdlib + `github.com/BurntSushi/toml`), rootless podman 5+, Quadlet, systemd user units, Traefik v3.7, Dozzle `docker.io/amir20/dozzle:v11.3.5`, the `tailscale` CLI 1.104+.

**Spec:**
- `handoff/HANDOFF.md`: §4 Machine-wide layer, §3.7 layer 7, §7, §8 Stage 3, §9 open items;
- the roadmap's Stage 3 section and decisions D1–D45 in `docs/superpowers/plans/2026-10-25-railswitch-roadmap.md`;
- the Machine board in `docs/reference/design-data.md` §6.

**User decisions, 2026-10-26, binding:**
- **Share:** every shared worktree gets its own tailnet HTTPS port, equal to its loopback host port. `unshare` turns off only those ports, and nothing ever runs `tailscale serve reset`.
- **Dozzle:** kept, at `logs.localhost`.
- **`railswitch project rm`:** added in this stage.
- **S3 endpoint:** the rustfs option gives the endpoint through Traefik, `http://s3.<worktree host>`. Workers join the proxy network so they can reach it. While shared, the endpoint becomes the tailnet URL.
- **Shell completion:** added for bash, zsh and fish.

## Global Constraints

Every Global Constraint of `docs/superpowers/plans/2026-10-25-railswitch-stages-0-2.md` still holds: names, paths, argv-only runner, timeouts, secrets, hardening, schema versions, `--json`, loopback-only publishing, exact names, no prune, `safefile`, the pinned port table and `make check` before every commit. Added or changed here:

- **Subnets:**
  - projects get `10.214.<n>.0/24` with n in **0..254**;
  - **10.214.255.0/24 is reserved** for the machine network `railswitch_machine` (gateway `.1`, Traefik `.2`, Dozzle `.3`), and is never handed to a project;
  - like every subnet, it is skipped if another podman network already uses it. In that case railswitch refuses to start the machine units and names the network.
- **Port bases:**
  - each project's port bases (every `[[ports]]` base of its pack, options and tools) are recorded in the registry;
  - two different bases on one machine must be at least `MaxWorktreeID + 1 = 100` apart, otherwise base+ID ranges would overlap;
  - a worktree ID is handed out only if none of its ports (every recorded base + ID) is a listening TCP socket.
- **Host isolation (D50):**
  - every Traefik router a project renders matches only clients in that project's subnet or in `HostClients`;
  - `HostClients` is the source address set for host traffic arriving through the published `127.0.0.1:80`, pinned in `internal/machine/hostclients.go` after measuring it on a real host (Task 2);
  - the `logs.localhost` router (Dozzle) matches only `HostClients`.
- **Sharing:**
  - tailnet port = the worktree's loopback host port (app `13000+ID`, storage `19000+ID`);
  - commands are `tailscale serve --bg --https=<port> http://127.0.0.1:<port>` and `tailscale serve --https=<port> off`;
  - never `tailscale serve reset`;
  - never turn off a port railswitch didn't turn on.
- **Read commands:** `machine` joins the D40 list; its fields are documented in `docs/json.md`.

## Review Focus

1. **Host traffic refused:** an empty `HostClients` set, or the wrong one, refuses every browser request on the host (404 from Traefik). The two-project test fetches the app and an `s3.` URL from the host, from the project's own browser container, and from the other project's app container (expect refused). *Pinned in Tasks 2 and 9.*
2. **A port already in use by another program:** a dev server already on `127.0.0.1:13001` before `wt add`. That ID is skipped, never handed out. *Pinned in Task 1.*
3. **Unshare while another project is shared:** project B shared on `13005`, then project A unshares. Only A's ports are turned off. A tailnet port held by something else is never overwritten. *Pinned in Task 5.*
4. **`project rm` with similar prefixes:** `api` and `api2`. Removing `api` leaves every `api2` container, unit, volume, network and Traefik attachment. *Pinned in Task 6.*
5. **Adding a project while others run:** Traefik joins the new project's network live. The other projects' routes never drop: no restart. *Pinned in Task 3.*

---

## File Structure

```
internal/machine/registry.go       # + port bases per project, MachineSubnet, ID choice that skips listening ports
internal/machine/listen.go         # Listening(procRoot) from /proc/net/tcp{,6}
internal/machine/hostclients.go    # HostClients: measured host source CIDRs (Task 2)
internal/machine/proxy.go          # + machine network unit, Dozzle unit, Traefik on the machine network
internal/render/quadlet.go         # router rules gain ClientIP; workers join proxy; share AddHost
internal/render/env.go             # layer 7 (share)
internal/render/model.go           # Values share.*, port names marked share
internal/pack/format.go            # [[ports]] share = true; [share] env (pack) / [share] gives (option)
internal/pack/builtin/...          # rails pack app port share, rustfs s3 port share, rustfs S3_ENDPOINT
internal/tailscale/tailscale.go    # Self, ServeOn, ServeOff, ServedPorts
internal/lifecycle/traefik.go      # attach/detach Traefik live
internal/lifecycle/share.go        # Share, Unshare
internal/lifecycle/project.go      # ProjectRemove
internal/cli/{machine,share,project,completion}.go
docs/json.md, docs/stage3-acceptance.md
```

---

### Task 1: Port bases in the registry; IDs skip listening ports

**Files:**
- Create: `internal/machine/listen.go`, `internal/machine/listen_test.go`, `internal/machine/testdata/proc/net/{tcp,tcp6}`
- Modify:
  - `internal/machine/registry.go` (+ test): `ProjectEntry.Ports`, `MachineSubnet`, `AddWorktree`;
  - the callers `internal/cli/new.go` and `internal/worktree/add.go`;
  - `internal/workspace/workspace.go`, so `lock`/`render` refresh the project's bases.

**Interfaces:**
- Produces:

  ```go
  // machine
  const MachineSubnet = 255                       // reserved; projects get 0..254
  type ProjectEntry struct { …; Ports map[string]int `toml:"ports"` } // port name → base
  func (r *Registry) SetPorts(prefix string, ports map[string]int) error // error naming both projects and bases when a base is within 99 of another project's different base
  func Listening(procRoot string) (map[int]bool, error)                  // LISTEN (st 0A) ports in <procRoot>/net/tcp and tcp6, any local address
  func (r *Registry) AddWorktree(prefix, slug string, busy map[int]bool) (WorktreeEntry, error) // lowest free ID in 1..99 whose base+ID for every base recorded on the machine is not in busy
  ```
  The model's port bases come from `render.Model.PortBases() map[string]int`, which this task adds to `internal/render/model.go`.
- Consumes: `render.Model` services' ports (Task 8 of Stages 0–2).

**Rules:**
- `new` and `init` call `SetPorts` right after `AddProject`. `workspace.Render` calls it whenever the model's bases differ from the registered ones; a conflict is a render error.
- `wt add` passes `Listening("/proc")`. The message for "no ID free" names the first blocking port and its owner if known: only the port, because the inode→process lookup isn't done.

- [ ] **Step 1: Write failing tests**
  - `TestListening`: the fixture `/proc/net/tcp` has `0100007F:3335 … 0A` (127.0.0.1:13109) and a `01` (ESTABLISHED) line for 13110. The fixture `tcp6` has `…:4A39 … 0A` (19001). The result is exactly `{13109, 19001}`.
  - `TestAddWorktreeSkipsBusy`: bases `{app:13000, s3:19000}`, busy `{13001}`; the result is ID 2. With busy `{13001, 19002}` it's ID 3.
  - `TestSetPortsConflict`: project a `{app:13000}`, project b `{x:13050}`; the error names `a`, `b`, `13000` and `13050`. `{app:13000}` for b is fine (same base).
  - `TestMachineSubnetReserved`: with 0..254 used, `AddProject` errors; index 255 is never returned. With `taken` holding `10.214.255.0/24`, project allocation is unaffected.
  - `TestPortBases`: the Filial fixture model's `PortBases()` is `{app:13000, debug:33000, nvim:17000, playwright:18888, s3:19000, db:55432}`. Use the real names from the packs. Adjust the expected names to whatever the pack files declare and keep the bases.
- [ ] **Step 2:** `go test ./internal/machine/ ./internal/render/ ./internal/worktree/ ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Same command. Expected: PASS.
- [ ] **Step 5:** `make check && git add <files> && git commit -m "feat(machine): port bases per project; IDs skip listening ports"`

### Task 2: Router isolation (ClientIP) and the host source measurement

**Files:**
- Create:
  - `internal/machine/hostclients.go`;
  - `internal/render/isolation_integration_test.go` (`//go:build integration`);
  - `docs/stage3-acceptance.md` (procedure; this task writes its "Host source address" section).
- Modify: `internal/render/quadlet.go` (+ goldens).

**Interfaces:**
- Produces: `machine.HostClients []netip.Prefix`, and render's router rule:

  ```
  Host(`<h>`) && (ClientIP(`10.214.<n>.0/24`) || ClientIP(`<each HostClients prefix>`))
  ```

**Steps:**
- [ ] **Step 1: Measure on the user's host (user action; the sandbox has no podman).** Write the procedure into `docs/stage3-acceptance.md` and ask the user to run it. With any project up, set Traefik to JSON access logs temporarily:
  - add `--accesslog=true --accesslog.format=json` to the running unit via `systemctl --user edit`;
  - or run `podman logs railswitch-traefik` after `curl http://<slug>.<prefix>.localhost/` from the host.

  Record `ClientHost` for a host request, and for a request made from the project's rails container (`podman exec <prefix>-<slug>-rails curl -s http://<slug>.<prefix>.localhost/`). Write both into the doc.
- [ ] **Step 2:** Set `HostClients` to the measured host source as a /32 or /128, plus `127.0.0.1/32` and `::1/128`. If the host source is inside a project subnet (e.g. the netns gateway `10.214.<n>.1`), use the per-project gateway: replace `HostClients` with `func HostClients(n int) []netip.Prefix` returning `machine.Gateway(n)/32` + loopback, and note it in D50.
- [ ] **Step 3: Write failing tests.**
  - `TestRouterClientIP`: every `traefik.http.routers.*.rule` label in the filial goldens equals the form above with `10.214.3.0/24`.
  - `TestIsolation` (integration): two fixture projects up; skipped without `RAILSWITCH_E2E_REPO`; reuses the e2e helpers. Expect:
    - a host `curl` of each app is 200;
    - each project's own rails container gets 200 for its own host;
    - A's rails container gets 404 for `-H "Host: <B host>" http://<A traefik ip>/`;
    - A's rails container gets a connection failure for B's app container IP.
- [ ] **Step 4:** `go test ./internal/render/`. Expected: FAIL, then implement, then PASS. Run the integration test on the host (user action) and record the result in `docs/stage3-acceptance.md`.
- [ ] **Step 5:** `make check && git commit -m "feat(render): routers accept only the project's subnet and the host"`

### Task 3: Machine network, Dozzle, and live Traefik attachment

**Files:**
- Modify:
  - `internal/machine/proxy.go` (+ goldens);
  - `internal/unithash/unithash.go`: a variant that skips `Network=` lines;
  - `internal/lifecycle/up.go`;
  - `internal/cli/new.go` (attach after register);
  - the app units' `Wants=`.
- Create: `internal/lifecycle/traefik.go` (+ test).

**Interfaces:**
- Produces:
  - `machine.ProxyUnits(r)` also returns `railswitch-machine.network` (`NetworkName=railswitch_machine`, Subnet 10.214.255.0/24, Gateway .1, IPRange `10.214.255.128/25`, `Options=mtu=1400`) and `railswitch-dozzle.container`;
  - Traefik gets `Network=railswitch-machine.network:ip=10.214.255.2`;
  - functions:

  ```go
  func StampExcept(unit []byte, key string, skip func(line string) bool, inputs ...[]byte) []byte // unithash: hash ignores lines skip reports
  func (l *L) AttachTraefik(ctx context.Context) error // lifecycle: connect missing project networks (podman network connect --ip <TraefikIP> railswitch-traefik <prefix>_proxy), disconnect railswitch-traefik from networks not in the registry and not railswitch_machine; no-op when Traefik isn't running
  ```

- **Dozzle unit:**
  - `Image=docker.io/amir20/dozzle:v11.3.5`;
  - `ContainerName=railswitch-dozzle`;
  - `Network=railswitch-machine.network:ip=10.214.255.3`;
  - `Volume=%t/podman/podman.sock:/var/run/docker.sock:ro`;
  - `NoNewPrivileges=true`, `DropCapability=all`, `Memory=256m`, `PidsLimit=256`;
  - labels:
    - `traefik.enable=true`;
    - `traefik.docker.network=railswitch_machine`;
    - router `railswitch-logs` with rule ``Host(`logs.localhost`) && (<HostClients ClientIPs>)``;
    - service port 8080;
  - `Restart=on-failure`;
  - the marker line `(machine)`.
- **Wants:** every app unit gets `Wants=railswitch-dozzle.service` next to Traefik's.
- **Traefik's unit-hash** (`railswitch.unit-hash`) uses `StampExcept` that skips `Network=` lines. Adding or removing a project changes the unit file, so a daemon-reload happens, but the hash stays the same, so no restart. `AttachTraefik` makes the live state match. `up` calls it after starting the project's proxy network service. `new` and `init` call it after registering. Task 6 calls it after unregistering.

- [ ] **Step 1: Write failing tests**
  - `TestProxyUnitsMachine`: goldens for three units; Dozzle lints clean; the Traefik unit has three `Network=` lines for two projects.
  - `TestTraefikHashIgnoresNetworks`: adding a project keeps the label value.
  - `TestAttachTraefik`: the Fake inspect output lists `a_proxy`, `gone_proxy` and `railswitch_machine`; registry {a, b}. The argvs are exactly `podman network connect --ip 10.214.<b>.2 railswitch-traefik b_proxy` and `podman network disconnect railswitch-traefik gone_proxy`. There is no `systemctl restart railswitch-traefik.service`.
  - `TestUpNoTraefikRestartOnNewProject` (Review Focus 5): only network lines changed, so no restart.
- [ ] **Step 2:** `go test ./internal/machine/ ./internal/unithash/ ./internal/lifecycle/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Read networks with `podman container inspect --format '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' railswitch-traefik` and add the wrapper `Podman.Networks(ctx, name)` with its test. `doctor` gains `machine subnet free` (10.214.255.0/24 not used by a foreign network).
- [ ] **Step 4:** Expected: PASS; `RAILSWITCH_QUADLET=$HOME/.local/bin/quadlet go test -tags integration ./internal/render/ -run TestQuadletDryRun` PASS (extend it to include the machine units).
- [ ] **Step 5:** `make check && git commit -m "feat(machine): Dozzle at logs.localhost; Traefik joins projects live"`

### Task 4: S3 endpoint through Traefik; workers on the proxy network

**Files:**
- Modify:
  - `internal/pack/builtin/options/rustfs/option.toml`;
  - `internal/render/model.go`: networks — every app, worker and tool package joins `proxy`; only routed services get router labels;
  - `internal/render/quadlet.go`: `AddHost` for every proxy-network container;
  - goldens.

**Rules:**
- **The endpoint:** rustfs `[gives] S3_ENDPOINT = "http://s3.{worktree.host}"`. It's reachable from the host (Traefik on 127.0.0.1:80) and from every proxy-network container (`AddHost` → Traefik `.2`, allowed by Task 2's subnet rule). Server-side S3 traffic now goes through Traefik.
- **Worker containers:** they get `Network=<prefix>-<slug>-dev.network` plus `Network=<prefix>-proxy.network`, the `AddHost` entries, and no `traefik.*` labels.

- [ ] **Step 1: Write failing tests**
  - `TestS3EndpointThroughTraefik`: rails env `S3_ENDPOINT=http://s3.master.filial.localhost`.
  - `TestWorkerOnProxy`: the sidekiq worker unit has both `Network=` lines and an `AddHost=s3.master.filial.localhost:10.214.3.2`, and no `Label=traefik`.
  - Update `docs/stage1-equivalence.md`'s rustfs and env rows: one line each, citing D49.
- [ ] **Step 2–4:** `go test ./internal/render/ ./internal/pack/`: FAIL → implement → PASS, and run the Quadlet dry-run.
- [ ] **Step 5:** `make check && git commit -m "feat(rustfs): S3 endpoint through Traefik; workers reach it"`

### Task 5: Tailnet share (layer 7) without reset

**Files:**
- Create:
  - `internal/tailscale/tailscale.go` (+ test);
  - `internal/lifecycle/share.go` (+ test);
  - `internal/cli/share.go` (+ test).
- Modify:
  - `internal/pack/format.go` + `load.go`: `[[ports]] share = true`; pack `[share] env`; option `[share] gives`;
  - `internal/config/project.go`: project `[share] env`, a layer-7 project override;
  - `internal/render/{model,env,quadlet}.go`;
  - `internal/machine/registry.go`: `WorktreeEntry.Share *ShareEntry`;
  - built-ins: rails app port `share = true` with `[share] env RAILS_DEVELOPMENT_HOSTS = "{worktree.host},{share.host}"`; rustfs `s3` port `share = true` with `[share] gives S3_ENDPOINT = "{share.url.s3}"`;
  - `docs/json.md` (`wt ls` gains `share`).

**Interfaces:**
- Produces:

  ```go
  // tailscale
  type T struct{ R sys.Runner }
  func (t T) Self(ctx) (host string, ip netip.Addr, err error) // `tailscale status --json` .Self.DNSName (trailing dot trimmed) + .Self.TailscaleIPs first v4
  func (t T) Served(ctx) (map[int]string, error)               // `tailscale serve status --json`: HTTPS port → proxy target
  func (t T) ServeOn(ctx, port int) error                      // tailscale serve --bg --https=<port> http://127.0.0.1:<port>
  func (t T) ServeOff(ctx, port int) error                     // tailscale serve --https=<port> off
  // machine
  type ShareEntry struct{ Host, IP string }                    // WorktreeEntry.Share, toml "share"
  // lifecycle
  func (l *L) Share(ctx, slug string) error
  func (l *L) Unshare(ctx, slug string) error
  ```
- **Values (render):**
  - while shared: `share.host`, and `share.url.<port name>` = `https://<host>:<base+ID>` for every port with `share = true`;
  - unset otherwise. Layer 7 is applied only while shared and is the highest layer;
  - routed containers get `AddHost=<share.host>:<share IP>`.

**Rules:**
- **`Share`:**
  1. `tailscale` on PATH and `Self` ok, else an error naming `tailscale up`;
  2. for each share port, `Served` holds it pointing elsewhere → refuse, naming the port and target. Pointing at `http://127.0.0.1:<port>` already → fine (idempotent);
  3. registry `Share = {host, ip}`, Save;
  4. Render;
  5. `Up(slug)`, so env changes restart the app (unit-hash covers env);
  6. `ServeOn` each share port;
  7. print `shared: https://<host>:<app port>/ (storage https://<host>:<s3 port>/)`.

  A `ServeOn` failure for a non-operator user is reported with the fix `sudo tailscale set --operator=$USER`.
- **`Unshare`:** `ServeOff` only the ports `Served` maps to `http://127.0.0.1:<port>`, clear `Share`, Render, `Up`.
- **Cleanup:** `down`, `wt rm` and `project rm` run `Unshare` first when shared.

- [ ] **Step 1: Write failing tests** (Fake runner; `tailscale` argvs):
  - `TestShareArgv`: the exact argvs `tailscale serve --bg --https=13002 http://127.0.0.1:13002` and the same for 19002. No argv contains `reset`.
  - `TestShareRefusesForeignPort`: `Served` has `{13002: "http://127.0.0.1:3000"}`. The error names 13002, and no `serve --bg` call is made.
  - `TestUnshareOnlyOwn` (Review Focus 3): `Served` has `{13002: own, 13005: own-of-other-worktree, 443: "http://127.0.0.1:9999"}`. Unsharing worktree 2 turns off exactly 13002 and 19002 (when present).
  - `TestShareEnvLayer`:
    - while shared, rails env `RAILS_DEVELOPMENT_HOSTS=master.filial.localhost,box.tail1234.ts.net`;
    - rails env `S3_ENDPOINT=https://box.tail1234.ts.net:19001`, layer `share`;
    - `AddHost=box.tail1234.ts.net:100.64.0.7`;
    - unshared, both are back to their layer-3/4 values.
  - `TestDownUnsharesFirst`.
- [ ] **Step 2–4:** `go test ./internal/tailscale/ ./internal/lifecycle/ ./internal/render/ ./internal/cli/`: FAIL → implement → PASS.
- [ ] **Step 5:** `make check && git commit -m "feat: share a worktree on its own tailnet HTTPS port"`

### Task 6: `railswitch project rm`

**Files:**
- Create: `internal/lifecycle/project.go` (+ test), `internal/cli/project.go` (+ test).

**Interfaces:**
- Produces: `func (l *L) ProjectRemove(ctx context.Context, prefix string, yes, purge bool) error`.
- `railswitch project rm <prefix> [--yes] [--purge]` asks before acting, defaulting to No, as `down` does.

**Rules:**
1. Unshare every shared worktree.
2. `Down(slug, yes=true)` for each worktree: exact names.
3. Remove every unit file whose marker names this prefix.
4. `podman network rm` the dev networks and `<prefix>_proxy`.
5. `AttachTraefik` (disconnect).
6. `RemoveProject` in the registry, then `RefreshProxyUnits`.
7. `--purge` also removes the project's shared `<prefix>_*` volumes, the workspace's `.railswitch/` and the base worktree's `git worktree remove`s. It never deletes the workspace directory or its git repo, and prints the path.

It works when the workspace directory is gone: names come from the registry and unit markers.

- [ ] **Step 1: Write failing tests**
  - `TestProjectRemoveExactNames` (Review Focus 4): registry {api, api2}. Every argv names only `api-…`, `api_…` or `api-proxy…` objects. Afterwards `api2`'s units still exist and the registry holds only api2.
  - `TestProjectRemoveDefaultsNo`.
  - `TestProjectRemoveMissingWorkspace`.
  - `TestProjectRemovePurgeKeepsRepo`.
- [ ] **Step 2–4:** FAIL → implement → PASS.
- [ ] **Step 5:** `make check && git commit -m "feat: railswitch project rm"`

### Task 7: `railswitch machine`

**Files:**
- Create: `internal/cli/machine.go` (+ test).
- Modify: `docs/json.md`.

**Output (text; `--json` has the same fields):**
- **projects:** prefix, workspace, subnet, Traefik IP, worktrees (slug, ID, ports by name, shared URL);
- **routes:** host → project/worktree/service, sorted;
- **machine:** Traefik and Dozzle state from `systemctl show`, and Traefik's attached networks (`Podman.Networks`) against the registry. A missing attachment is flagged `not attached: railswitch up`;
- **reach:** one row per network with its members, from the models (e.g. `demo-main-dev: rails, deps, db, cache`; `demo_proxy: Traefik, rails, worker`), plus the fixed lines:
  - "Traefik reaches every project network";
  - "a project's containers reach another project only through Traefik, which refuses them (D50)".

- [ ] **Step 1: Write failing tests**
  - `TestMachineTwoProjects`: two fixture projects (filial and the minimal fixture, prefix `mini`). Their IDs, ports, subnets, router names and hosts are pairwise disjoint (the roadmap's Stage 3 unit test).
  - `TestMachineJSON`: the field names.
  - `TestMachineFlagsDetached`.
- [ ] **Step 2–4:** FAIL → implement → PASS.
- [ ] **Step 5:** `make check && git commit -m "feat: railswitch machine"`

### Task 8: Shell completion

**Files:**
- Create: `internal/cli/completion.go` (+ test), `internal/cli/completion/{railswitch.bash,_railswitch.zsh,railswitch.fish}` (embedded).

**Interfaces:**
- `railswitch completion bash|zsh|fish` prints the script.
- Hidden: `railswitch __complete <args…>` prints one candidate per line for the last word.

**Candidates, by position:**
- the command names from the dispatch table;
- each command's subcommands and flags (from each command's `flag.FlagSet`, collected by running the parse setup with no args);
- after `-w` and for `wt rm`, `share`, `unshare`, the current workspace's worktree slugs, from the registry. Nothing outside a workspace;
- service ids for `logs`, `exec --service` and `fw`;
- secret names for `secret set|rm`;
- `containerfile` names for `show|eject`;
- registered prefixes for `project rm`.

Errors print nothing (completion never writes to stderr).

**The scripts** call `railswitch __complete` and contain no command list of their own. The README section (in `AGENTS.md`'s user-docs pointer, or a new `docs/completion.md`) shows:
- `railswitch completion bash > ~/.local/share/bash-completion/completions/railswitch`;
- zsh: into an fpath dir;
- fish: `~/.config/fish/completions/`.

- [ ] **Step 1: Write failing tests**
  - `TestCompleteCommands`: `__complete wt` lists `wt` subcommands `add, rm, ls`; an empty word lists every top-level command.
  - `TestCompleteSlugs`: in a fixture workspace, `__complete -w ""` gives `feat` and `master`.
  - `TestCompleteNoStderr`: outside a workspace, the output is empty and exit 0.
  - `TestCompletionScripts`:
    - each script is non-empty and names `__complete`;
    - `bash -n` passes on the bash script;
    - `zsh -n` and `fish -n` run only when those shells exist (skip otherwise).
- [ ] **Step 2–4:** FAIL → implement → PASS.
- [ ] **Step 5:** `make check && git commit -m "feat: shell completion for bash, zsh and fish"`

### Task 9: Stage 3 acceptance (two projects at once)

**Files:**
- Modify:
  - `internal/lifecycle/e2e_integration_test.go`: add `TestE2ETwoProjects`, skipped without `RAILSWITCH_E2E_REPO`;
  - `docs/stage3-acceptance.md`.

**Steps:**
- [ ] **Step 1: Write `TestE2ETwoProjects`.** It creates two projects from the same repo with prefixes `$RAILSWITCH_E2E_PREFIX` and `$RAILSWITCH_E2E_PREFIX-b`, then `up` on both base worktrees. Assertions:
  - each app host is 200 from the host;
  - each `s3.` host answers (`curl -s -o /dev/null -w %{http_code}` is not 000 or 404);
  - `railswitch machine --json` shows disjoint IDs, ports, subnets and hosts;
  - A's rails container gets a non-200 for B's host through A's Traefik IP;
  - A's rails container gets a connection failure for B's rails container IP;
  - after `share` on A's base worktree (only when `tailscale status` works; skip that part otherwise), `tailscale serve status --json` holds A's ports and nothing else changed.

  Cleanup: `project rm --yes` both, then check that no object of either prefix remains (`podman ps -a`, `podman volume ls`, `podman network ls`, unit files).
- [ ] **Step 2:** The user runs it on the host:

  ```sh
  RAILSWITCH_E2E_REPO=/tmp/rs-demo/demo RAILSWITCH_E2E_PREFIX=s3a \
    go test -tags integration ./internal/lifecycle/ -run E2ETwoProjects -timeout 60m
  ```

  Expected: PASS. Record the run log in `docs/stage3-acceptance.md`.
- [ ] **Step 3:** `make check && git commit -m "test: Stage 3 two-project acceptance"`
