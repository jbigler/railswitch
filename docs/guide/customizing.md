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

Next: [Sharing](sharing.md).
