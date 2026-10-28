# Templates: saving a stack and starting projects from it

A template is a project's stack ready to start a new project from: its
pack, options, pins, env and other lines, the Containerfiles you edited,
and the questions a new project answers (D108–D111). Project-only lines
(name, prefix, repo, base branch, per-worktree overrides) and secret
values are never in a template. Neither is anything each person picks
for themselves (D112): the editor (`[tools]`) and the agents
(`[agents]`), so a template primes a teammate's project without handing
them your editor config or your agents.

## The template directory

```
rails-pg.stack/
  stack.toml
  containerfiles/Containerfile.pg-client   only files edited from their default, and user addons
  packs/rails/…                            the local: pack its lines name, if any (D116)
  options/<name>/…                         each local: option its lines name
  README.md                                optional
```

Nothing else is allowed: no other file, no other directory, no symlink,
no file over 1 MiB. A bundled package holds only package files (the
paths `pack file write` accepts) and must be one the lines name as
`local:<name>@N`, with that name and version in its manifest; every
`local:` pack and option the lines name with the questions' defaults
applied must be bundled (a bundle an answer can pick is allowed too).
Agents are never bundled: a template has none. A template saved before
D116 that names a `local:` package has no bundle and no longer reads:
export the project again. `project new` writes only the bundles the new
stack uses (an answer or `--pack` may pick another). `stack.toml` is read strictly: a key it doesn't
define (`[project]`, `[worktrees]`, `[share]`, `[secrets]`, a secret
`value`) is refused, and `[tools]` or `[agents]` is refused by name, as
is `containerfiles/Containerfile.agent-base`. A template saved before
D112 has `[agents]` (export added the default agent) and no longer
reads: export the project again, after removing
`~/.config/railswitch/templates/<name>.stack/`.

```toml
schema = 1
name = "rails-pg"            # [a-z][a-z0-9-]*
version = 1                  # the ref is name@version
description = "Rails with PostgreSQL and Redis"

[stack]                      # the lines, as in project.toml
pack = "rails@1"
app = "rails"
[options]
database = "postgres@1"
cache = "redis@1"
[versions]
postgres = "16"
[env]
TZ = "UTC"
# [services.<id>], [tasks], [[links]], hide_links, [[routes]]
# and [firewall] likewise; never [tools] or [agents]

[image]                      # user addons only (containerfile new)
addons = ["containerfiles/Containerfile.extra"]

[[ask]]                      # a question a new project answers
key = "options.cache"        # options.<key>, versions.<name> or env.<NAME>
prompt = "Cache"
default = "redis@1"

[[secret]]                   # a declaration: never a value (D33)
name = "rails_master_key"
services = ["rails"]
env = "RAILS_MASTER_KEY"
```

- An `[[ask]]` key names the value it sets. A new project's answer
  replaces it; with no answer, the question's default does. Each key is
  asked once.
- `[[secret]]` declares a secret as `[secrets.<name>]` does: its services
  and delivery. The value is set later with `railswitch secret set`.
- The lines with every default applied must be a valid `project.toml`
  once `[project]` is added: a template that isn't is refused.
- An option's addon (`Containerfile.pg-client`) is never in `[image]`:
  the new project ejects it for the options it picks. A kept
  Containerfile of that name replaces the ejected one.

## Where templates live

- This machine: `~/.config/railswitch/templates/<name>.stack/`, ref
  `<name>@N`. The store holds one version per name: the latest export
  or import.
- A library: `templates/<name>/` in its repository, listed in its signed
  index as `kind = "template"`, ref `<alias>:<name>@N`
  (docs/libraries.md). It is read from the verified checkout only, with
  the content hash the index lists.

There are no built-in templates. A `local:` ref is never a template.

## Commands

- `railswitch template ls [--json]`: this machine's templates, then each
  library's, with the packages each uses. A template that doesn't read is
  left out, its reason on stderr.
- `railswitch template show <ref> [--json]`: its `stack.toml`, questions,
  secret names and files, and its content hash.
- `railswitch template export [--name <n>] [--version <N>] [--description
  <text>] [--to <dir>]`, in a workspace: writes the project as a template.
  The default name is the project's prefix; the default version is one more
  than the store's (1 with `--to`). Into the store, a version not above the
  one there is refused; `--to <dir>` writes the template directory `dir`
  itself, which must not exist (for a library: `--to
  <library>/templates/<name>`).

Export makes:

- a question per `[options]` key, `[versions]` pin and `[env]` value, its
  default the project's value;
- a `[[secret]]` per declared secret;
- `containerfiles/` with each Containerfile the stack builds whose body
  (below the `# Ejected from` header) differs from its default
  (`containerfile show --json` `diff`), and each user addon.

