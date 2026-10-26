# Stage 3 acceptance

This is the run procedure for the Stage 3 acceptance. Run it on a real host,
not in a sandbox. The reference host is CachyOS/Arch.

## Prerequisites

- podman ≥ 5 with netavark ≥ 1.7.0: every railswitch network is
  `isolate=strict` (D50), a value netavark added in 1.7.0 (podman ≥ 4.6).
  Check with `podman info --format '{{.Host.NetworkBackendInfo.Version}}'`.

## From a clean machine

For a host where no railswitch project exists (nothing in `railswitch machine`,
no `~/.config/containers/systemd/*.container` with a railswitch marker), skip
"Upgrading from Stage 2" and do this:

1. Build and install from the `stage-3` branch:
   `git switch stage-3 && git pull && make build && install -m755 railswitch ~/.local/bin/`.
2. `systemctl --user enable --now podman.socket` (Traefik and Dozzle read it).
3. `railswitch doctor`: every host check OK. If `unprivileged port 80`
   fails, run `railswitch allow-ports`, then `doctor` again. `machine subnet
   free` must pass (no other podman network on 10.214.255.0/24).
4. A fresh app at `/tmp/rs-demo/demo`, as in docs/stage2-acceptance.md
   Step 1 (no host Ruby needed).
5. Run "Two projects at once" below.

## Upgrading from Stage 2

The first `railswitch up` after upgrading changes these, on its own:

- **Traefik's unit.** It no longer publishes `127.0.0.1:8080` or runs
  `--api.insecure`. The dashboard is at http://traefik.localhost/ and the
  log view (Dozzle, new) at http://logs.localhost/, both from the host only
  (D50). Traefik's unit-hash label is stale, so this `up` restarts Traefik
  once: every project's routes drop for a few seconds.
- **The machine network** `railswitch_machine` (10.214.255.0/24) and
  Dozzle are created; `up` refuses to start when another podman network
  uses that subnet (`railswitch doctor`: `machine subnet free`).
- **Each worktree's units** gain the router rules and network options of
  Stage 3 on that worktree's own `up`; other worktrees keep their old
  units until then.

Then, by hand:

1. **Packs changed:** if `up` or `render` says `stack.lock` is stale, run
   `railswitch lock` first.
2. **Ejected Containerfiles** stay as they are. Only if you want the new
   default, run `railswitch containerfile eject <name> --force` (it
   replaces your file).
3. **Recreate the networks** (below): `up` prints a WARNING, and
   `railswitch doctor` fails `networks isolated`, while a network lacks
   `isolate=strict`.

### Recreating networks

Networks railswitch created before Stage 3 keep their old options:
`isolate=strict` (D50) only takes effect when podman creates the network
again (Quadlet runs `podman network create --ignore`). Until then, other
projects' containers reach these networks directly.

Recreating stops Traefik. Quadlet derives `Requires=` and `After=` from
each `Network=<x>.network` line, so the generated Traefik service requires
every project's proxy network service: stopping one stops Traefik, and
**every project's routes drop** until Traefik starts again (steps 5–6).

For every project created before Stage 3:

1. `railswitch down` in every worktree of the project. This stops each
   worktree's `<prefix>-<slug>-dev-network.service`.
2. `podman network disconnect <prefix>_proxy railswitch-traefik`: Traefik
   stays attached through `down`, and `podman network rm` refuses a network
   in use (`-f` would remove the Traefik container).
