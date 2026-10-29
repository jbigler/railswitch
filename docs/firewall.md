# The egress firewall

Every worktree container except a self-firewalled one (the browser) gets
an egress firewall, applied from outside the container by an OCI hook
(`.railswitch/firewall/hook.sh`): nothing inside a container can change
its own rules. It always allows loopback, replies and the subnets of the
container's own networks (the worktree's dev network, the proxy network).
Anything else is decided by the container's profiles and the mode.

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

Hostnames are resolved by the host when a container starts, so a host
whose addresses change (a CDN) can need a restart to follow them.

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
- `railswitch fw allow <service> <host>…`: allow a host in a running
  container until it restarts.
- Settings, Firewall card (docs/dashboard.md): the mode, each list, and
  "Blocked, or would be under enforce" with an Allow button per resolved
  host. Edits are staged and applied like any other Settings edit
  (`firewall.mode`, `firewall.allow.add`, `firewall.allow.remove`:
  docs/json.md).

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

## Known limits

- DNS: the worktree's resolver forwards any name upstream, so a name
  lookup itself is a narrow way out.
- `deps` must reach package registries and GitHub, so an install script an
  agent adds can still reach them. A changed `Gemfile` or `package.json`
  shows in the git diff before you run `up` or `deps`.
