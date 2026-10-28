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
then each `[image] addons` entry), then each tool image, then, when the
project has agents, `agent-base` (on the base) and one `agent <pkg>` step per
agent package (on `agent-base`), then each standalone option image. Then the project's warnings, e.g. an option's
suggested addon that is missing from `[image] addons`.

```json
{
  "steps": [
    {"name": "base", "file": "containerfiles/Containerfile.base", "tag": "localhost/filial/base:ruby3.3.6-node22.11.0-11d1777c", "from": ["docker.io/library/node:22.11.0-slim", "docker.io/library/ruby:3.3.6-slim"]},
    {"name": "addon pg-client", "file": "containerfiles/Containerfile.pg-client", "tag": "localhost/filial/base:ruby3.3.6-node22.11.0-ba118cc2", "from": []},
    {"name": "agent-base", "file": "containerfiles/Containerfile.agent-base", "tag": "localhost/filial/agent-base:ruby3.3.6-node22.11.0-3f2a9c10", "from": []},
    {"name": "agent claude", "file": "containerfiles/Containerfile.claude", "tag": "localhost/filial/agent-claude:ruby3.3.6-node22.11.0-7d41e0b2", "from": []},
    {"name": "playwright", "file": "containerfiles/Containerfile.playwright", "tag": "localhost/filial/playwright:1.54.0-8ed7a43c", "from": ["mcr.microsoft.com/playwright:v1.54.0-noble"]}
  ],
  "warnings": []
}
```

| Field | Type | Meaning |
|---|---|---|
| `steps` | array | One object per `podman build`, in build order. |
| `steps[].name` | string | `base`, `addon <name>` for an `[image] addons` entry, a tool's name (`nvim`), `agent-base`, `agent <pkg>` for an agent package (`agent claude`), or a standalone option image's name (`playwright`). |
| `steps[].file` | string | The Containerfile, relative to the workspace: `containerfiles/Containerfile.<name>`; `containerfiles/Containerfile.agent-base` for `agent-base` and `containerfiles/Containerfile.<pkg>` for `agent <pkg>`. |
| `steps[].tag` | string | The image tag it builds: `localhost/<prefix>/<repo>:<version>-<hash8>` (D9). |
| `steps[].from` | array of strings | The file's `FROM` images with build args expanded, without `${BASE_IMAGE}`, `scratch` and stage names (D36). Empty for an addon that only builds on the previous step. |
| `warnings` | array of strings | Non-fatal findings; empty when there are none. |

## `containerfile show <name>`

The workspace file `containerfiles/Containerfile.<name>` (read as a hostile
file: regular, at most 1 MiB), the built-in default the project's stack has
for that name, and the diff between them. A missing file is an error (run
`containerfile eject` or `containerfile new` first). Without `--json` the
command prints the file, or the default when there is none.

```json
{"name": "pg-client", "file": "containerfiles/Containerfile.pg-client",
 "kind": "addon", "text": "# Ejected from postgres@1 …", "hash": "sha256:9f2c…",
 "default": {"ref": "postgres@1", "text": "ARG BASE_IMAGE\n…"},
 "ejected": {"ref": "postgres@1", "sha256": "4be1…"},
 "default_changed": false,
 "diff": "--- default\n+++ containerfiles/Containerfile.pg-client\n@@ -5,3 +5,4 @@\n…"}
```

