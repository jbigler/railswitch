# railswitch `--json` output

Every read command accepts the global `--json` flag (before the command:
`railswitch --json env`) and prints one JSON document on stdout (D40). Field
names are stable: a later railswitch may add fields, but never renames or
removes one, or changes its type.

## `env [service]`

The effective env of the current worktree (`-w <slug>`, or the worktree the
working directory is in). It is an array with one object per variable. The
objects are grouped by service, in model order (app, deps, worker, data
services, tools), and sorted by `name` within each service. With a
`service` argument, only that service's variables are listed.

```json
[
  {"service": "rails", "name": "DATABASE_URL", "value": "••••••••", "layer": "option", "source": "option postgres@1 [gives]"},
  {"service": "rails", "name": "TWILIO_AUTH_TOKEN", "value": null, "layer": "secret", "source": "podman secret filial_twilio_auth_token"},
  {"service": "rails", "name": "TZ", "value": "Asia/Bangkok", "layer": "project", "source": "project.toml [env]"}
]
```

| Field | Type | Meaning |
|---|---|---|
| `service` | string | Service id, e.g. `rails`, `db`. |
| `name` | string | Env var name. |
| `value` | string or `null` | The value the container gets. A value that contains a generated value (D17, e.g. `DB_PASSWORD`) is `"••••••••"`. A podman secret delivered as an env var is `null`, because railswitch never has its value. |
| `layer` | string | The layer that set the variable: `pack`, `option`, `project`, `service`, `worktree` or `secret`. |
| `source` | string | Where it was declared, e.g. `pack rails@1 [env]`, `project.toml [services.rails.env]`, `worktree port app`. For a podman secret: `podman secret <podman secret name>`. |

## `containerfile list`

The image builds of the current worktree (`-w <slug>`, or the worktree the
working directory is in), in build order: the base chain (`[image] base`,
then each `[image] addons` entry), then each tool image, then each
standalone option image. Then the project's warnings, e.g. an option's
suggested addon that is missing from `[image] addons`.

```json
{
  "steps": [
    {"name": "base", "file": "containerfiles/Containerfile.base", "tag": "localhost/filial/base:ruby3.3.6-node22.11.0-11d1777c", "from": ["docker.io/library/node:22.11.0-slim", "docker.io/library/ruby:3.3.6-slim"]},
    {"name": "addon pg-client", "file": "containerfiles/Containerfile.pg-client", "tag": "localhost/filial/base:ruby3.3.6-node22.11.0-ba118cc2", "from": []},
    {"name": "playwright", "file": "containerfiles/Containerfile.playwright", "tag": "localhost/filial/playwright:1.54.0-8ed7a43c", "from": ["mcr.microsoft.com/playwright:v1.54.0-noble"]}
  ],
  "warnings": []
}
```

| Field | Type | Meaning |
|---|---|---|
| `steps` | array | One object per `podman build`, in build order. |
| `steps[].name` | string | `base`, `addon <name>` for an `[image] addons` entry, a tool's name (`nvim`), or a standalone option image's name (`playwright`). |
| `steps[].file` | string | The Containerfile, relative to the workspace: `containerfiles/Containerfile.<name>`. |
| `steps[].tag` | string | The image tag it builds: `localhost/<prefix>/<repo>:<version>-<hash8>` (D9). |
| `steps[].from` | array of strings | The file's `FROM` images with build args expanded, without `${BASE_IMAGE}`, `scratch` and stage names (D36). Empty for an addon that only builds on the previous step. |
| `warnings` | array of strings | Non-fatal findings; empty when there are none. |

## `secret ls`

The project's declared secrets (`project.toml [secrets]`), sorted by name.
It is an array with one object per secret. It never holds a value: railswitch
does not read a secret's value back from podman.

```json
[
  {"name": "github_token", "scope": "project", "podman_secret": "filial_github_token", "delivery": "file", "driver": "file", "set": false, "created": null, "services": ["nvim"], "driver_differs": false, "git_signing": false},
  {"name": "twilio_auth_token", "scope": "project", "podman_secret": "filial_twilio_auth_token", "delivery": "env TWILIO_AUTH_TOKEN", "driver": "file", "set": true, "created": "2026-10-25T09:08:07Z", "services": ["rails"], "driver_differs": false, "git_signing": false}
]
```

| Field | Type | Meaning |
|---|---|---|
| `name` | string | The secret's name in `[secrets]`. |
| `scope` | string | `project` (podman secret `<prefix>_<name>`) or `machine` (`railswitch_<name>`, shared by every project). |
| `podman_secret` | string | The podman secret's name. |
| `delivery` | string | `file` (mounted at `/run/secrets/<name>`) or `env <VAR>`. |
| `driver` | string | The podman secret driver the value is stored with; when not set, the driver `config.toml` configures (`file` by default). |
| `set` | boolean | Whether the podman secret exists. |
| `created` | string or `null` | When the podman secret was created (RFC 3339); `null` when not set. |
| `services` | array of strings | The service ids that receive it. |
| `driver_differs` | boolean | The value is stored with another driver than `config.toml` configures; `railswitch secret set <name>` re-creates it under the configured one. |
| `git_signing` | boolean | The value is an SSH commit signing key (`git_signing = true`, D61): its services sign commits and tags with it. |

