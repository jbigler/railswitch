# Shell completion

`railswitch completion bash|zsh|fish` prints a completion script. Each script calls `railswitch __complete <words…>` (hidden), which prints one candidate per line for the last word; the scripts hold no command list of their own.

## Install

bash (needs the bash-completion package):

```sh
mkdir -p ~/.local/share/bash-completion/completions
railswitch completion bash > ~/.local/share/bash-completion/completions/railswitch
```

zsh: write `_railswitch` into a directory on `$fpath`, before `compinit` runs:

```sh
mkdir -p ~/.zfunc
railswitch completion zsh > ~/.zfunc/_railswitch
# in ~/.zshrc, before compinit:
fpath=(~/.zfunc $fpath)
```

fish:

```sh
railswitch completion fish > ~/.config/fish/completions/railswitch.fish
```

## What completes

- Commands, subcommands (`wt add|rm|ls`, `project new|init|rm|ls`, …) and each command's flags. Flags come from a table in `internal/cli/completion.go`; a test fails when a command defines a flag the table lacks.
- After `-w`, and for `wt rm`, `share` and `unshare`: the current workspace's registered worktree slugs.
- `logs` and `fw show|allow`: the workspace's service ids.
- `secret set|rm`: the secrets `project.toml` declares.
- `nvim`: its subcommands `update` and `reset-config`, and `reset-config`'s flag `--yes`.
- `db`: its subcommand `dump`.
- `hooks`: its subcommands `check`, `backup` and `restore`, and `restore`'s flag `--force`.
- `agent`: its subcommands `add`, `ls` and `stop` together with the agents `project.toml` declares (`[agents.<name>]`), and its flag `--new-term`; `agent stop`: the agents. `agent add`: flags `--use` and `--run`.
- `containerfile show|eject|write`: the stack's built-in Containerfile names (`show` and `write` also the workspace's own `containerfiles/Containerfile.<name>`); `write`'s flags `--if-hash`, `--dry-run` and `--restart`.
- `pack`: its subcommands `eject`, `file`, `ls`, `new` and `show`; `pack eject|show`: the built-in packs and options (`rails@1`, `postgres@1`, …); `pack eject`'s flag `--as`; `pack new`'s flags `--dir` and `--option`; `pack file`: `show`, `write` (flags `--if-hash`, `--dry-run`, `--restart`) and `rm` (flag `--if-hash`).
- `project rm`: every registered project prefix.
- `project new --agent` takes `name=ref` or `none`; the flag itself completes, its value does not.
- `pr`: its subcommand `ls`. `task`: its subcommand `ls` together with the tasks `task ls` lists (the pack's `[tasks]` with `project.toml`'s over them), and its flag `--new-term`. `wt add`: flags `--name` and `--up`.
- `dashboard`: its subcommands `colors` and `serve`; `dashboard colors`: its subcommands `set` and `reset`; `dashboard colors set`: flags `--dark` and `--base` (its preset value does not complete); `dashboard colors reset`: flag `--dark`.

Workspace candidates honour `-C <dir>` on the command line. Outside a workspace, or on any error, completion offers nothing and prints nothing to stderr. It reads the registry without locking or creating it, and never runs podman, git or any other command.

bash and fish pass words to `__complete` as typed: a quoted or `~`-prefixed `-C` path (`-C "my dir"`, `-C ~/app`) finds no workspace, so nothing is offered. zsh unquotes the words first.
