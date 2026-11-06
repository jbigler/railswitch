# terok survey (primary sources, read 2026-11-05)

All claims come from the repos as cloned on 2026-11-05 (default branches: `master`, or `main` for terok-nix and kanban-tui) and from the GitHub API. Links point at the files that make each claim. Anything marked **[INFERENCE]** is my interpretation, not something a source states. Many terok docs pages begin with the banner *"This documentation was written by an AI agent and might be inaccurate"* (e.g. [concepts.md](https://github.com/terok-ai/terok/blob/master/docs/concepts.md)). Where a mechanism mattered, I checked the source code as well as the docs.

## 1. What terok is

> "An open, Podman-native runtime for sandboxing AI coding agents in YOLO mode. Terok runs each agent task inside a hardened, rootless container with default-deny outbound networking, a credential vault that keeps real keys on the host, a per-task git checkpoint, and a desktop notification path for live allow/deny decisions. It ships a CLI and a Textual TUI on top of a stack of independently-released Python packages." — [terok/README.md](https://github.com/terok-ai/terok/blob/master/README.md)

- **Origin:** started at CASUS / HZDR (Helmholtz-Zentrum Dresden-Rossendorf) at the end of 2025. The main author, per the SPDX headers, is Jiri Vyskocil. Sources: [README](https://github.com/terok-ai/terok/blob/master/README.md), [gate/server.py header](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/gate/server.py).
- **Language and stack:** Python 3.12+ throughout, with these parts:
  - Textual for the TUI.
  - aiohttp for the token broker and the web TUI gateway.
  - SQLCipher for the credential database.
  - dbus-fast and varlink for clearance.
  - Jinja2 for the Dockerfile templates.
  - Pydantic for the roster and config schemas.
  - Module boundaries are enforced with tach and import-linter (`tach.toml`, `.importlinter` in every repo).
  - Nix packaging lives in terok-nix.
- **License:** Apache-2.0 for every package (REUSE-compliant). mkdocs-terok is 0BSD; kanban-tui is a MIT fork. Source: GitHub API `orgs/terok-ai/repos`.
- **Maturity:** explicitly **alpha**. The README says: "until version 1.0.0 is released, APIs, internals, and security boundaries may change without deprecation notice. Not recommended for production deployment." ([README](https://github.com/terok-ai/terok/blob/master/README.md))
  - terok: 52 stars, 7 forks, 53 open issues. Created 2025-11-22, last push 2026-11-03.
  - terok releases: v0.8.0 (2026-06-03), 0.8.1–0.8.5 (June–July), 0.9.0a1/a2, 0.9.0 (2026-09-11), 0.9.1 (2026-09-29). That is a minor release every ~3 months with patches every 1–3 weeks.
  - The siblings ship alpha pre-releases almost daily: shield 0.10.0a1–a5, sandbox 0.7.0a1–a9, executor 0.5.1a1–a14, all through 2026-11-03.
  - Sources: GitHub API `/repos/terok-ai/*/releases`.
- **Docs sites:**
  - https://terok-ai.github.io/terok/
  - https://terok-ai.github.io/terok-sandbox/
  - https://terok-ai.github.io/terok-shield/
  - and so on, one site per package. The docs are built with properdocs/mkdocs.

### Org repos and their relevance

| Repo | Stars | Last push | Role | Covered |
|---|---|---|---|---|
| [terok](https://github.com/terok-ai/terok) | 52 | 2026-11-03 | Orchestrator: projects and tasks, CLI, Textual TUI, sickbay, panic | yes |
| [terok-executor](https://github.com/terok-ai/terok-executor) | 0 | 2026-11-04 | Per-task agent runner, YAML agent/provider roster, L0/L1 image factory, auth flows, ACP proxy | yes |
| [terok-sandbox](https://github.com/terok-ai/terok-sandbox) | 0 | 2026-11-04 | Hardened podman runtime, per-container supervisor, vault/token broker, SSH signer, git gate | yes |
| [terok-shield](https://github.com/terok-ai/terok-shield) | 4 | 2026-11-03 | nftables egress firewall via OCI hooks, DNS tiers, audit/harvest | yes |
| [terok-clearance](https://github.com/terok-ai/terok-clearance) | 1 | 2026-11-03 | Live allow/deny prompts: varlink hub plus D-Bus desktop notifications | yes |
| [terok-util](https://github.com/terok-ai/terok-util) | 1 | 2026-11-04 | Shared foundations: config stack, untrusted-fs I/O, process hardening, multi-distro test matrix | yes |
| [terok-nix](https://github.com/terok-ai/terok-nix) | 0 | 2026-10-13 | Nix flake packaging every package (with version patches) | briefly |
| [kanban-tui](https://github.com/terok-ai/kanban-tui) | 0 | 2026-04-15 | Fork of Zaloog/kanban-tui. Its git log has only upstream commits, no terok-specific changes. terok's `work_status.py` names it as a writer of `pending-phase.yml` | briefly |
| mkdocs-terok, docs-inventories, pages | 0 | — | Docs tooling, docs artifacts, website | not relevant |

### Architecture in one paragraph

Six packages are layered as `terok → terok-executor → terok-sandbox → {terok-shield, terok-clearance} → terok-util` ([README table](https://github.com/terok-ai/terok/blob/master/README.md), [sandbox index](https://github.com/terok-ai/terok-sandbox/blob/master/docs/index.md#where-it-sits-in-the-stack)). A **project** is a YAML config (`~/.config/terok/projects/<id>/project.yml`); each **task** is one rootless podman container with its own workspace clone ([concepts.md](https://github.com/terok-ai/terok/blob/master/docs/concepts.md#core-concepts)). Each container works like this:

1. `terok setup` installs two global OCI hook pairs, both firing at `createRuntime` and `poststop`. One is the shield nft hook plus an NFLOG reader; the other is the sandbox supervisor hook ([shield hooks/install.py](https://github.com/terok-ai/terok-shield/blob/master/src/terok_shield/hooks/install.py), [sandbox supervisor/install.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/supervisor/install.py)).
2. Before `podman run`, the stack writes a per-container state bundle and a sidecar JSON. It then passes annotations that trigger the hooks ([prepare-run-cleanup.md](https://github.com/terok-ai/terok-sandbox/blob/master/docs/prepare-run-cleanup.md)).
3. At container start, the hooks apply a pre-generated nft ruleset inside the container's netns and spawn a **per-container supervisor**. The supervisor runs the vault token broker, SSH signer, git gate HTTP server, clearance hub and verdict server, each as its own hardened child process. The supervisor dies with the container ([sandbox index](https://github.com/terok-ai/terok-sandbox/blob/master/docs/index.md#per-container-supervisor)).

---

## 2. Feature inventory

### 2.1 Container / agent sandboxing

| Feature | What it does / how it's implemented | Source |
|---|---|---|
| Rootless podman, keep-id userns | Every container runs under rootless podman with `--userns=keep-id:uid=1000,gid=1000`, built by `podman_userns_args`. Images create a `dev` user with passwordless sudo. | [terok-util README](https://github.com/terok-ai/terok-util/blob/master/README.md), [container-layers.md](https://github.com/terok-ai/terok/blob/master/docs/container-layers.md) |
| Net-cap drop | When shield is active it adds `--cap-drop NET_ADMIN --cap-drop NET_RAW`, so the workload cannot rewrite its own nftables. | [shield hooks/mode.py#L293](https://github.com/terok-ai/terok-shield/blob/master/src/terok_shield/hooks/mode.py), [shield security.md](https://github.com/terok-ai/terok-shield/blob/master/docs/security.md#trust-boundaries) |
| Reserved podman flags / passthrough denylist | `terok-sandbox run` rejects user flags after `--` that the sandbox owns: `--name --network --hooks-dir --annotation --cap-add --cap-drop --userns`, plus mounts at `/run/terok/**` and the bridges dir. `--privileged` and `--security-opt` are denied in `run.podman_args` passthrough. OCI annotation keys are allowlisted, and values containing control characters are rejected. | [podman_args.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/podman_args.py), [sandbox.py `_validate_annotations`](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/sandbox.py), [prepare-run-cleanup.md#collisions](https://github.com/terok-ai/terok-sandbox/blob/master/docs/prepare-run-cleanup.md#collisions-sandbox-rejects) |
| `--hosts-file none` | The container's `/etc/hosts` starts from an empty base, so host-private names don't leak in. `run.hosts_file` overrides this. | [prepare-run-cleanup.md#host-names](https://github.com/terok-ai/terok-sandbox/blob/master/docs/prepare-run-cleanup.md#host-names-in-etchosts) |
| Restricted vs unrestricted agent mode | Unrestricted is the default ("YOLO"). Each agent gets its own mechanism: Claude via `/etc/claude-code/managed-settings.json` `defaultMode: bypassPermissions`, Codex `--yolo`, `COPILOT_ALLOW_ALL`, `VIBE_BYPASS_TOOL_PERMISSIONS`, `OPENCODE_PERMISSION`. Restricted mode uses vendor defaults and adds `--security-opt no-new-privileges`. | [usage.md#permission-mode](https://github.com/terok-ai/terok/blob/master/docs/usage.md#permission-mode-unrestricted--restricted), [executor security.md#restricted-mode](https://github.com/terok-ai/terok-executor/blob/master/docs/security.md#restricted-mode) |
| Seccomp grants (TSan/ASLR, perf) | When opted in, it copies podman's default seccomp profile (taken from `podman info`) into the task state and adds exact `personality` argument grants and/or `perf_event_open`. It fails the launch if the profile is missing or has an unexpected shape, and never grants `CAP_SYS_ADMIN`. | [prepare-run-cleanup.md#seccomp](https://github.com/terok-ai/terok-sandbox/blob/master/docs/prepare-run-cleanup.md#seccomp-grants-for-threadsanitizer-and-perf), [runtime/seccomp.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/runtime/seccomp.py) |
| krun microVM runtime (experimental) | `run.runtime: krun` (requires `experimental: true`) runs the same OCI images under `podman --runtime krun` (libkrun/KVM). `exec` can't enter the VM, so login and doctor go over SSH to an in-guest sshd through a passt-forwarded loopback port. Executor provisions a vault-backed `%host` keypair to tmpfs. `run.cpus` becomes the `krun.cpus` annotation. Unix-socket services don't cross into the VM, so it forces `services.mode: tcp`. | [runtimes.md](https://github.com/terok-ai/terok/blob/master/docs/runtimes.md), [sandbox runtime/krun.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/runtime/krun.py), [executor krun.py](https://github.com/terok-ai/terok-executor/blob/master/src/terok_executor/krun.py) |
| Pluggable `ContainerRuntime` protocol | Runtime backends: podman (crun), krun, and a `null` in-memory stub for tests, selectable by `TEROK_RUNTIME`. `inspect()` returns strict snapshots or raises `RuntimeInspectionError`; "unknown" is never treated as "absent" before a destructive recovery. | [runtime/protocol.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/runtime/protocol.py), [sandbox index#strict-runtime-inspection](https://github.com/terok-ai/terok-sandbox/blob/master/docs/index.md#strict-runtime-inspection) |
| Sealed isolation | `project.isolation: sealed` copies the workspace, vendor config and sessions into the container instead of bind-mounting them. Only the read-only input dir and the event-ack dir stay live mounts. Recreating the container discards unexported work. | [container-layers.md](https://github.com/terok-ai/terok/blob/master/docs/container-layers.md), [usage.md#task-inputs](https://github.com/terok-ai/terok/blob/master/docs/usage.md#task-inputs-and-writable-state) |
| Host-authored read-only inputs vs writable state | Each task has three dirs: `agent-inputs/` (read-only at `/home/dev/.terok/input`, holding wrappers, instructions, prompts and the Toad token), `agent-events/` (a narrow writable ack channel) and `agent-config/` (writable status). The initial prompt is immutable. Delivery is acknowledged by atomically creating an `initial-prompt.consumed` marker. Prompts are capped at 120 KiB to fit argv. | [executor security.md#host-inputs](https://github.com/terok-ai/terok-executor/blob/master/docs/security.md#host-inputs-and-shared-vendor-state), [task_files.py](https://github.com/terok-ai/terok-executor/blob/master/src/terok_executor/task_files.py) |
| Untrusted-fs I/O helpers | Every host read or write into a container-writable dir opens each path component without following links (fd-relative, `O_NOFOLLOW`). It requires a single-link regular file and bounded reads (1 MiB for credentials, 4 MiB for merged config). Writes publish a fresh inode atomically and never modify in place. | [terok-util untrusted_fs.py](https://github.com/terok-ai/terok-util/blob/master/src/terok_util/untrusted_fs.py), [executor security.md](https://github.com/terok-ai/terok-executor/blob/master/docs/security.md#host-inputs-and-shared-vendor-state) |
| GPU passthrough per vendor | `run.gpus: all \| nvidia \| amd \| intel \| [list]`. Uses CDI when the host has a spec. Otherwise it falls back to the vendor-documented device nodes, mounting only that vendor's render nodes, never all of `/dev/dri`. Requesting an unavailable vendor fails loudly. | [runtime/gpu.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/runtime/gpu.py), [usage.md#gpu](https://github.com/terok-ai/terok/blob/master/docs/usage.md#gpu-passthrough) |
| Nested containers | Opt-in `run.nested_containers` for running podman inside the task; uses the `quay.io/podman/stable` base. | [usage.md#running-containers-inside](https://github.com/terok-ai/terok/blob/master/docs/usage.md#running-containers-inside-your-container) |
| SELinux / AppArmor helpers | `terok setup selinux` installs `terok_socket.te`, which allows `container_t` to connect to host Unix sockets. Without it, everything falls back to TCP mode. `terok setup apparmor` installs a dnsmasq profile addendum. Both show the exact `sudo` command and the rules before anything runs. | [README Notes](https://github.com/terok-ai/terok/blob/master/README.md#notes), [terok_socket.te](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/resources/selinux/terok_socket.te), [selinux.md](https://github.com/terok-ai/terok/blob/master/docs/selinux.md) |

### 2.2 Network egress control (terok-shield)

| Feature | What it does / how it's implemented | Source |
|---|---|---|
| Per-container nftables via OCI hooks | `Shield.pre_start()` resolves DNS and pre-generates the full `ruleset.nft` into a per-container state dir. It returns podman args and an annotation that points at that dir. A **global** hook (installed once and registered in `containers.conf`, matched by `when.annotations`) runs at `createRuntime`. It applies the ruleset inside the container's netns (`nsenter`) and starts dnsmasq. Works with pasta and slirp4netns. The hook is stdlib-only and runs on setup's isolated Python. | [shield design.md](https://github.com/terok-ai/terok-shield/blob/master/docs/design.md#hook-mode), [hooks/install.py](https://github.com/terok-ai/terok-shield/blob/master/src/terok_shield/hooks/install.py), [resources/nft_hook.py](https://github.com/terok-ai/terok-shield/blob/master/src/terok_shield/resources/nft_hook.py) |
| Tiered policy (t00–t40) with authority order | Output chain order: preamble (lo, established, DNS, infra ports, `+localhost:PORT` grants) → **t00** hard-deny floor (link-local, IMDS 169.254/16, AWS v6 IMDS, the host's own loopback/gateway) → **t10** break-glass override → **t20** security-deny (vault-relayed provider hosts, operator runtime denies, RFC1918/4193) → **t30** provider allow → **t40** project allow → bypass window → terminal `reject` logged as `BLOCKED`. The first match wins. Each tier is a plain-text policy file under `state_dir/policy/` plus a `live` runtime overlay. | [shield security.md#chain-evaluation-order](https://github.com/terok-ai/terok-shield/blob/master/docs/security.md#chain-evaluation-order), [design.md#state-bundle-layout](https://github.com/terok-ai/terok-shield/blob/master/docs/design.md#state-bundle-layout) |
| DNS tiers incl. dnsmasq `nftset=` | Preferred tier: a per-container dnsmasq with `nftset=` directives (`cache-size=0`). Each resolution inserts the returned IPs into the allow sets before the reply reaches the workload, which handles CDN IP rotation and `*.domain`. Fallbacks are `dnsmasq-static`, `lookup` (`dig`/`drill` with a 1 h mtime cache) and `getent`. `resolv.conf` is bind-mounted to point at the per-container dnsmasq. Sets learned through dnsmasq carry nft `timeout`s. | [design.md#allowlisting](https://github.com/terok-ai/terok-shield/blob/master/docs/design.md#allowlisting), [terok shield-security.md#dns-tiers](https://github.com/terok-ai/terok/blob/master/docs/shield-security.md#dns-tiers), [dns/dnsmasq.py](https://github.com/terok-ai/terok-shield/blob/master/src/terok_shield/dns/dnsmasq.py) |
| DNS sinkhole for denied names | On the dnsmasq tiers, security-denied names (vault-relayed provider hosts) are sinkholed, so rotated IPs are never learned. On `lookup`/`getent` the deny holds only by address, and the shield reports this "rotation risk" in audit notes, stderr and task details. | [shield security.md#the-deny-on-tiers-without-dnsmasq](https://github.com/terok-ai/terok-shield/blob/master/docs/security.md#the-deny-on-tiers-without-dnsmasq), [design.md#pinned-names](https://github.com/terok-ai/terok-shield/blob/master/docs/design.md#pinned-names-the-rotation-risk) |
| Postures: UP / DOWN / DISENGAGED / QUARANTINE | DOWN accepts and logs but keeps t00, t20 and the private-range rejects. DISENGAGED enforces nothing. QUARANTINE drops everything and logs it. After applying, each posture is verified (`verify_up` / `verify_down` / `verify_quarantine` check the ruleset invariants), and a failed verification raises. | [design.md#public-api](https://github.com/terok-ai/terok-shield/blob/master/docs/design.md#public-api), [shield security.md#security-boundary](https://github.com/terok-ai/terok-shield/blob/master/docs/security.md#security-boundary-nftrulespy) |
| Kernel-timed break-glass override | `shield.override` entries (host, IP or CIDR, with a mandatory `reason` and optional `expires`) become t10 nft set elements with a per-element `timeout`. The **kernel** expires them, so a crashed host process cannot extend them. Expiring overrides are kept out of the replayed `ruleset.nft` and re-timed by the hook at each start. Grant and revoke serialize on a per-container `flock`. | [shield security.md#break-glass-expiry](https://github.com/terok-ai/terok-shield/blob/master/docs/security.md#break-glass-expiry-is-kernel-enforced), [terok shield-security.md#per-project-allow](https://github.com/terok-ai/terok/blob/master/docs/shield-security.md#per-project-allow-and-break-glass-override) |
| Timed bypass window | `terok shield bypass <p> <t> --for 5m` adds an element to a `bypass_window` nft set with a kernel timeout. It is UP-only and sits below every deny, so it never opens vault hosts, private ranges or the floor. Each admitted connection is audit-logged as `bypass`. | [terok shield-security.md#bypass-window](https://github.com/terok-ai/terok/blob/master/docs/shield-security.md#the-bypass-window--a-timed-allow-all) |
| Live allow/deny with sticky denies | `allow`/`deny` mutate nft sets live and upsert `+target` / `-target` lines into `policy/live`, so denies persist across `shield up` and restarts. An allow is refused with `SecurityTierRefusal` if the target, a parent domain, or any address it resolves to (e.g. a shared CDN front) is in the security tier. | [design.md#persistent-deny](https://github.com/terok-ai/terok-shield/blob/master/docs/design.md#persistent-deny), [design.md#tier-authority](https://github.com/terok-ai/terok-shield/blob/master/docs/design.md#tier-authority) |
| Egress sets (named allow bundles) | Built-in sets: git-hosting, python, node, rust, go, container registries, os-packages (picked by the image's package family). User sets are `~/.config/terok/egress-sets/<name>.txt`. A project opts in with `shield.sets: [recommended, …]`. A missing set file fails the project load. `project.yml` is edited in place, byte-preserving. | [terok shield-security.md#egress-sets](https://github.com/terok-ai/terok/blob/master/docs/shield-security.md#egress-sets), [lib/core/egress_sets.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/core/egress_sets.py) |
| Bundled shield profiles | `base`, `dev-standard`, `dev-python`, `dev-node`, `nvidia-hpc`, `krun_guest` as `+`/`-` grammar `.txt` files. Note: terok tasks stopped composing profiles in 0.9 and use egress sets instead. | [shield resources/examples/](https://github.com/terok-ai/terok-shield/tree/master/src/terok_shield/resources/examples), [design.md#bundled-profiles](https://github.com/terok-ai/terok-shield/blob/master/docs/design.md#bundled-profiles) |
| Harvest → promote → export | A per-container NFLOG reader writes one audit line per destination per 30 s (`blocked`, `bypass`, `down`, `security_deny`, `private_range`). `harvest` reads it back, keyed by domain where DNS recovered one. `promote --to task\|project\|set:NAME\|override` turns one evidence row into policy, offering only the remedies that tier allows, and suggests egress sets by greedy cover. `export --scrub` produces a shareable JSON (`terok-egress-export` v1) with internal names and addresses removed. Running a task in DOWN still records what UP would have refused, so a "learning mode" is possible. | [terok shield-security.md#reading-back](https://github.com/terok-ai/terok/blob/master/docs/shield-security.md#reading-back-what-a-task-needed), [egress_promotion.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/domain/egress_promotion.py), [shield harvest.py](https://github.com/terok-ai/terok-shield/blob/master/src/terok_shield/harvest.py) |
| In-container ICMP probe | `shield_probe.py` uses `IP_RECVERR` + `MSG_ERRQUEUE` on a UDP socket to report the exact ICMP type and code without `CAP_NET_RAW`, so diagnostics can tell a shield reject from other unreachables. | [resources/shield_probe.py](https://github.com/terok-ai/terok-shield/blob/master/src/terok_shield/resources/shield_probe.py) |
| ruleset builder isolation | `nft/rules.py` may import only stdlib and `nft/constants.py`. This is enforced by tach and an AST import test. Every interpolated value passes a validator (`safe_ip`, `_safe_port`, `_safe_ident`, `_safe_timeout`, and so on). | [shield security.md#security-boundary](https://github.com/terok-ai/terok-shield/blob/master/docs/security.md#security-boundary-nftrulespy) |
| Host-loopback port grants | `+localhost:PORT` / `loopback_ports` open single host-loopback ports (e.g. the broker port in TCP mode). A t10 override can never reach the host loopback. | [design.md#allowlisting](https://github.com/terok-ai/terok-shield/blob/master/docs/design.md#allowlisting), [executor vault.md#selinux](https://github.com/terok-ai/terok-executor/blob/master/docs/vault.md#selinux-considerations) |

### 2.3 Credential / secret handling

| Feature | What it does / how it's implemented | Source |
|---|---|---|
| Phantom tokens + token broker | Containers only ever see per-task, per-provider **phantom** tokens in env vars, e.g. `ANTHROPIC_API_KEY=<phantom>`. An aiohttp reverse proxy inside the per-container supervisor validates the phantom against SQLCipher `proxy_tokens`. It injects the real header (by type: OAuth `Authorization: Bearer` or `x-api-key`), forwards over TLS, and handles OAuth refresh and WebSockets. Routing is by token, not URL path, because some SDKs strip the path. | [executor vault.md](https://github.com/terok-ai/terok-executor/blob/master/docs/vault.md), [token_broker.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/vault/daemon/token_broker.py) |
| Per-agent routing to the broker | The in-container loopback `localhost:9419` (socat bridge) fronts the broker's Unix socket. Each agent is pointed at it differently: Claude via `ANTHROPIC_BASE_URL` + `ANTHROPIC_UNIX_SOCKET`; Codex via `config.toml` rewrite plus an in-container self-signed TLS bridge, because Codex demands https; gh via `http_unix_socket`; glab via `GITLAB_API_HOST`. Config patches carry a `.terok-managed-config.json` ownership sidecar so only terok-written values get removed later. | [executor vault.md#per-agent-traffic-routing](https://github.com/terok-ai/terok-executor/blob/master/docs/vault.md#per-agent-traffic-routing), [bridges/ensure-bridges.sh](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/resources/bridges/ensure-bridges.sh) |
| YAML provider registry | `resources/providers/<name>.yaml` declares the endpoint (`upstream`, `auth` per credential type, `oauth_refresh`, `serves:` protocol map). `resources/agents/<name>.yaml` declares delivery (`token_env`, `base_url_env`, `socket_env`, `config_patch`). Users add providers in `~/.config/terok/providers/`. A generated `routes.json` holds one entry per provider. | [executor vault.md#yaml-registry](https://github.com/terok-ai/terok-executor/blob/master/docs/vault.md#yaml-registry-providers-and-bindings), [resources/agents/claude.yaml](https://github.com/terok-ai/terok-executor/blob/master/src/terok_executor/resources/agents/claude.yaml) |
| Security-deny of vault-relayed hosts | Provider hosts the broker relays for are put in shield's t20 security tier. The agent can only reach them through the broker, so a stolen phantom is worthless and direct exfiltration to those APIs is blocked. Reaching them directly needs an override with a reason and an expiry. | [design.md#tier-authority](https://github.com/terok-ai/terok-shield/blob/master/docs/design.md#tier-authority), [terok shield-security.md#live-allows](https://github.com/terok-ai/terok/blob/master/docs/shield-security.md#live-allows-stop-at-the-security-tier) |
| Host-side auth capture | `terok auth <provider>` runs the vendor CLI's OAuth login in a throwaway container with a temp config dir. Extractors pull the token out of `.credentials.json`, `auth.json`, `hosts.yml` and similar files into the DB. API keys are prompted on the TTY. In-container login is refused (`wrapper.refuse_subcommands: [setup-token]`), and credential files are mounted read-only as phantom markers so an in-container login can't replace them (issue #873). | [executor vault.md#auth-flow](https://github.com/terok-ai/terok-executor/blob/master/docs/vault.md#auth-flow), [concepts.md#shared-directories](https://github.com/terok-ai/terok/blob/master/docs/concepts.md#shared-directories), [credentials/extractors.py](https://github.com/terok-ai/terok-executor/blob/master/src/terok_executor/credentials/extractors.py) |
| SQLCipher DB + passphrase tier chain | The credentials DB is mandatory AES-256 SQLCipher (no plaintext mode). The passphrase is resolved in order: systemd-creds (TPM2/host-key sealed, systemd ≥ 257) → desktop keyring (Secret Service) → session cache (kernel `@u` keyring, or a tmpfs file) → `passphrase_command` (like `credential.helper`; fails closed on non-zero exit) → interactive TTY prompt. Non-interactive setup must pick a tier explicitly. | [credentials-encryption.md](https://github.com/terok-ai/terok-sandbox/blob/master/docs/credentials-encryption.md), [vault/store/tiers.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/vault/store/tiers.py) |
| Vault SSH signer (no key files) | Per-project deploy keys are minted into the DB as PKCS#8 DER; there are no on-disk key files. The SSH-agent protocol is served to the container on a per-container socket, guarded by a phantom-token handshake. The signer re-checks the token and key assignments before every sign, even on established connections. A host-local per-scope 0600 socket serves the host's own `gate-sync`. Keys map to projects many-to-many, and unlinking a key's last project deletes it. | [signer.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/vault/ssh/signer.py), [prepare-run-cleanup.md#trust-model](https://github.com/terok-ai/terok-sandbox/blob/master/docs/prepare-run-cleanup.md#trust-model), [lib/api/ssh_routing.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/api/ssh_routing.py) |
| BrokerContext scoping + live revocation | The sidecar pins `(scope, subject, credential_set)`. A token for another task or credential set is rejected, with no fallback to `default`. `terok task revoke-credentials` deletes a task's token rows: new requests get 401, and open HTTP/WebSocket streams are cancelled at the next 100 ms authorization poll. The container is left running for forensics. | [prepare-run-cleanup.md#trust-model](https://github.com/terok-ai/terok-sandbox/blob/master/docs/prepare-run-cleanup.md#trust-model), [task_credentials.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/domain/task_credentials.py) |
| Credential-use audit log | Append-only JSONL with one line per broker call: scope, subject, credential_set, provider, method, path, status, outcome, duration. No bodies. Writing is soft-fail. | [vault/daemon/audit.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/vault/daemon/audit.py), [audit-logging.md](https://github.com/terok-ai/terok-sandbox/blob/master/docs/audit-logging.md) |
| Process hardening of secret holders | Each supervisor child calls `harden_self()` before opening the DB: `PR_SET_DUMPABLE=0` (no ptrace or `/proc/pid/mem` from same-UID siblings), `RLIMIT_CORE=0`, `mlockall`, `PR_SET_NO_NEW_PRIVS`. It then installs a **Landlock** filesystem lane per service. `--debug` skips only the dumpable clear, and only when given explicitly. | [terok-util hardening.py](https://github.com/terok-ai/terok-util/blob/master/src/terok_util/hardening.py), [supervisor/children.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/supervisor/children.py), [credentials-encryption.md#runtime-service-boundaries](https://github.com/terok-ai/terok-sandbox/blob/master/docs/credentials-encryption.md#runtime-service-boundaries), [usage.md#debug-mode](https://github.com/terok-ai/terok/blob/master/docs/usage.md#debug-mode) |
| Passphrase rekey / recovery / escrow | `vault passphrase seal\|to-desktop-keyring`, change and recovery flows. PANIC destroys every passphrase tier, so recovery needs the escrowed passphrase. | [credentials-encryption.md](https://github.com/terok-ai/terok-sandbox/blob/master/docs/credentials-encryption.md#changing-the-passphrase), [usage.md](https://github.com/terok-ai/terok/blob/master/docs/usage.md#vault-passphrase-backend) |
| Sidecar tools with real keys | CodeRabbit's CLI can't be base-URL-redirected, so it runs in a **separate sidecar container** that gets the real key through `env_map`. This keeps the main agent container phantom-only. | [executor vault.md](https://github.com/terok-ai/terok-executor/blob/master/docs/vault.md#per-agent-traffic-routing), [launch-modes.md#tool-mode](https://github.com/terok-ai/terok-executor/blob/master/docs/launch-modes.md#tool-mode) |

### 2.4 Git / repo isolation

| Feature | What it does / how it's implemented | Source |
|---|---|---|
| Git gate (host bare mirror) | A host-side bare repo mirrors upstream over SSH using the vault key. Tasks clone from it. In **gatekeeping** mode `origin` is the gate and the human promotes to upstream; in **online** mode the gate only seeds the clone and `origin` is then repointed upstream. The docs state that it is "not a hard barrier" without the shield. | [git-gate-and-security-modes.md](https://github.com/terok-ai/terok/blob/master/docs/git-gate-and-security-modes.md), [concepts.md#the-git-gate](https://github.com/terok-ai/terok/blob/master/docs/concepts.md#the-git-gate) |
| Per-container token-auth smart-HTTP gate | The supervisor wraps `git http-backend` (CGI) in a threaded HTTP server on a per-container Unix socket, or a loopback port in TCP mode. The route regex allows only `/<repo>.git/{info/refs,git-upload-pack,git-receive-pack,HEAD}`. Basic-auth username = a 128-bit `terok-g-<hex>` token minted per task, scoped to one repo, held only in the sidecar and container env. Revocation = supervisor death. | [gate/server.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/gate/server.py), [gate/tokens.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/gate/tokens.py) |
| Repo content never runs on host | For agent pushes the gate sets `core.hooksPath` via `GIT_CONFIG_COUNT/KEY/VALUE` env to a sandbox-owned hooks dir *outside* every repo (or `/dev/null`). Repo-contained hooks therefore never execute on the host. | [gate/hooks.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/gate/hooks.py), [gate/server.py#L173](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/gate/server.py) |
| post-receive backups of destructive pushes | The injected `post-receive` hook saves the old tip of any force-moved or deleted `refs/heads/*` as `refs/terok/backup/<branch>/<stamp>-<sha12>`, and writes a `terok-push-marker` file the host watches by mtime. `core.logAllRefUpdates=always` keeps a reflog as the last resort. | [gate/hooks.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/gate/hooks.py), [usage.md#gate-sync-semantics](https://github.com/terok-ai/terok/blob/master/docs/usage.md#gate-sync-semantics) |
| Safe sync model with hidden ref namespaces | Sync only creates or fast-forwards `refs/heads/*`. Deletes and non-FF changes become *pending ops*, labelled lossless or lossy, and need operator confirmation. `refs/terok/{upstream,attic,backup}` are hidden via `transfer.hideRefs`, so containers can't read or forge them. Restore is compare-and-swap guarded. | [gate/mirror.py header](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/gate/mirror.py), [usage.md#gate-sync-semantics](https://github.com/terok-ai/terok/blob/master/docs/usage.md#gate-sync-semantics) |
| Review-lag detection | Warns when a gate branch tip is ahead of the SHA of an open MR/PR on the forge. Uses the operator's own `gh`/`glab` (one API call, host-side). Re-checks on the push marker, and surfaces a `~/.terok/review-status` file in the task's tmux status line. | [review_lag.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/domain/review_lag.py), [git-gate-and-security-modes.md#options](https://github.com/terok-ai/terok/blob/master/docs/git-gate-and-security-modes.md#options) |
| Upstream polling / auto-sync | The TUI polls `git ls-remote` every N minutes and can optionally auto-sync listed branches. | [git-gate-and-security-modes.md#options](https://github.com/terok-ai/terok/blob/master/docs/git-gate-and-security-modes.md#options) |
| Relaxed gatekeeping | `gatekeeping.expose_external_remote` adds upstream as a read-only `external` remote. | [concepts.md#gatekeeping-options](https://github.com/terok-ai/terok/blob/master/docs/concepts.md#gatekeeping-options) |
| Host never runs git in workspace | Removed host-side "view/copy diff" of task workspaces: "terok does not execute git in the task workspace on the host". Review happens through the gate. | [usage.md#task-inputs](https://github.com/terok-ai/terok/blob/master/docs/usage.md#task-inputs-and-writable-state) |
| Workspace naming `workspace-dangerous/` | The host path suffix deliberately reminds the operator that the contents are agent-produced and possibly malicious. | [concepts.md#task-workspace](https://github.com/terok-ai/terok/blob/master/docs/concepts.md#task-workspace) |
| Per-agent git identity | Each roster YAML sets `git_identity` (e.g. Claude `noreply@anthropic.com`). The human identity comes from `config.yml` or the host git config. | [claude.yaml](https://github.com/terok-ai/terok-executor/blob/master/src/terok_executor/resources/agents/claude.yaml), [agents.md#git-identity](https://github.com/terok-ai/terok-executor/blob/master/docs/agents.md#git-identity) |

### 2.5 Agent orchestration / multi-agent / queues

| Feature | What it does / how it's implemented | Source |
|---|---|---|
| Projects ⊃ tasks | A long-lived project config with many ephemeral task containers in parallel, each its own clone and its own shield rules. Agents can be mixed within one project. | [concepts.md#multi-task-parallel-work](https://github.com/terok-ai/terok/blob/master/docs/concepts.md#multi-task-parallel-work) |
| Launch modes | `cli` (tmux login), `headless` (`--prompt`, `--model`, `--timeout`, `--no-follow`, follow-up prompts restart a finished task), `toad` (web multi-agent TUI behind Caddy). `run-tool` runs sidecar tools. | [README](https://github.com/terok-ai/terok/blob/master/README.md#headless-agent-runs-unattended), [headless.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/orchestration/task_runners/headless.py), [executor launch-modes.md](https://github.com/terok-ai/terok-executor/blob/master/docs/launch-modes.md) |
| YAML agent roster | One YAML per agent or tool, schema-validated with typo-rejecting Pydantic. Fields: headless flags, auto-approve env/flags, session resume, install snippets (`run_as_root`/`run_as_dev`), auth modes, wrapper refusals, `git_identity`. User additions go in `~/.config/terok/agent/agents/`. Agents: Claude, Codex, Copilot, Vibe, OpenCode, Pi, Toad; tools gh, glab, sonar, coderabbit. | [roster/loader.py](https://github.com/terok-ai/terok-executor/blob/master/src/terok_executor/roster/loader.py), [resources/agents/](https://github.com/terok-ai/terok-executor/tree/master/src/terok_executor/resources/agents), [agents.md](https://github.com/terok-ai/terok-executor/blob/master/docs/agents.md) |
| Shell wrappers per agent | A Jinja template generates `claude()`, `codex()` and so on as bash functions. They record task-local agent history, set git identity, handle session resume and the `--terok-timeout` headless mode. | [provider/wrappers.py](https://github.com/terok-ai/terok-executor/blob/master/src/terok_executor/provider/wrappers.py), [agent-wrappers.sh.j2](https://github.com/terok-ai/terok-executor/blob/master/src/terok_executor/resources/templates/agent-wrappers.sh.j2) |
| Per-task ACP endpoint | A host-side daemon per container binds a Unix socket that aggregates every in-image agent behind the Agent Client Protocol as `agent:model` options. It lazily spawns the in-container `terok-<agent>-acp` wrapper on first prompt. `terok acp connect <proj> <task>` bridges stdio so Zed or Toad can use a sandboxed task as an agent server. | [acp/proxy.py](https://github.com/terok-ai/terok-executor/blob/master/src/terok_executor/acp/proxy.py), [acp/daemon.py](https://github.com/terok-ai/terok-executor/blob/master/src/terok_executor/acp/daemon.py), [cli/commands/acp.py](https://github.com/terok-ai/terok/blob/master/src/terok/cli/commands/acp.py) |
| Agent work-status file | The agent writes `/home/dev/.terok/work-status.yml` (`status: coding`/`blocked`/…, plus `message`). The host reads it as **bounded, untrusted display data**. | [work_status.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/core/work_status.py) |
| Pending-phase queue (host-only) | External tools (kanban-tui is named) queue deferred phase transitions (`phase` + `prompt`) in `pending-phase.yml`. The file lives in a **host-only `host-control/` dir outside every container mount**, so the agent can't forge commands. | [work_status.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/core/work_status.py), [usage.md#task-inputs](https://github.com/terok-ai/terok/blob/master/docs/usage.md#task-inputs-and-writable-state) |
| Layered agent instructions | Bundled default → global `config.yml` → project `project.yml`. Each layer's `agent.instructions` list keeps lower layers via an `_inherit` sentinel or replaces them. A per-project `instructions.md` is always appended. Delivered read-only through `agent-inputs`. | [lib/core/instructions.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/core/instructions.py), [usage.md#agent-instructions](https://github.com/terok-ai/terok/blob/master/docs/usage.md#agent-instructions) |
| Per-provider config values | `agent.model` / `timeout` / `unrestricted` accept either a flat value or a `{claude: …, codex: …, _default: …}` dict. CLI flags override both. | [usage.md#per-provider-config-values](https://github.com/terok-ai/terok/blob/master/docs/usage.md#per-provider-config-values) |
| Native sub-agents/MCP passthrough | terok has no sub-agent abstraction of its own. It relies on Claude's own `~/.claude/agents` and `mcpServers` in the shared mount. | [usage.md#native-claude-agents](https://github.com/terok-ai/terok/blob/master/docs/usage.md#native-claude-agents-and-mcps) |

### 2.6 Observability / UI

| Feature | What it does / how it's implemented | Source |
|---|---|---|
| Textual TUI | Project list, task list and detail, clearance screen (`C`), harvest/promote (`h`), bypass (`b`), mute (`m`/`M`), gate backups (`B`), SSH key routing matrix, GPU screen, SELinux fix screen, panic button widget. | [tui/](https://github.com/terok-ai/terok/tree/master/src/terok/tui), [shield-security.md](https://github.com/terok-ai/terok/blob/master/docs/shield-security.md) |
| Web-served TUI (`terok-web`) | `textual-serve` with an injected aiohttp Basic-auth middleware that gates every route, including the WebSocket upgrade. The password is scrypt-hashed in `~/.config/terok/serve.password` and minted randomly on first run. | [tui/serve.py](https://github.com/terok-ai/terok/blob/master/src/terok/tui/serve.py), [pyproject.toml](https://github.com/terok-ai/terok/blob/master/pyproject.toml) |
| Toad browser mode token | A Caddy front checks the `terok_token` cookie against `{$TOAD_TOKEN}`. A `?token=` query seeds an HttpOnly SameSite=Lax cookie. | [Caddyfile](https://github.com/terok-ai/terok-executor/blob/master/src/terok_executor/resources/scripts/Caddyfile), [toad.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/orchestration/task_runners/toad.py) |
| Clearance: live Allow/Deny prompts | NFLOG BLOCKED event → per-container varlink hub (socket 0600, same-UID) → `MultiSocketSubscriber` fan-in → freedesktop Notifications with action buttons. The verdict helper execs `terok-shield allow\|deny`. Every verdict must cite a triple the hub itself emitted. Falls back to a `NullNotifier` when headless. Failed verdicts produce a "Allow failed" popup carrying sanitized shield stderr. | [clearance docs/index.md](https://github.com/terok-ai/terok-clearance/blob/master/docs/index.md), [hub/server.py](https://github.com/terok-ai/terok-clearance/blob/master/src/terok_clearance/hub/server.py) |
| Mute without fail-open | `services.clearance: prompt\|mute\|off` per project. Mute silences prompts only: refusals still happen and are still audited. `off` starts no hub or verdict server, so "no verdict path exists that could widen the firewall". | [sandbox index#clearance-prompts](https://github.com/terok-ai/terok-sandbox/blob/master/docs/index.md#clearance-prompts), [terok shield-security.md#muting](https://github.com/terok-ai/terok/blob/master/docs/shield-security.md#muting-the-prompts-not-the-shield) |
| Wire-format sanitization | Strings that flow from the container (DNS names, hostnames) into hub events are reduced to printable ASCII with a length cap at both producer and consumer. A `WIRE_SPEC` grep tag keeps the copies in sync. Separately, `sanitize_tty` strips C0/C1/ANSI/OSC-52 from any untrusted string shown in a terminal (CWE-150). | [shield _wire_sanitize.py](https://github.com/terok-ai/terok-shield/blob/master/src/terok_shield/_wire_sanitize.py), [terok-util security.py](https://github.com/terok-ai/terok-util/blob/master/src/terok_util/security.py) |
| `shield watch` | Streams DNS-log, audit-log and NFLOG events. The reader self-confines with Landlock to its `state_dir`. | [watch.py](https://github.com/terok-ai/terok-shield/blob/master/src/terok_shield/watch.py), [_confine.py](https://github.com/terok-ai/terok-shield/blob/master/src/terok_shield/_confine.py) |
| Sickbay (health / reconcile) | `terok sickbay [project [task]] [--system] [--fix]` with exit codes 0/1/2. In-container checks run **from the host** via `podman exec` (SSH under krun), layered across the sandbox, executor and terok doctor protocols. `--fix` replays missed `post_stop` hooks. | [sickbay.py](https://github.com/terok-ai/terok/blob/master/src/terok/cli/commands/sickbay.py), [container_doctor.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/orchestration/container_doctor.py) |
| Login via in-container tmux | `podman exec -it <c> tmux new-session -A -s main` (idempotent attach). Host and container tmux use different prefixes and colours (host `^b` blue, container `^a` green). The TUI dispatches tmux new-window → desktop terminal → suspend. The `--tmux` wrapper stamps windows (`@terok-login`) to dedupe them. | [login-design.md](https://github.com/terok-ai/terok/blob/master/docs/login-design.md), [tmux_session.py](https://github.com/terok-ai/terok/blob/master/src/terok/tui/tmux_session.py) |
| TUI askpass for SSH | An optional 0600 Unix-socket askpass service plus a modal, so personal-SSH passphrase prompts don't corrupt the TUI. | [askpass_service.py](https://github.com/terok-ai/terok/blob/master/src/terok/tui/askpass_service.py) |
| journald native writer | A dependency-free journald datagram writer that falls back to a file when there is no socket. | [terok-util journal.py](https://github.com/terok-ai/terok-util/blob/master/src/terok_util/journal.py) |

### 2.7 Image building

| Feature | What it does / how it's implemented | Source |
|---|---|---|
| L0 / L1 / L2 layering | **L0** = distro + dev tools + `dev` user + init script. **L1** = agent CLIs chosen from the roster, tagged by selection (`terok-l1-cli:<base>-claude-codex`), with an unsuffixed default alias. **L2** = an optional per-project Dockerfile snippet. L0/L1 are cached across projects. Templates are Jinja2 with an `apt`/`dnf` family switch auto-detected from the base image name. | [container-layers.md](https://github.com/terok-ai/terok/blob/master/docs/container-layers.md), [executor container/build.py](https://github.com/terok-ai/terok-executor/blob/master/src/terok_executor/container/build.py), [l2.project.Dockerfile.template](https://github.com/terok-ai/terok/blob/master/src/terok/resources/templates/l2.project.Dockerfile.template) |
| Cache-bust control | `--refresh-agents` passes a fresh `AGENT_CACHE_BUST` build-arg (rebuilds the agent layers, keeps the system packages). `--full-rebuild` = `--no-cache --pull=always`. Tags are capped at 120 chars, with a digest fallback. | [container-layers.md#build-flow](https://github.com/terok-ai/terok/blob/master/docs/container-layers.md#build-flow) |
| Image staleness + cleanup | Detects stale images; `terok image usage/list/cleanup [--dry-run]`. | [container-lifecycle.md](https://github.com/terok-ai/terok/blob/master/docs/container-lifecycle.md), [image_cleanup.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/domain/image_cleanup.py) |
| Multi-distro test matrix | `terok-matrix` runs each sibling's test suite inside slot containers across 12+ distros (Alpine, Debian 12/13, Fedora 43/44, Ubuntu 24.04/26.04, Void, NixOS, Manjaro, Mageia, podman-in-podman). Driven by declarative `matrix.yml`, with an atomic JSON report. | [terok-util matrix/__init__.py](https://github.com/terok-ai/terok-util/blob/master/src/terok_util/matrix/__init__.py), [terok-util README](https://github.com/terok-ai/terok-util/blob/master/README.md#matrix-reporting) |

### 2.8 Host integration

| Feature | What it does / how it's implemented | Source |
|---|---|---|
| Per-container supervisor via OCI hook | The `createRuntime` hook reads the sidecar (path from the `terok.sandbox.sidecar` annotation) and `Popen`s a stdlib wrapper. The wrapper restarts the supervisor with exponential backoff, up to 5 attempts. The supervisor awaits `podman wait <id>`; `poststop` SIGTERMs it (SIGKILL after 2 s). "No lingering daemons between tasks; `pgrep terok` is empty when no containers are running." Services degrade independently, and the container starts regardless. | [sandbox index#per-container-supervisor](https://github.com/terok-ai/terok-sandbox/blob/master/docs/index.md#per-container-supervisor), [supervisor/main.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/supervisor/main.py) |
| Supervisor placement | If a per-user systemd manager answers, the supervisor is a transient unit `terok-supervisor-<id>.service` with NoNewPrivileges and similar settings. Otherwise it is a daemon in the runtime's userns. The choice decides whether the kernel-keyring cache is usable. | [credentials-encryption.md#where-the-supervisor-runs](https://github.com/terok-ai/terok-sandbox/blob/master/docs/credentials-encryption.md#where-the-supervisor-runs), [_util/_placement.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/_util/_placement.py) |
| Janitor (stray supervisor reaper) | A periodic sweep from host CLI context kills supervisor process groups whose container is gone. Each process is revalidated by PID and start time before signalling. This backs up `PR_SET_PDEATHSIG` and the poststop hook. | [supervisor/janitor.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/supervisor/janitor.py) |
| `prepare` / `run` / `cleanup` for BYO containers | `terok-sandbox prepare <name>` mints tokens, writes state and **prints podman flags**, which can be spliced into your own `podman run`, Compose or Podman Desktop setup. `cleanup` is idempotent. | [prepare-run-cleanup.md](https://github.com/terok-ai/terok-sandbox/blob/master/docs/prepare-run-cleanup.md) |
| Socket vs TCP service transport | Default: per-container sockets under a host dir bind-mounted **read-only** at `/run/terok`. Each service binds in its own child dir, so the container can connect but cannot replace socket inodes. TCP mode uses per-container loopback ports, admitted through shield `loopback_ports`. socat bridges in-container (`ensure-bridges.sh`). | [prepare-run-cleanup.md#emit-flags-only](https://github.com/terok-ai/terok-sandbox/blob/master/docs/prepare-run-cleanup.md#emit-flags-only-podman-desktop--compose-users), [executor vault.md](https://github.com/terok-ai/terok-executor/blob/master/docs/vault.md#per-agent-traffic-routing) |
| Shared port registry | Per-user JSON claim files in `/tmp/terok-ports/`. All users' claims are read before allocating, plus a socket bind test, so users on shared hosts don't collide. | [port_registry.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/port_registry.py) |
| Task lifecycle hooks | Host shell commands at `pre_start`, `post_start`, `post_ready`, `post_stop`, given `TEROK_*` env. Fired hooks are tracked in task metadata, and sickbay reconciles missed ones. | [usage.md#task-lifecycle-hooks](https://github.com/terok-ai/terok/blob/master/docs/usage.md#task-lifecycle-hooks), [orchestration/hooks.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/orchestration/hooks.py) |
| PANIC kill-switch | `terok panic` runs these independent best-effort actions in parallel, each under a deadline: kill all managed containers (SIGKILL by full ID, chosen by trusted annotations, without parsing agent status files), network quarantine, kill supervisors, hard-lock the vault (destroy every passphrase tier), write a panic marker. Containers stay on disk. | [lib/domain/panic.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/domain/panic.py), [usage.md](https://github.com/terok-ai/terok/blob/master/docs/usage.md#vault-passphrase-backend), [sandbox index#emergency-runtime-inventory](https://github.com/terok-ai/terok-sandbox/blob/master/docs/index.md#emergency-runtime-inventory) |
| Setup / uninstall | `terok setup` is idempotent: it installs hooks, the vault and routes, an XDG desktop entry and completions. `uninstall` reverses it and keeps the credential DB. Setup receipts detect package or interpreter drift. | [README#one-time-setup](https://github.com/terok-ai/terok/blob/master/README.md#one-time-setup), [design.md](https://github.com/terok-ai/terok-shield/blob/master/docs/design.md#pinned-names-the-rotation-risk) |
| Kernel keyring exhaustion doc | Documents the per-UID kernel keyring quota running out with many containers, and the workaround. | [kernel-keyring.md](https://github.com/terok-ai/terok/blob/master/docs/kernel-keyring.md) |
| Nix packaging | A flake packages all six packages and their uncommon dependencies (asyncvarlink, dbus-fast, agent-client-protocol). | [terok-nix flake.nix](https://github.com/terok-ai/terok-nix/blob/main/flake.nix) |

### 2.9 Config format

| Feature | What it does / how it's implemented | Source |
|---|---|---|
| YAML config stack | Global `~/.config/terok/config.yml` → per-project `project.yml` → CLI flags. `ConfigStack.deep_merge` with an `_inherit` sentinel in lists. | [terok-util config_stack.py](https://github.com/terok-ai/terok-util/blob/master/src/terok_util/config_stack.py), [README#configuration](https://github.com/terok-ai/terok/blob/master/README.md#configuration) |
| Strict, fail-closed config | Unknown values in `project.yml` fail the load and name the key. Incoherent combinations (gatekeeping with gate disabled) are rejected. Experimental features (krun) need a global `experimental: true` "so a typo can't silently switch isolation backends". Removed keys are rejected with migration directions, never aliased. | [git-gate-and-security-modes.md#incoherent](https://github.com/terok-ai/terok/blob/master/docs/git-gate-and-security-modes.md#incoherent-combination-rejected), [runtimes.md](https://github.com/terok-ai/terok/blob/master/docs/runtimes.md#enabling-krun), [credentials-encryption.md](https://github.com/terok-ai/terok-sandbox/blob/master/docs/credentials-encryption.md#where-the-passphrase-comes-from) |
| Byte-preserving YAML edits | CLI and TUI write `project.yml` changing only the lines that must change; comments, flow style and permissions are kept. A file that can't be edited this way is refused, with instructions. | [terok shield-security.md#egress-sets](https://github.com/terok-ai/terok/blob/master/docs/shield-security.md#egress-sets), [lib/util/yaml.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/util/yaml.py) |
| Generated config reference | `docs/gen_config_reference.py` builds the docs reference from the Pydantic schema. | [gen_config_reference.py](https://github.com/terok-ai/terok/blob/master/docs/gen_config_reference.py), [config_reference.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/core/config_reference.py) |

### 2.10 CLI UX

| Feature | What it does / how it's implemented | Source |
|---|---|---|
| Bare `terok` = TUI; wizard | `terok` launches the TUI; `terok project wizard` (TUI key `n`) creates config, builds images, sets up SSH and the gate. | [README#first-project](https://github.com/terok-ai/terok/blob/master/README.md#first-project) |
| Task ID prefixes | `terok login myproj t3x` re-attaches by task-ID prefix. | [README](https://github.com/terok-ai/terok/blob/master/README.md#first-project) |
| Shared CLI registry types | `CommandDef` / `ArgDef` / `CommandTree` drive argparse and completions across all siblings. | [terok-util README](https://github.com/terok-ai/terok-util/blob/master/README.md) |
| Preflight readiness gate | `terok-executor run` checks prerequisites. Mandatory ones block the launch after interactive remediation; optional ones print the consequence of skipping them. | [executor preflight.py](https://github.com/terok-ai/terok-executor/blob/master/src/terok_executor/preflight.py) |
| Security-relevant sudo steps shown first | SELinux and AppArmor setup print the exact `sudo` command and the rules before running anything. | [README#notes](https://github.com/terok-ai/terok/blob/master/README.md#notes) |

### 2.11 Other

| Feature | What it does / how it's implemented | Source |
|---|---|---|
| kanban-tui fork | A fork of a Textual kanban with CLI and MCP-server interfaces, usable by agents. The fork carries no terok-specific commits (its log shows only upstream). It is the named external writer of terok's pending-phase queue. | [kanban-tui README](https://github.com/terok-ai/kanban-tui/blob/main/README.md), [work_status.py](https://github.com/terok-ai/terok/blob/master/src/terok/lib/core/work_status.py) |
| Agent compat matrix | Per-agent table of how unrestricted mode, instruction delivery and config work with shared volumes. | [agent-compat-matrix.md](https://github.com/terok-ai/terok/blob/master/docs/agent-compat-matrix.md) |
| terok-in-Docker (experimental) | Runs the whole stack (web TUI, gate, shield, rootless podman) inside one Docker container, "for local evaluation only", and is "not tested regularly". | [docker.md](https://github.com/terok-ai/terok/blob/master/docs/docker.md) |

---

## 3. Security design choices and threat-model statements (quoted)

### Shield threat model

From [terok-shield/docs/security.md](https://github.com/terok-ai/terok-shield/blob/master/docs/security.md#threat-model):

> "**Attacker model:** arbitrary code execution *inside* the container, no host access. The container boundary (rootless Podman + user namespaces) is assumed intact."

> "**Non-goals:** Inbound filtering (containers are not exposed); DNS exfiltration through allowed resolvers; Host kernel / container-escape attacks; Application-layer inspection"

### Fail-closed hooks

From [terok-shield/docs/security.md#fail-closed-guarantees](https://github.com/terok-ai/terok-shield/blob/master/docs/security.md#fail-closed-guarantees):

> "**no path from 'firewall setup failed' to 'container running unrestricted.'**"

Hook exception, missing `nft`, a bad annotation, a bundle version mismatch, a missing ruleset or a failed dnsmasq all lead to "Hook exits non-zero → torn down". The same page adds a caveat:

> "Containers without Shield annotations are not protected by these hooks."

### Tier order is authority

From [terok-shield/docs/security.md#chain-evaluation-order](https://github.com/terok-ai/terok-shield/blob/master/docs/security.md#chain-evaluation-order):

> "tier order *is* the authority order — an `accept`/`reject` is a terminal verdict, so the first tier that matches wins."

The hard-deny floor (link-local and IMDS) "sits above even the override and is absolute".

### Kernel-enforced expiry

From [terok-shield/docs/security.md#break-glass-expiry-is-kernel-enforced](https://github.com/terok-ai/terok-shield/blob/master/docs/security.md#break-glass-expiry-is-kernel-enforced):

> "nothing in userspace keeps the clock, so a crashed or absent host process cannot stretch it."

The same page admits a caveat: the end of an override "ends new connections only … a connection opened through the override — a tunnel, a keep-alive — keeps its direct route until it closes."

### The gate is not a hard barrier

From [concepts.md](https://github.com/terok-ai/terok/blob/master/docs/concepts.md#gate-in-gatekeeping-mode):

> "The gate controls which remote is configured as `origin`. It does not physically prevent the agent from adding other remotes. To enforce the boundary, combine the gate with the **shield**."

### Repo content must never become host code

From [gate/hooks.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/gate/hooks.py):

> "repo content must never become host-side code … hooks now live in a directory the sandbox owns *outside* every gate repo."

### Hidden refs prevent forged state

From [gate/mirror.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/gate/mirror.py):

> "The whole `refs/terok` namespace is hidden (`transfer.hideRefs`), so containers can neither see nor overwrite the snapshot, attic, or backups — a container that could push the snapshot could forge 'lossless'."

### No real secret enters a container

From [executor security.md](https://github.com/terok-ai/terok-executor/blob/master/docs/security.md#vault):

> "No real API keys, OAuth tokens, or SSH private keys enter containers … a compromised agent cannot read, copy, or exfiltrate real credentials."

The limitation is stated plainly in [vault.md#limitations](https://github.com/terok-ai/terok-executor/blob/master/docs/vault.md#limitations): Claude OAuth "do[es] not support full routing", and Copilot "Not proxied yet".

### Container-writable paths are untrusted

From [executor security.md](https://github.com/terok-ai/terok-executor/blob/master/docs/security.md#host-inputs-and-shared-vendor-state):

> "The host treats container-writable paths as untrusted … reject symlinks in any path component, hardlinked files, directories, FIFOs … Writes replace files atomically rather than modifying an existing inode."

From [untrusted_fs.py](https://github.com/terok-ai/terok-util/blob/master/src/terok_util/untrusted_fs.py):

> "These operations prevent link redirection, not concurrent task edits … Keep authoritative host state outside task-writable mounts."

### Uncertainty is never "absent"

From [executor security.md](https://github.com/terok-ai/terok-executor/blob/master/docs/security.md#upgrade-requirements):

> "Failed inspection or unavailable container/image metadata aborts the operation instead of treating uncertainty as an obsolete task."

Also from [sandbox index](https://github.com/terok-ai/terok-sandbox/blob/master/docs/index.md#strict-runtime-inspection):

> "Do not use their empty/default values as evidence for destructive recovery."

### Process separation of secret holders

From [supervisor/children.py](https://github.com/terok-ai/terok-sandbox/blob/master/src/terok_sandbox/supervisor/children.py):

> "That put secret-holding code … in the same address space as convenience services (the desktop notifier), so a bug in any of them exposed all of them. Each service now runs in its own process."

From [credentials-encryption.md](https://github.com/terok-ai/terok-sandbox/blob/master/docs/credentials-encryption.md#runtime-service-boundaries):

> "This is a filesystem-path boundary, not a same-UID kernel-keyring boundary."

### Fail closed instead of silently downgrading

From [credentials-encryption.md](https://github.com/terok-ai/terok-sandbox/blob/master/docs/credentials-encryption.md#where-the-passphrase-comes-from), on `passphrase_command`:

> "Fails closed when the helper is configured but exits non-zero / times out — silent fall-through to a weaker tier would be an unannounced security downgrade."

### Muting is not fail-open

From [clearance index](https://github.com/terok-ai/terok-clearance/blob/master/docs/index.md#key-properties):

> "muting changes who is asked, never what the shield did. Nothing is auto-allowed."

From [terok shield-security.md](https://github.com/terok-ai/terok/blob/master/docs/shield-security.md#muting-the-prompts-not-the-shield), on why muting exists at all:

> "an operator who cannot silence it will silence the shield instead — which is the outcome this whole design exists to avoid"

### Verdicts must cite hub-emitted events

From [clearance index](https://github.com/terok-ai/terok-clearance/blob/master/docs/index.md#what-is-the-clearance-system):

> "its socket is bound same-UID-only (mode 0600) and every verdict must cite a triple the hub itself emitted."

### Unrestricted is the default

[usage.md](https://github.com/terok-ai/terok/blob/master/docs/usage.md#permission-mode-unrestricted--restricted) says: "By default, terok starts agents in **unrestricted** mode." Containment is the shield, the vault and the gate, not agent permission prompts.

### Shield DOWN is described as "the default"

In the harvest section of [terok shield-security.md#reading-back](https://github.com/terok-ai/terok/blob/master/docs/shield-security.md#reading-back-what-a-task-needed): "So a task running DOWN, the default, leaves a harvest as useful as one running UP." The `shield.down_on_task_run` config is also mentioned on the same page.

[INFERENCE] The out-of-the-box task posture may currently be DOWN, i.e. learning mode, with private ranges and vault hosts still denied. This contradicts the README's "default-deny egress firewall"; check the current default before relying on either.

### Disclosure

[SECURITY.md](https://github.com/terok-ai/terok/blob/master/SECURITY.md): "Until version 1.0.0 is released, security issues and breakages are expected to surface." Reports go to security@terok.ai (PGP) or GitHub private advisories.

---

## 4. Overlap with railswitch's described features ([INFERENCE] throughout)

This maps terok features onto railswitch's feature list as the assignment described it. I have not read the railswitch code.

| railswitch area | terok counterpart | Ideas terok has that the description doesn't mention |
|---|---|---|
| Per-container OCI-hook nftables firewall | terok-shield | dnsmasq `nftset=` live allow sets (handles CDN rotation, wildcards) · tiered authority chain with an absolute floor (IMDS, link-local, host loopback) · kernel-timed break-glass overrides and bypass windows (nft element `timeout`) · post-apply ruleset invariant verifiers · fail-closed hook table · harvest → promote → scrubbed export from NFLOG audit · DOWN-as-learning-mode · sinkholing provider hosts that only the broker may reach |
| Dashboard with browser terminals | terok TUI + `terok-web` + Toad/Caddy | Basic-auth on every route incl. WebSocket upgrade (scrypt-hashed password) · cookie-seeded token for per-task web UIs · desktop-notification Allow/Deny prompts (clearance) with mute that never fails open |
| Agent status files | `work-status.yml` | status read as bounded, untrusted display data · host-authored commands kept in a host-only `host-control/` dir outside every mount · immutable read-only prompt inputs with a separate writable "consumed" marker |
| Agent work queue | pending-phase queue (kanban-tui) | the separation above (agent-writable telemetry vs host-only commands) |
| git-guard (checks container-writable hooks/git config) | gate `core.hooksPath` pinned via `GIT_CONFIG_*` env to a host-owned dir · host never runs git in task workspaces | push-through smart-HTTP gate with a per-task token · `post-receive` backup refs for force-push/delete · hidden `refs/terok/*` namespaces · safe-sync (FF-only; destructive ops pending confirmation) · review-lag vs open PR |
| gitshadow (host-private git dir stand-in) | — (terok clones into the container and reviews via the gate) | the same "repo content never becomes host code" rule, enforced by not executing git on the host workspace at all |
| Secrets only via stdin | Vault: phantom tokens + per-container token broker + SSH-agent signer | real secrets never enter the container (header injection at the proxy) · live per-task revocation · per-request credential audit JSONL · SQLCipher with a passphrase tier chain (systemd-creds/TPM2, keyring, kernel keyring, `passphrase_command`) · `PR_SET_DUMPABLE=0`/`mlockall`/no-new-privs + Landlock per secret-holding process |
| Quadlet / per-worktree containers | per-container supervisor spawned by OCI hook (transient user unit when systemd is present) · janitor reaper | services live and die with the container, no lingering daemons · `prepare` prints podman flags for BYO containers |
| Signed pack libraries / templates | YAML agent roster + provider registry, L0/L1/L2 Jinja Dockerfiles | typo-rejecting schema · per-selection L1 tags · `AGENT_CACHE_BUST` |
| — | `terok panic`, `sickbay --fix`, task lifecycle hooks with missed-hook reconciliation, krun microVM runtime, vendor-aware GPU passthrough, seccomp grants for TSan/perf, `--hosts-file none`, untrusted-fs helpers, TTY/wire sanitization, shared port registry | candidate additions |
