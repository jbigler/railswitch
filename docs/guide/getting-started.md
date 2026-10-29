# Getting started

## Install

You need Linux with a systemd user session and podman 5 or newer (with its
Quadlet generator), plus `nft`, `nsenter`, `getent`, `jq`, `curl` and `git`.
Build and install railswitch, then let it check the machine:

```bash
make build && install -m755 railswitch ~/.local/bin/
railswitch doctor
```

Every check should say `ok`. Two common fixes:

- **Port 80.** Every app is served on `http://<worktree>.<prefix>.localhost`
  through one proxy on 127.0.0.1:80, which is a privileged port:
  `railswitch allow-ports` lets your user bind it.
- **Linger** (a warning). Without it, your projects and the dashboard stop
  when you log out and start again when you log in. That is fine on a
  desktop; `loginctl enable-linger` keeps them running.

## The dashboard

```bash
railswitch dashboard
```

This starts the dashboard (and from then on at every login) and prints a
login link. The dashboard lives at `http://railswitch.localhost:7700/`, on
127.0.0.1 only, behind a token only you can read. Run the command again
whenever you need the link.

## A first project

In the dashboard, choose **New project**:

1. Give it the repository URL. railswitch reads only the files that say
   which stack it is (a `Gemfile`, `package.json` and so on), without
   cloning the whole thing.
2. It suggests a pack and options (Rails, PostgreSQL, Redis…) and says why.
   Change anything, pick a prefix (it names every container, volume and
   hostname), and add your editor or agents.
3. **Next: review** shows the project file it will write, the containers,
   the routes and anything to watch out for. **Create project** clones the
   repository and starts it.

The same from the terminal:

```bash
railswitch project new ~/workspace/shop --repo git@github.com:you/shop.git --prefix shop --up
```

The app answers at `http://main.shop.localhost/` (the branch name, then the
prefix). The dashboard's project page shows every container, the links the
pack gives (mail previews, the job queue, storage) and whether anything
needs you.

## Worktrees

Each branch you work on is a worktree: a git worktree with its own
containers, database and ports.

```bash
railswitch wt add feature-x --up      # a branch, a PR number, or a new branch
cd ~/workspace/shop/feature-x
railswitch up | stop | restart | down
railswitch logs -f rails
railswitch exec bin/rails console
railswitch task test                  # the pack's and project's tasks
railswitch wt rm feature-x            # --force: even with uncommitted changes (lost)
```

The dashboard does the same from each worktree's row: up, stop, restart,
open the app, open a terminal, start an agent.

## Secrets

A project declares the secrets it needs (an API key, an agent's token);
you set the values:

```bash
railswitch secret set rails_master_key    # asks without echo, or reads stdin
```

Values live in podman's secret store, never in a file railswitch writes,
an environment file or a command line. The dashboard's Settings page sets
them too.

Next: [Concepts](concepts.md).
