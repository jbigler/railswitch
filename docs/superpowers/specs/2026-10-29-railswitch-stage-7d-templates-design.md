# railswitch Stage 7d: templates

Design for Stage 7d (D95), the last part of Stage 7, written on the user's
instruction to continue without questions (2026-10-28: "use your own
judgement"); decisions D108–D111. Builds on 6c (new project), 7a (review),
7b (signing) and 7c (libraries).

## Intent

HANDOFF §3.9: a template is a pack, a set of options and your overrides,
ready to start a project from. Exporting turns a project's lines into
questions with defaults (which option per slot, each pin, `TZ`-like env,
secrets); project-only lines are dropped. A template package is small:
`<name>.stack/` with `stack.toml`, `containerfiles/` (only the edited
ones) and `README.md`. The library shows Templates beside packs and
options; a project records the template it came from; an update to the
template appears as a diff, never applied silently.

## The template format (D108)

```
rails-pg.stack/
  stack.toml
  containerfiles/Containerfile.pg-client   # only files edited from their default
  README.md                                # optional
```

`stack.toml`:

```toml
schema = 1
name = "rails-pg"            # [a-z][a-z0-9-]*
version = 1                  # a template ref is name@version
description = "Rails with PostgreSQL and Redis"

[stack]                      # as project.toml, minus project-only lines
pack = "rails@1"
[options]
database = "postgres@1"
cache = "redis@1"
[versions]
postgres = "16"
[env]
TZ = "UTC"
[tasks]                      # links, hide_links, tools, agents likewise
[[ask]]                      # questions new projects answer (all optional)
key = "options.cache"        # a dotted project.toml path the answer sets
prompt = "Cache"
default = "redis@1"
[[ask]]
key = "versions.postgres"
prompt = "PostgreSQL version"
default = "16"
[[ask]]
key = "env.TZ"
prompt = "Time zone"
default = "UTC"
[[secret]]                   # secrets the project declares (names only, D33)
name = "rails_master_key"
```

- Project-only lines never appear: `[project]` (name, prefix, repo,
  base_branch), per-worktree overrides, machine-local anything. Secret
  values are never in a template; their declarations are.
- `ask` keys: `options.<key>`, `versions.<name>`, `env.<NAME>`; the
  answer replaces the value at that path. An option answer must be a ref
  that works with the pack (checked at create, as `project new` does).
- `containerfiles/Containerfile.<name>`: only files whose body differs from
  the default they were ejected from (the D32 header stays; the 6d diff
  rule decides "edited"). A user addon (`containerfile new`) is included.
- Strict decode, `schema = 1` (D39), every path in the template is a
  regular file under the allowlist above (no symlinks), ≤ 1 MiB each.

## Where templates live (D109)

- On this machine: `~/.config/railswitch/templates/<name>.stack/`
  (machine-wide, the user's), ref `<name>@N` (no built-in templates exist,
  so a bare ref is unambiguous; `local:` stays the workspace's).
- Libraries: `templates/<name>/` in a library repository (7c), listed in
  its signed index as `kind = "template"` (7b's index gains the kind);
  ref `<alias>:<name>@N`; resolved from the verified checkout only, as
  library packages are. The fetch allowlist (D104) gains `templates/`.
- `template ls [--json]`: local and library templates (ref, name,
  version, description, source, the packages it uses).
- `template show <ref> [--json]`: its `stack.toml`, questions, files.

## Export and start (D110)

- `template export [--name <n>] [--version <N>] [--to <dir>]` in a
  workspace: writes `<n>.stack/` (default: the local store; refuses to
  overwrite an existing version: a new version must be a higher number).
  Questions are generated: one per slot option, each `[versions]` pin, and
  each `[env]` value (default: the project's value); secret declarations
  copied. Prints what it dropped (project-only lines) and what it asks.
- `project new <dir> --template <ref> [--answer key=value]… [other new
  flags]`: the template's lines are the project's starting `project.toml`
  (its pack and options instead of detection's), answers applied, then
  the explicit flags (`--option`, `--pack`, `--name`, …) on top; then the
  usual `project new` path (`planNew`, `writeProject`); after the ejects,
  the template's `containerfiles/` files replace the ejected ones. `--dry-run`
  shows it all (D89) plus `template: {ref, questions}`. A library
  template's packages install through 7c's review (`install`,
  `--accept-install`), and the template itself is shown in that review
  (its `stack.toml` and Containerfiles, the Containerfiles flagged by the
  7a checks) — so `--accept-install` covers both.
- `project.toml` gains `[template] ref = "<ref>"`, `sha256 = "<content
  hash>"` (written by `project new --template`; ignored otherwise).

## Template updates (D111)

`template check [--json]` in a workspace: when its `[template]` ref
resolves to content with another hash (a library fetch, or a local
template re-exported under the same version — refused by export, so only
libraries), prints the diff of the template's `stack.toml` against the
recorded version (the old one from the 7a store: `project new --template`
stores the template's content like a package) and of its Containerfiles.
Nothing is applied: the user edits Settings or the Images page. `overview
--json` gains `template_update: bool`. "Accept" records the new sha
(`template check --accept <sha>`), clearing the notice.

## Page

- Library page: a Templates tab beside packs and options (from `template ls
  --json`): name, version, source, packages, description; selecting one
  shows `template show` (lines, questions, files). Projects page header
  (Settings): "Save as template" (name, version) → job `template export`.
- New project: "Start from a template" picker above "Read the repository":
  choosing one fills the stack and options from it and shows its questions
  (each a field with its default); the review step shows the template's
  Containerfiles; Create passes `--template <ref>` and `--answer`s.
- A project with `template_update` shows "The template <ref> changed" with
  a link to the diff (`template check`) and Accept.

## Errors

- A template that doesn't decode, names an unknown ref, or holds a
  non-allowlisted path: refused at `ls`/`show`/`new` with the reason.
- An answer for a key the template doesn't ask: refused.
- Export in a project with a stale lock or a pending review: refused (a
  template is made from what was accepted).

## Known limits

- The machine store keeps one version per name, the latest export: a
  project made from an older local version is told by `template check`
  that its version is gone, not shown an update. Updates come from
  libraries, which keep each published version's content by hash.

## Testing

- Format: strict decode; project-only lines refused in `stack.toml`;
  path allowlist; a symlink refused.
- Export: from the Filial fixture: questions per slot, pin and env;
  secrets as names; only edited Containerfiles; project-only lines absent;
  a second export of the same version refused.
- Start: `project new --template x@1 --dry-run` gives the exact
  `project.toml` (answers applied, flags on top) and the template's
  Containerfiles in place of the ejected ones after a real run; an unknown
  answer key refused; a library template needs `--accept-install`.
- Check: a library template fetched at a new sha: `template_update`, the
  diff, accept clears it.
- Web: Templates tab, Save as template job, New project from a template
  (argv exact, answers), template update notice; guard.

Host acceptance, `docs/stage7d-acceptance.md`: export Mastodon as a local
template; start a new project from it (a small public Rails app, with an
answer changed) through the page; it answers 200 with the template's
edited `Containerfile.pg-client`; a library template through the review.

## Decisions (roadmap)

- D108: a template is `<name>.stack/` (`stack.toml` with `[[ask]]`
  questions and secret names, edited Containerfiles, README); project-only
  lines and secret values never in it.
- D109: templates live in `~/.config/railswitch/templates/` (`<name>@N`) or
  in libraries (`templates/<name>/`, signed in the index, `<alias>:`).
- D110: `template export` and `project new --template` with `--answer`;
  a library template is part of the install review.
- D111: a project records its template; updates are shown as diffs by
  `template check` and only noted as accepted, never applied.
