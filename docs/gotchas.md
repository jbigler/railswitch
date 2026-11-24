# Gotchas

These are the run-time traps that `rails-dev-podman`, railswitch's predecessor, hit and fixed. Each one now has a check, a test or a design rule in railswitch, so a change must not reopen it. When you add a guard for a new trap, add a row here.

Render lints (`internal/render/lint.go`) check the rendered unit files; each row names its lint rule.

## Units and Quadlet

| Gotcha | How railswitch handles it |
|---|---|
| systemd expands `$` in unit values; Quadlet does not expand `${VAR}` | Render writes concrete values. Lint `single-dollar` flags any `$` not written `$$` |
| A Traefik regex backreference must be written `$${1}` | Lint `single-dollar` |
| A `Label=` or `Environment=` value with a space must be quoted, or a Traefik rule is silently cut in half | Lint `unquoted-space` |
| A bare `Network=` creates no dependency on the network's service | `Network=` always names the `.network` unit, so Quadlet adds `Requires=`/`After=` (D27). Lint `network-dependency` |
| podman won't create a missing bind-mount source | `up` preflight refuses (`internal/lifecycle/preflight.go`). Machine `[[mount]]`s can be `optional` (D57), and `doctor` reports missing sources |
| `deps` must not `RemainAfterExit`, so every app start re-runs it, and its checks must work offline | Pack contract. Lint `deps-remain` |
| Shared objects need a separator that per-worktree names can't produce | Shared objects are `<prefix>_<what>` (D6). Lint `shared-underscore` checks `Volume=` sources |
| Host ports bound beyond 127.0.0.1 let the LAN reach every worktree, VNC without a password included | Lint `loopback-publish` |
| `k8s-file` keeps container stdout out of the journal, but oneshots log to the journal | `logs` reads a oneshot's unit journal and `podman logs` for the rest (`internal/lifecycle/logs.go`) |

## Firewall

| Gotcha | How railswitch handles it |
|---|---|
| Without the OCI hook descriptor, podman starts containers unfirewalled without a word | `up` and `exec` refuse (`requireDescriptor` in `internal/lifecycle/preflight.go`). `doctor` checks for the descriptor |
| A unit that joins a network must add it to the firewall networks annotation | Lint `firewall-networks` |
| The hook must read subnets from netavark's `<name>.json`, not the netns: keep-id containers have no routes yet at createRuntime | `internal/firewall/descriptor.go`, tested against netavark fixtures |
| Allowlists resolve hostnames once per container start, and CDNs rotate their addresses | Documented in [firewall.md](firewall.md). Restart the container to resolve again |

## Names and git

| Gotcha | How railswitch handles it |
|---|---|
| A service named after an HSTS-preloaded TLD (`app`, `dev`, `page`…) makes Chromium force `http://app:3000` to https | Service ids are validated (`internal/naming`) |
| Name filters like `name=^<prefix>-<slug>-` are unanchored regexes, so `api` also matches `api-v2` | Containers, volumes and networks are removed by exact name (`internal/worktree/remove.go`, `down`, `gc`). Names are never parsed back into their parts (D6) |
| `git worktree prune` from a partial view deletes every other worktree's admin entry | Only `git worktree remove` of the one worktree, never `prune` |

## Machine and networking

| Gotcha | How railswitch handles it |
|---|---|
| Containers can't reach ports bound to the host's loopback | Traefik has a static IP on each project's proxy network, and `AddHost=` points worktree hosts at it (`internal/render/runargs.go`) |
| Rootless podman can't publish :80 below `ip_unprivileged_port_start` | `doctor` checks, and `allow-ports` sets the sysctl |
| Browsing `rails:3000` instead of the worktree host gives `*_url` links the wrong port | Routes and links use `{worktree.host}` ([packs.md](packs.md)) |
| `tailscale serve reset` clears every serve on the machine, and there is only one :443 | Never `serve reset`. Each shared port gets its own tailnet port and is turned off one at a time (D46, `internal/tailscale`) |
| The old share swapped `--env-file`s, and podman fails on a missing path | Share is env layer 7, rendered into the units (D46). There is no file to swap |
| A stale `$SSH_AUTH_SOCK` makes podman refuse the container | No SSH agent socket reaches a container (D33) |

## Dashboard

| Gotcha | How railswitch handles it |
|---|---|
| Containers can write worktree files, so the dashboard must never trust them or run `mise env` | Container-writable files are read through `internal/safefile`. Git reads go through a host-private shadow `GIT_COMMON_DIR` (`internal/gitshadow`) and `internal/gitguard` |

## History

This list started as §7 of the project's original handoff spec. That spec was removed once the code and `docs/` covered it. Read it with `git show e49f591:handoff/HANDOFF.md`. The plans and specs under `docs/superpowers/` cite it as "HANDOFF §n".
