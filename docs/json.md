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
