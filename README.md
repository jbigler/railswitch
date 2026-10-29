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
  too (enforce by default once a project has agents), closing the obvious
  ways out through the app; the [firewall](docs/firewall.md) lists what it
  doesn't cover.
- **Reviewed changes.** A package update, a library you fetch or a
  template someone hands you is shown as a diff, with risky lines
  (new images, hosts, ports, scripts) flagged, before anything runs.
- **Sharing without sharing a repo.** Export your stack as a template, or
  a customized pack as a package; a teammate imports it, reviews it, and
  starts their own project with their own editor and agents.

## Getting started

Download the binary for your machine from the
[latest release](https://github.com/jbigler/railswitch/releases/latest),
check it, and install it as `~/.local/bin/railswitch` (on your `PATH`; if
that folder is new, log in again). It's one static file; you don't need Go.

```bash
cd "$(mktemp -d)"
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
base=https://github.com/jbigler/railswitch/releases/latest/download
curl -fLO "$base/railswitch-linux-$arch"
curl -fLO "$base/SHA256SUMS"
sha256sum --check --ignore-missing SHA256SUMS &&
  install -D -m755 "railswitch-linux-$arch" ~/.local/bin/railswitch

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
  - [Releasing](docs/releasing.md)
- Design: the decisions behind it are in
  [the roadmap](docs/superpowers/plans/2026-10-25-railswitch-roadmap.md)
  and [the handoff](handoff/HANDOFF.md).

## Versions

Releases are numbered `v0.<minor>.<patch>` until 1.0: a minor release may
change commands or files, and its notes say how. railswitch's TOML files
(`project.toml`, `stack.lock`, the machine's registry and config) carry a
`schema` number; an older railswitch refuses a file with a higher one, so
it never misreads it. To upgrade, run the install commands again: the
dashboard restarts itself on the new binary. Building from source:
`make build VERSION=<tag>` (Go, see `go.mod`).

## Requirements

Linux with a systemd user session, podman 5 or newer with its Quadlet
generator and socket, cgroup v2, and `nft`, `nsenter`, `getent`, `jq`,
`curl` and `git` on the host. `railswitch doctor` checks all but `git` and
says how to fix what's missing.
