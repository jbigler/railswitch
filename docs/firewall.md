# The egress firewall

Every worktree container except a self-firewalled one (the browser) gets
an egress firewall, applied from outside the container by an OCI hook
(`.railswitch/firewall/hook.sh`): nothing inside a container can change
its own rules. It always allows loopback, replies and the subnets of the
container's own networks (the worktree's dev network, the proxy network).
Anything else is decided by the container's profiles and the mode.

## Never allowed (D179)

These IPv4 ranges are refused whatever an allow list says, right after
the container's own networks: `0.0.0.0/8`, the private ranges
`10.0.0.0/8`, `172.16.0.0/12` and `192.168.0.0/16`, carrier-grade NAT
`100.64.0.0/10` (which Tailscale uses) and link-local `169.254.0.0/16`,
which holds the cloud metadata address `169.254.169.254`. So a container
can't reach your LAN, your tailnet or a cloud host's credentials through
an allow entry, or through a hostname that resolves to one of them.
An allow entry, `[agents.<name>] allow` entry or `fw allow` argument
that is such an address or range, or contains one (`0.0.0.0/0`), is
refused with the reason. Under `log` they are recorded like anything
else the mode lets through. A container still reaches the worktree's
services and Traefik, which live on its own networks.

After loading the rules the hook reads them back from the kernel and
checks them: the never-allowed set holds every range, its rule comes
before the allow lists, and everything else ends in the mode's verdict.
A mismatch fails the container's start, as a failed load does.

## Profiles: one list per kind of container (D119)

| Container | Profiles | Its list |
|---|---|---|
| The app, its workers, `railswitch exec` | `app` | `app.allow`: the pack's `app.allow`, then `[firewall] allow.app` |
| `deps` (installs) | `deps` | `deps.allow`: the pack's `deps.allow` (its registries), then `[firewall] allow.deps` |
| An agent | `agent-<name>` | its package's `allow`, `[agents.<name>] allow`, then `[firewall] allow.agent` |
| nvim | `agent`, `deps` | both lists, plus the tool's own `allow` (GitHub's API and raw files, PyPI) and the config repo's host |
| Data (database, cache, storage) | `none` | nothing: a data container needs no outside access |

Each container gets only its own list. The deps list holds package
registries and `github.com`, which are also places to send data to; the
agent list holds the agent's API. The app's list is what your app itself
calls, and it is usually short.

Hostnames are resolved by the host when a container starts. With the DNS
filter (below) a container also gets each address it later looks up for an
allowed name, so a host whose addresses change (a CDN, Google) keeps
working; without it, it can need a restart to follow them.

## DNS filter (D182)