It leaves out each person's own lines (D112): `[tools.nvim]`, each
`[agents.<n>]`, `[firewall] allow.agent`, a `[services.<id>]` or
`[[routes]]` entry of the editor or an agent (`nvim`, `agent`,
`agent-<n>`), and those services in each secret's `services`; a secret
only they receive is dropped. The editor's and the agents'
Containerfiles (each tool's, `agent-base`, each agent's) are never kept,
even when edited, and `project new` refuses a template that holds one
(before the clone).

It prints what it dropped (`from`, `[project]` keys, `[worktrees.<slug>]`,
`[share]`, `[template]`, and the lines above), what it asks, the secrets it declares and the
Containerfiles it keeps, and the local packages it bundles: the
project's `local:` pack and options, as the project runs them (a locked
one's accepted copy). Export refuses while
`stack.lock` is out of date or an update waits for review: a template is
made from what was accepted.

## Sharing a template as a folder

`railswitch template export --to <dir>` writes the template as a folder
you can hand to someone: zip it, put it on a share, or commit it
anywhere. Unpack it as it was: the import refuses any other file, so a
zip carrying `__MACOSX/` or `.DS_Store` must be unpacked without them.
On their machine:

- `railswitch template import <dir> [--json]` (D113) reviews it: every
  file as a diff against the version of that name on this machine (all
  added when there is none; a bundled package new here is shown as its
  changes from its closest built-in, `bundles_compared_with`), flagged
  by the review checks (a bundled
  package's files by the package checks: its manifest's images, ports
  and capabilities too), with warnings for each library this machine
  hasn't added. Nothing is saved. The folder is hostile input: only the
  template files above, regular files ≤ 1 MiB each, at most 512 and
  16 MiB in all, no symlink (the folder itself
  included); any other entry refuses the import, and such an entry is
  never opened.
- `railswitch template import <dir> --accept <sha256>` saves it in the
  store, once the folder still has the hash its review printed. A
  template new to this machine, or a higher version than the one there,
  is saved; the same content is "already on this machine"; other
  content at a version not above the stored one is refused (exit 1,
  also from the review).
- The dashboard's Machine page does the same (docs/dashboard.md).

A folder has no update channel: a later version is another folder,
imported the same way. For updates that reach everyone as reviewed
diffs, publish the template in a signed library (docs/libraries.md).

## Starting a project from a template

```
railswitch project new ~/src/shop --repo <url> --prefix shop \
  --template rails-pg@1 --answer env.TZ=Asia/Bangkok --answer versions.postgres=17
```

`project new --template <ref> [--answer key=value]…` builds the new
`project.toml` in this order:

1. The template's lines.
2. Each question's value: its `--answer`, else its default. An answer
   with an empty value (`--answer env.TZ=`) sets the value empty; leave
   the flag out to keep the default. An answer for a key the template
   doesn't ask is refused.
3. The other flags on top: `--pack` (only when given), `--option`,
   `--tool`, `--agent` (as without a template: no flag adds the built-in
   default agent, `--agent none` none), and `--name`, `--prefix`,
   `--repo`, `--branch` for `[project]`.
4. Each chosen option's image pin the lines lack (`[versions]`).
5. `[template] ref` and `sha256`: where the project came from.

Then the usual `project new` path runs: every check before the clone (an
option answer the pack can't use is refused there; the stack resolves
with the bundled packages), the clone, the bundled packages written as
the new workspace's own `packs/<name>/` and `options/<name>/` (locked
as `local:`, yours to edit from then on), the ejects, then the template's Containerfiles written over the ejected ones,
its user addons appended to `[image] addons`, and its `[[secret]]`
declarations as `[secrets.<name>]`. `--dry-run` shows it all, with
`template` (each question and its answer) in `--json` (docs/json.md).

The template's content is stored beside the accepted packages
(`<data>/locked/<sha256>/`), so a later update can be shown as a diff.

A library template (`<alias>:<name>@N`) is part of the install review
(D110): the dry run's `install` lists it as a package of kind `template`,
its `stack.toml` and Containerfiles shown and flagged by the review checks
(docs/packs.md, Locking), even when it installs no library
package. `--accept-install <install.hash>` accepts it with the packages;
the hash covers its content, so a template changed since the review is
refused, and accepting it verifies its library again.

## Template updates

A project records the template it came from in `[template]`; nothing
reads it to change the project. When the ref resolves to other content
(a library fetch brought a new version of the same `name@N`; an export
never replaces a version in place), the template has an update:

- `railswitch overview --json` sets `template_update` (and always shows
  `template`), so the dashboard shows "The template <ref> changed".
- `railswitch template check [--json]`, in the workspace, shows the diff
  of every template file from the accepted copy (stored when the project
  was created) to the current one, flagged by the review checks.
  Nothing is applied: take what you want through Settings or the
  Containerfile editor.
- `railswitch template check --accept <current>` records the new content
  hash in `[template] sha256`, which clears the notice, and stores the
  content as the base of the next diff. Only that line of `project.toml`
  changes. A hash other than the current one is refused.

A ref that no longer resolves (the template left the store or the
library) is no update: `template check` reports why in `error`.