## `wt ls`

The project's registered worktrees, by ID; the base worktree is first (ID 1
on a machine with one project). It is an array with one object per worktree.

```json
[
  {"slug": "master", "id": 1, "branch": "master", "dir": "/home/u/src/filial/master", "dirty": false, "share": null},
  {"slug": "feat", "id": 2, "branch": "feat", "dir": "/home/u/src/filial/feat", "dirty": true, "share": {"host": "box.tail1234.ts.net", "ip": "100.64.0.7"}}
]
```

| Field | Type | Meaning |
|---|---|---|
| `slug` | string | The worktree's name: its directory under the workspace and the `<slug>` in its object names. |
| `id` | number | Its machine-wide ID; its host ports are derived from it. |
| `branch` | string | The branch checked out in it; `""` when detached, or when its directory is missing or not a git worktree. |
| `dir` | string | Its directory, `<workspace>/<slug>`. |
| `dirty` | boolean | `git status --porcelain` lists changes or untracked files. `false` when its directory is missing or not a git worktree. |
| `share` | object or null | Set while the worktree is shared on the tailnet (`railswitch share`): `host` is this machine's tailnet DNS name and `ip` its tailnet IPv4 address. Each port with `share = true` is then served at `https://<host>:<port>/`, the port being its loopback host port. `null` when not shared. |

## `project ls`

Every project registered on this machine, in registry order. It is an array
with one object per project. It reads the registry without locking or
creating it; with nothing registered it is `[]`.

```json
[
  {"prefix": "filial", "workspace": "/home/u/src/filial", "subnet": "10.214.0.0/24", "worktrees": 2, "workspace_missing": false},
  {"prefix": "old", "workspace": "/home/u/src/old", "subnet": "10.214.1.0/24", "worktrees": 1, "workspace_missing": true}
]
```

| Field | Type | Meaning |
|---|---|---|
| `prefix` | string | The project's prefix, the start of its object names. |
| `workspace` | string | Its workspace directory as registered. |
| `subnet` | string | Its `/24`, `10.214.<n>.0/24`. |
| `worktrees` | number | How many worktrees are registered for it. |
| `workspace_missing` | boolean | `<workspace>/project.toml` doesn't exist (moved or deleted workspace); `railswitch project rm` still removes it. |

## `status`

The services of the current worktree (`-w <slug>`, or the worktree the
working directory is in), in model order (app, deps, worker, data services,
tools, agents). It is an array with one object per service.

```json
[
  {"service": "rails", "role": "app", "state": "active/running", "health": "healthy"},
  {"service": "deps", "role": "deps", "state": "inactive/dead", "health": ""},
  {"service": "db", "role": "data", "state": "active/running", "health": "starting"}
]
```

| Field | Type | Meaning |
|---|---|---|
| `service` | string | Service id, e.g. `rails`, `db`. |
| `role` | string | `app`, `deps`, `worker`, `data`, `tool` or `agent`. |
| `state` | string | The unit's systemd state, `<ActiveState>/<SubState>`, e.g. `active/running`, `inactive/dead`, `failed/failed`. An on-demand agent has no unit: `running` while its container runs (`railswitch agent`), else `on-demand`. |
| `health` | string | podman's health status of the container (`starting`, `healthy`, `unhealthy`) for a service with a health check (the app included, D34); `""` for a service without one, or when its container does not exist. |

## `agent ls`

The project's agents (`project.toml [agents]`), sorted by name, with each
one's status in the current worktree (`-w <slug>`, or the worktree the
working directory is in). It is an array with one object per agent. The
status comes from the agent's status file
`<workspace>/.railswitch/status/<slug>/<name>.json` (D58), which the agent's
hook writes.

```json
[
  {"name": "claude", "package": "claude@1", "run": "on-demand", "state": "done", "message": "Ship it?", "ts": 1792919287, "reply": true},
  {"name": "probe", "package": "local:probe@1", "run": "service", "state": "off", "message": "", "ts": 0, "reply": false}
]
```

