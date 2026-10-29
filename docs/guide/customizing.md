# Customizing a project

Most changes happen on the project's **Settings** page. Each change is
staged first: the page shows what it does (a volume left behind, which
running worktrees restart, anything it blocks) before **Apply** writes
`project.toml`, locks and renders. Your comments in `project.toml` stay.

## Settings

- **Stack and options:** the pack, and each slot's option; **Add service**
  for one more (a second database). Switching a database to `none` leaves
  its volume behind, and the page says so.
- **Firewall:** the mode and the allow lists ([The firewall](firewall.md)).
- **Environment** and **Worktree links** (shortcuts on each worktree's
  row, with `{worktree.host}` filled in).
- **Secrets:** set or replace a value; it's sent on stdin, never in a URL
  or a file.
- **project.toml:** the whole file in an editor, for anything without a
  control. It's checked before it's saved.

Everything has a command too: `railswitch settings preview` and `settings
apply` take the same edits as JSON; `project show` and `project write`
read and write the whole file.

## Hooks

Hooks are commands railswitch runs for you at fixed points, inside the
containers, never on your machine. They cover the project's own setup
steps that the pack doesn't already do (it installs the lockfiles' gems
and packages in `deps`, and the Rails pack's app runs `bin/rails
db:prepare` before its server).

```toml
[hooks]
deps   = ["gem list -i retest >/dev/null || gem install retest --no-document"]
start  = ["bin/rails db:test:prepare"]
create = ["bin/rails runner 'Admin.create_default!'"]
```

| List | Runs in | When |
|---|---|---|
| `deps` | deps (the one container that reaches the package registries) | after its installs, on every app start |
| `start` | the app | before its server, on every app start |
| `create` | the running app | once per new worktree, after its first `up` is ready |

- Each command runs in its own `sh -ec`, in order; one that fails stops
  the rest. A failing `deps` or `start` hook fails the app's start, with
  its output in the start error. A failing `create` hook is run again,
  with the others, by the next `up`.
- `deps` hooks install into the shared gem and package caches, so what
  they install is there for the app, `exec` and the editor too: a test
  watcher, `ruby-lsp`. Write them to check first, as above, so a start
  doesn't fetch every time.
- `create` hooks run only for worktrees added after you add them, never
  for the ones you have.
- They are in `project.toml`, so they're yours. A template carries its
  `[hooks]`, and its review flags each one.

To copy files git doesn't track (`.env.development.local`, a `tags` file)
into each new worktree, list them in `[project] seed_files`: `wt add`
copies each from the base worktree when the new one doesn't have it.

## Images

The **Images** page lists the Containerfiles the project builds: the base
image, each option's addon, the editor's and each agent's. Each starts as
a copy of the package's default with a header saying where it came from.
Edit it in the browser; **Check** builds nothing but shows which images
change and which worktrees restart. When the package's default changes
later, the page shows the diff and you choose: keep yours, or reset.

## Your own packages

The **Library** page lists the built-in packages, the workspace's own
(`local:`) and those of your libraries.

- **New pack** or **New option** copies a built-in (`pack eject`) or
  starts blank (`pack new`) into the workspace's `packs/` or `options/`.
  Edit its files in the browser; a package the project uses is locked and
  rendered on save.
- A copy of a pack should keep its name (`local:rails@1`): options name
  the packs they work with.
- Then pick it in Settings.
- `pack import` (or **Import a package**) brings in a package someone
  handed you, as a new one or an update of yours; a folder at another
  version than the one the project uses is refused (import it under
  another name).

The package format: [docs/packs.md](../packs.md).

## Update reviews

`stack.lock` pins the content of every package the project runs. When a
package's content changes (a railswitch upgrade, a library update, an edit
outside railswitch), the project keeps running the copy you accepted, and
the project page shows **Package update waiting for review**. The review
shows each changed file as a diff and flags what matters: a new image, a
download run as a script, a firewall host, a host mount, a port, a
capability. Accept it (optionally restarting), or keep running the old
copy as long as you like.

```bash
railswitch lock --review
railswitch lock --accept <hash> [--restart]
```

## Cleaning up

Each rebuild leaves the previous images behind, and an update leaves the
copy it replaced.

```bash
railswitch gc --dry-run      # what it would remove
railswitch gc                # old images and unused stored package copies
railswitch gc --volumes      # also volumes no worktree mounts (lists them and asks: their data is lost)
```

A volume left behind is often data: the database of a worktree you
removed, or the old one after a PostgreSQL major upgrade
(`db-data-pg16`). Read the list before you answer.

It keeps everything any registered project's worktrees use now, running
or not, and everything of a project that doesn't load.

Removing a whole project removes its images too:

```bash
railswitch project rm shop            # worktrees, units, networks, images
railswitch project rm shop --purge    # also the shared volumes and <workspace>/.railswitch
```

The workspace folder and its git repositories are kept either way.

Next: [Sharing](sharing.md).