When the host has dnsmasq built with nftset (`railswitch doctor` checks:
Arch, Fedora and Debian's are), every container firewalled under
`enforce` (agents always are) resolves through its own filter:

- The firewall hook starts a dnsmasq in the container's network
  namespace, outside its processes, on `127.0.0.1:53`, and mounts a
  read-only `/etc/resolv.conf` that names it.
- It answers the host names on the container's allow lists (and their
  subdomains), the worktree's container names (`db`, `redis`, …) and
  `*.dns.podman`, by asking the network's own resolver. Each address it
  answers for an allowed name is allowed for an hour after it was first
  added (a later lookup doesn't extend it), beside the addresses resolved
  at start.
- Every other name gets `NXDOMAIN` ("no such name"): DNS is no longer a
  way out, and a name an app needs but the list lacks fails at once as
  "Name or service not known". (Not `REFUSED`: programs see that as a
  temporary failure, and npm and Bundler retried it for minutes.)
- Asking any of the container's networks' resolvers (each network's
  gateway: the dev network's and the proxy's) directly is blocked; only
  the filter may. It sends from port 953: below 1024, so nothing in the
  container (which has no capabilities) can take that port while the
  filter restarts.
- `fw allow <service> <host>` adds the name to that container's filter
  too, and restarts the filter. `fw allow --for` doesn't: the filter's
  names have no expiry, so each lookup would put the addresses back and
  outlast the time. A timed allow opens the addresses the host resolves
  now, and says the filter won't answer the name, so code that looks it
  up still fails; allow it without `--for` for that.
- It ends with the container: the poststop hook stops it, and a watcher
  the hook starts beside it ends it within a second of the container's
  process, should that hook not run. Both send SIGKILL: dnsmasq can start
  with SIGTERM blocked. (A dnsmasq left running kept a one-shot unit,
  `deps`, from ever finishing.) `hook.log` gets a line for each stop:
  `<id> stop: ended the DNS filter (pid N)`, or `was already gone` when
  the watcher was first.

A `meta:<name>` entry adds addresses, not names: list the host names the
app uses beside it (`["accounts.google.com", "meta:google"]`). Without
dnsmasq nothing changes: containers resolve any name, and `doctor` warns.
Installing it takes effect at the next render and container start.

An entry `meta:<name>` is a provider's published address ranges instead,
fetched by the host when a container starts (it needs `curl` and `jq`;
offline, it is skipped and named in `hook.log`). Use it for a provider
that answers each lookup from a large rotating pool, where the address
resolved at start is often not the one the app gets a moment later:

| Entry | Ranges |
|---|---|
| `meta:github` | GitHub's web, API and git ranges (`api.github.com/meta`) |
| `meta:google` | Google's own services: `goog.json` without every range that overlaps `cloud.json`, so not the Google Cloud addresses any rented server lives in. Without `cloud.json` nothing is added (D180). It still opens every Google front end, and those also serve what anyone can publish on Google: App Engine (`*.appspot.com`), Cloud Run (`*.run.app`), Firebase Hosting (`*.web.app`), Apps Script and Forms. The firewall checks addresses, not names, so a container with `meta:google` can send data to such a page. Use it for the app when it needs Google; avoid it in an `agent` list |
| `meta:datadog-us5` | Datadog US5's API ranges |
| `meta:anthropic` | Anthropic's inbound ranges, where `api.anthropic.com`, `claude.ai` and `platform.claude.com` answer: `160.79.104.0/23` and `153.61.224.0/22` ([Anthropic's list](https://platform.claude.com/docs/en/api/ip-addresses), 2026-11-06). Fixed in the hook, since Anthropic publishes no machine-readable list; nothing to fetch. The `claude` agent has it (D185) |

For example, an app that signs in with Google at start:
`allow.app = ["accounts.google.com", "meta:google"]`.

## Mode

| Mode | What it does |
|---|---|
| `enforce` | blocks what the lists don't allow |
| `log` | allows everything, records what enforce would block |
| `off` | no rules |

`[firewall] mode` sets it for the project. Without it the default is
`enforce` when the project has agents, else `log` (D119). Agents are
always `enforce`.

Why the default is `enforce` with agents: an agent edits the worktree's
files, which the other containers run. It could change code the dev server
reloads and then request that page itself (its own firewall allows the
worktree's network), add a package whose install script runs in `deps`, or
change a test or job you run later. Each of those runs in another
container, under that container's firewall. With the app in `log`, the
agent's own firewall is only as strong as the app's.

## Seeing and changing it

- `railswitch fw show [service] [--json]`: every container's profiles and
  mode, and what was blocked (or, in log mode, would have been), with the
  host each address resolves to (docs/json.md).
- `railswitch fw allow [--for <duration>] <service> <host>…`: allow a
  host in a running container until it restarts. With `--for` (`30m`,
  `2h`, at most `168h`) the kernel drops the allow when the time is up,
  even if railswitch isn't running; it takes addresses and hostnames,
  not ranges. A hostname is allowed by its addresses only (DNS filter,
  above). A container started before D179 has no set for timed
  allows: restart it first.
- Settings, Firewall card (docs/dashboard.md): the mode, each list, and
  "Blocked, or would be under enforce" with, per resolved host, "Add to
  <list> list" (staged and applied like any other Settings edit:
  `firewall.mode`, `firewall.allow.add`, `firewall.allow.remove`,
  docs/json.md; a container gets it from its next start) and, in a
  container under enforce, "Allow for" 10 minutes, 1 hour or until it
  restarts: `fw allow` in the running container, now (D184).

## Sharing

- `template export` keeps `[firewall]` (mode, `allow.app`, `allow.deps`)
  but drops `allow.agent`: agents are each person's own (D112).
- `pack export` carries the pack's `app.allow` and `deps.allow`: the place
  for hosts every app on that pack needs.
- A review flags every host a template's `firewall.*` lines, a pack's
  `app.allow` or `deps.allow`, an agent's `allow` or a tool's `allow` adds.

## Upgrading to D119

- A project with agents and no `[firewall] mode` goes from `log` to
  `enforce` for the app, its workers, `exec`, `task`, `deps`, data
  containers and nvim the next time each container starts (`up` restarts
  them: their units changed). Its `app` list starts empty, so anything the
  app calls out to is blocked until you allow it. Before the restart,
  `railswitch fw show` (or the Firewall card) lists what log mode
  recorded; allow those hosts first, or set `mode = "log"` to keep the old
  behaviour (the card then warns that the app is a way out).
- Found on the first host run: Node's corepack downloaded Yarn from
  `repo.yarnpkg.com` each time Vite started, in the app container. The
  rails pack now shares corepack's cache (`<prefix>_corepack` at
  `~/.corepack`, `COREPACK_HOME`) between `deps`, which fetches it, and the app,
  so the app needs no allow entry (accept the rails pack's review, then
  `up`).
