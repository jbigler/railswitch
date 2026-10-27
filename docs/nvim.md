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
