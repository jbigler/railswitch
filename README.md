# RailSwitch

RailSwitch runs a full development environment for each git branch of a
web app: its own app server, database, cache, storage and browser, its own
hostname, and optionally AI coding agents and your editor, each in a
container. Switching branches is switching worktrees: everything a branch
needs is already running beside it.

```
main.shop.localhost         the app on branch main
feature-x.shop.localhost    the same app on branch feature-x, with its own database
```

It runs on Linux with rootless podman: containers are systemd user
services (Quadlet), nothing runs as root, and ports listen on 127.0.0.1
only. A local dashboard at `http://railswitch.localhost:7700/` is where you
create and configure projects; the `railswitch` command is for starting,
stopping and one-off commands.

## What it does

- **One environment per worktree.** `wt add feature-x` makes a git
  worktree for the branch, with its own containers, ports and data
  volumes.
- **Stacks from packages.** A *pack* knows one framework (Rails is built
  in); *options* fill slots such as database, cache, jobs, storage and
  browser. New projects are detected from the repository.
- **Sandboxed agents.** Each AI agent runs in its own container with an
  egress firewall it can't change. The rest of the project is firewalled
  too, so agent-written code can't get out through the app.
- **Reviewed changes.** A package update, a library you fetch or a
  template someone hands you is shown as a diff, with risky lines
  (new images, hosts, ports, scripts) flagged, before anything runs.
- **Sharing without sharing a repo.** Export your stack as a template, or
  a customized pack as a package; a teammate imports it, reviews it, and
  starts their own project with their own editor and agents.

## Getting started

```bash
make build && install -m755 railswitch ~/.local/bin/
railswitch doctor          # checks podman, Quadlet, the firewall tools
railswitch dashboard       # starts the dashboard and prints its login link
```

Open the link and choose **New project**. The [guide](docs/guide/README.md)
walks through it, then through agents, the editor, the firewall and
sharing.

## Documentation

- [Guide](docs/guide/README.md): how railswitch works and how to use it.
- Reference:
  - [Dashboard](docs/dashboard.md)
  - [Packages](docs/packs.md)
  - [Firewall](docs/firewall.md)
  - [Templates](docs/templates.md)
  - [Libraries](docs/libraries.md)
  - [Editor](docs/nvim.md)
  - [JSON output](docs/json.md)
  - [Shell completion](docs/completion.md)
- Design: the decisions behind it are in
  [the roadmap](docs/superpowers/plans/2026-10-25-railswitch-roadmap.md)
  and [the handoff](handoff/HANDOFF.md).

## Requirements

Linux with a systemd user session, podman 5 or newer with its Quadlet
generator, and `nft`, `nsenter`, `getent`, `jq`, `curl` and `git` on the
host. `railswitch doctor` checks each one and says how to fix what's
missing.