- A project with nvim gets a pending review: the nvim tool's `tool.toml`
  gained `allow`. Until `railswitch lock --accept`, nvim runs the old copy
  without those hosts, so plugin and language-server downloads from
  `raw.githubusercontent.com` and PyPI are blocked under enforce.
- A project with the `claude` agent gets a pending review (D185): its
  `allow` gained `meta:anthropic` and `platform.claude.com`. Until
  `railswitch lock --accept`, the agent runs the old copy, which allows only
  the addresses `api.anthropic.com` and `claude.ai` resolve to at its start.

## Panic (D181)

`railswitch panic` is for when something is wrong: an agent doing what it
shouldn't. From anywhere, for every registered worktree, it cuts every
running railswitch container off the network (its firewall table becomes
drop-all but loopback, loaded from outside the container as the hook
does), then pauses every one of them, so its processes stop, systemd
doesn't restart it with fresh rules, a browser can't use its `NET_ADMIN`
to undo the cut, and it stays as it was to be looked at (`podman exec`
still works on it once unpaused; `podman logs` now). A container is the
worktree's when its name starts with `<prefix>-<slug>-`: the units'
containers and the throwaway ones too, since `railswitch deps`, `exec
--fresh` and on-demand agents name theirs `<prefix>-<slug>-<service>-run-<n>`
or after the agent. It works from the machine registry alone, so a project
whose `project.toml` doesn't load, or whose workspace is gone, is reached
all the same. It asks nothing first and goes on past a step that fails,
naming it; `--json` reports what it did (docs/json.md). To bring a
worktree back, restart it (`railswitch -w <worktree> restart`): new
containers get the normal rules.

## Host names

A container's `/etc/hosts` holds its own name, `localhost` and the
worktree's routed hosts (`--add-host`), never the host machine's
`/etc/hosts` entries, so names private to your machine or LAN don't
reach it (podman's `--hosts-file=none`, D181).

## Known limits

- DNS: without dnsmasq on the host (or under `log`), the worktree's
  resolver forwards any name upstream, so a name lookup itself is a narrow
  way out. With it (D182), an allowed domain's subdomains resolve too, so
  queries can still carry data to that domain's own name servers. A
  large answer that needs DNS over TCP fails.
- `deps` must reach package registries and GitHub, so an install script an
  agent adds can still reach them. A changed `Gemfile` or `package.json`
  shows in the git diff before you run `up` or `deps`.
