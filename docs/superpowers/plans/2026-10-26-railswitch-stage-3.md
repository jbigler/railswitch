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

Sharing is a new env layer (7) plus `tailscale serve` on each worktree's own HTTPS port. Isolation between projects has two parts: every railswitch network is `isolate=true`, and a Traefik router rule accepts a request only from the project's own subnet or from the host.

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
  - every Traefik router a project renders matches only clients in that project's subnet or the host clause `machine.HostClientClause()`;
  - the host clause is static and pinned in `internal/machine/hostclients.go` from a measurement on a real host (Task 2);
  - the `logs.localhost` router (Dozzle) and the `traefik.localhost` router (Traefik API, `api@internal`) match only the host clause; Traefik no longer publishes `127.0.0.1:8080` or runs `--api.insecure`;
  - every railswitch network (proxy, dev, machine) has `Options=isolate=true`, so a container reaches another project only through Traefik.
- **Sharing:**
  - tailnet port = the worktree's loopback host port (app `13000+ID`, storage `19000+ID`);
  - commands are `tailscale serve --bg --https=<port> http://127.0.0.1:<port>` and `tailscale serve --https=<port> off`;
  - never `tailscale serve reset`;
  - never turn off a port railswitch didn't turn on.
- **Read commands:** `machine` joins the D40 list; its fields are documented in `docs/json.md`.

## Review Focus

1. **Host traffic refused:** a wrong host clause refuses every browser request on the host (404 from Traefik), and the host source can differ per Traefik network. The two-project test fetches the app, an `s3.` URL, `logs.localhost` and `traefik.localhost` from the host, from the project's own rails container, and from the other project's rails container (expect refused). *Pinned in Tasks 2 and 9.*
2. **A port already in use by another program:** a dev server already on `127.0.0.1:13001` before `wt add`. That ID is skipped, never handed out. *Pinned in Task 1.*
3. **Unshare while another project is shared:** project B shared on `13005`, then project A unshares. Only A's ports are turned off. A tailnet port held by something else is never overwritten. *Pinned in Task 5.*
4. **`project rm` with similar prefixes:** `api` and `api2`. Removing `api` leaves every `api2` container, unit, volume, network and Traefik attachment. *Pinned in Task 6.*
5. **Adding a project while others run:** Traefik joins the new project's network live. The other projects' routes never drop: no restart. *Pinned in Task 3.*

---

## File Structure