| Field | Type | Meaning |
|---|---|---|
| `name`, `file` | string | The name and the workspace-relative file. |
| `kind` | string | `base`, `addon` (an option's addon), `tool`, `standalone` (an option's own image), `agent-base`, `agent`, or `user` (a `containerfile new` addon: no default). |
| `text` | string | The file. |
| `hash` | string | `sha256:<hex>` of the file's bytes: `containerfile write --if-hash` takes it. |
| `default` | object or null | The built-in default the stack has for `name`: `ref` (its package, `agent-base` or a tool's name) and `text`. `null` when there is none. |
| `ejected` | object or null | The file's first line read as `containerfile eject`'s header: `ref` and the `sha256` of the default it copied. `null` without one. |
| `default_changed` | bool | `default` and `ejected` are both present and the default's sha256 is no longer the header's: the default changed since the file was ejected. |
| `diff` | string | A unified diff (3 lines of context) from the default to the file without its header line; `""` when they are equal or there is no default. |

## `containerfile write <name> --if-hash <h> --dry-run`

What writing stdin (at most 1 MiB) to a Containerfile the project builds
would change: every registered worktree rendered in memory with the new
text, so the contract checks (D31, D36) run before anything is written.
`name` must be a step of the build (`containerfile list`), and the file's
hash must still be `<h>` ("changed on disk since you opened it"); both
refusals exit 1. A new text that does not render exits 0 with `errors`.

```json
{"hash": "sha256:51d0…",
 "steps": [{"worktree": "master", "name": "addon pg-client", "old_tag": "localhost/filial/base:ruby3.3.6-node22.11.0-ba118cc2", "new_tag": "localhost/filial/base:ruby3.3.6-node22.11.0-0c7e93aa"}],
 "restart": ["master"], "errors": []}
```

| Field | Type | Meaning |
|---|---|---|
| `hash` | string | `sha256:<hex>` of the new text. |
| `steps` | array | Each worktree's build step whose tag changes, in build order: `worktree`, `name` (as `containerfile list`), `old_tag`, `new_tag`. A step only one side builds has `""` for the other tag. The steps built on a changed one change too. |
| `restart` | array of strings | The registered worktrees whose app runs (one `systemctl show`) and whose stamped units change; `--restart` on the real write restarts them in order. |
| `errors` | array of strings | Why the new text does not render, one line each; empty when it does. |

Without `--dry-run` the write is atomic and, after it, every worktree is
rendered; a failed render puts the file back. It prints each new tag and,
with `--restart`, restarts the `restart` worktrees (stop, then `up`),
stopping at the first failure. A failed restart exits 3: the file is
written (as is `pack file write`'s).

## `pack show <ref>`

One pack or option: a built-in (no workspace needed) or a local package of
the current workspace (`local:<name>@<N>`, a pack when `packs/<name>/`
exists, else an option under `options/<name>/`).

```json
{
  "ref": "rails@1", "kind": "pack", "source": "built in", "path": "",
  "app": {"service": "rails", "server": "bin/dev", "port": 3000, "health": "curl -fsS http://localhost:3000/up"},
  "runtimes": [{"name": "bun", "from": [".bun-version", "package.json:packageManager.bun"], "fallback": ""}],
  "ports": [{"name": "debug", "base": 33000, "container": 0}, {"name": "app", "base": 13000, "container": 3000}],
  "pin": null, "gives": [], "readme": ""
}
```

| Field | Type | Meaning |
|---|---|---|
| `ref` | string | The ref as given: `name@N` or `local:name@N`. |
| `kind` | string | `pack` or `option`. |
| `source` | string | `built in` or `local`. |
| `path` | string | A local package's directory; `""` for a built-in. |
| `app` | object | A pack's app: `service`, `server`, `port`, `health`. Absent for an option. |
| `slot`, `service` | string | An option's slot and service id. Absent for a pack. |
| `runtimes` | array | A pack's version chains, by name: `name`, `from` (sources, in order), `fallback` (`""`: none). |
| `ports` | array | Published ports: `name`, `base` (the host port is `base` + worktree ID), `container` (`0` for `same_port`). |
| `pin` | object or null | An option's version pin: `name`, `default`. |
| `gives` | array of strings | The env names an option gives the app, sorted. |
| `readme` | string | The package's `README.md` (its app-side changes); `""` when it has none. |
| `files` | array | The package's regular files, sorted by `path`: `path` (slash-separated, relative to the package), `size` (bytes), `executable` (a built-in's `.sh` files, which `pack eject` writes 0755). Symlinks are not listed. |

## `pack file show <ref> <path>`

One file of a local package (read as a hostile file: regular, at most
1 MiB, no symlinked `entrypoints/` or `initdb/`), or of a built-in pack or
option (read-only). Without `--json` it prints the file.

```json
{"path": "entrypoints/entrypoint-app.sh", "text": "#!/bin/sh\n…", "hash": "sha256:7c1e…"}
```

| Field | Type | Meaning |
|---|---|---|
| `path` | string | As given. |
| `text` | string | The file. |
| `hash` | string | `sha256:<hex>` of its bytes: `pack file write` and `pack file rm` take it as `--if-hash`. |

## `pack file write <ref> <path> --if-hash <h> --dry-run`

What writing stdin (at most 1 MiB) to a file of the local package `<ref>`
would change. `--if-hash ""` adds a new file (refused when it exists).
`<path>` must be one of the package format's files (D93,
`docs/packs.md`); a symlinked package directory, `entrypoints/`, `initdb/`
or file is refused. These refusals and a hash mismatch exit 1. The package
must still load with the new text; when the project's stack uses it, every
registered worktree is also rendered with it in memory. A load or render
failure exits 0 with `errors`.

```json
{"hash": "sha256:51d0…", "used": true,
 "steps": [], "restart": ["master"], "errors": []}
```

| Field | Type | Meaning |
|---|---|---|
| `hash` | string | `sha256:<hex>` of the new text. |
| `used` | bool | The project's stack uses the package: the real write also writes `stack.lock` and renders, restoring the file and `stack.lock` when either fails. |
| `steps` | array | As `containerfile write --dry-run`: each worktree's build step whose tag changes (a package's own Containerfiles build only once ejected, so usually empty). Empty when not `used`. |
| `restart` | array of strings | The running worktrees whose stamped units or mounted package assets change (an entrypoint edit changes only an asset). Empty when not `used`. |
| `errors` | array of strings | Why the package does not load or the project does not render, one line each. |

Without `--dry-run` the write is atomic; a `.sh` file is written 0755,
others 0644. `pack file rm <ref> <path> --if-hash <h>` removes a file the
same way (never the manifest), with no `--dry-run`.

## `pack ls`

Every pack and option, for the new-project page's pickers: each built-in
name at its newest version, then, when run in a workspace (`-C <ws>`), its
local packages (`packs/<name>/`, `options/<name>/`) as `local:<name>@N`.
Each list is sorted by ref.

```json
{"packs": [{"ref": "rails@1", "name": "rails"}],
 "options": [{"ref": "postgres@1", "name": "postgres", "slot": "database", "works_with": ["*"]}],
 "agents": [{"ref": "claude@1", "name": "claude", "default": true}]}
```

| Field | Type | Meaning |
|---|---|---|
| `packs[].ref`, `options[].ref` | string | `name@N` or `local:name@N`. |
| `packs[].name`, `options[].name` | string | The package name. |
| `options[].slot` | string | The option's slot. |
| `options[].works_with` | string[] | Pack names it works with, or `["*"]`. |
| `agents[]` | array | Built-in agent packages, newest version per name: `ref`, `name`, `default` (the agent `project new` adds unless `--agent` says otherwise). |

A local package is read leniently: one that doesn't load yet is listed
when its manifest has a `version` (choosing it shows the load error); one
without is left out.

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

