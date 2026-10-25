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
  {"name": "github_token", "scope": "project", "podman_secret": "filial_github_token", "delivery": "file", "driver": "file", "set": false, "created": null, "services": ["nvim"], "driver_differs": false},
  {"name": "twilio_auth_token", "scope": "project", "podman_secret": "filial_twilio_auth_token", "delivery": "env TWILIO_AUTH_TOKEN", "driver": "file", "set": true, "created": "2026-10-25T09:08:07Z", "services": ["rails"], "driver_differs": false}
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

## `wt ls`

The project's registered worktrees, by ID; the base worktree is first (ID 1
on a machine with one project). It is an array with one object per worktree.

```json
[
  {"slug": "master", "id": 1, "branch": "master", "dir": "/home/u/src/filial/master", "dirty": false},
  {"slug": "feat", "id": 2, "branch": "feat", "dir": "/home/u/src/filial/feat", "dirty": true}
]
```

| Field | Type | Meaning |
|---|---|---|
| `slug` | string | The worktree's name: its directory under the workspace and the `<slug>` in its object names. |
| `id` | number | Its machine-wide ID; its host ports are derived from it. |
| `branch` | string | The branch checked out in it; `""` when detached, or when its directory is missing or not a git worktree. |
| `dir` | string | Its directory, `<workspace>/<slug>`. |
| `dirty` | boolean | `git status --porcelain` lists changes or untracked files. `false` when its directory is missing or not a git worktree. |

## `status`

The services of the current worktree (`-w <slug>`, or the worktree the
working directory is in), in model order (app, deps, worker, data services,
tools). It is an array with one object per service.

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
| `role` | string | `app`, `deps`, `worker`, `data` or `tool`. |
| `state` | string | The unit's systemd state, `<ActiveState>/<SubState>`, e.g. `active/running`, `inactive/dead`, `failed/failed`. |
| `health` | string | podman's health status of the container (`starting`, `healthy`, `unhealthy`) for a service with a health check (the app included, D34); `""` for a service without one, or when its container does not exist. |

## `doctor`

The host checks and, inside a workspace, the project's checks: its firewall
hook descriptor, and whether `render` would change any of its files or the
units of any of its worktrees. It is an array with one object per check, in a
fixed order. A workspace that doesn't load (a bad `project.toml`, no
`stack.lock`, a bad `-w`) is a failed `workspace` check, listed first, and
its project checks are skipped. The pass secret driver's checks (`pass`,
`gpg`, `gpg secret key`) are listed only when the machine config's
`[secrets] driver` is `pass` (D37); a machine config that can't be read is a
failed `machine config` check. `doctor` exits 1 when any check fails.

```json
[
  {"name": "podman >= 5.0", "ok": true, "detail": "podman 5.2.1", "fix": ""},
  {"name": "unprivileged port 80", "ok": false, "detail": "net.ipv4.ip_unprivileged_port_start = 1024", "fix": "railswitch allow-ports"},
  {"name": "units current", "ok": true, "detail": "render would change nothing", "fix": ""}
]
```

| Field | Type | Meaning |
|---|---|---|
| `name` | string | The check: `workspace`, `podman >= 5.0`, `quadlet generator`, `podman.socket active`, `unprivileged port 80`, `nft`, `nsenter`, `getent`, `jq`, `curl`, `firewall descriptor`, `units current`, `cgroup v2`, `pass`, `gpg`, `gpg secret key` or `machine config`. A binary's name (`nft`, `pass`, …) checks that it is on `PATH`. `quadlet generator` passes when `/usr/libexec/podman/quadlet` or `/usr/lib/podman/quadlet` exists. |
| `ok` | boolean | The check passed. |
| `detail` | string | What was found, e.g. the podman version, a binary's path, or the error. |
| `fix` | string | How to repair a failed check, e.g. a command to run (`railswitch allow-ports`, `railswitch render`, or `railswitch lock` for a stale or missing `stack.lock`); `""` for a passed check. |