```
internal/machine/registry.go       # + port bases per project, MachineSubnet, ID choice that skips listening ports
internal/machine/listen.go         # Listening(procRoot) from /proc/net/tcp{,6}
internal/machine/hostclients.go    # HostClientClause: static host-source rule fragment (Task 2)
internal/machine/proxy.go          # + machine network unit, Dozzle unit, Traefik on the machine network
internal/render/quadlet.go         # router rules gain ClientIP; workers join proxy; share AddHost
internal/render/env.go             # layer 7 (share)
internal/render/model.go           # Values share.*, port names marked share
internal/pack/format.go            # [[ports]] share = true; [share] env (pack) / [share] gives (option)
internal/pack/builtin/...          # rails pack app port share, rustfs s3 port share, rustfs S3_ENDPOINT
internal/tailscale/tailscale.go    # Self, ServeOn, ServeOff, Served
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
  - `internal/machine/registry.go` (+ test): `ProjectEntry.Ports`, `MachineSubnet`, `AddWorktree`; the project subnet loop bound becomes 254;
  - the callers `internal/cli/new.go` and `internal/worktree/add.go`;
  - `internal/workspace/workspace.go`, so `lock`/`render` refresh the project's bases.

**Interfaces:**
- Produces:

  ```go
  // machine
  const MachineSubnet = 255                       // reserved; projects get 0..254
  type ProjectEntry struct { …; Ports map[string]int `toml:"ports"` } // port name → base
  func (r *Registry) SetPorts(prefix string, ports map[string]int) error // error naming both projects and bases when a base is within 99 of another project's different base, or naming the project and both port names when two of its own distinct bases are within 99
  func Listening(procRoot string) (map[int]bool, error)                  // LISTEN (st 0A) ports in <procRoot>/net/tcp and tcp6, any local address
  func (r *Registry) AddWorktree(prefix, slug string, busy map[int]bool) (WorktreeEntry, error) // lowest free ID in 1..99 whose base+ID for every base recorded on the machine is not in busy
  ```
  The model's port bases come from `render.Model.PortBases() map[string]int`, which this task adds to `internal/render/model.go`.
- Consumes: `render.Model` services' ports (Task 8 of Stages 0–2).

**Rules:**
- `new` and `init` call `SetPorts` right after `AddProject` and before `AddWorktree` (`registerAndRender`), so the base worktree's ID honours the new project's own bases. `workspace.Render` calls it only when `!check` and the model's bases differ from the registered ones; a conflict is a render error. With `check`, a base difference is reported as a change and nothing is saved.
- `wt add` passes `Listening("/proc")`. The message for "no ID free" names the first blocking port and its owner if known: only the port, because the inode→process lookup isn't done.

- [ ] **Step 1: Write failing tests**
  - `TestListening`: the fixture `/proc/net/tcp` has `0100007F:3335 … 0A` (127.0.0.1:13109) and a `01` (ESTABLISHED) line for 13110. The fixture `tcp6` has `…:4A39 … 0A` (19001). The result is exactly `{13109, 19001}`.
  - `TestAddWorktreeSkipsBusy`: bases `{app:13000, s3:19000}`, busy `{13001}`; the result is ID 2. With busy `{13001, 19002}` it's ID 3.
  - `TestSetPortsConflict`: project a `{app:13000}`, project b `{x:13050}`; the error names `a`, `b`, `13000` and `13050`. `{app:13000}` for b is fine (same base).
  - `TestSetPortsOwnSpacing`: project a `{app:13000, x:13050}`; the error names `a`, `app`, `x`, `13000` and `13050`.
  - `TestRenderCheckNoRegistryWrite`: bases differ from the registry; `Render(check=true)` reports a change and the registry file is byte-identical.
  - `TestNewSetsPortsBeforeWorktree`: busy `{13001}` from another program; `new` gives the base worktree ID 2.
  - `TestMachineSubnetReserved`: with 0..254 used, `AddProject` errors; index 255 is never returned. With `taken` holding `10.214.255.0/24`, project allocation is unaffected.
  - `TestPortBases`: the Filial fixture model's `PortBases()` is `{app:13000, debug:33000, nvim:17000, playwright:18888, s3:19000, db:55432}`. Use the real names from the packs. Adjust the expected names to whatever the pack files declare and keep the bases.
- [ ] **Step 2:** `go test ./internal/machine/ ./internal/render/ ./internal/worktree/ ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Same command. Expected: PASS.
- [ ] **Step 5:** `make check && git add <files> && git commit -m "feat(machine): port bases per project; IDs skip listening ports"`

### Task 2: Router isolation (ClientIP), network isolation, and the host source measurement