## `signer ls`

The signers trusted per library (D100), from `signers.toml` in the machine
config directory. No workspace needed. Libraries are in the file's order.
`libraries` is `[]` when none is trusted, and no array is ever `null`.

```json
{
  "libraries": [
    {
      "id": "example.org/acme/stack-library",
      "last_serial": 7,
      "keys": [{"key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI…", "fingerprint": "SHA256:Ays0teoPOjVM/BphiuEfsQ7gCq7VhFUQd9F9RRAZJXU", "added": "2026-10-28"}],
      "revoked": [{"fingerprint": "SHA256:CX0ZgmTfq/8FQtQpmhqQvuOlkCvpUriJAz+IO6VEXIo"}]
    }
  ]
}
```

| Field | Type | Meaning |
|---|---|---|
| `libraries[].id` | string | The library's id, as its `library.toml` names it. |
| `libraries[].last_serial` | number | The highest serial verified as trusted. An index with a lower serial, or another index at this serial (`signers.toml`'s `last_index` holds the sha256 of the verified one), is `rolled-back`. `0` before the first trusted verify. |
| `libraries[].keys[]` | array | The keys trusted for this library only: `key` (`<type> <base64>`, no comment), `fingerprint` (`SHA256:…`, as `ssh-keygen -lf` prints it), `added` (the day it was trusted, `YYYY-MM-DD`). |
| `libraries[].revoked[]` | array | Fingerprints never trusted again for this library: `fingerprint`. `signer add` refuses them. |

An entry stays after `signer rm` removes its last key when it has
revocations or a `last_serial`. So `signer rm` then `signer add` can't undo
a revocation or accept an older index.

## `library verify <dir>`

The verdict on the library folder `<dir>` (D99): its signed index
`library.toml`, its signature `library.toml.sig`, and each package
directory under `packs/`, `options/` and `agents/`. It exits 0 only when
`status` is `trusted`, and prints the JSON whatever the status. A trusted
verify saves `signers.toml`: the library's `last_serial` and `last_index`, and the index's
`[[revoke]]` fingerprints moved from its keys to its revoked list. An
error (no `ssh-keygen`, an unreadable `packs/`) prints no JSON and exits 1.

```json
{
  "status": "rotation",
  "library": "example.org/acme/stack-library",
  "serial": 8,
  "fingerprint": "SHA256:Ays0teoPOjVM/BphiuEfsQ7gCq7VhFUQd9F9RRAZJXU",
  "key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI…",
  "new_fingerprint": "SHA256:FMVaaJrORVs5Xx60M485oK5AtUTpvWqQapBKMLidPeA",
  "new_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI…",
  "known_library": true,
  "packages": [
    {"ref": "django@1", "kind": "pack", "dir": "packs/django", "signed": false, "reason": "the index is not trusted"},
    {"ref": "", "kind": "option", "dir": "options/broken", "signed": false, "reason": "not in library.toml"}
  ]
}
```

| Field | Type | Meaning |
|---|---|---|
| `status` | string | `trusted`: signed by a key trusted for this library, serial not below `last_serial`. `unknown-signer`: validly signed by a key not trusted for it. `rotation`: signed by a new key that a trusted key vouches for in `[[rotate]]`, not yet confirmed. `rolled-back`: trusted or a rotation, but the serial is below `last_serial`, or equal to it with another `library.toml`. `revoked`: signed by a key revoked for it. `bad-signature`: the index or signature is malformed, or the signature does not cover the index. |
| `reason` | string | Why, for `bad-signature` and `rolled-back`; absent otherwise. It is text from the library, which the text form prints with control characters replaced. |
| `library` | string | The index's `library`; `""` when the index doesn't parse. |
| `serial` | number | The index's `serial`; `0` when the index doesn't parse. |
| `fingerprint`, `key` | string | The signing key; for a `rotation`, the trusted key that vouches for the new one. Absent when the signature doesn't parse. |
| `new_fingerprint`, `new_key` | string | A `rotation`'s new key, which signed the index; absent otherwise. |
| `known_library` | boolean | `signers.toml` already holds trusted keys or revocations for this library. With `unknown-signer`, the index is signed by a key no trusted key vouched for, which is not a first trust: `library trust` refuses it without `--new-signer`. |
| `packages[]` | array | Each package the index lists, then each package directory it doesn't. `[]` when the index or the signature doesn't parse. |
| `packages[].ref` | string | The package's ref; `""` for an unlisted directory that doesn't load. |
| `packages[].kind` | string | `pack`, `option` or `agent`. |
| `packages[].dir` | string | Its directory in the library, `<kind>s/<name>`. |
| `packages[].signed` | boolean | `status` is `trusted`, the index lists the package, and its content hash (`pack.ContentHash`, the hash `stack.lock` pins) matches. Always `false` for any other status. |
| `packages[].reason` | string | Why it is not signed: the index is not trusted (`"the index is not trusted"`), the directory doesn't load, declares another ref, its content differs, or the index doesn't list it. Absent when signed. |

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
  {"name": "claude", "package": "claude@1", "run": "on-demand", "state": "done", "message": "Ship it?", "ts": 1792919287, "reply": true, "running": true},
  {"name": "probe", "package": "local:probe@1", "run": "service", "state": "off", "message": "", "ts": 0, "reply": false, "running": false}
]
```

| Field | Type | Meaning |
|---|---|---|
| `name` | string | The agent's name in `[agents]`; its service is `agent-<name>`. |
| `package` | string | Its package ref: `<name>@<N>` for a built-in package, `local:<name>@<N>` for one under `<workspace>/agents/`. |
| `run` | string | `on-demand` (a transient container per `railswitch agent` run) or `service` (a unit that only `railswitch agent` starts, never `up`, and then attaches to; it runs until `agent stop`, `stop`/`down` or `wt rm`). |
| `state` | string | The status file's `state`: `permission`, `question`, `working`, `done`, `idle` or `off`. `off` also when the file is missing, not a regular file (a symlink, a FIFO), larger than 64 KiB, not valid JSON, has `v` other than `1` or an unknown state. |
| `message` | string | The status file's `message`; `""` when `state` is `off` for one of the reasons above. |
| `ts` | number | The status file's `ts` (Unix seconds, as the hook wrote it); `0` when unusable. |
| `reply` | boolean | The agent waits for a reply: `state` is `done` and `message` ends in `?`, maybe followed by spaces, closing brackets, quotes or `*`/`_`/`` ` `` emphasis. The text table shows `reply` as its `STATE`. |
| `running` | boolean | The agent's container runs in this worktree (podman, not the status file). At most one agent runs per worktree (D62). The text table marks it with `*` in its `RUNNING` column, and shows `running` as its `STATE` when the status file says `off`. |

