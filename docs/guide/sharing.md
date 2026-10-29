# Sharing

Teammates don't share a workspace: each person has their own editor,
agents and secrets. They share the parts that make a stack: a
**template** to start a project from, or a **package** they keep
customizing.

## Templates

A template is a project's stack: its pack and options, pins, env, tasks,
links, routes, firewall, the Containerfiles you edited, and the local
packs and options it uses, with questions for what each new project
answers (a time zone, which cache). It never holds the project's name,
repository, worktrees, secret values, editor or agents.

```bash
railswitch template export --name shop-stack --to ~/share/shop-stack
```

A teammate imports the folder, reviews every file (flagged as any update
is), and creates projects from it:

```bash
railswitch template import ~/Downloads/shop-stack     # the review
railswitch template import ~/Downloads/shop-stack --accept <hash>
railswitch project new ~/workspace/shop --repo … --prefix shop --template shop-stack@1
```

In the dashboard: **Machine → Templates → Import from a folder**, then
**New project → Start from a template**. A later version imports the same
way, shown as the diff from the one there.

Reference: [docs/templates.md](../templates.md).

## Packages

To share one customized package (your team's Rails pack):

```bash
railswitch pack export local:rails@1 --to ~/share/rails
railswitch pack import ~/Downloads/rails                 # in their workspace: the review
railswitch pack import ~/Downloads/rails --accept <hash>
```

A new package is shown as its changes from the closest built-in, so the
review shows only your team's edits. Importing a newer folder over it
later is an update, shown as a diff. The Library page has **Import a
package** and **Export**.

## Libraries

For a team that keeps changing its packages and templates, a **library**
is a git repository of them with a signed index. Each person adds it once
and trusts its signer's key once; every later change then arrives as a
reviewed update. Signing uses an SSH key you probably already have; the
key isn't shared, each publisher signs with their own.

```bash
railswitch library add git@github.com:team/stack-lib.git --alias team
railswitch library trust team --fingerprint SHA256:…
railswitch library fetch
```

Then `team:rails@2` and `team:shop-stack@1` are refs like any other.
Reference: [docs/libraries.md](../libraries.md).
