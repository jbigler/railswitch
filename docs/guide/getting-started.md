# Getting started

## Install

You need Linux with a systemd user session and podman 5 or newer (with its
Quadlet generator), plus `nft`, `nsenter`, `getent`, `jq`, `curl` and `git`.
Install railswitch, one static binary for amd64 or arm64 from the
[releases](https://github.com/jbigler/railswitch/releases), then let it
check the machine:

```bash
cd "$(mktemp -d)"
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
base=https://github.com/jbigler/railswitch/releases/latest/download
curl -fLO "$base/railswitch-linux-$arch"
curl -fLO "$base/SHA256SUMS"
sha256sum --check --ignore-missing SHA256SUMS &&
  install -D -m755 "railswitch-linux-$arch" ~/.local/bin/railswitch
railswitch doctor
```

`~/.local/bin` must be on your `PATH` (if the folder is new, log in
again). From v0.1.1 you can also check that the file was built from this
repository by its release workflow, in the same shell and folder as the
download, with the GitHub CLI logged in (`gh auth login`):

```bash
gh attestation verify "railswitch-linux-$arch" --repo jbigler/railswitch
```

To upgrade, run the same commands for the new release; the dashboard
restarts itself on the new binary. A dashboard you started from
another copy (a clone's `./railswitch`) keeps that one until `railswitch
dashboard` and then `systemctl --user restart railswitch-dashboard`. From a clone
with Go instead: `make build && install -m755 railswitch ~/.local/bin/`.

`doctor` checks podman, its socket and Quadlet, cgroup v2, port 80, the
firewall's tools and `ssh-keygen -Y` (for signed libraries). Every check
should say `ok`. Two common fixes:

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

Open it in a browser on the same machine, at that address, on port 7700:
it answers only as `railswitch.localhost:7700`, `localhost:7700` or
`127.0.0.1:7700`, and says "forbidden host" for any other name or port.
From another machine (a dev VM, a server), forward the same port and open
the same link there:

```bash
ssh -L 7700:127.0.0.1:7700 <the machine running railswitch>
```

The local port must be 7700 too, and free there: a dashboard of your own
on that machine has to be stopped first.

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

Like the page, it reads the repository for the pack and options and says
what it found ("detected database mariadb@1 (Gemfile: mysql2)"). An
`--option slot=ref` you give wins over a detected one; `--pack` or
`--template` skips the reading, and `--no-detect` takes the pack's
defaults.

The app answers at `http://main.shop.localhost/` (the branch name, then the
prefix). The dashboard's project page shows every container, the links the
pack gives (mail previews, the job queue, storage) and whether anything
needs you.

## Worktrees

Each branch you work on is a worktree: a git worktree with its own
containers, database and ports.

Project commands run in the workspace, worktree commands in a worktree
(or anywhere, with `-C <workspace>` and `-w <worktree>`):

```bash
cd ~/workspace/shop
railswitch wt add feature-x --up      # a branch, a PR number, or a new branch
cd feature-x
railswitch up | stop | restart
railswitch logs -f rails
railswitch exec bin/rails console
railswitch task test                  # the pack's and project's tasks
railswitch db dump                    # the databases, into <workspace>/db-dumps/
railswitch db restore                 # put them back into this worktree (asks first)
railswitch down                       # stops it AND deletes its volumes (asks first)
railswitch wt rm feature-x            # --force: even with uncommitted changes (lost)
```

`stop` keeps a worktree's data; `down` deletes its volumes, the database
included. The dashboard does the same from each worktree's row: up, stop,
restart, open the app and its links, and run a task (Shift+Enter runs it
in a new terminal).

Each worktree also publishes a few ports on 127.0.0.1 for tools on your
machine: the database (55432 + its number), the Ruby debugger (33000 +),
the Playwright server (18888 +) and nvim (17000 +). `railswitch env` and
the dashboard show them.

## Secrets

A project declares the secrets it needs (an agent's token, a
`github_token` for a GitHub repository); `project new` lists the ones to
set. You set the values, and declare your own:

```bash
railswitch secret set claude_token        # asks without echo, or reads stdin
railswitch secret add rails_master_key --service rails --env RAILS_MASTER_KEY
railswitch secret set rails_master_key
```

Values live in podman's secret store, never in a file railswitch writes,
an environment file or a command line. The dashboard's Settings page sets
them too.

Next: [Concepts](concepts.md).