3. `systemctl --user stop <prefix>-proxy-network.service <prefix>-<slug>-dev-network.service …`
   (every worktree's dev network service). Quadlet network services are
   oneshot with `RemainAfterExit=yes`, and `down` leaves the proxy network's
   running, so without this `up` would never create the network again.
   **This stops `railswitch-traefik.service` too** (see above).
4. `podman network rm <prefix>_proxy <prefix>-<slug>-dev …` (every worktree's
   dev network, by exact name; `railswitch wt ls` lists the slugs).
5. `railswitch up` in every worktree. The network units create the networks
   again, now with `isolate=strict`.
6. Last, make sure Traefik runs again: the first `up` of step 5 starts it
   (every app unit has `Wants=railswitch-traefik.service`); without one,
   run `systemctl --user start railswitch-traefik.service`. Its unit lists
   every project's network, so every other project's routes come back with
   it; `railswitch machine` shows `Traefik reaches every project network`.

Check with `railswitch doctor` (`networks isolated` passes) or
`podman network inspect <prefix>_proxy --format '{{.Options}}'`: it shows
`isolate:strict`.

## Host source address (confirmation, optional)

Every project router admits only clients in the project's subnet or the host
(D50). The host clause, `machine.HostClientClause()`, is pinned without this
measurement: it admits host traffic arriving from outside 10.214.0.0/16 (a)
and from the receiving Traefik address `10.214.<n>.2` (b):

```
!ClientIP(`10.214.0.0/16`) || ClientIP(`10.214.0.2/32`) || … || ClientIP(`10.214.255.2/32`)
```

The check that it works is Task 9's host run (`TestE2ETwoProjects`: the app,
an `s3.` URL, `logs.localhost` and `traefik.localhost` answer from the host
and refuse the other project's container). The measurement below only tells
which of (a) and (b) the host uses; any other outcome (c) means the clause is
wrong and every browser request on the host gets a 404 from Traefik.

With a project up (prefix `<prefix>`, worktree `<slug>`):

```sh
podman info --format '{{.Host.NetworkBackend}} {{.Host.RootlessNetworkCmd}}'
podman run -d --rm --name rs-whoami --network <prefix>_proxy -p 127.0.0.1:18999:80 docker.io/traefik/whoami
podman inspect rs-whoami --format '{{(index .NetworkSettings.Networks "<prefix>_proxy").IPAddress}}'   # whoami's IP
curl -s http://127.0.0.1:18999/ | grep RemoteAddr                                  # host
podman exec <prefix>-<slug>-rails curl -s http://<whoami IP>/ | grep RemoteAddr    # the project's rails container
podman rm -f rs-whoami
```

Record here:

| What | Value |
|---|---|
| `podman info` line | |
| whoami's IP on `<prefix>_proxy` | |
| host curl's `RemoteAddr` | |
| rails container's `RemoteAddr` | |
| Outcome | (a) outside 10.214.0.0/16 / (b) whoami's own address / (c) other |

## Two projects at once (`TestE2ETwoProjects`)

Prerequisites as in docs/stage2-acceptance.md (podman, a systemd user
session, `curl`, a generated app at `/tmp/rs-demo/demo`, `railswitch doctor`
all OK). No project with prefix `s3a` or `s3ab` may be registered. With
`tailscale status` working the share check runs too; otherwise the test logs
that it skipped it.

```sh
RAILSWITCH_E2E_REPO=/tmp/rs-demo/demo RAILSWITCH_E2E_PREFIX=s3a \
  go test -tags integration ./internal/lifecycle/ -run E2ETwoProjects -timeout 60m -v
```

The prefixes are `$RAILSWITCH_E2E_PREFIX` and `${RAILSWITCH_E2E_PREFIX}b`;
both must match `^[a-z][a-z0-9]{1,15}$` (so at most 14 characters for the
base), else the test fails at once. The test builds railswitch from source,
runs `project new` and `up` for both base worktrees in a fresh temp workspace, and
checks:

1. From the host: each app host is 200; each `s3.` host answers (not 000 or
   404); `traefik.localhost/api/rawdata` and `logs.localhost` are 200.
2. Each project's rails container gets 200 for its own host through its own
   Traefik IP (`10.214.<n>.2`, Host header), and its own `s3.` host answers
   by name (not 000 or 404: the `AddHost` to Traefik, D49).
3. A's rails container through A's Traefik IP: `traefik.localhost` and
   `logs.localhost` are not 200; B's host is 404.
4. A's rails container cannot connect (curl exit 7 or 28, `--max-time 5`)
   to B's rails container (`:3000`), B's Traefik IP or Dozzle
   `10.214.255.3:8080` (`isolate=strict`).
5. `railswitch machine --json`: IDs, ports, subnets, Traefik IPs and route
   hosts are disjoint between the projects.
6. Only with `tailscale status` working: `share` of A's base worktree adds
   only A's ports to `tailscale serve status --json`, each to its own
   `http://127.0.0.1:<port>`, and changes nothing else; `unshare` restores
   the previous serve config.
7. `project rm s3a --yes --purge`: nothing named `s3a-…`/`s3a_…` remains
   (containers, volumes, networks, unit files); every `s3ab` object is still
   there, `railswitch-traefik` runs and B's app is still 200. Then
   `project rm s3ab --yes --purge`: none of either project's objects (exact
   names recorded before) remains.

`--purge` is used so the shared `<prefix>_…` volumes go too; without it they
stay by design. If the test fails mid-way, its cleanup runs
`project rm <prefix> --yes --purge` for each project not yet removed.

### Run log

NOT YET RUN. The run is a user action on the host; record the date, host,
podman/netavark versions, whether the share part ran, and the `go test`
output here.
