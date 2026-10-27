# nvim

The nvim tool runs a headless Neovim server per worktree, on the worktree's
code at `/app`. You attach to it from the host. Turn it on in `project.toml`:

```toml
[tools.nvim]
config_repo = "https://github.com/you/nvim-config.git"   # optional
config_ref  = "v2"                                       # optional: a branch or tag
```

`project new --tool nvim` writes an empty `[tools.nvim]` table. The old
`[tools] nvim = true` is refused; write the table instead.

## Node for language servers (D71)

The nvim image is built on the project's base image, so it sees the app's
runtimes. Language servers installed with npm (Mason and the like) need
Node whatever the project uses, so `Containerfile.nvim` brings its own
(`ARG NVIM_NODE_VERSION=24`, from `docker.io/library/node:<version>-slim`,
in `/opt/nvim-node`):

- a base with npm (the rails pack's) keeps the app's Node and npm;
- a base without npm gets nvim's Node, `npm` and `npx` in `/usr/local/bin`.
  That includes a bun-only image, whose `node` is bun (the official bun
  images link it at the end of `PATH`); the project's `bun` stays usable.

`npm install -g` goes to the volume `<prefix>_npm_global_node<version>`
(the pack's `node` version), or `<prefix>_npm_global_node` for a pack
without a `node` runtime. To use another Node, edit `NVIM_NODE_VERSION` in
`containerfiles/Containerfile.nvim` and remove that volume
(`podman volume rm <prefix>_npm_global_node`), whose packages were built
for the old one. A project created before this keeps its ejected
`Containerfile.nvim` until `railswitch containerfile eject nvim --force`.

## The config (D54)

`~/.config/nvim` in the container is the volume `<prefix>_nvim_config`.
Every worktree's nvim shares it.

- **First start.** When `config_repo` is set and the volume is empty, the
  nvim entrypoint runs `git clone [--branch <config_ref>] <config_repo>`
  into it. A failed clone is printed, and the editor starts without the
  config.
- **Later starts never touch it.** When the clone's `origin` is not
  `config_repo`, the entrypoint prints
  `nvim config is from <origin>, project.toml says <repo>: railswitch nvim reset-config`
  and starts the editor.
- The repo's host (`https://host/…` or `git@host:…`) is added to nvim's
  firewall allow list, for the clone and for updates. A private repo on
  github.com uses the `github_token` secret given to `nvim`.

Without `config_repo` the volume starts empty, and whatever you configure
inside the container persists.

### `railswitch nvim update`

Updates the config inside the worktree's nvim container (it is started if
needed). It runs `git pull --ff-only`, or with `config_ref`,
`git fetch origin <ref>` and `git checkout --detach FETCH_HEAD`. It refuses
when:

- `config_repo` is not set;
- the config has local changes (it lists the paths);
- the clone's `origin` is not `config_repo` (use `nvim reset-config`).

Each git command has a 5 minute timeout.

### `railswitch nvim reset-config [--yes]`

Empties the config, for the whole project. It asks first (default No;
`--yes` skips the question, and without a terminal `--yes` is required).
It stops every worktree's nvim unit, and removes the volume
`<prefix>_nvim_config` (never with `-f`). The next nvim start clones
`config_repo` again.

## Attaching: `railswitch nvim`

`railswitch nvim` starts the worktree's nvim unit when it is not running,
then attaches:

1. runs the hook `~/.config/railswitch/hooks/nvim-attach`, if it exists and
   is executable;
2. runs the host's `nvim --remote-ui --server 127.0.0.1:<NVIM_PORT>`. With no
   `nvim` on the host `PATH`, it runs
   `podman exec -i [-t] <container> nvim --remote-ui --server 127.0.0.1:7777`
   instead (`-t` on a terminal);
3. runs the hook `~/.config/railswitch/hooks/nvim-detach` when the UI
   exits.

The server listens on the worktree's loopback port only: nvim RPC can run
any command.

### Hooks

The hooks are your own scripts; railswitch ships none. Each runs on the host
with `RAILSWITCH_NVIM_SERVER=127.0.0.1:<port>` in its environment and a 30
second timeout. A failing hook prints a warning; the attach goes on. The
config dir follows `XDG_CONFIG_HOME`.

Use them for terminal integration. This kitty example passes `ctrl+h/j/k/l`
to nvim's splits while it is attached (it pairs with a
`map --when-focus-on var:IS_VIM=true …` block in `kitty.conf`, as
vim-kitty-navigator uses), and pushes the host's light or dark mode into the
server:

`~/.config/railswitch/hooks/nvim-attach`:

```sh
#!/bin/sh
# Tell kitty this window runs vim, so it passes the navigation keys through.
[ -n "${KITTY_WINDOW_ID:-}" ] && kitten @ set-user-vars IS_VIM=true
# Push the current light/dark mode into the long-running server.
if [ -n "${NVIM_BACKGROUND:-}" ]; then
	nvim --server "$RAILSWITCH_NVIM_SERVER" \
		--remote-expr "execute('set background=$NVIM_BACKGROUND')" >/dev/null 2>&1 || true
fi
exit 0
```

`~/.config/railswitch/hooks/nvim-detach`:

```sh
#!/bin/sh
[ -n "${KITTY_WINDOW_ID:-}" ] && kitten @ set-user-vars IS_VIM=false
exit 0
```

Make both executable: `chmod +x ~/.config/railswitch/hooks/nvim-*`.

### Machine-local mounts (D57)

Plugins in the container that talk to the terminal (vim-kitty-navigator) need
the terminal's socket and client inside it. Add them per machine in
`~/.config/railswitch/config.toml`, never in `project.toml`. Each `[[mount]]`
takes `service` (a service id: `nvim`, `agent-<name>`), `source` (absolute or
`~/…`), `target` (absolute, not `/app`), `ro` (default `true`), `optional`
(default `false`: a missing source skips the mount instead of failing the
start), `relabel` (default `true`: the SELinux `z` option) and `projects`
(prefixes; empty = every project).

```toml
schema = 1

[[mount]]
service = "nvim"
source = "~/.local/state/kitty"
target = "/kitty-sock"
ro = false
optional = true

[[mount]]
service = "nvim"
source = "/usr/bin/kitten"
target = "/usr/bin/kitten"
optional = true
relabel = false   # root-owned: rootless podman cannot relabel it on an SELinux host
```

`relabel = false` drops `z` (options `ro`, or none when `ro = false`). Use it
for a source you do not own (it cannot be relabelled, so the start fails on an
SELinux-enforcing host) or whose host label must stay as it is.
