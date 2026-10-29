# The editor

Neovim can run in a container per worktree, with the worktree, the app's
runtimes and the pack's caches, so language servers see exactly what the
app does.

## Turning it on

Tick **Neovim** when creating the project, or add it to `project.toml`:

```toml
[tools.nvim]
config_repo = "https://github.com/you/nvim-config.git"   # optional
config_ref = "main"                                       # optional
```

The config repository is cloned into a volume the first time nvim starts.
Without one, nvim starts with an empty config you can build inside it. The
config and plugins are shared by the project's worktrees.

## Using it

```bash
railswitch nvim                  # this worktree's editor, in this terminal
railswitch nvim update           # git pull your config, refused on local changes
railswitch nvim reset-config     # empty the config volume (asks first)
```

`railswitch nvim` attaches with your host's `nvim --remote-ui` when
there is one, else inside the container; nvim listens on a loopback port
(17000 + the worktree's number) for any front end.

## What it may reach

nvim follows the project's firewall mode. It may reach the package
registries and GitHub (the `deps` list), the `agent` list, its own plugin
sources (GitHub's API and raw files, PyPI) and your config repository's
host. A plugin or language server from elsewhere needs its host added to
the `agent` list in Settings. For a private config repository on GitHub,
set the `github_token` secret `project new` declared; elsewhere, add a
token secret with `git_host` and `services = ["nvim"]`.

Reference: [docs/nvim.md](../nvim.md).

Next: [The firewall](firewall.md).