## `overview`

One project (`-C`): every registered worktree, in registry order (by ID),
with its stack state, services, agent and links. The dashboard polls it
every 2 s, so it is cheap: one `systemctl --user show` for every unit of
the project, exactly one `podman ps -a --format json` (running
on-demand services and agents, and each running container's unit-hash
label), no health check and no git call. The text form is a table `WORKTREE BRANCH STACK AGENT` (the base
worktree marked `(base)`) with each worktree's links indented under its
row.

```json
{
  "prefix": "filial",
  "workspace": "/home/u/workspace/filial",
  "review_pending": false,
  "signer_warnings": [],
  "worktrees": [
    {
      "slug": "feat-login", "id": 2, "branch": "jb/feat-login", "base": false,
      "stack": "up", "stale": false,
      "services": [{"service": "rails", "role": "app", "state": "active/running"}],
      "agent": {"name": "claude", "package": "claude@1", "run": "on-demand", "state": "permission", "message": "Bash: rm -rf tmp", "ts": 1793115142, "reply": false, "running": true},
      "links": [
        {"label": "App", "url": "http://feat-login.filial.localhost/", "group": "app"},
        {"label": "Flipper", "url": "http://feat-login.filial.localhost/flipper", "group": "project"}
      ]
    }
  ]
}
```

