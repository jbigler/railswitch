# The firewall

Every container gets an egress firewall, applied by railswitch from
outside it, so nothing running inside (an agent, code it wrote, an install
script) can change its rules. A container always reaches its own
worktree's containers and the proxy; anything outside this machine goes
through its list.

## One list per kind of container

| Container | Reaches |
|---|---|
| app, workers, `exec` | the `app` list: what your app itself calls (a payment API, a mail relay) |
| deps (installs) | the pack's registries and GitHub, plus the `deps` list |
| agents | the agent package's hosts, plus the `agent` list |
| nvim | the `deps` and `agent` lists, its plugin sources, your config repository |
| data (database, cache, storage) | nothing outside |

Each container gets only its own list. The registries `deps` needs are also
places code could send data to, so the app doesn't get them.

## Modes

- **enforce**: what isn't listed is blocked.
- **log**: everything goes through, and railswitch records what enforce
  would have blocked.
- **off**: no rules.

A project with agents defaults to **enforce**: an agent edits code the
app, workers and installs run, so with the app open the agent's own
firewall would be easy to get around. A project without agents defaults to
**log**. Agents themselves are always enforced.

## Allowing a host

On the Settings page, the **Firewall** card has the mode, the `app`,
`deps` and `agent` lists, and **Blocked, or would be under enforce**: each
address a container tried to reach, with the host it belongs to and an
**Allow in <list>** button that stages it into the right list. Apply like
any other Settings change. The lists are read when a container starts, so
running worktrees pick a change up when restarted: tick the restart box to
do it now.

From the terminal:

```bash
railswitch fw show [service]           # profiles, mode, what was blocked
railswitch fw allow rails api.example  # until that container restarts
```

A host shown only by address (no name that resolves back to it) has to be
added by name, by hand: railswitch won't offer a name the address's owner
picked.

## Moving an existing project to enforce

Leave it on `log` for a while, use the app, then look at what log mode
recorded in the Firewall card and allow what the app really needs before
switching. Hosts change behind CDNs; the lists hold names, resolved at
each start.

Reference: [docs/firewall.md](../firewall.md), including known limits.

Next: [Customizing a project](customizing.md).
