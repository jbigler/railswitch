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
| `steps[].default_changed` | boolean | The file carries an eject header and the default it was ejected from (as the project's `stack.lock` accepts it) has changed since: compare it with `containerfile show`, reset it with `containerfile eject --force`. |
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
local packages (`packs/<name>/`, `options/<name>/`) as `local:<name>@N`,
then the packages of every library in `libraries.toml` whose verified
checkout still resolves (its files hash to the signed index) as
`<alias>:<name>@N`. Each list is sorted by ref.

```json
{"packs": [{"ref": "rails@1", "name": "rails"}],
 "options": [{"ref": "postgres@1", "name": "postgres", "slot": "database", "works_with": ["*"]}],
 "agents": [{"ref": "claude@1", "name": "claude", "default": true}]}
```

| Field | Type | Meaning |
|---|---|---|
| `packs[].ref`, `options[].ref` | string | `name@N`, `local:name@N` or `<alias>:name@N`. |
| `library` (any entry) | string | The alias of the library it comes from; absent for built-in and local packages. |
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
  {"name": "github_token", "scope": "project", "podman_secret": "filial_github_token", "delivery": "file", "driver": "file", "set": false, "created": null, "services": ["nvim"], "driver_differs": false, "git_signing": false, "up": true},
  {"name": "twilio_auth_token", "scope": "project", "podman_secret": "filial_twilio_auth_token", "delivery": "env TWILIO_AUTH_TOKEN", "driver": "file", "set": true, "created": "2026-10-25T09:08:07Z", "services": ["rails"], "driver_differs": false, "git_signing": false, "up": true}
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
| `up` | boolean | `up` refuses to start while it is unset: a service `up` starts receives it. `false` for a secret only agents receive, which is checked when they start (`up` then starts the worktree without its default agent, with a warning); `true` for every secret when the services can't be told. |
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
| `libraries[].revoked[]` | array | Fingerprints never trusted again for this library: `fingerprint`. Added by `signer revoke` or a trusted index's `[[revoke]]`; `signer add`, `library trust` and rotations refuse them. |

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

## `library ls`

The library repositories on the machine (D103), in `libraries.toml`
order, each with its last fetch. `libraries` is `[]` when there are none.

```json
{
  "libraries": [
    {
      "alias": "acme",
      "url": "https://github.com/acme/stack-library",
      "branch": "",
      "id": "github.com/acme/stack-library",
      "status": "rotation",
      "serial": 7,
      "commit": "4f1c2a9e0b7d6c5a4b3e2f1d0c9b8a7f6e5d4c3b",
      "fetched": "2026-10-29T14:03:11Z",
      "packages": 3,
      "pending": {
        "fingerprint": "SHA256:Ays0teoPOjVM/BphiuEfsQ7gCq7VhFUQd9F9RRAZJXU",
        "new_fingerprint": "SHA256:FMVaaJrORVs5Xx60M485oK5AtUTpvWqQapBKMLidPeA",
        "status": "rotation",
        "known_library": true
      }
    }
  ]
}
```

| Field | Type | Meaning |
|---|---|---|
| `alias` | string | The ref prefix: `<alias>:<name>@N`. |
| `url`, `branch` | string | As added; `branch` `""` is the remote's default. |
| `id` | string | The library id of the first verified index; `""` until a fetch is trusted. |
| `status` | string | The last fetch's verdict (a `library verify` status); `""` when it was never fetched. |
| `serial`, `commit` | number, string | The current copy's index serial and git commit; `0` and `""` when there is no current copy. |
| `fetched` | string | When the last fetch reached its verdict, RFC 3339 UTC; `""` when never. |
| `packages` | number | The packages the current copy's index lists; `0` without one. |
| `pending` | object or null | A fetch waiting for `library trust <alias>`: `status` `unknown-signer` or `rotation`; `fingerprint` the signer (for a rotation, the trusted key that vouches for the new one); `new_fingerprint` a rotation's new key, else `""`; `known_library` as in `library verify` (trust needs `--new-signer`). `null` when none waits. |

## `template ls`

The templates a new project can start from (D109, docs/templates.md):
this machine's store, then each library's in `libraries.toml` order. A
template that doesn't read is left out (its reason on stderr).
`templates` is `[]` when there are none.

```json
{
  "templates": [
    {
      "ref": "acme:rails-pg@3",
      "name": "rails-pg",
      "version": 3,
      "description": "Rails with PostgreSQL and Redis",
      "source": "library acme",
      "packages": ["rails@1", "postgres@1", "acme:redis@1"]
    }
  ]
}
```

| Field | Type | Meaning |
|---|---|---|
| `ref` | string | `<name>@N` on this machine, `<alias>:<name>@N` from a library. |
| `name`, `version` | string, number | From its `stack.toml`. |
| `description` | string | One line; `""` for none. |
| `source` | string | `machine` or `library <alias>`. |
| `packages` | array of strings | The package refs its lines use: the pack, each option (slot order, then extra services; `none` left out), each agent's. |

## `template show <ref>`

One template, as `template ls` resolves it.

```json
{
  "ref": "rails-pg@1",
  "sha256": "3f0c…",
  "stack_toml": "schema = 1\nname = \"rails-pg\"\n…",
  "ask": [{"key": "options.cache", "prompt": "Cache", "default": "redis@1"}],
  "secrets": ["rails_master_key"],
  "containerfiles": ["pg-client"],
  "readme": "",
  "files": [{"path": "stack.toml", "text": "…"}]
}
```

| Field | Type | Meaning |
|---|---|---|
| `sha256` | string | The template's content hash (`pack.ContentHash`, lowercase hex, no prefix), as `[template] sha256` records it. |
| `stack_toml` | string | Its `stack.toml`. |
| `ask` | array | Its questions in order: `key` (`options.<key>`, `versions.<name>` or `env.<NAME>`), `prompt`, `default`. |
| `secrets` | array of strings | The secrets it declares (names; never a value). |
| `containerfiles` | array of strings | The `<name>` of each `containerfiles/Containerfile.<name>` it holds, sorted. |
| `readme` | string | Its `README.md`; `""` for none. |
| `files` | array | Every file, by path: `path`, `text`. |

## `template check`

The project's template (-C) against what `[template]` records (D111).
A project with no `[template]` is refused (exit 1). Exit 0 otherwise,
whether or not the template changed. Nothing is applied: `template check
--accept <current>` only writes `current` to `[template] sha256` (and
stores that content as the next diff's base); another hash is refused.

```json
{
  "ref": "acme:rails-pg@1",
  "sha256": "df728e1a…",
  "current": "1b924e2a…",
  "update": true,
  "error": "",
  "files": [
    {"path": "containerfiles/Containerfile.pg-client", "status": "changed", "diff": "--- a/…", "flags": [{"check": "runs-here", "line": 40, "text": "+ RUN curl … | sh"}]}
  ]
}
```

| Field | Type | Meaning |
|---|---|---|
| `ref`, `sha256` | string | As `[template]` records them. |
| `current` | string | The content hash `ref` resolves to now; `""` when it doesn't resolve. |
| `update` | boolean | `current` is not `""` and differs from `sha256`. |
| `error` | string | Why `ref` doesn't resolve (gone from the store or the library, or no longer matching its signed index); `""` when it does. |
| `files` | array | With `update`: every file that differs from the stored accepted copy to the current one, as in [`lock --review`](#lock---review) (`path`, `status`, `diff`, `flags`); a missing accepted copy shows every file `added`. `[]` without an update. |

## `template import <dir>`

The review of a template folder (D113, docs/templates.md): the
[`lock --review`](#lock---review) document with one package, the
template, plus its status. Nothing is saved. Exit 0, except `older`
(exit 1, the document printed too).

```json
{
  "ref": "rails-stack@2",
  "sha256": "1b924e2a…",
  "status": "update",
  "replaces": {"ref": "rails-stack@1", "sha256": "df728e1a…"},
  "hash": "1b924e2a…",
  "flags": 1,
  "packages": [
    {"ref": "rails-stack@2", "kind": "template", "status": "changed", "from": "df728e1a…", "to": "1b924e2a…", "also_locked_in": [],
     "files": [{"path": "stack.toml", "status": "changed", "diff": "--- a/…", "flags": [{"check": "firewall", "line": 0, "text": "firewall.mode: (none) -> off"}]}]}
  ],
  "restart": [],
  "warnings": []
}
```

| Field | Type | Meaning |
|---|---|---|
| `ref` | string | `<name>@<version>`, from its `stack.toml`. |
| `sha256`, `hash` | string | Its content hash (lowercase hex, no prefix): what `--accept` takes. |
| `status` | string | `new` (no template of its name here), `update` (a higher version than the stored one), `same` (the stored one, byte for byte), `older` (another content at a version not above the stored one: refused). |
| `replaces` | object or null | The stored template of its name: `ref`, `sha256`; `null` for `new`. |
| `flags` | number | The flags over every file. |
| `packages` | array | One entry, kind `template`: status `added` (new), `changed` or `unchanged` (`same`, no files); `files` as in `lock --review`. |
| `restart` | array | Always `[]`. |
| `warnings` | array of strings | Each library ref whose alias this machine hasn't added. |
| `bundles_compared_with` | object | By bundled package directory (`packs/rails`), the built-in that package is shown against when the stored version doesn't have it (its closest: the most identical files, a manifest's name line aside); `{}` for none. |

## `pack import <dir>`

The review of a package folder in the workspace (D114, docs/packs.md):
the [`lock --review`](#lock---review) document with one package, plus
its status. Nothing is written. Exit 0; a folder it refuses exits 1 with
no document.

```json
{
  "ref": "local:filial-rails@1",
  "kind": "pack",
  "sha256": "1b924e2a…",
  "status": "update",
  "replaces": {"ref": "local:filial-rails@1", "sha256": "df728e1a…"},
  "compared_with": "",
  "used": true,
  "hash": "1b924e2a…",
  "flags": 1,
  "packages": [{"ref": "local:filial-rails@1", "kind": "pack", "status": "changed", "from": "df728e1a…", "to": "1b924e2a…", "also_locked_in": [], "files": []}],
  "restart": ["main"],
  "warnings": []
}
```

| Field | Type | Meaning |
|---|---|---|
| `ref` | string | `local:<name>@<version>`: `--as` or the manifest's name, the manifest's version. |
| `kind` | string | `pack`, `option` or `agent`, from the folder's manifest. |
| `sha256`, `hash` | string | The content hash of what `--accept` writes (the manifest's name line set): what `--accept` takes. |
| `status` | string | `new` (no local package of that name), `update` (other content), `same` (byte for byte). |
| `replaces` | object or null | The local package there now: `ref` (its manifest's version), `sha256`; `null` for `new`. |
| `compared_with` | string | For `new`: the built-in its files are shown against (the one of its kind with the most identical files, a manifest's name line aside); `""` for none, or when it replaces a local package. |
| `used` | boolean | The project's stack names `ref`. |
| `flags` | number | The flags over every file. |
| `packages` | array | One entry, as in `lock --review`: status `added`, `changed` or `unchanged`. |
| `restart` | array of strings | For a used package, the running worktrees the write restarts; else `[]`. |
| `warnings` | array of strings | Always `[]` for now. |

## `gc`

What `railswitch gc` removed (or, with `--dry-run`, would remove), D120.
Exit 0; a failed removal is a warning.

```json
{"dry_run": true,
 "images": [{"name": "localhost/shop/base:ruby3.4.4-node22.11.0-3a626f74", "size": 1390000000}],
 "unsure": [{"name": "localhost/old/base:ruby3.4.4-node22.11.0-fc430f4a", "size": 888000000}],
 "store": ["df728e1a…"], "volumes": ["shop-feature-x-db-data"], "bytes": 1390000000, "warnings": []}
```

| Field | Type | Meaning |
|---|---|---|
| `images` | array | Images of a registered project (or carrying `railswitch.prefix=<an unregistered prefix>`) that no worktree's current render builds and no container uses: `name`, `size` (bytes). |
| `unsure` | array | Images shaped like railswitch's (`localhost/<prefix>/<repo>:<version>-<hash8>`) of no registered project and without the build label: never removed; remove each with `podman rmi <name>`. |
| `store` | array | Stored package copies (D96) no `stack.lock` (its tables and `[[removed]]`) or `[template] sha256` of a registered project names, and not changed in the last 10 minutes. `[]` when a project doesn't load. |
| `volumes` | array | Volumes of a registered project whose name a railswitch package declares (`<prefix>_<cache or shared name>`, `<prefix>-<slug>-<volume name>`, placeholders as wildcards) and no worktree's render mounts. Removed only with `--volumes`, after a prompt naming each one (or `--yes`), asked before anything else is removed: a refusal removes nothing (exit 1 without a terminal). |
| `bytes` | number | The removed images' size, layers they share with kept images included. |
| `warnings` | array | A project that doesn't load (nothing of its prefix is removed), a removal that failed. |

## `tidy`

What `railswitch tidy [--all] [--days <n>]` suggests removing (D160):
the project's (`--all`: every registered project's) worktrees and images.
It removes nothing; `tidy --rm [--force] <id>...` does (text output only).

```json
{"days": 14, "bytes": 1390004096,
 "items": [
  {"id": "wt:shop/feature-x", "kind": "worktree", "project": "shop", "name": "feature-x", "branch": "feature/x",
   "reasons": ["pr-merged", "stopped"], "pr": 42, "stack": "stopped",
   "idle_since": "2026-10-01T09:00:00Z", "idle_from": "systemd", "dirty": false, "bytes": 4096, "partial": false},
  {"id": "image:localhost/shop/base:ruby3.4.4-node22.11.0-3a626f74", "kind": "image", "project": "shop",
   "name": "localhost/shop/base:ruby3.4.4-node22.11.0-3a626f74", "branch": "", "reasons": ["unused"], "pr": 0,
   "stack": "", "idle_since": "", "idle_from": "", "dirty": false, "bytes": 1390000000, "partial": false}],
 "warnings": []}
```

| Field | Type | Meaning |
|---|---|---|
| `days` | number | `--days`: a stopped stack idle longer is a candidate (default 14). |
| `items[].id` | string | What `tidy --rm` takes: `wt:<prefix>/<slug>` or `image:<name>`. |
| `items[].kind` | string | `worktree` or `image`. |
| `items[].reasons` | array | A worktree: `pr-merged`, `pr-closed` (its branch's PR as `pr ls` picks it), `stopped` (the stack is stopped and idle over `days`). An image: `unused` (no worktree's current render builds it and no container uses it: `gc`'s judgement). The base worktree is never listed. |
| `items[].pr` | number | The merged or closed PR's number; `0` for none. |
| `items[].stack` | string | The worktree's stack state, as `overview` has it; `""` for an image. |
| `items[].idle_since` | string | For a stopped stack, RFC 3339 UTC: the latest time one of its units stopped, as systemd remembers it (`idle_from` `systemd`; forgotten at reboot), else HEAD's commit time (`commit`); `""` when neither is known (never `stopped` then). |
| `items[].dirty` | boolean | Uncommitted changes or untracked files: `tidy --rm` keeps it unless `--force`. |
| `items[].bytes` | number | A worktree: its checkout's regular files (apparent size, symlinks not followed). An image: podman's size, shared layers included. |
| `items[].partial` | boolean | The checkout walk stopped after 200,000 entries: `bytes` is a lower bound. |
| `bytes` | number | The items' bytes summed. |
| `warnings` | array | No PR states (gh missing or failing, a repo not on GitHub: no `pr-*` reasons), a project that doesn't load (`--all`), `gc`'s warnings. |

`tidy --rm <id>...` lists again first and removes only the ids still
listed: worktrees first, by `wt rm`'s path (its checks, exact names), then
images by `podman rmi <name>`. An id no longer listed, or a dirty worktree
without `--force`, is refused and kept; the others still go; exit 1 when
any was refused or failed. `--force` applies only to the dirty worktrees
named: they go as `wt rm --force` takes them.

## `fw show [service]`

The firewall of the worktree's containers (the workspace root: every
worktree's), docs/firewall.md. Exit 1 when `firewall.sh` refuses.

```json
{"containers": [
  {"name": "masto-main-rails", "firewall": "firewalled", "profiles": ["app"], "mode": "log",
   "rejected": [{"addr": "104.16.169.120", "proto": "tcp", "port": "443", "host": ""}]}
]}
```

| Field | Type | Meaning |
|---|---|---|
| `name` | string | The container. |
| `firewall` | string | `firewalled`; `self` (its own in-container firewall, the browser); `none` (no hook ran: `doctor`, then restart it). |
| `profiles` | array | Its profiles (`app`, `deps`, `agent-<name>`, `none`, …); `[]` unless firewalled. |
| `mode` | string | `enforce`, `log` or `off`; `""` unless firewalled. |
| `rejected` | array | What it rejected (enforce) or would have (log) since it started: `addr`, `proto`, `port`, `host` (the name the address resolves back to, `""` for none). |

## `wt ls`

The project's registered worktrees, by ID; the base worktree is first (ID 1
on a machine with one project). It is an array with one object per worktree.

```json
[
  {"slug": "master", "id": 1, "branch": "master", "dir": "/home/u/src/filial/worktrees/master", "dirty": false, "share": null},
  {"slug": "feat", "id": 2, "branch": "feat", "dir": "/home/u/src/filial/worktrees/feat", "dirty": true, "share": {"host": "box.tail1234.ts.net", "ip": "100.64.0.7"}}
]
```

| Field | Type | Meaning |
|---|---|---|
| `slug` | string | The worktree's name: its directory under the workspace and the `<slug>` in its object names. |
| `id` | number | Its machine-wide ID; its host ports are derived from it. |
| `branch` | string | The branch checked out in it; `""` when detached, or when its directory is missing or not a git worktree. |
| `dir` | string | Its directory, `<workspace>/worktrees/<slug>` (D143). |
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

## `stats [--all]`

Each service's resource use against its limits (D161): the current
worktree's (`-w <slug>`, or the worktree the working directory is in), or
with `--all` every worktree's, in registry then model order. One `podman
stats --no-stream` samples the running containers; a service whose
container isn't running has `running` false and zero figures.

```json
[
  {"worktree": "feat", "service": "rails", "role": "app", "container": "demo-feat-rails", "running": true,
   "cpu_percent": 3.5, "mem_used": 3221000000, "mem_limit": 4295000000, "mem_percent": 75.0, "pids": 12, "pids_limit": 4096},
  {"worktree": "feat", "service": "deps", "role": "deps", "container": "demo-feat-deps", "running": false,
   "cpu_percent": 0, "mem_used": 0, "mem_limit": 0, "mem_percent": 0, "pids": 0, "pids_limit": 4096}
]
```

| Field | Type | Meaning |
|---|---|---|
| `worktree` | string | The worktree's slug. |
| `service` | string | Service id, e.g. `rails`, `db`. |
| `role` | string | As in `status`. |
| `container` | string | Its container's exact name. |
| `running` | boolean | podman sampled it: its container runs. |
| `cpu_percent` | number | podman's CPU percentage for the sample (100 is one core); 0 when podman printed none. |
| `mem_used` | number | Memory in use, in bytes, as podman prints it (4 significant figures). |
| `mem_limit` | number | The container's memory limit in bytes, as podman reports it: the unit's `Memory=` (D34). |
| `mem_percent` | number | `mem_used` of `mem_limit`, in percent, podman's figure (else computed). |
| `pids` | number | Processes and threads in the container; 0 when podman printed none. |
| `pids_limit` | number | The unit's `PidsLimit=` (D34), from the model; known while it is stopped too. |

## `agent ls`

The project's agents (`project.toml [agents]`), sorted by name, with each
one's status in the current worktree (`-w <slug>`, or the worktree the
working directory is in). It is an array with one object per agent. The
status comes from the agent's status file
`<workspace>/.railswitch/status/<slug>/<name>.json` (D58), which the agent's
hook writes.

```json
[
  {"name": "claude", "package": "claude@1", "run": "on-demand", "state": "done", "message": "Ship it?", "ts": 1792919287, "reply": true, "running": true, "config": true, "default": {"from": "main", "promoted": "2026-10-30T12:00:00Z"}, "default_pending": false, "usage": {"input": 1840, "output": 52300, "cache_read": 1250000, "cache_write": 88000}},
  {"name": "probe", "package": "local:probe@1", "run": "service", "state": "off", "message": "", "ts": 0, "reply": false, "running": false, "config": false, "default": null, "default_pending": false, "usage": null}
]
```

| Field | Type | Meaning |
|---|---|---|
| `name` | string | The agent's name in `[agents]`; its service is `agent-<name>`. |
| `package` | string | Its package ref: `<name>@<N>` for a built-in package, `local:<name>@<N>` for one under `<workspace>/agents/`. |
| `run` | string | `on-demand` (a transient container per `railswitch agent` run) or `service` (a unit whose container holds the agent's tmux session, D142: `up` starts it for the default agent, `railswitch agent` for any; `railswitch agent` joins or starts the session; it runs until `agent stop`, `stop`/`down` or `wt rm`). |
| `state` | string | The status file's `state`: `permission`, `question`, `working`, `done`, `idle` or `off`. `off` also when the file is missing, not a regular file (a symlink, a FIFO), larger than 64 KiB, not valid JSON, has `v` other than `1` or an unknown state. |
| `message` | string | The status file's `message`; `""` when `state` is `off` for one of the reasons above. |
| `ts` | number | The status file's `ts` (Unix seconds, as the hook wrote it); `0` when unusable. |
| `reply` | boolean | The agent waits for a reply: `state` is `done` and `message` ends in `?`, maybe followed by spaces, closing brackets, quotes or `*`/`_`/`` ` `` emphasis. The text table shows `reply` as its `STATE`. |
| `running` | boolean | The agent's container runs in this worktree (podman, not the status file); for a service agent also while no agent runs in its session (`up` started it, D142). At most one agent runs per worktree (D62). The text table marks it with `*` in its `RUNNING` column, and shows `running` as its `STATE` when the status file says `off`. |
| `config` | boolean | Its package has `[config] paths`, so it can have a project default (`agent default`, D135). |
| `default` | object or null | Where the project's default for it came from: `from` (the worktree promoted) and `promoted` (RFC 3339); `null` without one. |
| `default_pending` | boolean | Its home in this worktree holds the request to take the default (a new home, or `agent default apply`): its entrypoint applies it at the agent's next start. |
| `usage` | object or null | The status file's `usage`: the tokens of the agent's current (or last) session, as its package's hook counts them: `input`, `output`, `cache_read`, `cache_write` (integers), and `cost` (a number, US dollars) only when the package reports one (D162). `null` when the file has none, or one with a count below 0 or above 10^13, or a cost below 0 or above 10^6. claude@1 counts them from the session's transcript when a turn ends or a session starts; it reports no cost. |

## `agent usage [--days N] [--all]`

The agents' token history (D162): per local day, worktree and agent, the
growth of each agent's session counter (`agent ls`'s `usage`). Without
`--all` the current worktree's (`-w <slug>`), with `--all` every
worktree's of the project. `--days` (1 to 90, default 14) is the window,
today included.

The history is recorded on the host by the commands that read the status
files: every `overview` (the dashboard polls it every 2 to 10 s for every
project) and every `agent usage`, which records before it reports. Each
reading adds the growth since the last one to the local day of the status
file's `ts` (today's when `ts` is 0, in the future or past the 90 days
kept). A count that drops (or a cost that drops) is a new session: all of
its counter is new, nothing is subtracted. A session that ends between
two readings loses its growth since the last one, and a new session that
already passed the last one's counts when first read is taken as growth.
Each sum is capped at 10^15.

The file is `~/.local/share/railswitch/usage/<prefix>.json` (0600, its
directory 0700, written atomically under `<prefix>.json.lock`): one JSON
object, `{"schema": 1, "last": {"<worktree>/<agent>": {...}}, "days":
[<row>...]}`, `last` each counter as last read. Days older than 90 are
dropped. A file with a `schema` above 1 is refused as written by a newer
railswitch (D39); `overview` then records nothing but still answers.

```json
{"today": "2026-11-04", "days": 2,
 "daily": [{"day": "2026-11-03", "total": 2100000, "input": 100000, "output": 0, "cache_read": 2000000, "cache_write": 0, "cost": null},
           {"day": "2026-11-04", "total": 9300000, "input": 300000, "output": 40000, "cache_read": 8900000, "cache_write": 60000, "cost": null}],
 "rows": [{"day": "2026-11-03", "worktree": "feat", "agent": "claude", "input": 100000, "output": 0, "cache_read": 2000000, "cache_write": 0, "cost": null},
          {"day": "2026-11-04", "worktree": "feat", "agent": "claude", "input": 300000, "output": 40000, "cache_read": 8900000, "cache_write": 60000, "cost": null}],
 "worktrees": [{"worktree": "feat", "total": 11400000, "input": 400000, "output": 40000, "cache_read": 10900000, "cache_write": 60000, "cost": null, "today": 9300000, "median": 2100000, "runaway": true}]}
```

| Field | Type | Meaning |
|---|---|---|
| `today` | string | Today, `YYYY-MM-DD` in local time. |
| `days` | number | The window, `--days`. |
| `daily` | array | Every day of the window, oldest first, summed over the worktrees (and agents) reported; days without usage are zeros. Never null. |
| `rows` | array | The window's days with usage, per worktree and agent: oldest first, then by worktree, then agent. Never null. |
| `worktrees` | array | Per worktree with usage in the window, by name: its sums over the window. Never null. |
| `input`, `output`, `cache_read`, `cache_write` | number | Tokens. |
| `total` | number | The four summed (on `daily` and `worktrees`; `rows` have the four only). |
| `cost` | number or null | US dollars, summed over the readings whose package reported a cost; `null` when none did. railswitch never prices tokens. |
| `today` (worktree) | number | Its total today. |
| `median` | number | The median total of its days with usage among the 14 before today (whatever `--days`). |
| `runaway` | boolean | Today is far above its usual day: at least 3 days with usage among those 14, today at least 1,000,000 tokens and over 3 times `median`. The text form prints `runaway: <worktree> ...` under the table. |

The text form is a table `DAY WORKTREE AGENT TOTAL INPUT OUTPUT CACHE
READ CACHE WRITE COST` of `rows` (`-` for no cost).

## `agent screen [<name>]`

A service agent's screen in the current worktree, as it shows now (D144):
`podman exec <ctr> tmux -L agent capture-pane -p -J -t =agent:`, the
visible pane of the agent's session with wrapped lines joined. Each line
has its control characters made spaces and its trailing spaces cut;
trailing blank lines are cut. The name defaults as for `railswitch agent`.
An on-demand agent is refused (exit 1). The text form prints the lines.

```json
{"agent": "claude", "running": true, "session": true, "lines": ["> fix the failing spec", "", "● Reading spec/models/user_spec.rb"]}
```

| Field | Type | Meaning |
|---|---|---|
| `agent` | string | The agent's name. |
| `running` | boolean | Its container runs in this worktree. `false`: `session` is `false` and `lines` empty, exit 0. |
| `session` | boolean | Its session server holds the agent's session. `false` (the container runs, no agent in it): `lines` empty, exit 0. |
| `lines` | array of strings | The screen, top to bottom; never null. |

## `agent queue ls`

The current worktree's agent work queue (D159), in order: the prompts
`agent queue add` queued (or `pr agent --queue`), which `agent queue next`
types into the worktree's default agent one at a time, as it goes idle.
Kept host-side in `<workspace>/.railswitch/queue/<slug>/queue.json`
(0600, no container mounts it; `wt rm` removes it); at most 20 items of at
most 4096 bytes each, checked as `agent send` checks its text. The text
form is a table `ID TEXT` with each item's first line (control characters
as spaces).

```json
{"slug": "feat-login", "delivered": 1793115142, "items": [{"id": 3, "text": "Fix the failing spec\nthen run the linter", "added": 1793115100}]}
```

| Field | Type | Meaning |
|---|---|---|
| `slug` | string | The worktree. |
| `delivered` | number | When the last delivery started (Unix seconds), `0` for none. `next` waits for an agent status from a later second, so one status never takes two items. |
| `items` | array | Oldest first; never null. |
| `items[].id` | number | The item's id in this queue, never reused; `agent queue rm <id>` removes it. |
| `items[].text` | string | What is typed, as given (UTF-8, line breaks and tabs only). |
| `items[].added` | number | When it was queued (Unix seconds). |

`agent queue next` types the first item when the default agent runs, its
status is `idle` or `done` without a question to the user (`reply`), at
least 5 s old and not older than `delivered`; it prints `held: <why>` and
exits 0 otherwise, and when another delivery of the worktree runs.
Typing goes through `agent prompt` (D152), whose refusals apply; the item
is removed only once typed, so a failed delivery keeps it (exit 1).

## `issue ls`

One project (`-C`): its open GitHub issues (D144), from `gh issue list
--repo <owner/name> --state open --limit 100 --json
number,title,url,labels,assignees,updatedAt,author` on the host with
`GH_PROMPT_DISABLED=1`, in `/`, 30 s timeout: the 100 newest, in gh's
order (newest created first). The repo comes from `project.toml` as for
[`pr ls`](#pr-ls); one not on GitHub fails with "issue ls: <url> is not a
GitHub repo". The text form is a table `ISSUE AUTHOR ASSIGNEES LABELS
TITLE` (control characters as spaces).

```json
{"me": "ann", "issues": [{"number": 42, "title": "Login times out", "url": "https://github.com/o/n/issues/42", "labels": ["bug"], "assignees": ["ann"], "author": "bob", "updated": "2026-10-31T19:00:32Z"}]}
```

| Field | Type | Meaning |
|---|---|---|
| `me` | string | The login gh signs in as (`gh api user`), for "assigned to me"; `""` with a warning on stderr when gh can't tell. |
| `issues` | array | Never null. |
| `number`, `title`, `url` | number, string, string | As GitHub has them. |
| `labels` | array of strings | Label names; never null. |
| `assignees` | array of strings | Logins; never null. |
| `author` | string | The author's login. |
| `updated` | string | `updatedAt`, RFC 3339. |

## `whiteboard show`

A worktree's office whiteboard (D151), in
`<workspace>/.railswitch/whiteboard/<slug>/` (`board.excalidraw`, `board.png`,
0600; read with `safefile.Read`, at most 12 MiB and 4 MiB). Run it in the
worktree or with `-w <slug>`. `wt rm` removes the folder.

```json
{"slug": "feat", "hash": "sha256:4be1…", "scene": {"type": "excalidraw", "version": 2, "elements": […], "appState": {…}, "files": {…}}, "png": "iVBORw0KGgo…"}
```

| Field | Type | Meaning |
|---|---|---|
| `slug` | string | The worktree. |
| `hash` | string | `sha256:<hex>` of the scene file: what `whiteboard write` and `whiteboard rm` take as `--if-hash`; `""` without a board. |
| `scene` | object or null | The Excalidraw scene as saved (pasted pictures in its `files`); null without a board. |
| `png` | string | The board's picture, base64 PNG, as the office shows it; `""` for none. |

`whiteboard write --if-hash <h>` reads `{"scene": <Excalidraw scene>, "png":
"<base64>"}` on stdin and saves both when the board still has hash `h`
(`""`: there is no board yet). The scene must say `"type": "excalidraw"`
and have an `elements` array (at most 12 MiB); the picture must be a PNG
of at most 4 MiB and 4096 px a side, or `""`. The picture is written
first, then the scene, each through a temp file, under a lock.
`whiteboard rm --if-hash <h>` clears the board. A stale hash or a refused
document exits 1 and changes nothing. With `--json` both print `{"hash":
…}`, the next edit's `--if-hash` (`""` after `rm`).

## `office set check <dir>`

A Virtual Office set's folder checked against the piece list (D153,
`internal/web/static/office/kit/pieces.json`, written by
`tools/office-kit/pieces.py`); nothing is written. Exit 0 only when
`errors` is empty and no piece is `refused`.

```json
{"name": "cabin", "version": 3, "pieces": 1, "look": {"background": "#1b1612", "bloom": 0},
 "results": [{"piece": "Deco_Mug", "status": "missing"},
             {"piece": "Furn_Desk", "status": "refused", "reason": "Furn_Desk: x -1.03..1.00, y 0.00..0.75, z -0.40..0.40 m; the list says x -1.00..1.00, y 0.00..0.75, z -0.40..0.40 (±0.02)", "min": [-1.03, 0, -0.4], "max": [1, 0.75, 0.4]}],
 "errors": []}
```

| Field | Type | Meaning |
|---|---|---|
| `name`, `version`, `pieces`, `look` | | From `set.toml` (empty when it is missing or refused): the set's name, its own version, the piece list version it was built for, its look (each key only when set). |
| `results[]` | array | One per piece of the list, plus one per `.gltf` the list doesn't have, by name. `status` is `ok`, `missing` (drawn as a box) or `refused` with a `reason`; `min` and `max` are the model's box (colliders left out) when it was read. |
| `errors[]` | array of strings | Problems with the folder or `set.toml`: a symlink, a special file, more than 1,000 files, a file over 16 MiB or 256 MiB in all, a stray file, a glTF URI with a scheme (`data:` included), absolute or leaving the folder, a `pieces` other than the list's version. A piece is also refused when its glTF requires Draco, Meshopt or KTX2 (`extensionsRequired`): the page sets no decoder for them. |

`office set add <dir>` checks the folder, then copies the files the check
accepted to `<data>/office/<name>/` (0700/0600) with an `index.json`
(`{"files": {<path>: {"sha256", "size", "mtime"}}, "pieces": [...], "added": <time>}`) and
checks the copy again; a refusal, or a built-in name, adds nothing.
Re-adding a name replaces that set. `office set use <name>` writes
`dashboard.toml` `[office] set` (refused for an unknown, stale or broken
set; the built-in default is stored as no key); `office set rm <name>`
removes an installed set by exact path and, when it was active, the key.

## `office set ls`

The built-in sets, then the installed ones, by name.

```json
{"active": "cyberpunk", "sets": [
 {"name": "cyberpunk", "source": "builtin", "version": 1, "pieces": 1, "present": 109, "missing": 0, "status": "ok", "description": "Neon, worn metal and a city in the smog", "look": {…}, "have": ["Arch_CeilingDuct_4m", …]},
 {"name": "cabin", "source": "installed", "version": 3, "pieces": 1, "present": 12, "missing": 97, "status": "broken", "reason": "Furn_Desk.bin: changed since it was added", "description": "Pine, wool and lamplight", "look": {…}, "have": ["Furn_Desk", …]}]}
```

| Field | Type | Meaning |
|---|---|---|
| `active` | string | `dashboard.toml` `[office] set`, else `cyberpunk`. It may name a set that is no longer listed (removed by hand). |
| `source` | string | `builtin` (in the dashboard's static files) or `installed` (`<data>/office/<name>/`). |
| `version`, `pieces`, `description`, `look` | | From `set.toml`. |
| `present`, `missing` | integer | Pieces of the list the set has, and doesn't. |
| `status`, `reason` | string | `ok`; `stale`: built for another piece list version; `broken`: an installed set whose `index.json` or `set.toml` doesn't verify, or a file it lists is missing, not a regular file, or has another size or modification time than when it was added (a stat per file: the dashboard runs `ls` for every office page, so it hashes only `set.toml`). |
| `have` | array of strings | The pieces of the list the set has, which the page loads (the rest are boxes). |

## `office tv`

The sources the lounge's big screen offers (D168), each `null` when not
set up. The API key itself is never printed.

```json
{"jellyfin": {"url": "https://jellyfin.example.lan", "playlist": "Lounge", "user": "sam", "key_set": true}}
```

| Field | Type | Meaning |
|---|---|---|
| `jellyfin.url` | string | `dashboard.toml` `[office.tv.jellyfin] url`: `http(s)://host[:port][/path]`. |
| `jellyfin.playlist`, `jellyfin.user` | string | The playlist's exact name, and its owner (`""`: none). |
| `jellyfin.key_set` | boolean | Whether `office tv jellyfin key` has stored a key. |

## `notes ls`

The workspace's shared notes (D141, D144): each regular file in
`<workspace>/notes/` whose name matches `^[A-Za-z0-9][A-Za-z0-9._-]{0,99}\.md$`,
newest first. A symlink, directory or other name is left out. No `notes/`
folder lists none. No worktree is needed.

```json
{"notes": [{"name": "run_db_prepare_after_a_schema_change.md", "size": 412, "modified": "2026-10-31T19:00:32Z"}]}
```

| Field | Type | Meaning |
|---|---|---|
| `notes` | array | Never null. |
| `name` | string | The file name. |
| `size` | number | Bytes. |
| `modified` | string | Its modification time, RFC 3339 UTC. |

## `notes show <name>`

One note, read as a hostile file (`safefile.Read`: a regular file, not a
symlink, at most 1 MiB). The text form prints the note with control
characters other than line breaks and tabs as spaces.

```json
{"name": "run_db_prepare_after_a_schema_change.md", "text": "Run bin/rails db:prepare …\n", "hash": "sha256:9f2c…"}
```

| Field | Type | Meaning |
|---|---|---|
| `name` | string | The file name. |
| `text` | string | Its content. |
| `hash` | string | `sha256:<hex>` of the bytes: what `notes write` and `notes rm` take as `--if-hash`. |

`notes write <name> --if-hash <h>` replaces the note with stdin (at most
1 MiB, UTF-8) when it still has hash `h`; `--if-hash ""` creates it and
refuses an existing one. The file is written 0644 through a temp file
renamed over the name; `notes/` is created 0700 when missing. `notes rm
<name> --if-hash <h>` removes it at that hash. A stale hash, a missing
note or a symlink in its place exits 1 and changes nothing. With `--json`,
both print `{"hash": …}`: the note's new hash, the one the next edit passes
as `--if-hash` (`""` after `rm`).

## `overview`

One project (`-C`): every registered worktree, in registry order (by ID),
with its stack state, services, agent and links. The dashboard polls it
every 2 s, so it is cheap: one `systemctl --user show` for every unit of
the project, exactly one `podman ps -a --format json` (running
on-demand services and agents, and each running container's unit-hash
label), no health check and no git call (each worktree's HEAD commit, for `tests.stale`, is read from its git files). The text form is a table `WORKTREE BRANCH STACK AGENT TESTS` (the base
worktree marked `(base)`) with each worktree's links indented under its
row.

```json
{
  "prefix": "filial",
  "workspace": "/home/u/workspace/filial",
  "review_pending": false,
  "signer_warnings": [{"ref": "acme:django@1", "library": "github.com/acme/stack-library", "fingerprint": "SHA256:…"}],
  "worktrees": [
    {
      "slug": "feat-login", "id": 2, "branch": "jb/feat-login", "base": false,
      "stack": "up", "stale": false, "queue": [],
      "tests": {"state": "failed", "stale": false, "last": {"exit_code": 1, "started_at": "2026-11-04T10:02:11Z", "ended_at": "2026-11-04T10:05:40Z", "commit": "9f2c…"}},
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
| `signer_warnings` | array | One `{ref, library, fingerprint}` per library package the project's `stack.lock` pins whose lock `signer` is no longer trusted for its library in `signers.toml` (distrusted with `signer rm`, or revoked) (D102): `ref` as `project.toml` names it (`acme:django@1`), `library` the library's id, `fingerprint` the lock's signer. The project keeps running it. `[]` when none (never null); a `signers.toml` that can't be read trusts nothing. |
| `template` | object or null | The template the project started from, as `project.toml` `[template]` records it: `ref`, `sha256` (lowercase hex, no prefix). `null` when it records none. |
| `template_update` | boolean | `template.ref` resolves to content with another hash than `template.sha256` (a library fetch changed it, D111). [`template check`](#template-check) shows the diff; nothing is applied. `false` without a template or when the ref doesn't resolve. |
| `defaults_changed` | array of strings | The `<name>` of each `containerfiles/Containerfile.<name>` ejected from a default that changed since (as `containerfile list`'s `default_changed`), sorted; `[]` for none. A package update changes a default only once accepted. |
| `worktrees` | array | One object per registered worktree, by ID; `[]` when none. |
| `worktrees[].slug` | string | The worktree's slug, its directory under the workspace. |
| `worktrees[].id` | number | Its registry ID. |
| `worktrees[].branch` | string | The branch checked out, read from its git files; `""` when detached, unreadable or the directory is gone (the worktree is still listed). |
| `worktrees[].base` | boolean | It is the base worktree (`<workspace>/worktrees/<base_branch>`). |
| `worktrees[].stack` | string | Over the services with roles `app`, `worker`, `data` (not `deps`, `tool`, `agent`): `failed` if any unit's ActiveState is `failed`, `up` if all are `active`, `stopped` if none is `active` or `activating` (or there are none), else `partial`. |
| `worktrees[].stale` | boolean | A running container of a stack service (not an on-demand one) was created from an older unit than the one rendered in the Quadlet directory: its `<prefix>.unit-hash` label differs from the unit file's (a missing label counts as different, as in `up`). `up` restarts such a container; the dashboard shows "restart to apply". A container that does not run, or a unit not rendered yet, never makes it `true`. |
| `worktrees[].services` | array | Every service in model order, as in [`status`](#status) without `health`: `service`, `role`, `state` (`<ActiveState>/<SubState>`; an on-demand service `running` or `on-demand`). |
| `worktrees[].services[].container` | string | The service's container name (`<prefix>-<slug>-<service>`), as podman and Dozzle show it. |
| `worktrees[].services[].restartable` | boolean | A long-lived unit, a service agent included (not an on-demand service or agent, or a oneshot like deps), that `railswitch restart <service>` restarts alone. |
| `worktrees[].tests` | object or null | The last run of the worktree's `test` task (D158); `null` when the project has no task named `test`. Only `railswitch task test` without arguments (the whole suite) is recorded, run in a terminal, as a dashboard job or with `--new-term`, and only when the command ended on its own: a cancelled run, or podman failing to start it, records nothing. railswitch writes the record itself from its own child's exit code, in `<workspace>/.railswitch/tests/<slug>/last.json` (0600, a 0700 dir no container mounts; `wt rm` removes it). |
| `worktrees[].tests.state` | string | `never` (no run recorded), `passed` (exit 0) or `failed`. |
| `worktrees[].tests.stale` | boolean | The worktree's HEAD is now another commit than the one the run started on. `false` when either commit can't be read (a reftable repo, an unborn branch). The commits come from the worktree's git files, which a container can write: stale is a hint, not proof. |
| `worktrees[].tests.last` | object or null | `null` for `never`; else `exit_code` (number, the task's own, `0` for passed), `started_at` and `ended_at` (RFC 3339 UTC), `commit` (the full HEAD commit at the start; `""` when unreadable). |
| `worktrees[].agent` | object or null | The worktree's running agent (D62), else the first configured agent (`[agents]` order) whose status is not `off`, else `null`. Fields as in [`agent ls`](#agent-ls). |
| `worktrees[].queue` | array | Its agent work queue's items (D159), as [`agent queue ls`](#agent-queue-ls)'s `items`; `[]` for none, or when the queue file can't be read (`agent queue ls` says why). |
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
| `packages[].ref` | string | `name@N`, `local:name@N` or `<alias>:name@N`. |
| `packages[].kind` | string | `pack`, `option`, `agent`, `tool` (the nvim tool when `[tools.nvim]` is set, ref `nvim`) or `base` (the agent base image when the project has agents, ref `agent-base`) (D107). |
| `packages[].status` | string | A library package new to the lock is `added` with every file shown and flagged (an Install review, D106); `changed` (same ref, new content; also a ref back in the lock whose content differs from the entry `stack.lock` remembers in `[[removed]]`); `first` (changed, no stored copy of what was accepted: compared against nothing, every file `added`); `added` (a ref new to the lock); `removed` (a ref the lock drops; a version or locality change is a `removed` and an `added`). |
| `packages[].from`, `to` | string | The content hash locked now and the one the accept locks (lowercase hex); `""` for none (`added`: no `from`, `removed`: no `to`). |
| `packages[].also_locked_in` | array | Prefixes of other registered projects whose `stack.lock` already pins this ref at `to`; `[]` when none. |
| `packages[].files` | array | Every file that differs, by path (unchanged files left out). |
| `files[].status` | string | `added`, `removed` or `changed`. |
| `files[].diff` | string | Unified diff, 3 lines of context (`--- a/<path>`/`+++ b/<path>`, `/dev/null` for an added or removed side). Every file is diffed as text: a NUL or invalid UTF-8 byte is shown as `\xNN` (and the file carries a `runs-here` flag); `""` only for an empty file added or removed. |
| `files[].flags` | array | `{check, line, text}`, `[]` when none. `check` is one of `firewall`, `runs-here`, `image`, `host-mount`, `port`, `privilege`. `line` is the 1-based line in the new file (map it through the hunks' `+N,M` ranges); `0` for a manifest field flag (listed above the diff) or a file-level flag. A removed file has no flags. `text` is the flagged line as `+ <line>`, or a sentence for a line-0 flag. |
| `restart` | array | Running worktrees whose units or mounted package assets the accept changes (as in [`settings preview`](#settings-preview)); `[]` when none or nothing to review. |
| `ejected` | array of strings | The `<name>` of each of the project's ejected `containerfiles/Containerfile.<name>` whose default a package in this review changes: accepting updates the default, not the copy (`overview`'s `defaults_changed` then lists it). `lock --review` only; `[]` elsewhere. |

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
  {"slug": "feat-login", "branch": "jb/feat-login", "pr": {"number": 2141, "title": "Login", "url": "https://github.com/o/filial/pull/2141", "state": "open", "checks": "pass", "review": "approved", "review_app": "https://app-pr-2141.example.dev"}}
]
```

| Field | Type | Meaning |
|---|---|---|
| `slug` | string | The worktree's slug. |
| `branch` | string | The branch checked out, as in [`overview`](#overview); `""` gets no PR. |
| `pr` | object or null | The PR whose head branch is `branch`: an open (or draft) one first, then the highest number. A merged or closed PR counts only while the worktree is still at its head commit (`headRefOid`): a branch that moved on (a `master` someone once opened a PR from) has none. `null` when there is none. PRs from forks (`isCrossRepository`) never match: their head branch is the fork's. |
| `pr.number`, `pr.title`, `pr.url` | number, string, string | As GitHub has them. |
| `pr.state` | string | `draft` (open and draft), `open`, `merged` or `closed`. |
| `pr.checks` | string | `fail` if any check concluded failure, error, timed out, cancelled, action required or startup failure; else `pending` if any is not completed (or a status context is pending or expected); else `pass`; `none` with no checks. |
| `pr.review` | string | `approved`, `changes_requested`, `pending` (review required) or `none`. |
| `pr.review_app` | string | An open or draft PR's review app: the `environmentUrl` of the newest `ACTIVE` GitHub deployment of the PR's head commit whose latest status is `SUCCESS` (how most hosting services announce one), http or https only; one `gh api graphql` query for all of them. `""` for none, or when GitHub couldn't be read (a warning on stderr). |

### `pr ls --all`

Every open pull request of the repo (not only the worktrees'), with who
it is for: `gh pr list --repo <owner/name> --state open --limit 100 --json
number,title,url,headRefName,isDraft,author,assignees,reviewRequests,reviewDecision,updatedAt`
(no checks: a repo-wide list with them times out at GitHub), and `me` as
for [`issue ls`](#issue-ls). PRs from forks are kept. The text form is a
table `PR AUTHOR ASSIGNEES REVIEWERS REVIEW TITLE` (a draft's title starts
`[draft]`).

```json
{"me": "ann", "prs": [{"number": 7, "title": "Add billing", "url": "https://github.com/o/n/pull/7", "branch": "billing", "draft": false, "author": "bob", "assignees": [], "reviewers": ["ann"], "review": "pending", "updated": "2026-11-01T00:00:00Z"}]}
```

| Field | Type | Meaning |
|---|---|---|
| `me` | string | As for `issue ls`. |
| `prs[].branch` | string | The head branch. |
| `prs[].author`, `assignees`, `reviewers` | string, arrays of strings | Logins; `reviewers` are the people a review is asked of (teams left out); arrays never null. |
| `prs[].review` | string | `approved`, `changes_requested`, `pending` or `none`, as `pr ls`. |


## `pr show [<number>]`

One pull request (D152): the number given (`7` or `#7`), else the PR of the
worktree's branch (run it in the worktree or with `-w <slug>`), picked as
[`pr ls`](#pr-ls) picks one. It runs `gh pr view <n> --repo <owner/name>
--json …` and one `gh api graphql` query for the repo's merge methods and
whether the base branch has a merge queue, both as `pr ls` runs gh. Every
text is GitHub's, written by anyone: the text form prints it with control
characters as spaces, and a URL that is not an absolute http or https URL
(a check's `url`, a comment's `url`: never `javascript:` or `data:`) is `""`.

```json
{"number": 2141, "title": "Login", "url": "https://github.com/o/r/pull/2141", "state": "open", "author": "ann",
 "body": "## Why\n…", "base": "main", "head": "jb/login", "labels": ["ui"], "mergeable": "mergeable", "merge_state": "blocked",
 "review": "changes_requested", "checks": "fail", "additions": 84, "deletions": 12,
 "created": "2026-10-30T09:00:00Z", "updated": "2026-11-01T11:00:00Z", "auto_merge": "", "queue": false, "methods": ["merge", "squash", "rebase"],
 "check_runs": [{"name": "test", "workflow": "ci", "state": "fail", "url": "https://github.com/o/r/actions/runs/41/job/5", "run": 41}],
 "reviews": [{"author": "bo", "state": "changes_requested", "body": "Rename it", "submitted": "2026-11-01T10:00:00Z"}],
 "comments": [{"author": "cy", "body": "LGTM", "created": "2026-11-01T11:00:00Z", "url": "https://github.com/o/r/pull/2141#issuecomment-1"}],
 "files": [{"path": "app/a.rb", "additions": 40, "deletions": 8, "change": "modified"}]}
```

| Field | Type | Meaning |
|---|---|---|
| `number`, `title`, `url`, `author` | | As GitHub has them (`author` a login). |
| `state`, `review`, `checks` | string | As in `pr ls`. |
| `body` | string | The description, Markdown. |
| `base`, `head` | string | The branches: into, from. |
| `labels` | array | Label names. Never null. |
| `mergeable` | string | `mergeable`, `conflicting` or `unknown` (GitHub has not computed it yet). |
| `merge_state` | string | GitHub's `mergeStateStatus`, lower case: `clean`, `blocked` (requirements unmet), `behind`, `dirty` (conflicts), `unstable` (non-required checks fail), `has_hooks`, `draft`, `unknown`. |
| `additions`, `deletions` | number | Lines, over all files. |
| `created`, `updated` | string | RFC 3339. |
| `auto_merge` | string | The method of the PR's auto-merge or merge-queue entry, lower case; `""` without one. |
| `queue` | boolean | The base branch has a merge queue: merging joins it. |
| `auto_merge_allowed` | boolean | The repo allows auto-merge (Settings → General → Allow auto-merge): `pr merge --auto` without a merge queue needs it. |
| `methods` | array | The merge methods the repo allows, of `merge`, `squash`, `rebase`, in that order. |
| `check_runs[]` | array | Each check: `name`, `workflow` (`""` for a status), `state` (`pass`, `fail`, `pending`, `skipped`), `url` (its log), `run` (its Actions run ID, 0 when not one). |
| `reviews[]` | array | Each submitted review: `author`, `state` (`approved`, `changes_requested`, `commented`, `dismissed`), `body`, `submitted`. A pending (unsubmitted) review is left out. |
| `comments[]` | array | The conversation's comments (not those on a line): `author`, `body`, `created`, `url`. |
| `files[]` | array | Each changed file: `path`, `additions`, `deletions`, `change` (`added`, `modified`, `deleted`, `renamed`, `copied`, `changed`). |
| `threads[]` | array | The review conversations on lines of the diff, the first 100 (from the same `gh api graphql` query): `id` (the thread's GraphQL ID, `PRRT_…`: what `pr reply`, `resolve`, `unresolve` and `agent --thread` take), `path`, `line` (the line now, or the line it was on when `outdated`), `resolved`, `outdated`, `hunk` (the diff hunk its first comment is on) and `comments[]` (as `comments[]`, the first 50). |

`pr merge [<number>] [--method merge|squash|rebase] [--auto]` merges the PR
(`gh pr merge --<method> [--auto]`, 2 minutes' timeout) with one of the
repo's methods, the first it allows by default; a method it doesn't allow
exits 1. `--auto` merges once the PR's requirements pass (the repo must allow
auto-merge, or the PR exits 1); with a merge queue on the base, `gh` adds
the PR to the queue. `pr ready [<number>]`
marks a draft ready for review (`gh pr ready`; a PR that is no draft exits
1). `pr rerun [<number>]` reruns the failed jobs of each Actions run with a
failed check (`gh run rerun <id> --failed`), each run once. They print one
line and have no `--json`: they are writes.

`pr comment [<number>]` adds stdin's text to the PR's conversation (`gh pr
comment --body-file -`). `pr review [<number>] --approve|--request-changes|--comment`
submits a review with stdin's text (`gh pr review --body-file -`; may be
empty to approve). `pr reply <thread>` replies to a review thread with
stdin's text, and `pr resolve|unresolve <thread>` resolves or reopens it
(`gh api graphql` mutations; the reply is read with `-F body=@-`). The text
is UTF-8, at most 65536 bytes, never blank where it is needed, and never in
gh's arguments. `pr agent [<number>] [--thread <id>]...` types the named
threads (default: every unresolved one) into the current worktree's
default agent, as `agent prompt` does: the PR, then each thread's file,
line and comments, its control characters made spaces, at most 4096
bytes (a long comment is cut; threads that don't fit are counted, with
the PR's URL). A number other than the worktree's PR is refused, and so
is an agent whose status is `permission` or `question`.
## `stack ls`

One project (`-C`): every registered worktree, in registry order, with its
branch's stack as GitHub's gh-stack extension sees it (`gh stack view
--json` in the worktree, no prompt, 30 s). gh-stack runs git and writes
lock files in the repository's common dir, which a container can write,
so it sees the repository only through a private shadow
(`GIT_COMMON_DIR`: an allowlisted copy of the config, links to the refs
and objects; a lock it creates lands there). A shadow that can't be built
is each worktree's `error`, and gh stack doesn't run. Without the
extension (`gh extension list` has no `gh stack`) no worktree is asked.

```json
{"available": true, "worktrees": [
  {"slug": "main", "branch": "main", "stack": null, "error": ""},
  {"slug": "feat", "branch": "jb/b", "error": "", "stack": {"trunk": "main", "current": "jb/b", "branches": [
    {"name": "jb/a", "current": false, "merged": true, "queued": false, "needs_rebase": false, "pr": {"number": 41, "state": "merged"}},
    {"name": "jb/b", "current": true, "merged": false, "queued": false, "needs_rebase": true, "pr": null}]}}]}
```

| Field | Type | Meaning |
|---|---|---|
| `available` | boolean | gh has the gh-stack extension (`gh extension install github/gh-stack`). |
| `worktrees[].stack` | object or null | The branch's stack; `null` when it is in none, on the trunk (gh stack shows the stack above it, with no branch current), or `available` is false. |
| `stack.number` | number | GitHub's stack number, as gh stack view's header shows it ("Stack #10350"), from gh-stack's catalog `.git/gh-stack` (read as a hostile file); `0` before `gh stack submit`. |
| `stack.branches` | array | Bottom (next to `trunk`) to top: `name`, `current`, `merged`, `queued` (in the merge queue), `needs_rebase`, `pr` (`null` before `gh stack submit`): `number`; `state`: `open`, `merged` or `queued` as gh-stack reports them, `draft` for an open PR GitHub says is a draft; `checks` (`pass`, `fail`, `pending`, `none`) and `review` (`approved`, `changes_requested`, `pending`, `none`) of an open PR, read with one `gh pr list --head` per open branch, `""` for a merged one or when GitHub couldn't be read (a warning on stderr). |
| `worktrees[].error` | string | Why gh stack couldn't say (`""` otherwise). |

## `task ls`

The worktree's (`-w`) tasks, by name: the pack's `[tasks]` with
`project.toml`'s `[tasks]` over them (a project task replaces a pack task
of the same name; `""` hides one). `railswitch task <name> [args…]`
runs one; a run of the `test` task without arguments is recorded as the
worktree's test status ([`overview`](#overview) `tests`, D158). The text
form is a table `NAME SOURCE COMMAND`. Hidden pack tasks are not listed:
`project show` has them.

```json
[
  {"name": "console", "command": "bin/rails console", "source": "pack", "pack": "bin/rails console"},
  {"name": "lint", "command": "bin/rubocop", "source": "project"},
  {"name": "test", "command": "bin/rspec", "source": "project", "pack": "bin/rails test"}
]
```

| Field | Type | Meaning |
|---|---|---|
| `name` | string | The task's name. |
| `command` | string | Its shell command, run as `sh -c '<command> "$@"' <name> <args…>`. |
| `source` | string | `pack` or `project`. |
| `pack` | string | The pack's command for this name; absent when the pack has no such task. A `project` task with one overrides it, and `task rm` brings it back (D156). |
| `fresh` | bool | `true` when `fresh_tasks` names it: it runs in a new container, never the running app (D171). Absent otherwise. |
| `terminal` | bool | `true` when `terminal_tasks` names it: the dashboard runs it in a terminal panel, not as a job (D175). Absent otherwise. |

## `db snapshot ls`

The worktree's (`-w`) database snapshots (D157), newest first; none is
`[]`. Each is a `db dump` kept by name in
`<workspace>/.railswitch/snapshots/<worktree>/<name>/` (dir 0700, files
0600). The text form is a table `NAME CREATED SIZE`.

```json
[
  {"name": "before-migrate", "created": "2026-11-04T10:00:00+01:00", "bytes": 2500000, "files": ["app_dev.dump", "rustfs_data.tar.gz"]}
]
```

| Field | Type | Meaning |
|---|---|---|
| `name` | string | The snapshot's name: `db snapshot restore <name>`, `db snapshot rm <name>`. |
| `created` | string | When it was taken (RFC 3339; its dir's modification time). |
| `bytes` | number | The size of its files together. |
| `files` | array | Its dumps and volume seeds, as `db dump` names them. |

## `settings preview`

Reads an edit list on stdin (a JSON array; ops `pack` (`ref`: sets
`[stack] pack`, D115), `firewall.mode` (`value`: `log`, `enforce`, `off`,
or absent for the default), `firewall.allow.add` and
`firewall.allow.remove` (`key`: `app`, `deps` or `agent`; `value`: a
host) (D119), `option`, `env.set`,
`env.unset`, `link.add`, `link.remove`, `link.hide`, `link.show`,
`task.set` (`name`; `value`: the command, absent or `""` to hide the
pack's task of that name) and `task.unset` (`name`: takes the project's
`[tasks]` entry out; D156), `task.fresh.add` and `task.fresh.remove`
(`name`: adds it to or takes it from `fresh_tasks`; D171),
`task.terminal.add` and `task.terminal.remove` (the same for
`terminal_tasks`; D175),
`hooks.set` (`key`: `deps`, `start` or `create`; `values`: the list's
commands in order, absent or `[]` to remove it; D126), `agent.setup`
(`key`: an agent's name; `values`: its setup commands in order, absent or
`[]` to remove them; D133; no other op takes `values`); strict: an unknown field or op is an error), writes nothing and judges each edit
against the project with the edits before it applied. Without `--json` it
prints a short summary and the diff. Exit 0 even when an edit is blocked.

```json
{
  "hash": "sha256:5cc7…",
  "diff": "--- project.toml\n+++ project.toml\n@@ -14,7 +14,7 @@\n…",
  "edits": [
    {"index": 0, "impact": "check", "reasons": ["REDIS_URL is no longer given to rails"], "error": ""}
  ],
  "restart": ["master"],
  "install": null
}
```

| Field | Type | Meaning |
|---|---|---|
| `install` | object or null | The Install review (D106): when the project as the edits leave it uses a library package `stack.lock` doesn't lock yet, the [`lock --review`](#lock---review) document of just those packages (each `added`, compared against nothing), `hash` the hash of the exact `stack.lock` `settings apply --accept-install <hash>` writes, `restart` always `[]` (the top-level `restart` lists the worktrees). `null` when the edits install no library package, or when the edited project doesn't resolve (the edits say why). |
| `hash` | string | `sha256:<hex>` of the current `project.toml`; `settings apply` and `project write` take it as `--if-hash`. |
| `diff` | string | Unified diff of `project.toml` before and after every edit that could be placed. |
| `edits[].index` | number | Position in the edit list. |
| `edits[].impact` | string | `none` (no data affected), `check` (an env value the app gets changes), `data` (a non-cache volume is left behind) or `blocked`. |
| `edits[].reasons` | string[] | Why, one line each; empty for `none`. |
| `edits[].error` | string | For `blocked`, the message; else `""`. |
| `restart` | string[] | Registered worktrees whose app unit is active and whose rendered units the edits change. One batched `systemctl show` reads the states. |
| `writes` | string[] | Containerfiles the edits eject with `project.toml` (D123): a newly chosen option's addon, or its own image's Containerfile (`[image] build`, playwright), that the workspace has no file for, copied from the package the lock accepted (a package back from `[[removed]]` at its old content). An option or pack edit keeps `[image] addons` in line with the options it changed (the old option's addon leaves the list, its file stays; the new one's is added); addons of options it leaves alone stay as you set them. `[]` for none. |

`settings apply --if-hash <h> [--accept-install <review-hash>] [--restart]` takes the same list and prints
text, not JSON: `wrote <path>/project.toml`, `wrote <path>/stack.lock`,
the render's `added|changed|removed <file>` lines, then `restarted <slug>`
per restarted worktree. It refuses (exit 1) when the hash differs
(`project.toml changed since the preview`) or an edit is blocked (one
`edit <i> is blocked: <error>` line each), and puts the old
`project.toml` and `stack.lock` back when lock or render fails. An edit
list that installs a library package needs `--accept-install` with the
preview's `install.hash`: without it the lock refuses (`installing
acme:django@1 needs review`), with another hash it refuses (`the install
changed since it was reviewed`), and accepting verifies the library's
checkout again against today's trust (one `ssh-keygen` call), refusing a
signer distrusted or revoked since the fetch (`the signer of acme is no
longer trusted`). Every refusal leaves `project.toml` and `stack.lock` as
they were.

## `project show`

What the settings page reads: the project's choices, env and links.

```json
{
  "prefix": "filial", "name": "Filial Rails",
  "repo": "git@github.com:[ORG]/[REPO].git", "base_branch": "master",
  "hash": "sha256:5cc7…", "file": "schema = 1\n…",
  "hooks": {"deps": [], "start": ["bin/rails db:test:prepare"], "create": []},
  "agents": [{"name": "claude", "setup": ["gh extension install github/gh-stack"]}],
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
  ],
  "tasks": [
    {"name": "console", "command": "", "source": "hidden", "pack": "bin/rails console"},
    {"name": "lint", "command": "bin/rubocop", "source": "project"}
  ]
}
```

| Field | Type | Meaning |
|---|---|---|
| `prefix`, `name`, `repo`, `base_branch` | string | The `[project]` table. |
| `hash` | string | As `settings preview`. |
| `file` | string | The text of `project.toml`, for the raw editor; `project write --if-hash <hash>` saves it. |
| `hooks` | object | `deps`, `start`, `create`: each `[hooks]` list's commands in order (`[]` for none; D126). |
| `agents` | array | Each `[agents.<name>]` by name: `name`, `setup` (its setup commands in order, never null; D133). `[]` without agents. |
| `firewall` | object | `mode`: `[firewall] mode` (`""`: the default); `effective`: the mode the project's services get (`enforce` with agents, else `log`, unless `mode` is set: D119); `allow`: `app`, `deps`, `agent`, each `[firewall] allow.<profile>` (`[]` for none). |
| `pack` | object | `ref`: `[stack] pack`; `choices`: the packs Settings offers, sorted, the current one among them: the newest built-in of each name, the workspace's `packs/<name>/` as `local:<name>@N` and the libraries' packs, each that loads with the current pack's app service (so `[stack] app` stays valid) and that the project's other lines resolve with (an option's `works_with`). |
| `options[]` | array | Every slot (`config.Slots` order), then the other `[options]` keys by name. A slot left out reads `ref: "none"`. |
| `used` | string[] | The stack's refs: the pack, each option, each agent package (once). |
| `add_choices` | string[] | Every option a new extra key (D81) can take: built-ins at their newest version, then the workspace's `local:<name>@N`. |
| `options[].slot` | bool | The key is a slot (otherwise an extra service, D81). |
| `options[].choices` | string[] | For a slot, the built-in options of that slot (newest version per name) then the workspace's `options/<name>/` ones as `local:<name>@N`; for an extra key every option; always ending in `none`. The current ref is always among them. |
| `env[]` | array | The `[env]` table by name. |
| `links[]` | array | The project's `[[links]]` (`from: "project"`), then the stack's: pack, options in key order, tools (`pack <ref>`, `option <ref>`, `tool <name>`). URLs keep their `{placeholders}`. |
| `links[].hidden` | bool | The label is in `hide_links`. |
| `tasks[]` | array | As `task ls`, by name, with the pack tasks the project hides too: `source: "hidden"`, `command: ""`, `pack` their command (D156). `[]` for none. |

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
there (the base worktree's dir, `.gitignore`, `.railswitch/generated.toml`,
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
    "Clone https://github.com/acme/shop (main) into /home/me/shop/worktrees/main",
    "Write project.toml and stack.lock, generate local passwords",
    "Eject 4 Containerfiles",
    "Register, lock and render",
    "Start main (up)"
  ],
  "install": null,
  "template": {
    "ref": "rails-pg@1",
    "sha256": "8ef3eafac52875f5c98c2d13eeb7b18d27d559c8de7cfacafdb37d9dad969e9d",
    "ask": [{"key": "env.TZ", "prompt": "Time zone", "default": "UTC", "answer": "Asia/Bangkok"}]
  }
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
| `install` | object or null | The Install review of the library packages the project uses, as `settings preview`'s `install`, and of a library template (a package of kind `template`, every file added and flagged); `null` when it uses neither. Its `hash` covers the template's ref and content hash too. The real run then needs `project new … --accept-install <install.hash>`, checked before the clone and again by the lock. |
| `template` | object or null | With `--template`: `ref`, `sha256` (its content hash, lowercase hex, no prefix, as `[template] sha256` records it) and each question in order: `key`, `prompt`, `default`, `answer` (the value the project gets: the `--answer` given, `""` included, else the default). `null` without `--template`. |

The summary comes from a render in memory: the base worktree is the only
worktree, on a registry entry with the next free subnet and worktree ID,
never saved. Without `--json`, the dry run prints the same content as text.

`project new --up` (without `--dry-run`) runs `up` only when podman
holds every secret `up` needs (`secret ls`'s `up`; a machine secret, or
one a removed project of the prefix left, may be set already).
Otherwise it prints `not started: <slug> needs its secrets first
(<names>)` and the `secret set` commands, and exits 0. An unset secret
only an on-demand agent receives doesn't stop it; its `secret set` line
follows the start. The dry run, which asks podman nothing, ends with
"Start <slug> (up), unless a secret is unset: then leave it stopped
until it is set (<names>)" when the project declares secrets `up`
needs. It exits 3 when the project was created and registered but
`up` failed: stderr says so and names the command to retry. Exit 1 means
the project was not created.

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
`dnsmasq (DNS filter)` checks for a dnsmasq built with nftset in the
firewall hook's PATH; without one, enforce-mode containers resolve any
name (D182), so a failure is a warning.
`ssh-keygen -Y` checks that `ssh-keygen` supports `-Y verify` (OpenSSH 8.2
or newer), which `library verify` and `library sign` need; a failure is a
warning, since nothing else needs it.
`doctor` exits 1 when any check fails; a warning never changes the exit
code, and the text output shows it as `warn`.

```json
[
  {"name": "podman >= 5.3", "ok": true, "warn": false, "detail": "podman 5.4.2", "fix": ""},
  {"name": "unprivileged port 80", "ok": false, "warn": false, "detail": "net.ipv4.ip_unprivileged_port_start = 1024", "fix": "railswitch allow-ports"},
  {"name": "units current", "ok": true, "warn": false, "detail": "render would change nothing", "fix": ""},
  {"name": "git-guard", "ok": false, "warn": true, "detail": "1 finding(s): hook-missing-backup /home/u/app/main/.git/hooks/pre-push", "fix": "railswitch hooks check"}
]
```

| Field | Type | Meaning |
|---|---|---|
| `name` | string | The check: `workspace`, `podman >= 5.3`, `quadlet generator`, `podman.socket active`, `unprivileged port 80`, `nft`, `nsenter`, `getent`, `jq`, `curl`, `dnsmasq (DNS filter)`, `ssh-keygen -Y`, `machine subnet free`, `networks isolated`, `firewall descriptor`, `units current`, `proxy subnet`, `git-guard`, `cgroup v2`, `machine mounts`, `pass`, `gpg`, `gpg secret key` or `machine config`. A binary's name (`nft`, `pass`, …) checks that it is on `PATH`. `quadlet generator` passes when `/usr/libexec/podman/quadlet` or `/usr/lib/podman/quadlet` exists. |
| `ok` | boolean | The check passed. |
| `warn` | boolean | A failed check is only a warning (`git-guard`, `ssh-keygen -Y`, `dnsmasq (DNS filter)`): it doesn't change the exit code. `false` for a passed check. |
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
| `machine.dashboard` | object | The dashboard's plain systemd user unit (`unit`: `railswitch-dashboard.service`, at `$XDG_CONFIG_HOME/systemd/user/`, written by every render for the railswitch it already runs (only `railswitch dashboard` points it at another; a render writes its own path only when there is no unit or its railswitch is gone, D129), started by `Wants=` from every app unit), its systemd `state` (`<ActiveState>/<SubState>`) and the `url` it is opened at, `http://railswitch.localhost:7700/` (it listens on `127.0.0.1:7700` only). `railswitch dashboard` prints the login link. The text output's line is `<unit> <state> <url>`. |
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