| Field | Type | Meaning |
|---|---|---|
| `prefix` | string | The project's prefix. |
| `workspace` | string | Its workspace directory as registered. |
| `review_pending` | boolean | A package update waits for review (D97): a package whose content changed under the ref `stack.lock` pins runs its accepted copy from the store, or `stack.lock` is out of date. [`lock --review`](#lock---review) shows it. |
| `signer_warnings` | array of strings | Library packages this project runs whose signer was revoked or is no longer trusted (D102). Always `[]` until library packages can be installed (Stage 7c). |
| `worktrees` | array | One object per registered worktree, by ID; `[]` when none. |
| `worktrees[].slug` | string | The worktree's slug, its directory under the workspace. |
| `worktrees[].id` | number | Its registry ID. |
| `worktrees[].branch` | string | The branch checked out, read from its git files; `""` when detached, unreadable or the directory is gone (the worktree is still listed). |
| `worktrees[].base` | boolean | It is the base worktree (`<workspace>/<base_branch>`). |
| `worktrees[].stack` | string | Over the services with roles `app`, `worker`, `data` (not `deps`, `tool`, `agent`): `failed` if any unit's ActiveState is `failed`, `up` if all are `active`, `stopped` if none is `active` or `activating` (or there are none), else `partial`. |
| `worktrees[].stale` | boolean | A running container of a stack service (not an on-demand one) was created from an older unit than the one rendered in the Quadlet directory: its `<prefix>.unit-hash` label differs from the unit file's (a missing label counts as different, as in `up`). `up` restarts such a container; the dashboard shows "restart to apply". A container that does not run, or a unit not rendered yet, never makes it `true`. |
| `worktrees[].services` | array | Every service in model order, as in [`status`](#status) without `health`: `service`, `role`, `state` (`<ActiveState>/<SubState>`; an on-demand service `running` or `on-demand`). |
| `worktrees[].agent` | object or null | The worktree's running agent (D62), else the first configured agent (`[agents]` order) whose status is not `off`, else `null`. Fields as in [`agent ls`](#agent-ls). |
| `worktrees[].links` | array | The worktree's links, in order: `app` (`App`, `http://<worktree host>/`); `project` (`project.toml [[links]]`, in file order); `stack` (the pack's, then options' in slot order, then enabled tools', each in file order), minus labels in `hide_links`. Placeholders expanded; a URL without a scheme gets `http://`. Each `{"label", "url", "group"}`, `group` one of `app`, `project`, `stack`. |

## `lock --review`

The review of what `lock --accept` would write against the current
`stack.lock` (D97): every package whose lock entry would change, each
changed file as a diff, risky changes flagged (the checks in
[packs.md](packs.md#updates-wait-for-review-d97)). It writes nothing.
Package content is resolved as on disk, never from the store; the old side
of a `changed` package is the store's copy of what the lock accepted.

```json
{"hash": "sha256:2c07…", "flags": 1,
 "packages": [{"ref": "rails@1", "kind": "pack", "status": "changed",
   "from": "e5aa…", "to": "462d…", "also_locked_in": ["maybe"],
   "files": [{"path": "deps.allow", "status": "changed",
     "diff": "--- a/deps.allow\n+++ b/deps.allow\n@@ -1,3 +1,4 @@\n…",
     "flags": [{"check": "firewall", "line": 4, "text": "+ objects.githubusercontent.com"}]}]}],
 "restart": ["main"]}
```

| Field | Type | Meaning |
|---|---|---|
| `hash` | string | `sha256:<hex>` of the exact `stack.lock` bytes the accept writes; `lock --accept` takes it and refuses any other. |
| `flags` | number | Every flag of every file, summed; Accept needs "I've read the N flagged items" when it is above 0. |
| `packages` | array | One per package the accept changes; `[]` when there is nothing to review. |
| `packages[].ref` | string | `name@N` or `local:name@N`. |
| `packages[].kind` | string | `pack`, `option` or `agent`. |
| `packages[].status` | string | `changed` (same ref, new content; also a ref back in the lock whose content differs from the entry `stack.lock` remembers in `[[removed]]`); `first` (changed, no stored copy of what was accepted: compared against nothing, every file `added`); `added` (a ref new to the lock); `removed` (a ref the lock drops; a version or locality change is a `removed` and an `added`). |
| `packages[].from`, `to` | string | The content hash locked now and the one the accept locks (lowercase hex); `""` for none (`added`: no `from`, `removed`: no `to`). |
| `packages[].also_locked_in` | array | Prefixes of other registered projects whose `stack.lock` already pins this ref at `to`; `[]` when none. |
| `packages[].files` | array | Every file that differs, by path (unchanged files left out). |
| `files[].status` | string | `added`, `removed` or `changed`. |
| `files[].diff` | string | Unified diff, 3 lines of context (`--- a/<path>`/`+++ b/<path>`, `/dev/null` for an added or removed side). Every file is diffed as text: a NUL or invalid UTF-8 byte is shown as `\xNN` (and the file carries a `runs-here` flag); `""` only for an empty file added or removed. |
| `files[].flags` | array | `{check, line, text}`, `[]` when none. `check` is one of `firewall`, `runs-here`, `image`, `host-mount`, `port`, `privilege`. `line` is the 1-based line in the new file (map it through the hunks' `+N,M` ranges); `0` for a manifest field flag (listed above the diff) or a file-level flag. A removed file has no flags. `text` is the flagged line as `+ <line>`, or a sentence for a line-0 flag. |
| `restart` | array | Running worktrees whose units or mounted package assets the accept changes (as in [`settings preview`](#settings-preview)); `[]` when none or nothing to review. |

## `pr ls`

One project (`-C`): every registered worktree, in registry order (by ID),
with its branch's pull request. For each distinct worktree branch it runs
`gh pr list --repo <owner/name> --head=<branch> --state all --limit 20
--json
number,title,url,headRefName,isCrossRepository,isDraft,state,reviewDecision,statusCheckRollup`
on the host with `GH_PROMPT_DISABLED=1`, in `/`, 30 s timeout each (one
repo-wide query with checks times out at GitHub, HTTP 504, on a busy
repo). The repo comes from `project.toml`'s `repo` (never a worktree's
`.git/config`); one that is not on GitHub fails with "pr ls: <url> is not
a GitHub repo". `gh` missing or not logged in: exit 1 with gh's message.
The text form is a table `WORKTREE BRANCH PR STATE CHECKS REVIEW TITLE`
(branch and title with control characters as spaces).

```json
[
  {"slug": "master", "branch": "master", "pr": null},
  {"slug": "feat-login", "branch": "jb/feat-login", "pr": {"number": 2141, "title": "Login", "url": "https://github.com/o/filial/pull/2141", "state": "open", "checks": "pass", "review": "approved"}}
]
```

| Field | Type | Meaning |
|---|---|---|
| `slug` | string | The worktree's slug. |
| `branch` | string | The branch checked out, as in [`overview`](#overview); `""` gets no PR. |
| `pr` | object or null | The PR whose head branch is `branch`: an open (or draft) one first, then the highest number; `null` when there is none. PRs from forks (`isCrossRepository`) never match: their head branch is the fork's. |
| `pr.number`, `pr.title`, `pr.url` | number, string, string | As GitHub has them. |
| `pr.state` | string | `draft` (open and draft), `open`, `merged` or `closed`. |
| `pr.checks` | string | `fail` if any check concluded failure, error, timed out, cancelled, action required or startup failure; else `pending` if any is not completed (or a status context is pending or expected); else `pass`; `none` with no checks. |
| `pr.review` | string | `approved`, `changes_requested`, `pending` (review required) or `none`. |

## `task ls`

The worktree's (`-w`) tasks, by name: the pack's `[tasks]` with
`project.toml`'s `[tasks]` over them (a project task replaces a pack task
of the same name; `""` removes one). `railswitch task <name> [args…]`
runs one. The text form is a table `NAME SOURCE COMMAND`.

```json
[
  {"name": "console", "command": "bin/rails console", "source": "pack"},
  {"name": "lint", "command": "bin/rubocop", "source": "project"}
]
```

| Field | Type | Meaning |
|---|---|---|
| `name` | string | The task's name. |
| `command` | string | Its shell command, run as `sh -c '<command> "$@"' <name> <args…>`. |
| `source` | string | `pack` or `project`. |

## `settings preview`

Reads an edit list on stdin (a JSON array; ops `option`, `env.set`,
`env.unset`, `link.add`, `link.remove`, `link.hide`, `link.show`; strict:
an unknown field or op is an error), writes nothing and judges each edit
against the project with the edits before it applied. Without `--json` it
prints a short summary and the diff. Exit 0 even when an edit is blocked.

```json
{
  "hash": "sha256:5cc7…",
  "diff": "--- project.toml\n+++ project.toml\n@@ -14,7 +14,7 @@\n…",
  "edits": [
    {"index": 0, "impact": "check", "reasons": ["REDIS_URL is no longer given to rails"], "error": ""}
  ],
  "restart": ["master"]
}
```

| Field | Type | Meaning |
|---|---|---|
| `hash` | string | `sha256:<hex>` of the current `project.toml`; `settings apply` and `project write` take it as `--if-hash`. |
| `diff` | string | Unified diff of `project.toml` before and after every edit that could be placed. |
| `edits[].index` | number | Position in the edit list. |
| `edits[].impact` | string | `none` (no data affected), `check` (an env value the app gets changes), `data` (a non-cache volume is left behind) or `blocked`. |
| `edits[].reasons` | string[] | Why, one line each; empty for `none`. |
| `edits[].error` | string | For `blocked`, the message; else `""`. |
| `restart` | string[] | Registered worktrees whose app unit is active and whose rendered units the edits change. One batched `systemctl show` reads the states. |

`settings apply --if-hash <h> [--restart]` takes the same list and prints
text, not JSON: `wrote <path>/project.toml`, `wrote <path>/stack.lock`,
the render's `added|changed|removed <file>` lines, then `restarted <slug>`
per restarted worktree. It refuses (exit 1) when the hash differs
(`project.toml changed since the preview`) or an edit is blocked (one
`edit <i> is blocked: <error>` line each), and puts the old
`project.toml` and `stack.lock` back when lock or render fails.

## `project show`

What the settings page reads: the project's choices, env and links.

```json
{
  "prefix": "filial", "name": "Filial Rails",
  "repo": "git@github.com:[ORG]/[REPO].git", "base_branch": "master",
  "hash": "sha256:5cc7…", "file": "schema = 1\n…",
  "options": [
    {"key": "database", "slot": true, "ref": "postgres@1", "choices": ["postgres@1", "local:mariadb@1", "none"]},
    {"key": "jobs", "slot": true, "ref": "none", "choices": ["sidekiq@1", "none"]}
  ],
  "used": ["rails@1", "postgres@1", "claude@1"],
  "env": [{"name": "TZ", "value": "Asia/Bangkok"}],
  "links": [
    {"label": "Admin", "url": "{worktree.host}/admin", "from": "project", "hidden": false},
    {"label": "Mailer previews", "url": "{worktree.host}/rails/mailers", "from": "pack rails@1", "hidden": false},
    {"label": "Storage console", "url": "s3-ui.{worktree.host}", "from": "option rustfs@1", "hidden": false}
  ]
}
```

| Field | Type | Meaning |
|---|---|---|
| `prefix`, `name`, `repo`, `base_branch` | string | The `[project]` table. |
| `hash` | string | As `settings preview`. |
| `file` | string | The text of `project.toml`, for the raw editor; `project write --if-hash <hash>` saves it. |
| `options[]` | array | Every slot (`config.Slots` order), then the other `[options]` keys by name. A slot left out reads `ref: "none"`. |
| `used` | string[] | The stack's refs: the pack, each option, each agent package (once). |
| `add_choices` | string[] | Every option a new extra key (D81) can take: built-ins at their newest version, then the workspace's `local:<name>@N`. |
| `options[].slot` | bool | The key is a slot (otherwise an extra service, D81). |
| `options[].choices` | string[] | For a slot, the built-in options of that slot (newest version per name) then the workspace's `options/<name>/` ones as `local:<name>@N`; for an extra key every option; always ending in `none`. The current ref is always among them. |
| `env[]` | array | The `[env]` table by name. |
| `links[]` | array | The project's `[[links]]` (`from: "project"`), then the stack's: pack, options in key order, tools (`pack <ref>`, `option <ref>`, `tool <name>`). URLs keep their `{placeholders}`. |
| `links[].hidden` | bool | The label is in `hide_links`. |

`project write --if-hash <h>` reads the whole `project.toml` on stdin,
refuses on a hash mismatch, parse, validation or resolve error (exit 1,
file untouched), else writes, locks and renders as `settings apply` does.

## `project detect <repo> [--branch <b>] [--pack <ref>]`

What the new-project page reads (D88): the pack and options a repository's
files suggest, each with its evidence. railswitch reads only the files
the built-in packages' detection rules name (`docs/packs.md`), straight
from the object store of a shallow, blobless clone with no working tree
(`git ls-tree`, `git cat-file`), with no hooks, no LFS and no prompts, in
a temp dir under `$XDG_RUNTIME_DIR` that it always removes (the fetch has
2 minutes in all; a `railswitch-detect-*` dir of yours older than 10
minutes, left by a killed run, goes at the next detect). Only a regular
file of at most 1 MiB counts: a path that is a symlink, is under one, is a
directory or is over 1 MiB counts as absent, and no file is inflated onto
disk. A git failure exits 1
with git's error; a repository or branch starting with `-`, or a
`<transport>::` URL (`ext::`, `fd::`, any remote helper), is refused
before git runs.

```json
{"branch": "main",
 "pack": {"ref": "rails@1", "evidence": ["Gemfile", "config/application.rb"], "others": []},
 "options": [
   {"key": "database", "ref": "postgres@1", "source": "repo", "evidence": "Gemfile: pg", "others": []},
   {"key": "mail", "ref": "none", "source": "default", "evidence": "", "others": []}
 ]}
```

| Field | Type | Meaning |
|---|---|---|
| `branch` | string | `--branch`, else the remote's HEAD branch. |
| `pack` | object or null | `null` when no pack's `[detect] files` are all in the repository (and no `--pack`). |
| `pack.ref` | string | `--pack`, else the first pack by ref whose files all exist. |
| `pack.evidence` | string[] | The pack's detect files the repository has. |
| `pack.others` | string[] | The other packs whose files all exist. |
| `options[]` | array | One per slot, `config.Slots` order; empty when `pack` is `null`. |
| `options[].key` | string | The slot. |
| `options[].ref` | string | Among the slot's options that work with the pack and have a matching rule: the pack's `[defaults]` choice if it matched, else the first by ref. With no match, the pack's default (`none` when it has none). |
| `options[].source` | string | `repo` (a rule matched) or `default`. |
| `options[].evidence` | string | The matching rule's `evidence`; `""` for `default`. |
| `options[].others` | string[] | The other matching options of the slot. |

## `project new --dry-run`

What `project new` with the same flags would create (D89). The dry run makes
every check the real run makes and refuses with the same message (exit 1):
an existing `project.toml`, a path the run would create that is already
there (the base worktree's dir, `.gitignore`, `mise.local.toml`,
`stack.lock`, each ejected `containerfiles/Containerfile.<name>`), a
workspace path that is not a directory, a bad name, a prefix in use, a bad branch, an
unknown ref, an option the pack can't use, an env clash, a render error. It
writes nothing: no clone, no workspace dir, no registry entry, no units, and
no podman or systemctl call. Without `--branch` it reads the remote's HEAD
branch (`git ls-remote`).

```json
{
  "project_toml": "schema = 1\n\n[project]\nname = \"shop\"\nprefix = \"shop\"\n…",
  "worktree": {
    "slug": "main",
    "routes": [{"host": "main.shop.localhost", "service": "rails"}],
    "containers": [
      {"name": "shop-main-rails", "role": "app"},
      {"name": "shop-main-deps", "role": "deps"},
      {"name": "shop-main-db", "role": "data"},
      {"name": "shop-main-cache", "role": "data"},
      {"name": "shop-main-agent-claude", "role": "agent"}
    ],
    "env": ["APP_PORT", "BINDING", "DATABASE_URL", "PGDATABASE", "…", "REDIS_URL", "WORKTREE_SLUG"]
  },
  "warnings": [],
  "steps": [
    "Clone https://github.com/acme/shop (main) into /home/me/shop/main",
    "Write project.toml and stack.lock, generate local passwords",
    "Eject 4 Containerfiles",
    "Register, lock and render",
    "Start main (up)"
  ]
}
```

| Field | Type | Meaning |
|---|---|---|
| `project_toml` | string | The bytes the real run writes to `project.toml`, with `[image]` and the declared secrets. |
| `worktree.slug` | string | The base worktree: the base branch, sanitized. |
| `worktree.routes[]` | array | Each Traefik route of the base worktree: its host and the routed service's id. |
| `worktree.containers[]` | array | Every service of the stack in model order, as its container name and role (`app`, `deps`, `worker`, `data`, `tool`, `agent`). An on-demand agent is listed too. |
| `worktree.env` | string[] | The app service's env names, sorted. No values: a real run generates the passwords. |
| `warnings` | string[] | The render's warnings. |
| `steps` | string[] | What the real run does, in order. The last step is there only with `--up`. |

The summary comes from a render in memory: the base worktree is the only
worktree, on a registry entry with the next free subnet and worktree ID,
never saved. Without `--json`, the dry run prints the same content as text.

`project new --up` (without `--dry-run`) exits 3 when the project was
created and registered but `up` failed: stderr says so and names the
command to retry. Exit 1 means the project was not created.

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
`.git/config.worktree`, `.git/worktrees/*/config.worktree` and the submodule
git dirs' `.git/modules/**/config` (those that exist) against an allowlist,
that no `.git/commondir` (nor one in `modules/**`) exists, that each
`.git/worktrees/*/commondir` reads `../..` and each `*/gitdir` names a
registered worktree's `.git`, that each linked worktree's `.git` pointer names
such an entry, and reports any hook in `.git/modules/**/hooks/` and any
symlink or file where `.git/worktrees`, `.git/modules` or a directory under
them should be (`not-a-dir`); a finding
is a warning (`warn` true), and `railswitch hooks check` lists them.
`ssh-keygen -Y` checks that `ssh-keygen` supports `-Y verify` (OpenSSH 8.2
or newer), which `library verify` and `library sign` need; a failure is a
warning, since nothing else needs it.
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
| `name` | string | The check: `workspace`, `podman >= 5.0`, `quadlet generator`, `podman.socket active`, `unprivileged port 80`, `nft`, `nsenter`, `getent`, `jq`, `curl`, `ssh-keygen -Y`, `machine subnet free`, `networks isolated`, `firewall descriptor`, `units current`, `proxy subnet`, `git-guard`, `cgroup v2`, `machine mounts`, `pass`, `gpg`, `gpg secret key` or `machine config`. A binary's name (`nft`, `pass`, …) checks that it is on `PATH`. `quadlet generator` passes when `/usr/libexec/podman/quadlet` or `/usr/lib/podman/quadlet` exists. |
| `ok` | boolean | The check passed. |
| `warn` | boolean | A failed check is only a warning (`git-guard`, `ssh-keygen -Y`): it doesn't change the exit code. `false` for a passed check. |
| `detail` | string | What was found, e.g. the podman version, a binary's path, or the error. |
| `fix` | string | How to repair a failed check, e.g. a command to run (`railswitch allow-ports`, `railswitch render`, or `railswitch lock` for a missing `stack.lock`, `railswitch lock --review` for a stale one); `""` for a passed check. |

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
    "dashboard": {"unit": "railswitch-dashboard.service", "state": "active/running", "url": "http://railswitch.localhost:7700/"},
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
| `machine.dashboard` | object | The dashboard's plain systemd user unit (`unit`: `railswitch-dashboard.service`, at `$XDG_CONFIG_HOME/systemd/user/`, written by every render, started by `Wants=` from every app unit), its systemd `state` (`<ActiveState>/<SubState>`) and the `url` it is opened at, `http://railswitch.localhost:7700/` (it listens on `127.0.0.1:7700` only). `railswitch dashboard` prints the login link. The text output's line is `<unit> <state> <url>`. |
| `machine.traefik_networks` | array | The networks Traefik must be on: the machine network `railswitch_machine` (`project` `""`), then each registered project's proxy network. `attached` is whether the running Traefik container is on it; `false` for each when Traefik isn't running. The text output flags a missing one `not attached: railswitch up`. |
| `reach.networks` | array | One object per network with its members: `railswitch_machine` (Traefik and Dozzle), then for each project its proxy network (Traefik and the services on it) and each worktree's dev network (`<prefix>-<slug>-dev`: every service of that worktree). Members are service ids, plus `Traefik` and `Dozzle`. `Traefik` is a member only of a network it is attached to (`machine.traefik_networks`); the text output marks the others `(Traefik not attached: railswitch up)`. |
| `reach.notes` | array of strings | Two lines on what the networks don't show. First, Traefik's reach: `Traefik reaches every project network` when every network of `machine.traefik_networks` is attached, else `Traefik reaches <attached networks, or "no network">; <the others> not attached: railswitch up`. Second, isolation: when every network of `reach.isolated` is `true`, `a project's containers reach another project only through Traefik, which refuses them (D50); networks are isolate=strict, and the Traefik API and Dozzle answer only host clients`; else `networks not isolate=strict (created before Stage 3): <networks>; other projects' containers reach them directly, not only through Traefik; recreate them (…)` with the procedure of docs/stage3-acceptance.md, "Upgrading from Stage 2". |
| `reach.isolated` | object | One field per railswitch network that exists (`railswitch_machine`, each registered project's proxy network, each registered worktree's dev network), named after the network: `true` when `podman network inspect` shows `isolate=strict` (D50), `false` for a network created before Stage 3 with other options. A network that doesn't exist yet has no field. |

## `dashboard colors`

The dashboard's colours, from `~/.config/railswitch/dashboard.toml`
(`$XDG_CONFIG_HOME/railswitch/dashboard.toml`): for each mode, its preset,
the tokens changed from it and all twelve resulting colours, and the
contrast warnings. `railswitch dashboard colors set [--dark] [--base
<preset>] [<token>=#rrggbb…]` changes one mode (light without `--dark`);
`railswitch dashboard colors reset [--dark]` drops one mode's changed
tokens and keeps its preset. The text form is a table `MODE TOKEN COLOUR
FROM` (`FROM` is the preset, or `set` for a changed token), then a
`warning: <mode> <warning>` line per warning; `set` prints the changed
mode's warnings to stderr.

```json
{
  "light": {"base": "flexoki", "overrides": {"text": "#EEEEEE"},
            "colors": {"bg": "#FFFCF0", "bg2": "#F2F0E5", "text": "#EEEEEE", "muted": "#6F6E69", "accent": "#BC5215", "blue": "#205EA6", "green": "#536907", "yellow": "#8E6B01", "sel": "#E6E4D9", "border": "#CECDC3", "purple": "#5E409D", "red": "#AF3029"}},
  "dark": {"base": "flexoki", "overrides": {}, "colors": {"bg": "#100F0F", "…": "…"}},
  "warnings": {"light": ["text: contrast 1.1:1 against bg (below 4.5:1)"], "dark": []}
}
```

| Field | Type | Meaning |
|---|---|---|
| `light`, `dark` | object | One mode. |
| `….base` | string | Its preset: `flexoki` (the default), `paper`, `solar` or `contrast`. |
| `….overrides` | object | Token → `#RRGGBB` (upper case) for each editable token changed from the preset; `{}` when none is. |
| `….colors` | object | All twelve tokens → `#RRGGBB`: the preset with the overrides applied. Editable: `bg`, `bg2`, `text`, `muted`, `accent`, `blue`, `green`, `yellow`; fixed by the preset: `sel`, `border`, `purple`, `red`. |
| `warnings.light`, `warnings.dark` | array of strings | For each editable token but `bg` and `bg2` whose contrast against `bg` is below 4.5:1 (WCAG), in that order: `<token>: contrast <ratio>:1 against bg (below 4.5:1)`. `[]` when none. |

### `dashboard.toml`

`schema = 1` (D39), then one table per mode with its preset and only the
tokens changed from it (railswitch writes it; a token equal to the
preset's value is dropped). A missing file, or a missing table, is
`flexoki` with no changes. An unknown preset or token, a fixed token, or a
value that isn't `#rrggbb` is refused. docs/dashboard.md describes the
dashboard as a whole.

```toml
schema = 1

[colors.light]
base = "flexoki"
accent = "#BC5215"

[colors.dark]
base = "flexoki"
```