**Files:**
- Create:
  - `internal/machine/hostclients.go` (+ test);
  - `docs/stage3-acceptance.md` (procedure; this task writes its "Host source address" section, and notes that existing networks keep their old options until recreated: `railswitch down` every worktree, `podman network rm` the project's networks, `up`).
- Modify: `internal/render/quadlet.go` (+ goldens): router rules; `Options=isolate=true` on every proxy and dev network unit.

**Interfaces:**
- Produces:

  ```go
  // machine
  func HostClientClause() string // Traefik rule fragment admitting host traffic; static (never reads the registry), so adding a project never changes app labels
  ```
  and render's router rule for project subnet `10.214.<n>.0/24`:

  ```
  Host(`<h>`) && (ClientIP(`10.214.<n>.0/24`) || <HostClientClause()>)
  ```
  Traefik v3's `ClientIP` takes exactly one argument, so alternatives are joined with `||`.
- Network option: every railswitch network unit (`<prefix>-proxy.network`, `<prefix>-<slug>-dev.network`, and `railswitch-machine.network` in Task 3) carries `Options=isolate=true` next to `Options=mtu=1400`. On podman 5 / netavark, bridges otherwise forward to each other; podman 6 defaults to `strict`, so railswitch pins it.

**Steps:**
- [ ] **Step 1: Measure on the user's host (user action, already requested; the sandbox has no podman).** Write the procedure into `docs/stage3-acceptance.md`. With a project up:

  ```sh
  podman info --format '{{.Host.NetworkBackend}} {{.Host.RootlessNetworkCmd}}'
  podman run -d --rm --name rs-whoami --network <prefix>_proxy -p 127.0.0.1:18999:80 docker.io/traefik/whoami
  curl -s http://127.0.0.1:18999/ | grep RemoteAddr                                  # host
  podman exec <prefix>-<slug>-rails curl -s http://<whoami IP>/ | grep RemoteAddr    # the project's rails container
  podman rm -f rs-whoami
  ```
  Record both `RemoteAddr` values, whoami's own IP on `<prefix>_proxy`, and the `podman info` line in the doc.
- [ ] **Step 2: Pin the router's host clause from the outcome.**

  | Host curl's RemoteAddr | `HostClientClause()` | Router rule |
  |---|---|---|
  | (a) outside 10.214.0.0/16 (e.g. `127.0.0.1` or another rootless-netns range) | ``!ClientIP(`10.214.0.0/16`)`` | ``Host(`h`) && (ClientIP(`<project subnet>`) \|\| !ClientIP(`10.214.0.0/16`))`` |
  | (b) the receiving container's own address on the network (F2: rootlessport's child dials from inside the container's netns) | ``ClientIP(`10.214.0.2/32`) \|\| ClientIP(`10.214.1.2/32`) \|\| … \|\| ClientIP(`10.214.255.2/32`)``, generated for every n in 0..255 | ``Host(`h`) && (ClientIP(`<project subnet>`) \|\| ClientIP(`10.214.<n>.2/32`) for every n in 0..255)`` |
  | (c) anything else | — | stop: the controller decides with the user before coding |

  Both are safe: with `isolate=true`, another project's container can only reach Traefik from its own subnet with host part ≥ 3, which neither clause admits ((a) excludes all of 10.214.0.0/16; (b) admits only `.2`). Record the chosen row and the measurement in D50.
- [ ] **Step 3: Write failing tests.**
  - `TestHostClientClause`: equals the pinned row exactly. For (b): 256 `ClientIP` terms, the first `10.214.0.2/32`, the last `10.214.255.2/32`. Registering a project doesn't change it.
  - `TestRouterClientIP`: every `traefik.http.routers.*.rule` label in the filial goldens equals ``Host(`<h>`) && (ClientIP(`10.214.3.0/24`) || <HostClientClause()>)``.
  - `TestNetworkIsolate`: every `.network` unit in the filial goldens has `Options=isolate=true`.
  - The live cross-project checks (host 200, own container 200, other project refused) are in Task 9's `TestE2ETwoProjects`.
- [ ] **Step 4:** `go test ./internal/machine/ ./internal/render/`. Expected: FAIL, then implement, then PASS. `RAILSWITCH_QUADLET=$HOME/.local/bin/quadlet go test -tags integration ./internal/render/ -run TestQuadletDryRun` PASS.
- [ ] **Step 5:** `make check && git commit -m "feat(render): routers accept only the project's subnet and the host; isolated networks"`

### Task 3: Machine network, Dozzle, and live Traefik attachment

**Files:**
- Modify:
  - `internal/machine/proxy.go` (+ goldens): machine network, Dozzle, Traefik on the machine network, Traefik API router; `RefreshProxyUnits` writes or removes all three machine-wide units (`railswitch-traefik.container`, `railswitch-machine.network`, `railswitch-dozzle.container`); delete the per-project `Requires=`/`After=<prefix>-proxy-network.service` lines (Quadlet derives them from `Network=`, D27);
  - `internal/unithash/unithash.go`: a variant that skips lines;
  - `internal/lifecycle/up.go`: restart Traefik only when stale; start the proxy network service, then `AttachTraefik`; machine-subnet check; route wait through `traefik.localhost`;
  - `internal/lifecycle/preflight.go`, `internal/doctor`: the machine-subnet check;
  - `internal/podman/podman.go` (+ test): `Networks`;
  - the app units' `Wants=`.
- Create: `internal/lifecycle/traefik.go` (+ test).

**Interfaces:**
- Produces:
  - `machine.ProxyUnits(r)` also returns `railswitch-machine.network` (`NetworkName=railswitch_machine`, Subnet 10.214.255.0/24, Gateway .1, IPRange `10.214.255.128/25`, `Options=mtu=1400`, `Options=isolate=true`) and `railswitch-dozzle.container`;
  - Traefik gets `Network=railswitch-machine.network:ip=10.214.255.2`;
  - functions:

  ```go
  func StampExcept(unit []byte, key string, skip func(line string) bool, inputs ...[]byte) []byte // unithash: hash ignores lines skip reports
  func (p Podman) Networks(ctx context.Context, container string) ([]string, error)              // podman container inspect --format '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' <container>
  func (l *L) AttachTraefik(ctx context.Context) error
  // lifecycle, no-op when Traefik isn't running:
  // for each registered project whose <prefix>_proxy exists (a key of Podman.NetworkSubnets) and isn't attached:
  //   podman network connect --ip <TraefikIP> <prefix>_proxy railswitch-traefik
  // registered networks that don't exist yet are skipped;
  // for each attached network neither registered nor railswitch_machine:
  //   podman network disconnect <network> railswitch-traefik
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
    - router `railswitch-logs` with rule ``Host(`logs.localhost`) && (<machine.HostClientClause()>)`` (host clause only, Task 2);
    - service port 8080;
  - `Restart=on-failure`;
  - the marker line `(machine)`.
- **Traefik API:** drop `--api.insecure=true` and the `127.0.0.1:8080` publish; keep `--api=true`. Traefik's unit gets the labels `traefik.enable=true`, `traefik.http.routers.railswitch-api.rule=Host(`traefik.localhost`) && (<machine.HostClientClause()>)` and `traefik.http.routers.railswitch-api.service=api@internal`. `up`'s route wait queries `http://127.0.0.1/api/http/routers` with header `Host: traefik.localhost`. If the host clause is wrong, the route wait breaks (caught by Task 9).
- **Wants:** every app unit gets `Wants=railswitch-dozzle.service` next to Traefik's.
- **Traefik's unit-hash** (`railswitch.unit-hash`) uses `StampExcept` that skips `Network=` lines. Adding or removing a project changes the unit file, so a daemon-reload happens, but the hash stays the same. `up` drops the `ch.traefik` trigger (a Traefik unit Added/Changed in the render diff, `up.go:92-100,172,210`) and restarts Traefik only when `staleContainer` reports its label stale. `AttachTraefik` makes the live state match: `up` runs `systemctl --user start <prefix>-proxy-network.service`, then `AttachTraefik`, then starts the app. `new`/`init` don't call it (their network doesn't exist yet). Task 6 calls it after unregistering.
- **Machine subnet:** `up` refuses when `machine.SubnetUsers(nets, "railswitch_machine", 255)` is non-empty, naming those networks; `doctor` gains `machine subnet free` with the same check.

- [ ] **Step 1: Write failing tests**
  - `TestProxyUnitsMachine`: goldens for three units; Dozzle lints clean. The Traefik unit has three `Network=` lines for two projects, no `Requires=`/`After=` naming a project, no `--api.insecure`, no `127.0.0.1:8080` publish, and the `railswitch-api` labels.
  - `TestRefreshProxyUnitsMachine`: with one project, all three files exist; after the last project is removed, none does.
  - `TestTraefikHashIgnoresNetworks`: adding a project keeps the label value.
  - `TestAttachTraefik`: the Fake inspect output lists `a_proxy`, `gone_proxy` and `railswitch_machine`; registry {a, b, c}; `NetworkSubnets` lists `a_proxy`, `b_proxy` and `gone_proxy`, not `c_proxy`. The argvs are exactly `podman network connect --ip 10.214.<b>.2 b_proxy railswitch-traefik` and `podman network disconnect gone_proxy railswitch-traefik`. Nothing names `c_proxy`. There is no `systemctl restart railswitch-traefik.service`.
  - `TestUpNoTraefikRestartOnNewProject` (Review Focus 5): only network lines changed, so no restart; the order is network service start → connect → app start.
  - `TestUpRefusesMachineSubnetTaken`: a foreign network `other` on 10.214.255.0/24; the error names `other`.
  - `TestPodmanNetworks`: argv and parsing.
- [ ] **Step 2:** `go test ./internal/machine/ ./internal/unithash/ ./internal/lifecycle/ ./internal/podman/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
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
  - `TestWorkerOnProxy`: the test copies the Filial fixture with `jobs = "sidekiq@1"` (the goldens stay unchanged). The `worker` unit has both `Network=` lines and an `AddHost=s3.master.filial.localhost:10.214.3.2`, and no `Label=traefik`.
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
  - `internal/workspace/workspace.go`: `Model` fills `render.Worktree.Share` from the registry;
  - `internal/machine/registry.go`: `WorktreeEntry.Share *ShareEntry`;
  - `internal/worktree/remove.go` + `add.go` (`Manager.Unshare` hook), `internal/cli/wt.go` (wiring), `internal/lifecycle/down.go`;
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
  - `render.Worktree` (`internal/render/model.go`) gains `Share *ShareEntry` (host, ip), filled by `workspace.Model` from the registry;
  - while shared: `share.host`, and `share.url.<port name>` = `https://<host>:<base+ID>` for every port with `share = true`;
  - unset otherwise. Layer 7 is applied per instance in `expandEnv` only when `Worktree.Share` is set, and is the highest layer. It is never put into the model-wide `EnvSpec`, so an unshared render never meets a `{share.*}` placeholder;
  - pack `[share] env` and option `[share] gives` reach the same services as the pack `[env]` / option `[gives]` they override, `deps` included (sharing restarts deps; accepted);
  - every proxy-network container (the same `onProxy` gate as Traefik's `AddHost`) gets `AddHost=<share.host>:<share IP>`.

**Rules:**
- **`Share`:**
  1. `tailscale` on PATH and `Self` ok, else an error naming `tailscale up`;
  2. for each share port, `Served` holds it pointing elsewhere → refuse, naming the port and target. Pointing at `http://127.0.0.1:<port>` already → fine (idempotent);
  3. registry `Share = {host, ip}`, Save, **Close** (flock is per open file);
  4. Render;
  5. `Up(slug)`, so env changes restart the app (unit-hash covers env);
  6. `ServeOn` each share port;
  7. print `shared: https://<host>:<app port>/ (storage https://<host>:<s3 port>/)`.

  If step 4, 5 or 6 fails: `ServeOff` the ports step 6 turned on, clear `Share` (Save, Close), Render and `Up` again, then return the original error.

  A `ServeOn` failure for a non-operator user is reported with the fix `sudo tailscale set --operator=$USER`.
- **`Unshare`:** for each of **this worktree's** share ports, `ServeOff` it only if `Served` maps it to `http://127.0.0.1:<that port>`; clear `Share`, Save, Close; Render; `Up`.
- **`dropShare(ctx, slug)`** (unexported): the same `ServeOff`s and clearing of `Share`, without Render or `Up`.
- **Cleanup:** `down` and `project rm` call `dropShare` first when shared. `wt rm` reaches it through a new hook `worktree.Manager.Unshare func(ctx context.Context, slug string) error`, wired in `internal/cli/wt.go` to `dropShare` and run before the worktree's units stop.

- [ ] **Step 1: Write failing tests** (Fake runner; `tailscale` argvs):
  - `TestShareArgv`: the exact argvs `tailscale serve --bg --https=13002 http://127.0.0.1:13002` and the same for 19002. No argv contains `reset`.
  - `TestShareRefusesForeignPort`: `Served` has `{13002: "http://127.0.0.1:3000"}`. The error names 13002, and no `serve --bg` call is made.
  - `TestUnshareOnlyOwn` (Review Focus 3): `Served` has `{13002: "http://127.0.0.1:13002", 19002: "http://127.0.0.1:19002", 13005: "http://127.0.0.1:13005", 443: "http://127.0.0.1:9999"}`, where 13005 is another project's. Unsharing worktree 2 turns off exactly 13002 and 19002.
  - `TestShareEnvLayer`:
    - while shared, rails env `RAILS_DEVELOPMENT_HOSTS=master.filial.localhost,box.tail1234.ts.net`;
    - rails env `S3_ENDPOINT=https://box.tail1234.ts.net:19001`, layer `share`;
    - `AddHost=box.tail1234.ts.net:100.64.0.7`;
    - unshared, both are back to their pack/option values (RAILS_DEVELOPMENT_HOSTS from the rails pack, S3_ENDPOINT from the rustfs option).
  - `TestShareAddHostWorker`: while shared, the `worker` unit (fixture copied with `jobs = "sidekiq@1"`, as in Task 4) has `AddHost=box.tail1234.ts.net:100.64.0.7`.
  - `TestShareRollsBack`: `ServeOn` for 19002 fails; afterwards the registry's `Share` is nil, 13002 was turned off again, and the error names 19002.
  - `TestWtRmDropsShareWithoutRestart`: `wt rm` of a shared worktree calls `ServeOff` and makes no `systemctl … restart` call.
  - `TestDownUnsharesFirst`.
- [ ] **Step 2–4:** `go test ./internal/tailscale/ ./internal/lifecycle/ ./internal/render/ ./internal/cli/`: FAIL → implement → PASS.
- [ ] **Step 5:** `make check && git commit -m "feat: share a worktree on its own tailnet HTTPS port"`

### Task 6: `railswitch project rm`

**Files:**
- Create: `internal/lifecycle/project.go` (+ test), `internal/cli/project.go` (+ test).
- Modify:
  - `internal/render/apply.go`: export `MarkedUnits(dir, prefix string) ([]MarkedUnit, error)`, where `MarkedUnit{Path string; ContainerNames, VolumeNames, NetworkNames []string}` is read from the `ContainerName=`, `Volume=<name>:` and `NetworkName=` lines of units whose marker names exactly `prefix`;
  - `internal/podman/podman.go` (+ argv tests): `NetworkRemove(ctx, name)` (`podman network rm <name>`, no `-f`), `VolumeExists(ctx, name) (bool, error)` (`podman volume exists <name>`, exit 1 → false), `VolumeRemove(ctx, name)` (`podman volume rm <name>`).

**Interfaces:**
- Produces a package function, not a method on `*L` (`L` needs a loaded workspace):

  ```go
  func ProjectRemove(ctx context.Context, r sys.Runner, dirs machine.Dirs, out io.Writer, in io.Reader, tty bool, prefix string, yes, purge bool) error
  ```
- `railswitch project rm <prefix> [--yes] [--purge]` asks before acting, defaulting to No, as `down` does.

**Rules (in this order):**
1. For each shared worktree, `dropShare(ctx, slug)` (Task 5): `ServeOff` its own ports, clear `Share`; no render, no restart.
2. Stop every worktree: when the registered workspace loads, `Down(slug, yes=true)` for each; otherwise `systemctl --user stop` each `<unit>.service` that `render.MarkedUnits` names. Exact names only.
3. `RemoveProject` in the registry, `Save`, `Close` (flock), then `RefreshProxyUnits` (Traefik's unit drops this project's `Network=`; its hash ignores that, Task 3).
4. `AttachTraefik`: disconnects `<prefix>_proxy`, which is no longer registered.
5. `podman network rm` (no `-f`) each network the marked units name, `<prefix>_proxy` last; exact names.
6. Remove every unit file `MarkedUnits` returned.
7. `systemctl --user daemon-reload`.
8. `--purge` also: for each exact volume name from the marked units' `Volume=` lines that starts with `<prefix>_`, `VolumeExists` then `VolumeRemove`; and the workspace's `.railswitch/` when the directory exists. No `--filter`, no `git worktree remove`. It never deletes the workspace directory or its git repo, and prints the path.

It works when the workspace directory is gone: names come from the registry and unit markers.

- [ ] **Step 1: Write failing tests**
  - `TestProjectRemoveExactNames` (Review Focus 4): registry {api, api2}. No argv names an `api2` object (`api2-…`, `api2_…`). Every argv that names no `api-…`/`api_…` object is one of `podman container inspect … railswitch-traefik` or `systemctl --user daemon-reload`. Afterwards `api2`'s units still exist and the registry holds only api2.
  - `TestProjectRemoveOrder`: `podman network disconnect api_proxy railswitch-traefik` comes before `podman network rm api_proxy`; no argv contains `rm -f`.
  - `TestProjectRemoveDefaultsNo`.
  - `TestProjectRemoveMissingWorkspace`: the workspace dir is deleted; names come from the marked units.
  - `TestProjectRemovePurgeKeepsRepo`: `.railswitch/` is gone, the repo and workspace dir remain, volumes are removed by exact name after `volume exists`.
- [ ] **Step 2–4:** `go test ./internal/lifecycle/ ./internal/cli/ ./internal/render/ ./internal/podman/`: FAIL → implement → PASS.
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
  - "a project's containers reach another project only through Traefik, which refuses them (D50); networks are `isolate=true`, and the Traefik API and Dozzle answer only host clients".

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
- each command's subcommands and flags, from a static table in `completion.go` (`completionFlags map[string][]string`, keyed by command path such as `wt add`). Commands are never run to collect flags. `TestCompletionFlagsMatch` scans `internal/cli/*.go` for `fs.Bool/String/Int("<name>"` calls (test-only source scan), so a new flag without a table entry fails the test;
- after `-w` and for `wt rm`, `share`, `unshare`, the current workspace's worktree slugs, from the registry. Nothing outside a workspace;
- service ids for `logs` and `fw` (`exec` has only `--fresh`; no `--service`);
- secret names for `secret set|rm`;
- `containerfile` names for `show|eject`;
- registered prefixes for `project rm`.

Errors print nothing (completion never writes to stderr).

**The scripts** call `railswitch __complete` and contain no command list of their own. The README section (in `AGENTS.md`'s user-docs pointer, or a new `docs/completion.md`) shows:
- `railswitch completion bash > ~/.local/share/bash-completion/completions/railswitch`;
- zsh: into an fpath dir;
- fish: `~/.config/fish/completions/`.

- [ ] **Step 1: Write failing tests**
  - `TestCompleteCommands`: `__complete wt ""` lists `wt` subcommands `add, rm, ls`; `__complete ""` lists every top-level command; `__complete w` lists `wt` (the last word is completed).
  - `TestCompletionFlagsMatch`: the static flag table matches every flag the commands define (see Candidates).
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
- [ ] **Step 1: Write `TestE2ETwoProjects`.** It creates two projects from the same repo with prefixes `$RAILSWITCH_E2E_PREFIX` and `${RAILSWITCH_E2E_PREFIX}b` (both must match `^[a-z][a-z0-9]{1,15}$`, so the base prefix is at most 14 characters; the test fails early otherwise), then `up` on both base worktrees. It also holds the cross-project isolation checks (moved here from Task 2; the helpers live in package `lifecycle_test`). Assertions:
  - each app host is 200 from the host (`curl http://<host>/`);
  - each project's own rails container gets 200 for its own host;
  - each `s3.` host answers (`curl -s -o /dev/null -w %{http_code}` is not 000 or 404);
  - `curl -H "Host: traefik.localhost" http://127.0.0.1/api/rawdata` is 200 from the host, and not 200 from A's rails container through A's Traefik IP;
  - `logs.localhost` is 200 from the host and not 200 from A's rails container;
  - `railswitch machine --json` shows disjoint IDs, ports, subnets and hosts;
  - A's rails container gets 404 for `-H "Host: <B host>" http://<A traefik ip>/`;
  - A's rails container gets a connection failure (curl exit 7 or 28, `--max-time 5`) for B's rails container IP, for B's Traefik IP `10.214.<b>.2` and for Dozzle `10.214.255.3:8080` (`isolate=true`, Task 2);
  - after `share` on A's base worktree (only when `tailscale status` works; skip that part otherwise), `tailscale serve status --json` holds A's ports and nothing else changed.

  Cleanup: `project rm --yes` both, then check that no object of either prefix remains (`podman ps -a`, `podman volume ls`, `podman network ls`, unit files), and that `railswitch-traefik` is still running after the first `project rm`.
- [ ] **Step 2:** The user runs it on the host:

  ```sh
  RAILSWITCH_E2E_REPO=/tmp/rs-demo/demo RAILSWITCH_E2E_PREFIX=s3a \
    go test -tags integration ./internal/lifecycle/ -run E2ETwoProjects -timeout 60m
  ```

  Expected: PASS. Record the run log in `docs/stage3-acceptance.md`.
- [ ] **Step 3:** `make check && git commit -m "test: Stage 3 two-project acceptance"`