| Field | Type | Meaning |
|---|---|---|
| `name` | string | The agent's name in `[agents]`; its service is `agent-<name>`. |
| `package` | string | Its package ref: `<name>@<N>` for a built-in package, `local:<name>@<N>` for one under `<workspace>/agents/`. |
| `run` | string | `on-demand` (a transient container per `railswitch agent` run) or `service` (a unit the app wants; `railswitch agent` attaches to it). |
| `state` | string | The status file's `state`: `permission`, `question`, `working`, `done`, `idle` or `off`. `off` also when the file is missing, not a regular file (a symlink, a FIFO), larger than 64 KiB, not valid JSON, has `v` other than `1` or an unknown state. |
| `message` | string | The status file's `message`; `""` when `state` is `off` for one of the reasons above. |
| `ts` | number | The status file's `ts` (Unix seconds, as the hook wrote it); `0` when unusable. |
| `reply` | boolean | The agent waits for a reply: `state` is `done` and `message` ends in `?`, maybe followed by spaces, closing brackets, quotes or `*`/`_`/`` ` `` emphasis. The text table shows `reply` as its `STATE`. |

## `doctor`

The host checks (among them whether another podman network uses the
machine network's reserved 10.214.255.0/24: `machine subnet free`; and
whether every railswitch network of the registry that exists is
`isolate=strict`: `networks isolated`, whose fix is the recreate procedure
of docs/stage3-acceptance.md, "Upgrading from Stage 2") and,
inside a workspace, the project's checks: its firewall
hook descriptor, whether `render` would change any of its files or the
units of any of its worktrees, and whether another podman network overlaps
its proxy subnet. It is an array with one object per check, in a
fixed order. A workspace that doesn't load (a bad `project.toml`, no
`stack.lock`, a bad `-w`) is a failed `workspace` check, listed first, and
its project checks are skipped. The pass secret driver's checks (`pass`,
`gpg`, `gpg secret key`) are listed only when the machine config's
`[secrets] driver` is `pass` (D37); a machine config that can't be read is a
failed `machine config` check. `machine mounts` is listed only when the
machine config has `[[mount]]` entries (D57); it fails when a non-optional
source is missing. `git-guard` (inside a workspace, D60) compares the base
worktree's installed git hooks to their backups in the workspace repo's
`githooks/`, checks the key names (never the values) of its `.git/config`,
`.git/config.worktree` and `.git/worktrees/*/config.worktree` (those that
exist) against an allowlist and each linked worktree's `.git` pointer; a finding
is a warning (`warn` true), and `railswitch hooks check` lists them.
`doctor` exits 1 when any check fails; a warning never changes the exit
code, and the text output shows it as `warn`.

```json
[
  {"name": "podman >= 5.0", "ok": true, "warn": false, "detail": "podman 5.2.1", "fix": ""},
  {"name": "unprivileged port 80", "ok": false, "warn": false, "detail": "net.ipv4.ip_unprivileged_port_start = 1024", "fix": "railswitch allow-ports"},
  {"name": "units current", "ok": true, "warn": false, "detail": "render would change nothing", "fix": ""},
  {"name": "git-guard", "ok": false, "warn": true, "detail": "1 finding(s): hook-missing-backup /home/u/app/main/.git/hooks/pre-push", "fix": "railswitch hooks check"}
]
```

| Field | Type | Meaning |
|---|---|---|
| `name` | string | The check: `workspace`, `podman >= 5.0`, `quadlet generator`, `podman.socket active`, `unprivileged port 80`, `nft`, `nsenter`, `getent`, `jq`, `curl`, `machine subnet free`, `networks isolated`, `firewall descriptor`, `units current`, `proxy subnet`, `git-guard`, `cgroup v2`, `machine mounts`, `pass`, `gpg`, `gpg secret key` or `machine config`. A binary's name (`nft`, `pass`, …) checks that it is on `PATH`. `quadlet generator` passes when `/usr/libexec/podman/quadlet` or `/usr/lib/podman/quadlet` exists. |
| `ok` | boolean | The check passed. |
| `warn` | boolean | A failed check is only a warning (`git-guard`): it doesn't change the exit code. `false` for a passed check. |
| `detail` | string | What was found, e.g. the podman version, a binary's path, or the error. |
| `fix` | string | How to repair a failed check, e.g. a command to run (`railswitch allow-ports`, `railswitch render`, or `railswitch lock` for a stale or missing `stack.lock`); `""` for a passed check. |

## `machine`

The whole machine, from anywhere (no workspace needed): every registered
project, its worktrees and routes, the machine-wide units, and which
containers can reach which. It reads `registry.toml` without locking or
creating it; with none, no project is listed. The ports, routes and reach
rows of a project come from its workspace's model. A project whose
workspace doesn't load (moved, no `project.toml`, no `stack.lock`, …) is
still listed from the registry, with its port bases as last recorded and a
`note`; it has no routes and no reach rows. It is one object.

```json
{
  "projects": [
    {"prefix": "filial", "workspace": "/home/u/src/filial", "subnet": "10.214.0.0/24", "traefik_ip": "10.214.0.2", "note": "",
     "worktrees": [{"slug": "master", "id": 1, "ports": {"app": 13001, "db": 55433, "debug": 33001}, "shared_url": "https://box.tail1234.ts.net:13001/"}]}
  ],
  "routes": [
    {"host": "master.filial.localhost", "project": "filial", "worktree": "master", "service": "rails", "router": "filial-master-app"},
    {"host": "s3.master.filial.localhost", "project": "filial", "worktree": "master", "service": "storage", "router": "filial-master-s3"}
  ],
  "machine": {
    "traefik": {"unit": "railswitch-traefik.service", "state": "active/running"},
    "dozzle": {"unit": "railswitch-dozzle.service", "state": "active/running"},
    "traefik_networks": [
      {"network": "railswitch_machine", "project": "", "attached": true},
      {"network": "filial_proxy", "project": "filial", "attached": true}
    ]
  },
  "reach": {
    "networks": [
      {"network": "railswitch_machine", "members": ["Traefik", "Dozzle"]},
      {"network": "filial_proxy", "members": ["Traefik", "rails", "storage"]},
      {"network": "filial-master-dev", "members": ["rails", "deps", "db", "cache", "storage"]}
    ],
    "notes": ["Traefik reaches every project network", "a project's containers reach another project only through Traefik, which refuses them (D50); networks are isolate=strict, and the Traefik API and Dozzle answer only host clients"],
    "isolated": {"railswitch_machine": true, "filial_proxy": true, "filial-master-dev": true}
  }
}
```

| Field | Type | Meaning |
|---|---|---|
| `projects` | array | One object per registered project, sorted by `prefix`. |
| `projects[].prefix` | string | The project's prefix. |
| `projects[].workspace` | string | Its registered workspace directory. |
| `projects[].subnet` | string | Its proxy network's subnet, `10.214.<n>.0/24`. |
| `projects[].traefik_ip` | string | Traefik's address on that network, `10.214.<n>.2`. |
| `projects[].note` | string | Why its workspace or model didn't load, or why a worktree's routes didn't resolve; `""` when all did. |
| `projects[].worktrees` | array | Its registered worktrees, by ID. |
| `…worktrees[].slug` | string | The worktree's slug. |
| `…worktrees[].id` | number | Its machine-wide ID. |
| `…worktrees[].ports` | object | Port name → its loopback host port (base + ID), for every `[[ports]]` of the project's pack, options and tools. |
| `…worktrees[].shared_url` | string | While shared on the tailnet (`railswitch share`): the first shared port's URL, `https://<tailnet host>:<port>/` (the app's). `""` when not shared. |
| `routes` | array | One object per Traefik route of every worktree, sorted by `host`. |
| `routes[].host` | string | The host name the route answers, e.g. `s3.master.filial.localhost`. |
| `routes[].project` | string | Its project's prefix. |
| `routes[].worktree` | string | Its worktree's slug. |
| `routes[].service` | string | The service it routes to. |
| `routes[].router` | string | Traefik's router name, `<prefix>-<slug>-<route name>`. |
| `machine.traefik`, `machine.dozzle` | object | The machine-wide unit's `unit` name and its systemd `state`, `<ActiveState>/<SubState>` as in `status`. |
| `machine.traefik_networks` | array | The networks Traefik must be on: the machine network `railswitch_machine` (`project` `""`), then each registered project's proxy network. `attached` is whether the running Traefik container is on it; `false` for each when Traefik isn't running. The text output flags a missing one `not attached: railswitch up`. |
| `reach.networks` | array | One object per network with its members: `railswitch_machine` (Traefik and Dozzle), then for each project its proxy network (Traefik and the services on it) and each worktree's dev network (`<prefix>-<slug>-dev`: every service of that worktree). Members are service ids, plus `Traefik` and `Dozzle`. `Traefik` is a member only of a network it is attached to (`machine.traefik_networks`); the text output marks the others `(Traefik not attached: railswitch up)`. |
| `reach.notes` | array of strings | Two lines on what the networks don't show. First, Traefik's reach: `Traefik reaches every project network` when every network of `machine.traefik_networks` is attached, else `Traefik reaches <attached networks, or "no network">; <the others> not attached: railswitch up`. Second, isolation: when every network of `reach.isolated` is `true`, `a project's containers reach another project only through Traefik, which refuses them (D50); networks are isolate=strict, and the Traefik API and Dozzle answer only host clients`; else `networks not isolate=strict (created before Stage 3): <networks>; other projects' containers reach them directly, not only through Traefik; recreate them (…)` with the procedure of docs/stage3-acceptance.md, "Upgrading from Stage 2". |
| `reach.isolated` | object | One field per railswitch network that exists (`railswitch_machine`, each registered project's proxy network, each registered worktree's dev network), named after the network: `true` when `podman network inspect` shows `isolate=strict` (D50), `false` for a network created before Stage 3 with other options. A network that doesn't exist yet has no field. |
